# Reconcile-Loop Architecture — Active Computer Stack per Zone

Status: **Phase B-C shipped and running in production.** Phase D
(safety-critical zone migration) pending. This doc started as design
notes from 2026-05-20 / 2026-05-21 conversations and now records both
what was designed and what was actually built.

The core question this doc started by trying to answer:

> What's the smallest mental model that makes the current imperative
> tangle disappear, *without* forcing us to grow another tap-on-the-
> harvester workaround the next time we add a Computer?

## Implementation status (2026-05-24)

### What shipped

| Version | What landed |
|---|---|
| v0.8.7 | Shadow resolver — read-only first step; observes conflicts |
| v0.8.8 | `IOTConditionConflict` alert on shadow's multi-contributor metric |
| **v0.9.0** | **Active Computer Stack data model + PushActivation RPC + flag-gated reconciler (Phase B foundation)** |
| v0.9.1–0.9.4 | Bridge from eval loop and imperative `activateRemediation` to PushActivation; Zone.Status.ReconcilerStack reflection; metrics + traces |
| v0.9.5 | `RemoveActivation` for deactivate paths; source-kind threading (Alert → priority 200 vs Binding → 100); fade declares its own TTL via `TTLAdvisor` |
| `9976452a` (uncut) | `query` Computer `on_error.*` fail-safe + heater/pump invariant test suites (Phase D prereqs) |
| `98c3e534` (uncut) | `prom_scalar` Computer for continuous PromQL → axis mapping (shelved pending real use case) |

Latest tagged: v0.9.5. Two commits on `main` not yet tagged; planned
to ship as v0.9.6 after Phase D migration finishes.

### Reconcile-managed zones today

As of 2026-05-24, `-conditioner.reconcile-zones=bedside-zach,foyer,office,living-area`.

| Zone | Shape | Notes |
|---|---|---|
| `bedside-zach` | motion + circadian + buttons | First canary; clean since v0.9.0 |
| `foyer` | motion (evening + nightvision) + scheduled scenes + buttons + S31 plug | The original ping-pong test case; `foyer-motion-nightvision` window now spans `sunset-1h → sunrise+30m` |
| `office` | active_compute=circadian + scene CT overlap + buttons | Documented within-tick metric noise from circadian+scene-on-CT-axis overlap; lamp behavior correct |
| `living-area` | button-heavy + cron + time-window scenes | Most recent expansion (2026-05-24); cron `Spec.Schedule` at 22:30 MDT bypasses bridge but target matches what the bridge pushes anyway |

Heater zones (`prop-house-heater`, `mainsuite-heater`, `office-heater`)
and `pond-pump` are deliberately NOT migrated. Phase D will take them
on once the canary set has soaked through real edge cases.

### Divergences from the original design

The design held up well; only a few divergences worth recording.

1. **No new `ZonePolicy` CRD (yet).** The original "small step vs big
   step" fork at Phase A picked small. Existing `Condition` CRs stay
   as the operator-facing surface; the reconciler-managed view lives
   on `Zone.Status.ReconcilerStack` for now. Big step (ZonePolicy
   CRD as the declarative input) is still on the table for Phase E
   if value materializes.

2. **Sensor-bound Condition skip.** v0.9.3 added the rule "if a
   Condition is referenced by a `property=occupancy|water_leak|
   contact|vibration|tamper` Binding, skip it from the eval-loop
   bridge — its TimeIntervals are sensor gates, not schedule
   triggers." This wasn't in the original design; emerged from the
   v0.9.2 ping-pong on the foyer S31 plug, where motion-on and
   motion-off Conditions both had TimeIntervals and both were being
   bridged as TIME_WINDOW pushes that competed every tick. Button-
   property bindings (`property=action`) do NOT trigger the skip —
   button Conditions with TimeIntervals are legitimate schedule
   triggers AND button aliases.

3. **Bridge instead of replace.** The original migration plan had
   the reconciler eventually *replacing* the imperative path
   (`applyDesired` and friends). What actually shipped is a parallel
   path: imperative `activateRemediation` intercepts reconcile-
   managed zones and routes them to `bridgeImperativeActivate`
   (which pushes onto the stack); non-managed zones continue
   imperative. The full retirement is Phase E, after all zones
   migrate.

4. **TTL refresh semantics.** Open question #1 was answered: REFRESH
   in place (preserve args, bump PushedAt + Ttl). REPLACE is also
   supported via `PushPolicy_PUSH_POLICY_REPLACE` but no production
   caller uses it yet.

5. **`TTLAdvisor` interface for short-lived Computers.** Open
   question #3 evolved into this. Fade declares `duration + 30s` as
   its TTL so a 3-second fade doesn't pin the stack for the
   imperative-default 5 minutes. Stateless Computers (circadian,
   sun-position) don't implement it and use the default.

6. **No Computer signature change** for `zone_status`. Open question
   #6 stayed in the doc; nothing has needed it yet. Fade resolves
   "from current" via its own `FadeSnapshotStore` (seeded by the
   ActivateCondition handler before `activateRemediation` runs)
   rather than reading zone Status during Compute.

### Production lessons (Phase B-C)

These hit production and shaped the architecture; documented here
so they aren't re-discovered later.

- **Multi-writer race on Zone.Status.** Initial Status reflection
  in v0.9.1 fought with zonekeeper's `Status().Update`. Fixed in
  v0.9.4 by switching zonekeeper to `Status().Patch` on only its
  owned fields. Both writers now commute via JSON merge patch.

- **CRD must be applied separately from tanka.** The reconciler
  Status fields are CRD additions (`reconciler_stack`,
  `last_reconciled_at`). Tanka manages Deployments but not CRDs in
  this stack; need `kubectl apply -f config/crd/bases/iot.iot_zones.yaml`
  on each schema bump or apiserver strips the new fields.

- **RBAC: `patch` on `zones/status` is distinct from `update`.**
  v0.9.2's Status reflection failed silently in production until
  v0.9.3 added `patch` to the operator's ClusterRole. The Debug-
  level log made it invisible; would have been quicker to detect
  with a Warn-level RBAC error.

- **Imperative deactivate had to gain a stack-aware counterpart.**
  v0.9.4 shipped activate-bridges-to-push but kept
  `deactivateRemediation` / `forceDeactivate` imperative. Race: an
  alert resolve wrote OFF via `applyDesired` while the activate
  entry stayed on the stack and the reconciler kept re-asserting
  ON. v0.9.5's `Reconciler.RemoveActivation` evicts by `(SourceKind,
  SourceName)` so deactivate evicts what activate pushed.

- **Source-kind matters for stack identity.** Stack `id` is
  `(SourceKind, SourceName)`. Without source-kind threading, a
  binding-driven activate and an alert resolve for the same
  Condition name would have collided on the same stack entry.
  v0.9.5 threads source-kind through `activate*FromSource` /
  `deactivate*FromSource` variants.

- **Within-tick top-changed metric noise is cosmetic.** When two
  time-window Conditions push to the same axis at priority 50 in
  one eval tick (e.g. office's circadian + day scene both on CT),
  each `PushActivation` fires its own immediate `ReconcileZone` and
  the `top_changed_total` counter increments per push. Final lamp
  state is correct (last push wins, cache absorbs); the metric just
  reflects the within-tick stack growth. Not a regression vs the
  imperative path's per-Condition `applyDesired` writes.

- **Cron `Spec.Schedule` bypasses the bridge.** Cron path goes
  through `schedule.run → execRequest` directly to ZoneKeeper,
  never touching the stack. Acceptable when the cron's target
  matches what's pushed via TimeIntervals on the same axis;
  documented for the day a cron fires a direction the stack hasn't
  composed (no examples in production yet).

### Open design questions — current status

| # | Question | Resolved? |
|---|---|---|
| 1 | TTL refresh semantics | YES — REFRESH default, REPLACE opt-in |
| 2 | Computer position-in-stack awareness | NO — Computers stay pure; reconciler picks top |
| 3 | Fade ↔ stack interaction | YES — `TTLAdvisor` interface, fade returns duration+30s |
| 4 | Disabled Conditions | YES — `Spec.Enabled=false` → never pushed (eval loop skips at line ~80 of evaluator.go) |
| 5 | Audit trail | YES — `Zone.Status.ReconcilerStack` reflects top per axis with source name + pushed_at + expires_at |
| 6 | Computer signature: add `zone_status` | DEFERRED — no Computer has needed it yet |

## The shape, in one paragraph

At any moment, each zone has a target state expressed per axis
(`state`, `brightness`, `color_temperature`, `color`). The target
comes from a stack of **Computer-Activations** per axis. The
reconciler runs periodically (and on event wakeups), reads the top
non-expired Activation off each axis stack, evaluates its Computer
to produce the current value, and flushes the composed target to the
zone. Activations can be **pushed** by events (motion, button, alert,
time-window opening) and **expire** after a TTL — at which point the
stack pops back to whatever was underneath. The bottom of each
stack is the zone's *background* Computer — the answer to "what
should this axis be doing when nothing else is asserting?"

## Why this dissolves the current pain

The motion-zone overnight 2026-05-20 → 2026-05-21 was a perfect
illustration of what the current model can't express cleanly. The
zone in question was a transit space (a foot-traffic area with a
PIR sensor); the same shape applies to any binding-driven zone with
overlapping time-window Conditions:

```
transit-motion-nightvision: state=on, scene=nightvision
  • fired by Binding on every occupancy=true event
  • ALSO fired by the eval loop every 60s during its window

transit-motion-off: state=off
  • fired by Binding only after 5m of sustained occupancy=false

Result overnight:
  - PIR sensor heartbeats occupancy=false every ~6 min (no real motion)
  - After 5 false heartbeats, motion-off's dwell completes → state=OFF
  - Next eval tick of motion-nightvision (60s later) → state=ON
  - 30-minute oscillation, ~16 cycles overnight, neither alert catches it
```

The bug is that `transit-motion-nightvision` is *simultaneously*:

- A background-state-during-window Condition (eval-loop-applicable)
- A motion-response Condition (binding-applicable)

It wears two hats. The two firing paths use different state
(eval-loop ignores motion events; binding ignores eval-loop ticks).
They produce different effective behaviors. The async dwell on the
other side (`motion-off`) completes at a third schedule. There's no
authoritative answer to "what should the zone's state be at 02:00
MDT given no motion in the last 30 min" — three Conditions all have
opinions and none of them agree.

In the proposed model, the same intent is one declaration:

```
transit-zone.state stack:
  bottom: { computer: "off", source: "default" }                # background
  optional push by Binding(occupancy=true, window=22:30-05:48):
         { computer: "nightvision-on", source: "motion-bind", ttl: 5m }
```

One Computer is active at a time. Motion pushes the override
(refreshing TTL on each new event); no motion for 5 min → TTL
expires → stack pops → background "off" wins. No async dwell
fighting with eval-loop ticks. No fights to resolve. The reconciler
just reads the top of the stack each tick.

## The data model

`Activation` is a proto message — it serves both as the in-process
struct and the wire format for the `PushActivation` RPC (see
"Uniform event source" below). Three small enums replace
free-form strings for grouped sets:

```protobuf
enum AxisKind {
  AXIS_UNSPECIFIED       = 0;
  AXIS_STATE             = 1;
  AXIS_BRIGHTNESS        = 2;
  AXIS_COLOR_TEMPERATURE = 3;
  AXIS_COLOR             = 4;
}

enum SourceKind {
  SOURCE_UNSPECIFIED = 0;
  SOURCE_BACKGROUND  = 1;  // bottom of stack; never expires
  SOURCE_TIME_WINDOW = 2;
  SOURCE_BINDING     = 3;
  SOURCE_ALERT       = 4;
  SOURCE_BUTTON      = 5;
  SOURCE_MANUAL      = 6;  // direct RPC from operator
}

enum PushPolicy {
  PUSH_POLICY_UNSPECIFIED = 0;
  PUSH_POLICY_REFRESH     = 1;  // same source re-pushes refresh TTL in place
  PUSH_POLICY_REPLACE     = 2;  // same source re-pushes replace existing entry
}

message Activation {
  string computer_name = 1;          // resolved via Computer registry
  map<string, string> args = 2;       // computer-specific config
  SourceKind source_kind = 3;
  string source_name = 4;             // operator-authored identifier
                                      // (e.g. "my-motion-binding")
  google.protobuf.Timestamp pushed_at = 5;
  google.protobuf.Duration ttl = 6;   // 0 = no expiration (background)
  int32 priority = 7;
  PushPolicy push_policy = 8;
}
```

`source_name` stays a string because operator-authored identifiers
are open-ended. `computer_name` stays a string because the Computer
registry is runtime-extensible — an enum would force a code change
per new Computer.

In-process, each Activation is wrapped to cache the resolved
Computer function pointer so the reconciler's hot loop avoids the
per-tick registry lookup:

```go
// runtimeActivation embeds the wire form and caches the resolved
// Computer. The cache is populated once at push time; the
// reconciler's per-tick loop calls runtime.resolved.Compute(...)
// directly — one indirection, no map access per tick.
type runtimeActivation struct {
    *iotv1proto.Activation                // declarative declaration
    resolved   computer.Computer          // cached function pointer
    id         string                     // stable id (source_kind+source_name) for refresh/replace
}

// axisStack holds the prioritized list of Activations for one axis.
// No "currently running" Computer — top() picks fresh each tick.
type axisStack struct {
    entries []*runtimeActivation
    timers  map[string]Timer  // keyed by activation id; TTL wakeups
}

// zonePolicy is the per-zone runtime view. Built lazily from
// declared sources (Conditions, Bindings, etc.) and live push
// events.
type zonePolicy struct {
    zone   string
    stacks map[iotv1proto.AxisKind]*axisStack
}
```

The `top()` function — the only "selection logic" the reconciler
needs — is a small linear scan:

```go
func (s *axisStack) top(now time.Time) *runtimeActivation {
    var best *runtimeActivation
    for _, a := range s.entries {
        if a.expired(now) {
            continue
        }
        if best == nil || a.Priority > best.Priority ||
           (a.Priority == best.Priority && a.PushedAt.After(best.PushedAt)) {
            best = a
        }
    }
    return best
}
```

Typical n is 1-5 entries per axis; 4 axes × 25 zones × one walk per
60s tick = ~500 comparisons per minute. Negligible. n > 10 is
implausible for this use case — if profiling ever shows the scan as
a hot spot we swap to a heap, but pre-optimizing is over-engineering.

The reconciler's per-zone tick — short enough to fit on one screen:

```go
func (r *Reconciler) ReconcileZone(ctx context.Context, zone string, now time.Time) error {
    policy := r.policies[zone]
    target := AxisTarget{}
    for axis, stack := range policy.stacks {
        // 1. Lazy expiration on read — the correctness primitive.
        stack.removeExpired(now)
        // 2. Pick top (linear scan above).
        top := stack.top(now)
        if top == nil {
            continue
        }
        // 3. Evaluate the cached function pointer.
        val, err := top.resolved.Compute(ctx, now, r.location, top.Args)
        if err != nil {
            r.recordComputeError(zone, axis, top.SourceName, err)
            continue
        }
        target[axis] = val
    }
    // 4. Compare to last, flush on delta. Same shape as today's
    //    applyDesired cache but at zone granularity.
    if !target.Equal(r.lastApplied[zone]) {
        if _, err := r.zk.ApplyValues(ctx, target.ToProto(zone)); err != nil {
            return err
        }
        r.lastApplied[zone] = target
    }
    return nil
}
```

The reconciler is **not** a polling background scanner. It runs only
on event wakeups, with three trigger sources:

| Trigger | When | Mechanism |
|---|---|---|
| **Push** | An Activation is added | `PushActivation` RPC or in-process matcher / alert / time-window handlers |
| **TTL expiration** | An Activation's TTL elapses | `afterFunc` timer scheduled at push time, fires a reconcile-wakeup |
| **Periodic tick** | Safety-net resync | The existing `evaluate()` 60s loop |

The TTL timer is a wakeup optimization — `afterFunc` triggers a
reconcile within milliseconds of expiration so a 5m motion override
pops at exactly the 5m mark, not at the next 60s tick. Lazy
expiration in `top()` is the correctness primitive: even if a timer
fails to fire (clock skew, process restart), the next reconcile
read still filters the expired entry.

Push semantics:

```go
func (r *Reconciler) PushActivation(zone string, axis iotv1proto.AxisKind, act *iotv1proto.Activation) error {
    comp, ok := computer.Get(act.ComputerName)
    if !ok {
        return fmt.Errorf("unknown computer %q", act.ComputerName)
    }
    s := r.policies[zone].stacks[axis]
    id := activationID(act.SourceKind, act.SourceName)

    // PUSH_POLICY_REFRESH (default): same source re-push updates
    // PushedAt + TTL in place. PUSH_POLICY_REPLACE: swap entry
    // entirely so new args take effect.
    if existing := s.find(id); existing != nil && act.PushPolicy == iotv1proto.PushPolicy_PUSH_POLICY_REFRESH {
        existing.PushedAt = act.PushedAt
        existing.TTL = act.TTL
    } else {
        s.entries = append(s.entries, &runtimeActivation{Activation: act, resolved: comp, id: id})
    }
    r.scheduleTTL(zone, axis, id, act.TTL)
    r.scheduleReconcile(zone)
    return nil
}
```

## Uniform event source — Activation in proto + PushActivation RPC

The same `Activation` message is the wire format for a new RPC that
makes every push source — internal or external — go through one shape:

```protobuf
service EventReceiverService {
  // ... existing methods (Alert, ActivateCondition, Epoch) ...

  // PushActivation is the canonical push entry. Internal callers
  // (matcher, alert handler, time-window scheduler) call the
  // in-process Reconciler.PushActivation directly with the same
  // proto type — no actual gRPC round-trip for in-process pushes.
  // External callers (operator scripts, Home Assistant, etc.) go
  // through the wire. Same shape; different transport.
  rpc PushActivation(PushActivationRequest) returns (PushActivationResponse);
}

message PushActivationRequest {
  string zone = 1;
  AxisKind axis = 2;
  Activation activation = 3;
}
```

Three benefits stack:

- **Uniform event source.** Binding match, Alert fire, time-window
  open, AND `grpcurl push-activation ...` from the operator's
  laptop all produce the same shape. The reconciler doesn't branch
  on source kind for the apply logic; it just reads the top of the
  stack.
- **Test injection becomes natural.** A CI smoke test that pushes a
  synthetic motion event is one RPC call away.
- **External integrations.** Home Assistant, Node-RED, phone
  shortcuts — all can call one RPC instead of multiple per-source
  paths.

## How current concepts map

| Current | New |
|---|---|
| `Condition.Remediation.active_state` (state-applying) | A Computer that returns `ApplyValues{State: X}`; lives at the bottom of `state` stack as the background, OR pushed into the stack via the source that activated it (Binding, time-window, alert). |
| `Condition.Remediation.active_compute: circadian` | An Activation for `color_temperature` axis with `Computer: "circadian"`. Lives at the bottom of the CT stack as background. |
| `Condition.Remediation.time_intervals` | The gating condition for a *time-window* push source. When the window opens, the Activation is pushed; when it closes, removed. Operator authoring stays the same shape; reconciler interprets the window as push/pop trigger rather than per-tick gate. |
| `Condition.Remediation.active_scene: dusk-full` | The Scene maps to a *bundle of Activations across multiple axes* — `state` gets ON, `brightness` gets FULL, `color_temperature` gets EVENING. Sugar for "push these four Activations at once with the same source/priority/TTL." |
| `Binding.event → Condition.activate` | A Binding push: when the binding matches, push the referenced Condition's Activations into the matching axis stacks with the configured TTL. Refresh on subsequent matches. |
| `Alert.matches → Condition.activate` | Similar to Binding push, but the source is alert firing/resolution. Alert firing pushes; alert resolution pops. |
| `Spec.Schedule` cron | Same shape as time_intervals — push/pop trigger. Cron-only fires push at cron time; pop on a corresponding cron or fixed TTL. |

## Concrete examples

### Foyer at night

```
ZonePolicy: transit-zone
  state:
    Background: { computer: "off" }
    SourcePushes:
      - Binding(transit-pir, occupancy=true) during window(22:30-05:48 MDT):
          → { computer: "on", ttl: 5m, refresh_on: ["motion"], priority: 50 }
      - Binding(transit-pir, occupancy=true) during window(18:00-21:00 MDT):
          → { computer: "on", ttl: 5m, refresh_on: ["motion"], priority: 50 }
  scene:
    Background: { computer: "scene/none" }
    SourcePushes:
      - same windows as above, scene set to "nightvision" or "dusk-full"
        respectively
```

Overnight at 02:00 MDT with no motion for 10 min: transit-zone.state stack
has only the background "off" remaining; reconciler applies state=off
exactly once. No flicker. PIR's no-motion heartbeats don't even
participate — they're occupancy=false events with no push action.

### Bedside-zach pre-bedtime + bedtime nightvision

```
ZonePolicy: bedroom-zone
  state:
    Background: { computer: "off" }
    SourcePushes:
      - TimeWindow(sun_relative sunset-2h..sunset OR 18:00-22:00 MDT):
          → { computer: "motion-on" with ttl on Binding, priority: 50 }
      - Alert(epoch=sunset, when_gate -10m..0):
          → { computer: "on", ttl: 4h, priority: 60 }
  color_temperature:
    Background: { computer: "circadian" } during window(04:00-22:00 MDT)
    Background: { computer: "static: 2200K" } during window(22:00-04:00 MDT)
  color:
    Background: { computer: "none" }
    SourcePushes:
      - Binding(motion) during window(22:00-04:00 MDT):
          → { computer: "static: #FF0000 (nightvision red)", ttl: 5m, priority: 60 }
```

Motion at 2am: pushes red color for 5min; lamp goes red+VERYLOW;
5min later pops; lamp goes back to off (because state-axis background
is "off"). No competing eval-loop fires; no async dwell completion.

### Heater (safety-critical, simple)

```
ZonePolicy: heater-zone
  state:
    Background: { computer: "query: temp < threshold", refresh: 60s }
```

That's it. The Background Computer is a query Computer that
continuously evaluates "is it cold enough?" and returns on/off.
Nothing pushes overrides; the heater is purely background-driven.
Same query Computer that powers today's heater alert path.

This is why the model is safe for safety-critical zones: simple
zones look simple. The complexity comes from override stacks, which
heaters don't need.

### Pond pump

```
ZonePolicy: pump-zone
  state:
    Background: { computer: "query: water_signal > 0.5", refresh: 60s }
```

Same shape. The query Computer FAILS SAFE — if the PromQL evaluator
errors or the metric is missing, the Computer returns `state=off`
(or undefined, which the reconciler treats as "no claim" → falls
through to a final off default). The "never run dry" invariant is a
property of the Computer's failure mode, not of the reconciler.

## Composition with multiple Computers per axis (forward extension)

The same axis can have multiple Activations from different sources,
resolved by priority. Today this is implicit (last-write-wins);
explicit priority makes operator intent visible.

```
ZonePolicy: work-zone
  brightness:
    Background: { computer: "cloud_cover", priority: 10 }    # baseline
    SourcePushes:
      - Button(dimmer): { computer: "const_brightness: dim", ttl: 30m, priority: 80 }
      - Alert(focus-mode): { computer: "const_brightness: full", ttl: 4h, priority: 70 }
```

cloud_cover is the always-on baseline. Button or alert can override
for a while. After TTL, the override pops and cloud_cover wins again.
No fight; explicit precedence.

### Top wins, not fold

The stack model is **"top wins per tick"** — the highest-priority
non-expired Activation produces the absolute axis value. The
reconciler doesn't fold multiple contributors together. If two
Activations claim the same axis, the loser is not consulted; its
output is simply not applied.

For most use cases this is right. Cloud cover replacing the
brightness baseline → top wins (cloud_cover is the brightness
Computer when active). Motion overriding for 5min → top wins.
Button press → top wins.

For "+/- delta" use cases — e.g. "comfort bias +200K in cold
weather on top of whatever circadian says" — the stack doesn't
fold. Two ways to express the same intent:

1. **Operator authors absolute ranges in the Computer's args.**
   The Computer takes `base + scale` and produces the final value:

   ```yaml
   active_compute: cloud_cover_brightness
   active_compute_args:
     base: 0.5
     scale: 0.4   # 0% cloud → 0.5; 100% cloud → 0.9
   ```

   This expresses "delta" as part of the Computer's parameterization,
   not the stack composition.

2. **Future: extend `Compute()` to take `zoneStatus`.** Then a
   `relative_adjust` Computer can read the current brightness from
   Zone Status and produce `current + 0.02`. Backward-compatible
   because existing Computers ignore the extra arg. Defer until the
   first Computer that needs it lands.

If concrete use cases push for genuine fold composition later (sum
of contributors, multiply, min/max), add an explicit reduce
semantic at that point. Don't pre-build it.

## Discrete vs continuous output: separate Computers, not modes

The `query` Computer today is intentionally discrete: PromQL
thresholded against zero, pick one of two operator-supplied bundles.
That shape is right for heater hysteresis and pump water-present —
both safety-critical, both "above/below a line." Keep it that way.

For continuous-output use cases — cloud-coverage-driven brightness,
PromQL-scalar-to-axis-value mappings — add a separate Computer
alongside `query`:

```yaml
active_compute: prom_scalar
active_compute_args:
  query: avg_over_time(cloud_coverage_percent[10m])
  output_axis: brightness
  in_min: 0
  in_max: 100
  out_min: 0.5
  out_max: 0.9
```

Same Computer interface; different code inside. Both ship; operators
pick by use case. Keeps the discrete-vs-continuous distinction
visible at the authoring layer instead of hiding it behind a `mode`
arg on a single overloaded Computer.

`ApplyValues.BrightnessValue float64` and `ColorTemperatureKelvin
int32` exist already (added in the fade work for canonical.go) — no
new ApplyValues shape needed for continuous Computers; they populate
the existing continuous fields and leave the discrete enum fields
alone.

## Source kinds (the things that can push)

A small enumeration:

| Source | Push trigger | Pop trigger |
|---|---|---|
| **Background** | Always present, no push/pop | n/a |
| **TimeWindow** | Window opens | Window closes |
| **Binding** | Match event fires | TTL expiration; refresh on each match |
| **Alert** | Alert fires | Alert resolves; or TTL |
| **Button** | Press event | TTL; long-press might extend |
| **Manual override** | API call | API call to clear; or TTL |

The "source" is also the unit of refresh. A binding-pushed activation
gets refreshed each time its binding re-matches. This is exactly
today's `MinDuration` semantic but framed as TTL refresh on each
event.

## What the reconciler does NOT do

- **Doesn't trigger Computer side effects.** Computers stay pure.
  The fade Computer's snapshot store goes away — fade becomes
  `f(now, args, zone_state) → value` with `args` carrying the start/
  duration/from/to, and `zone_state` providing the current value to
  interpolate from when relevant. The "remember where we are in the
  envelope" lives in `args.start_at` + `now`, not in a separate
  snapshot.

- **Doesn't dispatch event-driven Conditions imperatively.** All
  paths converge at the per-tick reconciler. Events just push
  Activations and schedule a near-immediate reconcile.

- **Doesn't have a per-Condition apply cache.** The cache, if any,
  is per-zone target → last-applied. One entry per zone, not one
  per (Condition, zone). Simpler shape.

## Migration plan

The phases were renamed during implementation; mapping from this doc's
original labels to the actual delivery labels:

| This doc (original) | Delivery name | Status |
|---|---|---|
| Phase 1 | (pre-Phase-A baseline) | DONE — shadow resolver v0.8.7 + alert v0.8.8 |
| Phase 2 | Phase A — design | DONE — this doc + user review |
| Phase 3 | Phase B — canary writer | DONE — v0.9.0–v0.9.5 |
| Phase 4 | Phase C — lighting expansion | IN PROGRESS — 4 of N zones migrated as of 2026-05-24 |
| Phase 5 | Phase D — safety-critical | PENDING — see `docs/phase-d-migration.md` |
| (new)   | Phase E — imperative retirement | PENDING — after Phase D |

### Phase A — design (done)

- `docs/reconcile-design.md` (this doc).
- Small-step variant chosen: extend existing `Condition` Remediation,
  no new ZonePolicy CRD. Reconciler-managed view lives on
  `Zone.Status.ReconcilerStack`.

### Phase B — canary writer (done, v0.9.0–v0.9.5)

- `modules/conditioner/stack.go` — axis-stack, runtime Activation,
  per-zone policy, REFRESH/REPLACE push semantics.
- `modules/conditioner/reconciler.go` — per-zone writer with
  lastApplied cache, TTL timers, RemoveActivation, Status reflection.
- `modules/conditioner/bridge.go` — eval-loop bridge for declared
  Conditions (TimeInterval-driven + active_compute) AND imperative
  bridge for binding/alert paths through `activateRemediation`.
- `-conditioner.reconcile-zones=<csv>` flag.
- Sensor-bound Condition skip (Bindings on occupancy / water_leak
  etc. don't bridge their referenced Condition from the eval loop —
  TimeIntervals are gates for those, not triggers).
- `TTLAdvisor` interface; fade declares its own TTL.
- Source-kind threading: bindings push at priority 100, alerts at 200.
- `Zone.Status.ReconcilerStack` + `last_reconciled_at` reflection.

### Phase C — lighting expansion (in progress)

Add lighting zones one at a time via the `-conditioner.reconcile-zones`
flag. As of 2026-05-24: `bedside-zach,foyer,office,living-area`.
Remaining lighting zones in the deployment (axel, bedroom, etc.)
to migrate as appetite + soak windows allow.

Verify per zone:
- `iotcontroller_reconciler_stack_depth{zone="<zone>"}` populates.
- `iotcontroller_reconciler_top_changed_total{zone="<zone>"}` rate
  doesn't sustain above ~2/min unless multiple Conditions overlap on
  the same axis (within-tick cosmetic; not a real ping-pong).
- `iotcontroller_zonekeeper_state_changes_total{zone="<zone>"}` rate
  doesn't increase post-migration.
- `Zone.Status.ReconcilerStack` is populated when Conditions are
  in-window.

### Phase D — safety-critical (pending)

Heaters and pond pump migrate only after Phase C lighting zones
soak through a multi-day window. The plan + zone-topology question
(unify sensor + actuator zones?) is its own document:
[`docs/phase-d-migration.md`](phase-d-migration.md).

### Phase E — imperative retirement (pending)

After all zones migrate, delete `activateRemediation`'s imperative
branches (`applyDesired`, `condState` cache, `forceDeactivate`'s
imperative branch). The bridge becomes the only writer. `Condition`
CRD stays as the operator-facing surface (per Phase A small-step
decision) or is replaced by ZonePolicy if value materializes.

## Open design questions

1. **TTL refresh semantics.** When a binding matches and there's
   already an Activation from that source in the stack, do we
   refresh the existing entry's `PushedAt` (keep its other
   properties), or replace it entirely? Current draft says refresh.
   Pro-replace: lets the binding push different `args` per event
   (e.g. brightness step). Pro-refresh: simpler, matches MinDuration
   semantics today.

2. **What if a Computer's gate depends on its position in the
   stack?** E.g. circadian wants to fire only if no override is
   active above it. Today's Computers don't know about the stack.
   Probably right answer: they don't need to. The reconciler picks
   the top; circadian only runs when it's the top.

3. **How does fade interact?** Fade's natural shape is "interpolate
   from current toward target over duration." In the stack model,
   fade is a Computer pushed onto an axis with a TTL = duration.
   When TTL expires, fade pops and the stack restores. Mid-fade
   refresh = restart interpolation from current value (clean).
   Mid-fade pop (e.g. button press) = fade pops, button push
   becomes top, button's Computer's value applied. Clean.

4. **Disabled Conditions.** Today `Spec.Enabled=false` skips the
   Condition. In the new model: the Activation isn't pushed if its
   declaration is disabled. Operator-facing toggle remains.

5. **Audit trail.** Per-(zone, axis) push/pop log so operators can
   reconstruct "why is zone X displaying color Y right now?" → grep for
   the last push on `<zone>.color`. Cheap to add; valuable for the same
   reasons today's status-drift alert is valuable.

6. **Computer signature change?** Today Computers take `(now,
   location, args) → ApplyValues`. The stack model would benefit
   from `(now, location, args, zone_status) → AxisValue` so a
   Computer can interpolate from current. Backward-compat: the new
   signature is a superset; today's Computers ignore the new arg.

## Relationship to fade-design.md and circadian-design.md

Both designs already speak in Computer-output terms. circadian
produces a CT value; fade interpolates between values. Neither
needs to change semantically — they just become Activations in the
new stack model rather than Conditions in the imperative one.

The `_to_compute` argument grammar parked in fade-design.md
(forward extension #1) becomes free: a fade Activation's `to`
argument can reference another Computer (by name) that the
reconciler dereferences at fade-seed time. The stack model gives
us a clean place to express "fade to whatever circadian says right
now."

## Out of scope (for this doc)

- The eventual replacement of Scene CRs with stack declarations.
  Scenes might stay as a sugar layer ("apply these N Activations
  at once") or go away entirely. Decision deferred until canary
  is up.

- Cross-zone composition (e.g. "all lights in this room follow
  the entry-zone's state"). Real use case eventually; not v1.

- Per-device override (e.g. "this one bulb in the zone is
  scheduled out for maintenance"). Out of scope; device-level
  intent lives in Device CRs already.

- HVAC and audio domains. The model generalizes (the user's
  intuition that the pattern matters beyond lighting), but those
  domains have their own per-axis vocabularies that need their
  own design passes.
