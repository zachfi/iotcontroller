# Reconcile-Loop Architecture — Active Computer Stack per Zone

Status: design draft, no implementation. Captures the architectural
direction agreed in the 2026-05-20 / 2026-05-21 conversations. The
shadow resolver shipped in v0.8.7 is the read-only first step; this
doc lays out the model the resolver would eventually become the
writer for.

The core question this doc is trying to answer:

> What's the smallest mental model that makes the current imperative
> tangle disappear, *without* forcing us to grow another tap-on-the-
> harvester workaround the next time we add a Computer?

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

Phase 1 — already shipped:
  - Shadow resolver runs alongside imperative path, observes
    composition disagreements (v0.8.7).
  - IOTConditionConflict alert on multi-contributor axes (v0.8.8).

Phase 2 — design:
  - This doc.
  - User feedback / iteration.
  - Decide: small step (add `claims` + `ttl` fields to Remediation,
    keep existing CRD shape, teach the resolver to interpret them)
    OR bigger step (new `ZonePolicy` CRD, deprecate Condition for
    new uses).

Phase 3 — canary:
  - Implement the resolver as a writer for one canary zone
    (a low-stakes, recently-rebuilt single-lamp zone is the obvious
    pick; operators choose per deployment).
  - Behind a `-conditioner.reconcile-zones=<csv>` flag.
  - Run alongside imperative path on other zones; compare via
    metrics.
  - Heaters and pump explicitly OUT of the canary set.

Phase 4 — expand:
  - Migrate the rest of the lighting zones one at a time once the canary has
    demonstrated correctness for a week including edge cases.
  - Lighting zones first; heaters and pump LAST.

Phase 5 — safety-critical:
  - Heaters and pump migrate only after lighting zones have run
    cleanly through a multi-day period that includes the safety-
    critical zones' real edge cases (cold snap for heaters; sensor
    failure modes for pump).

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
