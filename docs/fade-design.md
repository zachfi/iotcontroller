# Fade — Time-Bounded Interpolation of Zone State

Status: drafted, no implementation. Supersedes the earlier
`fade-to-off-design.md` and `motion-light-design.md` drafts, which
arrived at the same primitive from two anchors (window vs event) and
duplicated most of the machinery.

## Mental model: envelopes

The clearest way to reason about every use case below is the
attack/sustain/release envelope from audio synthesis. A zone has a
*gate* — held while a triggering condition is true, released when it
falls — and an envelope that responds to gate transitions:

```
audio synth                      lighting zone
─────────────────────────────────────────────────────────────────────
gate-on (note-on)                gate signal asserts
  └─ Attack: silence → peak        └─ Attack:   off → sustain target
Sustain: held while gate held    Sustain: held while gate held
gate-off (note-off)              gate signal falls
  └─ Release: peak → silence       └─ Release: sustain → terminal
```

Two flavors of gate, both supported by the same primitive:

| Gate source           | "gate-on"                                  | "gate-off"                                  |
|-----------------------|--------------------------------------------|---------------------------------------------|
| **Event-anchored**    | Binding fires `<zone>-on` Condition        | Binding fires `<zone>-off` Condition        |
| **Window-anchored**   | wall-clock `time_intervals` opens          | wall-clock `time_intervals` closes          |

In both cases the on-Condition and the off-Condition are two halves
of one envelope. The Conditioner stays unaware of this — it just sees
Remediations. The envelope abstraction lives at the
operator-facing layer (jsonnet helpers, see [Deployment
story](#deployment-story-deployment_tools)).

Where the audio analogy stops being literal:

- **Decay is mostly dead weight for lighting.** Lighting Attack
  almost always wants to walk directly to sustain; a "blast and
  settle" Decay phase is a niche I'd compose from two back-to-back
  Attack remediations if it's ever needed. The vocabulary is **ASR**
  in practice.
- **Multi-axis is shared-timeline, not per-axis-envelope.** A single
  envelope drives brightness, color temperature, and color all on the
  same progress curve. Independent envelopes per axis would be two
  Remediations on the same Condition — already legal, but I haven't
  seen the operator who wants that.
- **Re-gate while ringing = retrigger.** In monophonic-synth terms.
  The fade snapshot clears on re-activation and seeds afresh from
  current values. No polyphonic stacking.

### Envelope shapes the design unlocks

Every use case the earlier drafts called out is one of these shapes:

1. **Window Release.** Living-area at 23:27→23:30, brightness
   FULL→VERYLOW, `terminal_state: off`. Replaces today's cron-fired
   hard-cut `<zone>-auto-off`.
2. **Event Release.** Foyer's `occupancy=false` Binding fires
   `foyer-motion-off`; brightness current→VERYLOW over 30s,
   `terminal_state: off`. Re-gate (new motion) retriggers; if the
   gate doesn't reopen, the apply lands.
3. **Window Attack→Sustain.** Office at 06:30→07:00, brightness
   LOW→FULL, no `terminal_state`. Wake-up brighten. Subsequent
   Remediations own the daytime sustain.
4. **Window slow drift (no Release).** Sunset CT walk 19:00→19:20,
   CT LATEAFTERNOON→EVENING + brightness FULL→DIM, multi-axis,
   no terminal. Same primitive; it just runs longer and stops at a
   non-silent sustain.
5. **Color drift, same shape as #4.** RGB-axis variant. Daylight-white
   hex → candle-amber hex over 20 min.
6. **Night-light Release.** Like #2 but `terminal_state` omitted and
   `brightness_to: BRIGHTNESS_LOW` — motion brightens, no-motion
   fades to a still-lit Sustain rather than off. Falls out for free
   from the primitive; worth noting because operators ask for it.

The existing `ramp` Computer is a special case of shape #1 with a
single axis; it collapses into a deprecated alias.

## Internal representation: continuous values

The fade Computer surfaces a representation problem worth fixing in
this PR rather than deferring. Today brightness and CT are discrete
enums:

- `Brightness`: VERYLOW..FULL (6 steps)
- `ColorTemperature`: FIRSTLIGHT..EVENING (5 steps)

Color is already continuous (hex RGB). Devices speak continuous
values at the ZCL layer — Hue brightness is 0-254, CT is mireds
(153-500 for white-spectrum bulbs). The enums are an authoring
convenience at the top of the stack; everything below either
quantizes or re-expands.

Smoothing through 5 or 6 steps along a multi-minute curve produces
visible stepping on the wall. A 3-minute fade from FULL to VERYLOW
at 60s eval ticks emits at most 3 samples; over 6 brightness steps
that's 2 distinct levels along the curve. Fade has to interpolate
in continuous space and emit continuous values, or it isn't really
fading.

`ApplyValues` grows two sibling fields:

- `BrightnessValue float64` in [0, 1] — normalized.
- `ColorTemperatureKelvin int32` — Kelvin.

The existing enums stay. They map to canonical points on the
continuous range — `BRIGHTNESS_FULL = 1.0`, `BRIGHTNESS_VERYLOW =
0.05`; `CT_DAY = 5500K`, `CT_EVENING = 2700K`. The mapping is
defined in one place; everywhere else operates on the continuous
value.

What this avoids:

- **A second migration when `circadian` lands.** Continuous CT is
  prerequisite for sun-position-driven color; shipping fade against
  the enum forces every CT consumer to change twice.
- **A second migration for percentage authoring.** "65% brightness"
  eventually wants to be expressible in a Scene directly.
  Continuous internally means that's a Scene-CRD addition later,
  not a plumbing change.
- **Step artifacts in every fade.** The whole point of the primitive.

What this *doesn't* change:

- **Operator authoring.** Scenes keep their enum vocabulary;
  `withZoneStandardBindings` keeps emitting enum-valued Scenes;
  deployment_tools stays unchanged. The new fields are siblings,
  not replacements.
- **Device behaviour.** ZCL already accepts continuous; we just
  stop quantizing on the way down.
- **Status reporting.** Zone Status keeps publishing the enum
  field for backward compatibility; finer-grained consumers
  (Grafana dashboards, future percentage authoring) read the
  continuous field.

Cost of locking this in: two proto fields, a `canonical.go` with
the enum-to-continuous mappings, and an update to the ZoneKeeper
to prefer the continuous field when set. Maybe 150 LOC. Cost of
deferring: every consumer that touches brightness or CT changes
twice across the fade and circadian releases. The pattern is
clearest in the doc when the implementation lands with it.

Alternative considered: ship enum-only fade now, add continuous
later. Rejected because the operator-visible artifact (step-y
fades) undercuts the credibility of the feature on release.

## What already exists

The audit found that most of the perceived "motion-light feature" and
some of the "fade-to-off feature" already compose from primitives in
the tree. The fade Computer is genuinely the only new primitive.

- **Sustained-on / countdown-after-no-motion / countdown-reset.**
  `Binding.spec.event.min_duration` (`api/v1/binding_types.go:78`)
  handles all three at the matcher layer. The matcher debounces
  per-`(property, device)`, resets the timer on value transitions,
  and dispatches the target Condition only after the dwell.
  Documented in `docs/bindings.md:72-117`.

  **Two bugs in the matcher gate.** The dwell as written today
  doesn't actually deliver what the operator and the doc both
  assume — there are two distinct failure modes that present as one
  symptom (foyer sensor's `bindings_debounce_events_total
  {outcome="fired"}` staying at zero while `state_changes_total
  {state="ZONE_STATE_OFFTIMER"}` keeps incrementing). Both are fixed
  before fade ships, in this order:

  1. **Matcher dwell is event-driven, not time-driven.** The fire
     check `now.Sub(entry.firstSeen) >= winner.minDuration` at
     `pkg/iot/bindings/match.go:243` only runs from `debounceDispatch`,
     which only runs from `FindCondition`, which only runs on inbound
     DeviceEvents. There's no timer goroutine or eval-loop poll. A
     single-shot sensor whose first sample opens the window and then
     goes quiet never satisfies the dwell — the window remains open
     in the map but is never sampled. Fix: a `*time.Timer` per
     debounce key, armed in `observeValue` when a candidate Binding
     has non-zero MinDuration, fires at `firstSeen + min_duration + ε`,
     cancelled on value transition.
  2. **Matcher/legacy collision in the router.** `Matcher.FindCondition`
     returns `""` while the dwell is unsatisfied, indistinguishable
     from "no Binding matched." `dispatchEvent` returns `false`, the
     legacy `occupied()` switch arm at
     `pkg/iot/routers/zigbee2mqtt/zigbee2mqtt.go:170-174` fires, and
     `OccupancyHandler` stamps the zone into `ZONE_STATE_OFFTIMER`
     with the 7m goroutine timer. So even *with* bug 1 fixed, the
     legacy path wins the race and the matcher's metric stays at
     zero. Fix: delete `occupied()` + `OccupancyHandler` outright
     (audit 2026-05-19 confirmed zero orphan motion sensors — the
     foyer is the only zoned motion sensor; the one unzoned Aqara
     falls through both paths today).

  Both fixes land before any fade work. Sequencing matters: the
  matcher fix ships first and soaks with the legacy fallback still
  in place, so the production deployment can observe the `fired`
  counter actually moving before the safety net is removed.

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

What's *missing* and load-bearing for fade itself:

- **Per-(Condition, zone) snapshot state.** A small map in the
  Conditioner that remembers "we started fading X to Y at time T,
  beginning from snapshot S." Per-voice state, in synth terms.
  Lifetime: from seed (window-open or ActivateCondition, whichever
  applies) to terminal apply or invalidation.
- **Out-of-band apply detection.** The Conditioner today has no
  signal when a button press or other RPC moves a zone outside its
  control. Mid-envelope retrigger via the activate path is already
  handled; what's missing is the "key-up while attacking" case where
  a non-Conditioner actor moves the zone. See
  [Out-of-band invalidation](#out-of-band-invalidation).
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
  # current Status at seed time.
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
  # as one atomic RPC. Omit for shapes that fade to a non-silent
  # sustain (use cases #3, #4, #5, #6 in the envelope shapes list).
  terminal_state: off
```

### Axes

Any combination of `brightness`, `color_temperature`, and `color`
fades together on the same progress curve. Operator omits axes they
don't want to touch. All three interpolate in continuous space; see
[Internal representation: continuous values](#internal-representation-continuous-values)
above for the why.

- **Brightness** interpolates linearly in normalized [0, 1].
  Enum-valued `_from`/`_to` args resolve to their canonical
  continuous points at seed time (FULL=1.0, VERYLOW=0.05). The
  Computer emits via `BrightnessValue`; the enum-field `Brightness`
  is set to the nearest enum step for backward-compatible Status
  reporting.
- **Color temperature** interpolates linearly in Kelvin space.
  Enum-valued args resolve to canonical Kelvin (DAY=5500K,
  EVENING=2700K, etc.). Emits via `ColorTemperatureKelvin`; enum
  field is set to the nearest step.
- **Color** is hex RGB. Interpolation is linear in sRGB-decoded
  linear-light space (decode → lerp → re-encode), then formatted
  back to a 6-digit hex string. Approximating in raw sRGB produces
  visibly muddy midpoints on saturated transitions; the linear-
  light step is cheap and the operator-visible artifact is large
  enough to be worth it.

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

Re-gating in event mode (a new `ActivateCondition` arriving while a
fade is in progress) clears any existing entry and seeds a fresh one,
restarting the curve from current. Monophonic retrigger.

### Terminal state

At progress ≥ 1.0, the returned `ApplyValues` carries the final axis
values *and* `State = terminal_state` (when set). The conditioner's
existing apply path (`evaluator.go:225-231`) already pipes State
into `ApplyValuesRequest`, so this is a no-cost wire-up.

The snapshot entry is cleared after the terminal apply lands. In
window mode the next day's window-open seeds a fresh entry; in event
mode the next `ActivateCondition` does.

`terminal_state` is optional. Wake-up brighten, sunset drift, color
drift, and night-light Release all omit it.

### Out-of-band invalidation

The audit found the Conditioner is blind to applies that didn't
originate in it (button presses, alert RPCs from other sources,
direct `SetState` calls). In envelope terms: the gate is asserted
and the Conditioner is generating a curve, then a non-Conditioner
actor moves the zone — the Conditioner doesn't see it, and the next
eval tick (≤60s later) reads its stale snapshot and overwrites the
operator's intent.

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

The envelope framing collapses to two jsonnet helpers that operators
write. Each emits the underlying Bindings + Conditions; the operator
never types `active_compute: fade` directly.

### `withMotionEnvelope(zone, sensor, opts)` — event-anchored

Emits the Binding-on + Binding-off + Condition-on + Condition-off
quartet for a motion-driven zone, parameterized in envelope terms:

```jsonnet
withMotionEnvelope('foyer', foyerMotionIeee, {
  attack:  { duration: '0s' },                   // jump to sustain
  sustain: { scene: 'foyer-day' },               // gate-held target
  release: {
    duration: '30s',
    brightness_to: 'BRIGHTNESS_VERYLOW',
    terminal_state: 'off',
  },
  gate: {
    on_dwell:  '2m',                             // MinDuration true
    off_dwell: '5m',                             // MinDuration false
  },
})
```

Underneath: two `binding` resources (one occupancy=true, one
occupancy=false) with their MinDurations; one Condition for the
on-Binding pointing at a Scene apply (Attack duration 0 = current
behavior) or a fade Attack; one Condition for the off-Binding
pointing at the fade Release. The on-side fade is a free extension
the moment `attack.duration` is non-zero — no controller change.

Night-light mode: drop `release.terminal_state` and pick
`release.brightness_to: BRIGHTNESS_LOW`. The Release fades to a
still-lit Sustain instead of off. Same helper signature.

### `withFadeAutoOff(zone, endTime, duration)` — window-anchored Release

Replaces today's `withAutoOffCondition`:

```jsonnet
withFadeAutoOff('living-area', '23:30', '3m')
```

Emits a single Condition with one fade Remediation, window-anchored,
duration starting at `endTime - duration` and ending at `endTime`,
with `terminal_state: off`. Migration: `withZoneStandardBindings`'s
`autoOff` parameter grows to accept either a string (today's
hard-cut, kept for compatibility) or a `{ time, fade }` object.

### Where this lands

`withMotionEnvelope` is the operator's mental model for any
motion-driven zone going forward. `withFadeAutoOff` is the
operator's mental model for scheduled fade-to-off. Neither leaks
the underlying envelope structure into the CRDs — the on-Condition
and off-Condition stay independent Remediations, the Conditioner
stays unaware they're two halves of one envelope, and we can refine
the abstraction at the jsonnet layer without CRD migrations.

## Modulation: input sources

The Computer interface `(now, location, args) → ApplyValues` is the
signature of an audio-synth modulation source. The registry already
has proto-LFOs:

| Computer        | LFO role                                    |
|-----------------|---------------------------------------------|
| `sun`           | slow periodic (one cycle/day)               |
| `rotate_colors` | square-wave (cycles a pool on fixed period) |
| `query`         | sample-and-hold (PromQL → value)            |
| `fade` (new)    | envelope generator (ASR with anchor)        |

What's missing is *chaining*. Every Computer's output goes straight
to ZoneKeeper today; `fade`'s `_to` is always a literal.

The operator-facing analog of a modulation matrix exists already.
`withTimeAwareCondition` in deployment_tools picks one of N
Conditions per time-of-day window; each Binding activates the whole
set; only the active-window Condition's `time_intervals` gate lets
the apply through. That's a manually-routed LFO modulating the
on-side activation. The LFO framing just names the pattern that's
already in production.

Two forward extensions worth committing to the doc *now*, before
the fade PR, so the next person extending the registry doesn't
re-derive them — and so we don't accidentally build features that
would need rewriting once chaining lands:

1. **`_to_compute` / `_to_scene` argument grammar.** Let
   `brightness_to` accept a literal, a Computer reference (e.g.
   `{compute: circadian}`), or a Scene reference (e.g.
   `{scene: evening}`). The evaluator resolves the inner reference
   *at seed time*, pins the result, and the fade runs against that
   fixed endpoint. Pin-once preserves the fade contract; the value
   comes from upstream. Scene references dereference all three
   axes from the named Scene's primitive values — operators get
   "fade to the `evening` scene" without listing each axis.
2. **`circadian` Computer + continuous CT.** Function of
   `(now, lat, lon)` → Kelvin, mapped from solar altitude (same
   lat/lon plumbing as `sun.go`). Composes with the continuous-CT
   representation above to give "fade CT to whatever's circadian-
   appropriate at sunset" in one Remediation. Ships independently
   from chaining as a standalone Computer — the standalone use
   case ("kitchen tracks the sun") needs only the Computer plus
   the continuous-CT representation, no chaining required.

Neither is in the fade PR. Both are unblocked by the continuous-
representation commitment above — that's the load-bearing piece.

Out of scope, named here as constraints rather than tasks:

- **Polyphonic modulation.** Two LFOs driving the same axis (an
  alert briefly overlaying a circadian curve) has no defined
  semantics today; last-write-wins via applyDesired is the current
  behaviour. If a real need arises, a priority arg on the
  modulation source is the answer. Defer.
- **Full modulation matrix.** A generic `modulators` block on a
  Remediation declaring N upstream sources routed by name to
  destinations is a big architecture change. Defer until the
  manually-routed `withTimeAwareCondition` pattern hurts.

## Out of scope / follow-ups

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
  Low-priority; folds into the fade PR if convenient.
- **Per-axis envelopes.** A single Remediation drives all axes on
  one timeline. Operators who want CT to ride a slower curve than
  brightness can express it as two Remediations on the same
  Condition today; if that becomes common, a per-axis-shape arg in
  the Computer is a forward extension.
- **Decay phase.** Not exposed. If a real use case for "blast then
  settle to sustain" arrives, compose as two back-to-back Attack
  Remediations rather than growing the Computer surface.
