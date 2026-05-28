# Phase D — Safety-Critical Zone Migration

Companion to [`reconcile-design.md`](reconcile-design.md). Captures
the plan to move heater zones (`prop-house-heater`, `mainsuite-heater`,
`office-heater`) and the pond pump (`pond-pump`) from the imperative
alert-driven path onto the reconcile-loop architecture.

Status: **pending**. Phase C lighting expansion is in progress
(4 zones as of 2026-05-24). Phase D starts after Phase C zones have
soaked for a multi-day window covering at least one real edge case
per safety-critical zone class.

## Why this is its own document

Heater + pump are safety-critical: getting them wrong damages
hardware or kills plants. The architectural migration is the same
shape as Phase C (add zones to `-conditioner.reconcile-zones`), but
the prerequisites and rollback constraints are different enough that
they need their own checklist.

## Safety contract

The migration must NEVER violate these invariants — checked manually
before flip, monitored continuously during soak:

| Zone class | Invariant | Test surface |
|---|---|---|
| Heater | When the configured `zoneTempLow` alert fires, heater state reaches ON within one eval tick (≤60s). | `heater_test.go`'s `TestHeater_TempLowFiring_TurnsOn` covers the alert path. |
| Heater | When `zoneTempHigh` fires, state reaches OFF. | `TestHeater_TempHighFiring_TurnsOff`. |
| Heater | `zoneTempLow` resolving while still in winter must NOT turn the heater off — short-cycling the relay reduces lifespan. Hysteresis preserved by empty `inactive_state` on the low Condition. | `TestHeater_HysteresisPreservedOnLowResolve`. |
| Heater | An alert firing OUTSIDE its `time_intervals` window must force the heater OFF (window-close safety contract from v0.6.5). | `TestHeater_FiringOutsideWindow_ForcesOff`. |
| Pump | When water_leak signal crosses the threshold, pump state reaches ON. | Today: `pondLeak` alert path. After migration: `active_compute=query` Condition with the OR-of-two-sensors PromQL. |
| Pump | When water signal drops below threshold, pump state reaches OFF. **Running dry damages the upgraded pump.** | `pump_test.go`'s `TestPump_Query_WaterAbsent_ReturnsOff`. |
| Pump | Mimir / metric outage MUST NOT leave the pump running indefinitely. Declared `on_error.state=ZONE_STATE_OFF` overrides the cache fallback. | `TestPump_Query_FailSafeOnMimirOutage_AfterRunning` and `TestPump_Query_FailSafeOnFirstCall`. |

## Prerequisites (must be true before flipping any safety zone)

- [x] `on_error.*` args on the `query` Computer (v0.9.5+ commit `9976452a`)
- [x] `heater_test.go` and `pump_test.go` covering all rows of the
      safety contract table above
- [x] `RemoveActivation` + source-kind threading (v0.9.5) so
      alert resolves cleanly evict instead of racing the stack
- [ ] Phase C lighting zones soaked for ≥ 3 days with no real
      lamp-flapping incidents (within-tick metric noise is acceptable
      and documented)
- [ ] Zone-topology decision (next section) made
- [ ] Rollback procedure rehearsed in a non-cold-snap / non-leak
      window
- [ ] Tag + ship v0.9.6 (or whatever is current) — Phase D should
      NOT migrate against an unreleased binary

## Zone topology question — sensor + actuator unification

Currently each safety-critical pair lives in two zones:

| Sensor zone | Actuator zone | Devices in sensor zone | Devices in actuator zone |
|---|---|---|---|
| `prop-house` | `prop-house-heater` | temperature sensor(s) | Sonoff S31 relay |
| `office` | `office-heater` | temperature sensor + Hue lights + buttons | heater relay |
| `mainsuite` | `mainsuite-heater` | temperature sensor + lights | heater relay |
| `pond` | `pond-pump` | water-leak sensors | Third Reality relay |
| `tunnel` | `tunnel-fan` | (none / sensor-only) | fan relay |

The split exists for the imperative alert path: Alertmanager fires
alerts with `zone=<sensor>` labels, the Condition's `matches:`
selects on that label, and the Remediation's `zone:` targets the
actuator. Two zones because one Condition can only match one alert-
zone label and one Remediation can only target one zone.

The reconcile path makes this split optional:

- `active_compute=query` Conditions don't need `matches:` at all —
  they read PromQL directly, no alert dependency.
- A Remediation can target whichever zone has the actuator device.
- Multiple devices in one zone get the same ApplyValues; sensors
  ignore `state=on` (no-op), the relay obeys.

### Option A — keep the split

- No deployment_tools changes beyond adding zone names to
  `-conditioner.reconcile-zones`.
- Conditions stay alert-driven; we just route them through the
  reconciler instead of through `applyDesired`.
- Pump's query Condition stays in `pond-pump` zone; reads
  `iot_zigbee2mqtt_water_leak{zone="pond"}` (cross-zone metric
  reference).
- Pros: minimal change, alert-rule labels and dashboards keep
  working unchanged.
- Cons: the split has no architectural reason once the reconciler
  is the writer; future operators will wonder why.

### Option B — unify (user is open to this)

- Move sensors AND actuator into a single zone (rename `pond-pump`
  devices into `pond`; rename `*-heater` actuators into the
  corresponding sensor zone).
- PromQL queries simplify (one zone label, not two).
- Conditions can fully migrate to `active_compute=query` —
  no `matches:` needed.
- Pros: one zone per "intent" (the room or area); cleaner mental
  model; the alert path can stay imperative for the heater pair if
  desired (low + high alerts continue to fire) without forcing the
  split.
- Cons: deployment_tools jsonnet changes; Grafana dashboards keyed
  on `zone="<actuator>"` need updating; alert-rule labels (which
  emit `zone="<sensor>"`) keep working but the actuator zone label
  on rule expressions becomes invalid.

### Recommendation

Option B for the pond pump first (single sensor + single actuator,
smallest blast radius). Heaters can move under Option A initially
to keep the alert path intact, and revisit Option B for heaters
later if the doubled zones bother us.

## Migration sequence

Tackle the safety zones one at a time, with at least 24h of
observation between each flip. The order is chosen to put the most
forgiving cases first.

### Step 1 — Pond pump (shipped 2026-05-28)

Originally planned as a topology unification (Option B: merge sensors
+ actuator into one `pond` zone) plus a single `active_compute=query`
Condition. Real-world experience reshaped the plan:

- A leak event 2026-05-26 → 2026-05-27 exposed a 1h 45m gap in pump
  coverage during a sustained 11-hour leak. Root cause was the
  2-minute smoothing window oscillating around the `> 0.5` threshold
  while sensor sample density dropped, plus a coincident alloyd
  scrape blip in the same window.
- The leak sensors are a fragile signal: SNZB-05P devices reporting
  on transitions only, low link quality at the pond's distance,
  occasional stuck-true and stuck-silent states.
- The `active_compute=query` path is end-to-end Mimir-dependent —
  PromQL eval freshness, scrape success, conditioner's 60s poll
  cadence all in the critical path.

The shipped shape keeps the `pond-pump` zone (didn't unify) and adds
a Binding-driven path **alongside** the existing query Condition:

1. **No topology change.** `pond-pump` zone stays. `pond` zone stays.
   Sensors in `pond`, relay in `pond-pump`. The Binding's
   selector-by-zone (`event.selector.zone=pond`) picks up either
   leak sensor; the Condition's `remediations[].zone=pond-pump`
   routes the apply to the relay zone. Inter-zone routing is
   intrinsic to the architecture, no reason to fight it for this
   case.

2. **Symmetric Binding-driven path added to `bindings.libsonnet`:**
   - `pond-leak-on`: `water_leak=true` from any device in zone=pond
     → activates `pond-leak-on` Condition (state=on on pond-pump).
     No on-side dwell — sub-second response to leak detection.
   - `pond-leak-off`: `water_leak=false` with `min_duration=2m` dwell
     → activates `pond-leak-off` Condition (state=off). The 2m
     dwell preserves the smoothing semantic the original PromQL had.

3. **`on_error.state: ZONE_STATE_OFF` added to the existing
   `pond-pump` Condition** in `conditions.libsonnet`. Mimir/scrape
   outage now defaults to OFF instead of the cache-fallback
   direction. Running dry damages the pump; "we can't see data"
   must mean "stop."

4. **`pond-pump` added to `-conditioner.reconcile-zones`** in
   `tk/lib/iot/controller.libsonnet`. The zone now uses the stack
   model — Binding pushes at priority 100, query pushes at priority
   50. Binding wins composition while sensor publishes refresh
   PushedAt; query Condition acts as Mimir-fed redundancy with its
   own fail-safe direction.

5. **Existing alert-driven `pondLeak` path retired implicitly** —
   the `pond-pump` Condition has `matches: 0`, so Alertmanager
   webhooks targeting `alertname=pondLeak` don't match any
   Condition. The webhook hookreceiver still receives them but
   they no-op. Can clean up the Alertmanager rule and the
   hookreceiver no-match noise in a follow-up.

Observe:
- `iotcontroller_reconciler_push_total{zone="pond-pump",source_kind="SOURCE_KIND_BINDING"}` —
  should increment on each `water_leak=true` event.
- `iotcontroller_reconciler_apply_suppressed_total{zone="pond-pump",reason="no_delta"}` —
  steady-state during a sustained leak (Binding push refreshes,
  target stays state=on, cache absorbs).
- `iotcontroller_conditioner_query_fail_safe_total{condition="pond-pump"}` —
  rises only during Mimir/scrape outage; should be zero in normal
  operation.
- `Zone.Status.ReconcilerStack` on `pond-pump` zone — should show
  the active Binding entry (or query entry between leak events).

### Step 1 known limitations (carried forward)

These are sensor / signal problems, not architecture problems.
Logging them so the next strategy iteration starts from the right
question:

- **Stuck-true sensor traps OR aggregation.** If one of the two
  pond sensors gets stuck reporting `water_leak: true`, the OR-of-
  two-sensors design fires the pump indefinitely. Observed 2026-05-27
  when sensor `0x08ddebfffeee504d` woke from 28h silence reporting
  true. Mitigation today: physical inspection / sensor reset.
  Future: consider a more robust PromQL aggregation (e.g. require
  inter-sensor agreement above a threshold), OR a "stuck-source"
  detector that suppresses contributors whose value hasn't toggled
  in some window.

- **Pump can run dry.** If sensors detect water (e.g. rain on the
  sensor) but the pump intake is dry, the relay energizes and the
  motor runs without water-moving load (low W draw, no thermal
  rise). Observed 2026-05-28: rain wetted sensor 504d, pump drew
  39.6W for hours without warming up. Dry running shortens pump
  life. Mitigation: a power-monitoring threshold check (if running
  AND power < N watts for M minutes → turn off; the relay's smart-
  plug telemetry already provides current/power), OR a float-switch
  at the pump intake as a separate gate.

- **Phase D step 1 doesn't move the heater zones.** That's
  intentional and unchanged: heater migration remains gated on
  pond-pump soaking cleanly, plus the planned cold-snap validation
  window.

### Step 2 — One heater (Option A first)

Pick the lowest-stakes heater (`office-heater` if its plants are
hardiest; otherwise `mainsuite-heater`). Add to
`-conditioner.reconcile-zones`. The existing alert-driven Conditions
keep their `matches:` and Remediation routing; reconciler bridges
the imperative activates instead of applyDesired-ing them.

Observe through at least one cold-snap event. Verify:
- Alert firing → heater ON within one eval tick.
- Alert resolving → heater state unchanged (hysteresis preserved
  by empty inactive_state on the low Condition).
- Paired high alert firing → heater OFF.

Rollback: remove the zone from the flag.

### Step 3 — Remaining heaters

After Step 2 soaks ≥ 48h and includes at least one alert fire/resolve
cycle, migrate the remaining heater zones one per day.

### Step 4 — Decide on heater zone unification (Option B revisit)

After all heaters are reconcile-managed, decide whether to unify
their sensor + actuator zones (Option B). Mostly a topology cleanup
at this point — no functional change.

## Rollback

The migration is **fully flag-reversible**. Removing a zone from
`-conditioner.reconcile-zones` and restarting the conditioner
restores the imperative path:

```bash
# Edit tk/lib/iot/controller.libsonnet to remove the zone
cd ~/Org/znet/deployment_tools && git commit -o tk/lib/iot/controller.libsonnet -m "iot: emergency rollback of <zone>"
git push origin main && git push forgejo main
cd tk && tk apply environments/iot --auto-approve=always
# Conditioner pod restarts; imperative path resumes
```

A zone whose alert Condition has TimeIntervals will see the new
imperative path's `withinActiveWindow` check immediately. A zone
whose alert was firing at the moment of rollback will require the
next webhook to re-fire (Alertmanager retries on its own cadence).

Rollback does NOT require pulling the bridge code out of the
binary — the bridge intercepts ONLY for zones in
`-conditioner.reconcile-zones`. Non-listed zones never enter the
bridge path.

## Metrics + alerts to watch during migration

| Metric / alert | What to look for |
|---|---|
| `iotcontroller_reconciler_apply_error_total{zone="<zone>"}` | Any non-zero rate is a problem — investigate the `compute` error or zonekeeper error before continuing |
| `iotcontroller_conditioner_query_fail_safe_total{condition=~".*pump.*\|.*heater.*"}` | Non-zero rate = Mimir outage active. Pump should be OFF; heater should be in its safe direction |
| `iotcontroller_conditioner_query_outcome` | Should track real-world signal (temperature for heater, water for pump) |
| `iotcontroller_zonekeeper_state_changes_total{zone=~".*pump.*\|.*heater.*"}` | Rate should match reality. Sustained churn = investigate immediately |
| `IOTConditionConflict` alert | Should not fire on the migrated zones (one writer per axis after reconcile-managed flag) |
| `IOTZoneStateChurn` alert | Should not fire on the migrated zones; the reconciler dedup caches prevent it |

## When NOT to migrate

- During an active alert (cold snap for heaters, leak event for pump).
  Wait for the alert to resolve and stay resolved for at least one
  eval interval before flipping.
- During a Mimir outage. The query Computer's fail-safe path is
  tested but un-observed in production. Don't combine "first
  production use of fail-safe" with "first production use of
  reconcile-managed heater/pump."
- During a deployment freeze (deployment_tools rules).

## Open questions for this phase

1. **Heater zone unification.** Defer to after Step 3 unless we
   find a compelling reason during heater migration.

2. **Should the pump migrate to active_compute=query, or stay
   alert-driven?** active_compute is the cleaner shape (no alert
   round-trip; on_error.* fail-safe; query Computer's outcome
   metric for free) but it adds a new failure mode (Mimir
   unavailability). Today's pondLeak alert path has its own
   resilience (Alertmanager retries). Recommend: try active_compute,
   keep the pondLeak alert as a backup until we observe the
   active_compute path through at least one real leak event.

3. **Cron-scheduled writes still bypass the bridge.** Per the
   simplify-review documented caveat. None of the safety-critical
   zones use `Spec.Schedule` today; if one is added later, audit
   that its target aligns with what the stack composes.

4. **Phase E (imperative retirement) timing.** Don't retire
   `applyDesired` until heater + pump are reconcile-managed AND
   cron-scheduled writes have been migrated to TimeIntervals or
   explicit Activations.
