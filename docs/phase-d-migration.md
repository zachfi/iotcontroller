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

### Step 1 — Pond pump (Option B unification)

1. Topology change in deployment_tools: move the pump relay
   (`0xffffb40e06074ee5`) into the `pond` zone. Delete the
   `pond-pump` zone CR.
2. New Condition: `pond-pump-query` with
   - `active_compute: query`
   - `args.query: max(avg_over_time(iot_zigbee2mqtt_water_leak{zone="pond"}[2m])) > 0.5`
   - `args.on_true.state: ZONE_STATE_ON`
   - `args.on_false.state: ZONE_STATE_OFF`
   - `args.on_error.state: ZONE_STATE_OFF` (safety-critical)
3. Existing alert-driven `pondLeak` Condition can stay (defense-in-
   depth) or be retired once the query Condition proves out.
4. Add `pond` to `-conditioner.reconcile-zones`.
5. Apply, watch:
   - `iotcontroller_conditioner_query_fail_safe_total{condition="pond-pump-query"}`
     should be zero in normal operation (rises only during Mimir
     outage).
   - `iotcontroller_conditioner_query_outcome{condition="pond-pump-query"}`
     should track water presence.
   - `iot_zigbee2mqtt_state_on{device="0xffffb40e06074ee5"}` should
     match the query outcome.

Rollback: remove `pond` from `-conditioner.reconcile-zones`. The
imperative alert path resumes immediately.

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
