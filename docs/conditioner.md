# Conditioner

The Conditioner owns Conditions, decides when each one is active, and
drives the ZoneKeeper to apply the resulting Remediations. It has four
trigger sources, one apply path, and one idempotency cache.

## Trigger sources

| Trigger              | RPC / mechanism                         | Cadence |
|----------------------|-----------------------------------------|---------|
| Alert                | `Conditioner.Alert` (from HookReceiver) | On Alertmanager webhook |
| Binding match        | `Conditioner.ActivateCondition` (from Router) | Per matched event |
| Periodic evaluator   | internal loop                            | `cfg.EvaluationInterval` (default 60s) |
| Spec.Schedule (deprecated) | internal cron path                   | Per cron expression |

All four route into the same `activateRemediation` function, which
handles time-gating, toggle resolution, brightness-delta dispatch,
and the `applyDesired` idempotency cache. The Conditioner doesn't
have parallel apply paths per trigger source.

### Alert

Alertmanager sends an HTTP POST to the HookReceiver's `/alerts`
endpoint. HookReceiver translates each alert into a
`Conditioner.Alert(name, zone, status)` RPC. The Conditioner matches
on Condition's `spec.matches` labels:

```yaml
spec:
  matches:
    - labels:
        alertname: pondLeak
        zone: pond
  remediations:
    - zone: pond-pump
      active_state: on
      inactive_state: off
```

`status=firing` → walk `remediations`, apply `active_state`.
`status=resolved` → apply `inactive_state` to revert.

This is the only path that supports symmetric activate/deactivate
through a single Condition; Bindings only activate.

### Binding match

The router's matcher calls `ActivateCondition(name)` when a
DeviceEvent matches a Binding (see [`bindings.md`](bindings.md)).
ActivateCondition fires all Remediations once. It's a one-shot
trigger — there's no associated "deactivate" event.

### Periodic evaluator

A background goroutine in the Conditioner ticks every
`cfg.EvaluationInterval` (default 60s). On each tick it walks all
enabled Conditions and dispatches Remediations by type:

| Remediation shape         | Eval-loop action |
|---------------------------|------------------|
| `active_compute: <name>`  | Invoke the named Computer; apply result via `ApplyValues` RPC. |
| `time_intervals: [...]` with `active_state` / `active_scene` | Call `activateRemediation` — fires within window, idempotency-cached. |
| Neither set               | Skip. RPC-driven only (Alert / ActivateCondition). |

The eval loop is what makes Conditions like:

```yaml
remediations:
  - zone: living-area
    active_state: off
    time_intervals:
      - times: [{start_time: "04:00", end_time: "11:00"}]
```

work without a cron expression. Within the window, the eval loop
fires every minute; `applyDesired` absorbs identical re-applies, so
only the first one emits a Zigbee command. Outside the window, the
loop skips with `direction=time-gated` recorded on
`iotcontroller_conditioner_apply_suppressed_total`.

### Spec.Schedule (deprecated)

Legacy cron strings on `Condition.Spec.Schedule`. The Conditioner
keeps a per-Condition cron schedule registered with the
`pkg/conditioner/schedule` runner. Each cron tick activates the
Condition's Remediations exactly as the eval loop does.

When the controller sees a Condition with `Spec.Schedule` set, it
logs a one-time deprecation warning. The field is scheduled for
removal — all production Conditions migrate to `time_intervals`
during Stage 5 of the unified-evaluator plan.

## Time-gating: `withinActiveWindow`

A Remediation with `time_intervals` only activates when the current
time falls inside one of the windows. The check evaluates each
`TimeIntervalSpec` entry against two sources:

- **Standard Prometheus shape** — `times`, `weekdays`, `daysOfMonth`,
  `months`, `years`, `location`. Round-trip through the Prometheus
  alertmanager `timeinterval` library to leverage its parsing.
- **Sun-relative windows** — `sun_relative: [{event, before, after}]`.
  Anchored to today's `sunrise`, `sunset`, `noon`, or `midnight` at
  the operator-configured lat/lon. Continuous span: `before: 30m,
  after: 12h` around `sunset` is one [start, end] window that may
  cross midnight.

OR semantics across both within an entry, and across entries: any
matching window makes the Remediation active.

## `applyDesired` idempotency cache

Every activate call routes through `applyDesired`, which keys on
`(Condition, Zone)` and stores the last successfully applied
`(state, scene)`. If the current request would set the same values,
the apply is suppressed and `iotcontroller_conditioner_apply_suppressed_total
{direction="activate"}` increments.

The cache is the reason the eval loop can run at 60s without
generating 1 Zigbee command per minute per active Condition:

- First tick within a window: apply fires; cache populated.
- Subsequent ticks: cache hit; apply suppressed; no Zigbee command.
- Out of window: time-gated; apply skipped.

TTL: `cfg.ApplyDesiredRefreshAge` (default 30 minutes). After the
TTL expires the cache entry is dropped and the next apply re-asserts
the desired state — useful for absorbing drift (someone manually
toggled the zone outside the controller).

The cache is in-process; pod restart wipes it. First post-restart
apply for any `(Condition, Zone)` always reaches the ZoneKeeper.

### Delta path bypasses the cache

`active_brightness_delta` Remediations skip `applyDesired` and
always dispatch through `AdjustBrightness` directly. The cache is
for absolute values (state, scene) where "same as before = no-op";
deltas must fire every time so repeated presses walk the brightness
enum step-by-step.

## Toggle resolution

`active_state: toggle` is resolved at apply time by reading the
zone's CRD `status.state` from the informer cache and inverting it.
The eval-loop and Binding-driven activations both go through this
resolution. Two consecutive toggle presses within the `applyDesired`
cache window resolve to the same value and the second is suppressed
— that's a deliberate physical-button-bounce debounce, not a missed
toggle.

If the zone status lookup fails (cache miss, kubeclient blip),
toggle resolves to `on` — easier for an operator to recover from
than a dark room.

## Configuration

```
-conditioner.evaluation-interval=60s          # eval loop tick
-conditioner.location.lat=40.5555             # operator location for sun events
-conditioner.location.lon=-105.1382
-conditioner.apply-desired-refresh-age=30m    # cache TTL
-conditioner.query.endpoint=<url>             # PromQL endpoint for query Computer (optional)
-conditioner.query.tenant=ops                 # X-Scope-OrgID for Mimir multi-tenancy
-conditioner.query.timeout=5s
-conditioner.query.auth-token-env-var=PROM_BEARER_TOKEN
```

The `query` Computer is registered only when `query.endpoint` is
non-empty.

## Observability

Per-tick metrics:

- `iotcontroller_conditioner_evaluation_total` — eval-loop ticks.
- `iotcontroller_conditioner_evaluation_duration_seconds` — wall
  clock per pass.
- `iotcontroller_conditioner_evaluation_compute_unknown_total{compute}`
  — Remediation referenced an unregistered Computer name.
- `iotcontroller_conditioner_evaluation_compute_error_total{compute}`
  — Computer.Compute returned an error.
- `iotcontroller_conditioner_evaluation_apply_error_total{compute}`
  — `ApplyValues` RPC failed.

Cache behaviour:

- `iotcontroller_conditioner_apply_suppressed_total{condition, zone, direction}`
  — labeled by `(activate, deactivate, time-gated)`. High activate
  rate + low SetState rate proves the cache is doing its job. High
  time-gated rate proves windows are gating as designed.

Tracing:

- `Conditioner.Alert`, `Conditioner.ActivateCondition`,
  `Conditioner.evaluate`, `Conditioner.evaluateCompute`
  — each a span. The Router's matcher span sits upstream; following
  the trace from the matcher through to the ZoneKeeper shows the
  full activation timeline.
