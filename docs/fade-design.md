# Fade — Time-Bounded Interpolation of Zone State

Status: drafted, no implementation. Supersedes the earlier
`fade-to-off-design.md` and `motion-light-design.md` drafts, which
arrived at the same primitive from two anchors (window vs event) and
duplicated most of the machinery.

## What this unlocks

A single Computer named `fade` covers every shape of "interpolate one
or more zone dimensions from now-or-an-explicit-start to a target
over some duration, optionally settling into a terminal state when
done." The same machinery serves:

1. **Scheduled fade-to-off.** Living-area at 23:27 ramps down to
   VERYLOW over three minutes, then flips off. Replaces today's
   cron-fired hard-cut `<zone>-auto-off` Condition.
2. **Motion-light fade-off.** Foyer goes dark thirty seconds after
   the no-motion countdown expires, instead of cutting. New motion
   during the fade cancels via the existing activation path.
3. **Sunset CT + brightness drift.** Office walks from
   `COLOR_TEMPERATURE_LATEAFTERNOON` + `BRIGHTNESS_FULL` to `EVENING`
   + `BRIGHTNESS_DIM` over twenty minutes as the sun goes down,
   without two competing Remediations on independent timelines.
4. **Color fade.** Living-area drifts from a daylight-white hex to a
   candle-amber hex over twenty minutes. Today's `ApplyValues.Color`
   is a string the existing Computers can't interpolate; the fade
   Computer adds RGB lerp.
5. **Wake-up brighten.** Office walks from `BRIGHTNESS_LOW` to
   `BRIGHTNESS_FULL` over thirty minutes anchored to 07:00 local.
   Same primitive in reverse — `terminal_state` is omitted because
   the zone is already on.

The existing `ramp` Computer collapses into a deprecated alias.

## What already exists

The audit found that most of the perceived "motion-light feature" and
some of the "fade-to-off feature" already compose from primitives in
the tree. The fade Computer is genuinely the only new piece.

- **Sustained-on / countdown-after-no-motion / countdown-reset.**
  `Binding.spec.event.min_duration` (`api/v1/binding_types.go:78`)
  handles all three at the matcher layer. The matcher debounces
  per-`(property, device)`, resets the timer on value transitions,
  and dispatches the target Condition only after the dwell.
  Documented in `docs/bindings.md:72-117`.

  **Field gotcha — the matcher and the legacy `occupied()` fallback
  both fire today.** While the dwell is unsatisfied,
  `Matcher.FindCondition` returns `""` (see `bindings/match.go:166-180`,
  `bindings/debounce_test.go:62-68`). The Z2M router treats `""` as
  "no Binding matched" and runs the legacy `occupied()` fallback
  (`routers/zigbee2mqtt/zigbee2mqtt.go:170-174`), which calls
  `OccupancyHandler` and stamps the zone into `ZONE_STATE_OFFTIMER`
  with the 7m goroutine timer. So a motion sensor with `min_duration:
  2m` on its `occupancy=true` Binding gets both: the matcher's 2m
  debounce starts (and never reaches `fired` if the zone has nothing
  else to wake it up), *and* the legacy path turns the lights on
  immediately and arms its own 7m timer.

  Observed in production on the foyer sensor after a fresh
  deployment: `bindings_debounce_events_total{outcome="fired"}` was
  zero on the on-side Binding, while `state_changes_total{state=
  "ZONE_STATE_OFFTIMER",zone="foyer"}` kept incrementing. The
  user-visible lights worked — just via the path the migration was
  trying to leave behind.

  The fix is the OFFTIMER deletion plan below: the matcher should be
  the *only* path that owns occupancy events, with no fallback. Until
  then, MinDuration on `occupancy=true` is decorative; only the
  `occupancy=false` side (which has no legacy counterpart in the
  router) actually fires.
- **Time-window gating.** `time_intervals` on each Remediation, plus
  `sun_relative` for solar-anchored windows. Already wired through
  `conditioner.withinActiveWindow`.
- **State-only applies.** `ApplyValuesRequest.State` is honored
  end-to-end (`zonekeeper.go:222-225` only writes State when
  non-UNSPECIFIED). The fade's terminal-state apply is a no-cost
  reuse.
- **Per-Remediation Computer dispatch.** The Conditioner already
  calls a named Computer per tick with a 60s default
  `EvaluationInterval`, and the registry (`computer/computer.go`)
  supports re-registering names — so `ramp` can stay as an alias
  for `fade` with no migration friction.
- **applyDesired idempotency cache.** Same-(state, scene) re-applies
  are absorbed (`conditioner.go:73-111`). The fade's ~3 ticks across
  a 3-minute duration produce 1–2 distinct brightness steps; the
  cache handles the rest.

What's *missing* and load-bearing for fade:

- **Per-(Condition, zone) snapshot state.** A small map in the
  Conditioner that remembers "we started fading X to Y at time T,
  beginning from snapshot S." Lifetime: from seed (window-open or
  ActivateCondition, whichever applies) to terminal apply or
  invalidation.
- **Out-of-band apply detection.** The Conditioner today has no
  signal when a button press or other RPC moves a zone outside its
  control. The fade's "operator pressed a button mid-fade → cancel"
  behaviour depends on this. See [Out-of-band invalidation](#out-of-band-invalidation).
- **RGB interpolation.** The existing Computers don't lerp colors.
  `rotate_colors` validates hex but doesn't blend.

## The Computer: `fade`

```yaml
active_compute: fade
active_compute_args:
  # Target axes — any subset. Omitted axes are left alone.
  brightness_to: BRIGHTNESS_VERYLOW
  color_temperature_to: COLOR_TEMPERATURE_EVENING
  color_to: "#FFB070"

  # Optional explicit start values. If a `_to` is set without a
  # matching `_from`, the start value is captured from the zone's
  # current Status at seed time (the "from: current" semantics from
  # the original fade-to-off draft).
  brightness_from: BRIGHTNESS_FULL
  color_temperature_from: COLOR_TEMPERATURE_LATEAFTERNOON
  color_from: "#FFEFD5"

  # Anchor selects where t=0 comes from.
  #   window: today's start_at in the configured timezone (default).
  #   event:  the most recent ActivateCondition for this Condition.
  anchor: window
  duration: 3m

  # Required when anchor=window. Ignored when anchor=event.
  start_at: "23:27"
  timezone: America/Denver

  # Optional. When set, at progress >= 1.0 the apply also carries
  # this State. The apply path treats State and the final axis values
  # as one atomic RPC.
  terminal_state: off
```

### Axes

Any combination of `brightness`, `color_temperature`, and `color`
fades together on the same progress curve. Operator omits axes they
don't want to touch.

- **Brightness** uses the `brightnessPhysical` perceptual order
  (`ramp.go:65-72` today) so VERYLOW→FULL means "perceptually
  brighter," not "smaller enum integer." Existing semantics.
- **Color temperature** uses `colorTempPhysical`
  (`ramp.go:78-84`) — cool→warm.
- **Color** is hex RGB. Interpolation is linear in sRGB-decoded
  linear-light space (decode → lerp → re-encode), then formatted
  back to a 6-digit hex string. Approximating in raw sRGB
  produces visibly muddy midpoints on saturated transitions; the
  linear-light step is cheap and the operator-visible artifact is
  large enough to be worth it.

Validation: if `_to` is set without a matching axis being
representable (e.g. a zone whose actuators don't accept color), the
Computer still emits the axis — the zonekeeper / device layer is
responsible for ignoring it. This matches today's `ApplyValues`
contract: zero-value axes are skipped, non-zero axes are sent.

### Anchors

```
anchor: window    →  t=0 := today's start_at in `timezone`
anchor: event     →  t=0 := time of the most recent ActivateCondition
                            for this Condition
```

Both anchors produce the same machine state once seeded:
`{startedAt, snapshot_brightness, snapshot_ct, snapshot_color}`.
Stored in a per-(Condition, zone) map in the Conditioner. The only
difference between modes is *when the entry is written*:

- **Window:** lazily, on the first eval tick where the Computer
  finds the window active and no entry exists yet. Snapshot is the
  zone's `Status` at that moment. The entry's `startedAt` is set to
  the window's nominal `start_at`, not the tick time — so if an eval
  is delayed a few seconds the curve still ends at the right moment.
- **Event:** eagerly, in the `ActivateCondition` RPC handler before
  the first eval tick can run. Snapshot is the zone's `Status` as of
  the activate call. The entry's `startedAt` is the activate time.

A re-activation in event mode clears any existing entry and seeds a
fresh one, restarting the curve from current brightness. Same call
chain as the initial seed.

### Terminal state

At progress ≥ 1.0, the returned `ApplyValues` carries the final axis
values *and* `State = terminal_state` (when set). The conditioner's
existing apply path (`evaluator.go:225-231`) already pipes State
into `ApplyValuesRequest`, so this is a no-cost wire-up.

The snapshot entry is cleared after the terminal apply lands. In
window mode the next day's window-open seeds a fresh entry; in event
mode the next `ActivateCondition` does.

`terminal_state` is optional. Wake-up brighten and sunset CT drift
don't use it.

### Out-of-band invalidation

The audit found the Conditioner is blind to applies that didn't
originate in it (button presses, alert RPCs from other sources,
direct `SetState` calls). For fade this manifests as: operator
presses ON in the middle of a fade, the lights jump to FULL, the
*next eval tick* (≤60s later) reads stale snapshot, overwrites
the operator's intent.

The fix has to be cheap and not add a kube watcher we don't already
need. Two viable options, in order of preference:

1. **Status read inside `applyDesired`.** Before the cache check,
   read the Zone CR's `Status.State`/`Status.Brightness`. If they
   disagree with the snapshot's *current expected* values (where the
   fade should be at this point on the curve), drop the snapshot and
   treat the next tick as a fresh seed. One extra kube read per
   applyDesired call. The conditioner already has a kube client.
2. **Zonekeeper push.** A new "applied" event channel on the
   ZoneKeeper service, tagged with the originating actor (conditioner
   vs router vs alert). Conditioner subscribes, invalidates snapshots
   for events it didn't emit. Cleanest, but requires a proto change
   and a long-lived stream.

Option 1 is the recommendation. The poll happens only on the cache
check (every eval tick + every activate), so cost is bounded.
Option 2 is a follow-up if the Conditioner ever needs other
cross-process state hygiene that polling can't cover.

### Snapshot lifecycle summary

```
seed:
  window mode: first eval tick inside the time_intervals window
               where no entry exists for (cond, zone)
  event mode:  ActivateCondition RPC handler, before any tick

evaluate (every tick):
  progress = (now - startedAt) / duration
  if progress < 0:  return ApplyValues{}        # not started
  if progress >= 1: return ApplyValues{final axes + terminal_state}
                    and schedule cleanup        # done
  else:             return ApplyValues{interpolated axes}

invalidate (before each tick's applyDesired check):
  if zone Status disagrees with the curve's current expected value:
    drop entry; next tick re-seeds from new current

cleanup:
  after terminal apply lands successfully
  on window close in window mode (entry was never going to fire)
  on Condition deletion (handled by existing CRD watcher cleanup)
```

## Migration: `ramp` → `fade`

`computer/computer.go`'s registry supports re-registering names —
`Register("ramp", fade{})` keeps the old name working alongside
`Register("fade", fade{})`. Existing Remediations using
`active_compute: ramp` with `field: brightness | color_temperature`
continue to work; the new `field` arg is interpreted as a shorthand
for "set `<field>_to`/`_from` from the legacy `to`/`from` args."

Behavior-preservation contract for the alias:

- Default anchor is `window` (matches existing `ramp` semantics).
- Default `from`/`to` for the legacy `field`-style args route through
  the new axis-style fields.
- No `terminal_state` is set unless the operator writes one in.

One release later, the alias gets a deprecation warning. Two
releases later, deletion. No live Remediations use `ramp` outside of
the foyer-motion staging zones yet, so the migration window can be
short.

## Deployment story (deployment_tools)

```jsonnet
// lib/iot/conditions.libsonnet — new helper
withFadeAutoOff(zone, endTime, duration)::
  local m = startMinusDuration(endTime, duration);
  [
    _newCondition(zone + '-auto-off')
    + condition.spec.withRemediations({
      zone: zone,
      active_compute: 'fade',
      active_compute_args: {
        brightness_to: 'BRIGHTNESS_VERYLOW',
        anchor: 'window',
        start_at: m,
        duration: duration,
        timezone: 'America/Denver',
        terminal_state: 'off',
      },
      time_intervals: [{
        times: [{ start_time: m, end_time: endTime }],
        location: 'America/Denver',
      }],
    }),
  ],
```

`withZoneStandardBindings`'s `autoOff` parameter grows an optional
second value: `autoOff='HH:MM'` keeps today's hard-cut behaviour;
`autoOff={ time: 'HH:MM', fade: '3m' }` switches to `withFadeAutoOff`.

Foyer motion migration (steps 1 and 2 from the original
motion-light draft):

```jsonnet
// 1. MinDuration on the existing event-on Binding.
+ binding.spec.event.withMinDuration('2m')

// 2. New event-off Binding pointing at a fade-driven Condition.
binding.new('foyer-motion-off')
+ binding.spec.event.withProperty('occupancy')
+ binding.spec.event.withValue('false')
+ binding.spec.event.withMinDuration('5m')
+ binding.spec.event.selector.withIeee(foyerMotionIeee)
+ binding.spec.withCondition('foyer-motion-off')

// 3. The Condition itself uses an event-anchored fade.
_newCondition('foyer-motion-off')
+ condition.spec.withRemediations({
  zone: 'foyer',
  active_compute: 'fade',
  active_compute_args: {
    brightness_to: 'BRIGHTNESS_VERYLOW',
    anchor: 'event',
    duration: '30s',
    terminal_state: 'off',
  },
})
```

The `foyer-motion-evening` and `foyer-motion-nightvision` Bindings
keep their time-of-day-aware Conditions for activation; only the
off path moves to fade.

## Out of scope / follow-ups

- **OFFTIMER deletion.** Promoted from "dormant cleanup" to "active
  interference" by the MinDuration field-gotcha above. While
  OFFTIMER + the legacy `occupied()` fallback exist, the matcher's
  on-side debounce is silently overridden every time the dwell isn't
  yet satisfied. Once the foyer migrates to the Binding path,
  `ZoneKeeper.OccupancyHandler` (`zonekeeper.go:337-410`) is the
  *only* remaining caller of the OFFTIMER state. Its sole caller in
  turn is `pkg/iot/routers/zigbee2mqtt/zigbee2mqtt.go:291` (the
  `occupied()` method). Delete plan:
  1. Migrate every motion sensor to the Binding pattern in
     deployment_tools (foyer is the canonical one; audit others).
  2. Remove the `occupied()` call to `OccupancyHandler`. Even before
     OFFTIMER itself is removed, this single deletion is what makes
     `min_duration` on `occupancy=true` Bindings actually do what the
     docs claim.
  3. Delete `OccupancyHandler` and the OFFTIMER timer goroutine.
  4. Decide on the `ZoneState_OFFTIMER` enum value — leave for
     transient compatibility, then remove in a major version.
- **Deprecated `Spec.Schedule`.** Zero live callers in
  `tk/environments/iot/`; helpers exist but aren't invoked. Safe to
  delete: `setSchedule` + `runTimer` in `conditioner.go:613-655`
  (~60 lines), the deprecation warner in `evaluator.go:141-158`
  (~20 lines), the CRD field, and the unused helpers in
  `conditions.libsonnet`. Reasonable to bundle with this fade work
  or take separately.
- **Computer-parser consolidation.** `parseHHMM`, `parseBrightness`,
  `parseColorTemp`, `parseColorPool` each appear once. ~30 lines of
  shared `parseEnum[T]` helper would dedupe the two enum parsers.
  Low-priority; not blocking fade.
- **Symmetric fade-on in event mode.** The motion-light proposal's
  fade-on path. Same primitive supports it (omit `terminal_state`,
  set `brightness_to: BRIGHTNESS_FULL`, anchor `event`). Worth a
  separate Binding migration once the foyer-off path is verified
  stable.
