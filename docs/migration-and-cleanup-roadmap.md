# Migration + Cleanup Roadmap

A single page tracking what's left to finish the reconcile-loop migration and
clean up the resulting environment + code. Issues referenced live at
[code.znet/znet/iotcontroller/issues](https://code.znet/znet/iotcontroller/issues).

## Zone migration status (as of 2026-05-30)

| Zone | State | Notes |
|---|---|---|
| `bedside-zach` | reconcile-managed | shipped Phase B |
| `foyer` | reconcile-managed | shipped Phase C |
| `office` | reconcile-managed | shipped Phase C; composition fight on CT axis → #2 |
| `living-area` | reconcile-managed | shipped Phase C |
| `pond-pump` | reconcile-managed | shipped Phase D step 1 (2026-05-28); sensor strategy under review |
| `sunroom` | reconcile-managed | shipped 2026-05-30 (v0.9.6); template Conditions, 0 devices — empty stacks |
| `axel` | reconcile-managed | shipped 2026-05-30 (v0.9.6); empty stacks |
| `library` | imperative | **deprecated** — schedule for deletion, not migration |
| `prop-house-heater` | imperative | gated by #4 |
| `mainsuite-heater` | imperative | gated by #5 |
| `office-heater` | imperative | gated by #5 |
| `office-water-heater` | imperative | gated by #5 |
| `brew0-heater` | imperative | gated by #5 |
| `tent0-fan`, `tent1-fan`, `tunnel-fan` | imperative | gated by #8 |
| `barn-router`, `boysroom`, `brew0`, `closet`, `laundry`, `mainsuite`, `pond`, `porch`, `prop-house`, `shop`, `shop-router-east`, `tent0`, `tent1`, `tent1-light`, `tunnel` | imperative | 0 Conditions today; flip to reconciler when convenient (empty stacks; no behavior change) |

**Remaining safety-critical migrations:** 5 heaters, 3 fans (8 zones across #4, #5, #8).
**Remaining no-op migrations:** ~15 zones with 0 Conditions; can batch-flip any time.

## Code cleanup chart

Sequenced so each step is fully reversible until the imperative path goes.

### Stage 1 — Active path improvements (in flight)

- **#1** matcher dispatch-to-all + spans
- **#2** scene priority differentiation (close office composition fight)
- **#3** zonekeeper Status patch debounce (mirror of bb9cec15)

Closes user-visible bugs and prepares the reconciler for the safety-critical
zones. **Required before Stage 2 begins.**

### Stage 2 — Safety-critical migrations (gated)

- **#4** Phase D step 2: `prop-house-heater` first, with explicit
  `on_error.state: ZONE_STATE_ON` fail-safe.
- Soak ≥1 week through a cold-snap window.
- **#5** Phase D step 3: remaining four heaters in one rollout.
- **#8** Phase D supplementary: three fan zones.

### Stage 3 — No-op zone migrations (batch)

- All zero-Condition zones added to `-conditioner.reconcile-zones` in one
  libsonnet edit. No behavior change (empty stacks); satisfies Phase E gate.

### Stage 4 — Phase E retirement (#7)

- Delete `activateRemediation`, `applyDesired`, the `condState` cache,
  `forceDeactivate`, `deactivateRemediation`, matcher tie-break.
- Delete the `isReconcileManaged` branch in `conditioner.go`.
- Retire `-conditioner.reconcile-zones` flag (or repurpose as opt-OUT).
- Net deletion ≥200 LOC.

### Stage 5 — Naming + doc cleanup (optional, post-Phase E)

- Rename `Conditioner` module to `Reconciler` if/when comfortable.
- Drop "Phase X" framing in `docs/`; the architecture IS the reconcile loop.
- Refresh `docs/architecture.md`; remove imperative-path examples.

## Environmental cleanup chart

These are NOT migration items — they're things the migration revealed.

### E1 — Delete the `library` zone (deprecated)

- Source: `[[library-deprecated]]` memory note.
- 7 template Conditions exist (`library-{auto-off,brighter,dim,dimmer,full,off,toggle}`)
  but 0 devices. The Conditions are dead config.
- Action:
  1. Remove `library` from `zones.libsonnet` in deployment_tools.
  2. Remove the 7 library Conditions from `conditions.libsonnet`.
  3. Delete the live Zone CR and 7 Condition CRs from the cluster.
- Risk: zero (no devices, no live state).

### E2 — Audit unused Conditions

- Goal: identify Conditions that haven't fired in 30+ days.
- Method: PromQL `sum_over_time(iotcontroller_condition_fires_total[30d]) == 0`
  on each Condition (need to verify the metric name exists).
- Action: list candidates; user decides per-Condition (some may be cold-snap or
  alert paths that haven't triggered).

### E3 — Audit unused Bindings

- Goal: identify Bindings that haven't matched in 30+ days.
- Method: `iot_binding_matches_total{binding="..."}` rate over 30d.
- Action: same as E2 — list, user decides.

### E4 — Audit unused DeviceType templates

- Goal: identify DeviceType CRs that have no devices.
- Method: cross-reference `kubectl get devicetypes` against
  `kubectl get devices -o json | jq '.items[].spec.type'`.
- Action: delete unused DeviceTypes from the deployment.

### E5 — Audit deprecated/dead zones beyond `library`

- Candidates today (0 devices, 0 active Conditions referenced by Bindings):
  `axel`, `sunroom`, several router zones. `axel` is migrating to reconciler
  tonight but its 0-device state suggests it may be deprecated too — needs
  user confirmation before deletion.
- Memory: `[[library-deprecated]]` is the precedent.

## Apiserver health (out of scope but recurring)

This roadmap repeatedly observed apiserver-not-ready / TLS handshake timeout
errors during 2026-05-29/30 sessions. The iotcontroller's `Status().Patch()`
calls are hit hardest because they're synchronous on the hot path (#3
addresses this). But the underlying cluster instability deserves its own
investigation — likely outside this repo's scope. If it persists past the
#3 debounce landing, escalate.

## Tracking

When this doc goes stale, prune it. The issues at code.znet are the source of
truth; this page is just the cross-reference + sequencing.
