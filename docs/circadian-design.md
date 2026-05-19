# Circadian — Sun-Position-Driven Continuous Color Temperature

Status: standalone Computer shipped in v0.8.1
(`modules/conditioner/computer/circadian.go`). The chaining
extension (`_to_compute: circadian` from inside a `fade` Computer)
is forward work and tracked in `docs/fade-design.md`'s Modulation
section. Until that lands, circadian is callable as its own
`active_compute: circadian` on a Remediation; the "always be
circadian" use case is fully supported and is the recommended way
to start using it.

## What this unlocks

A `circadian` Computer that, given `(now, lat, lon)`, returns a
continuous Kelvin value tracking the kind of light the sun is
actually producing at that moment. Three operator-visible behaviours
fall out:

1. **"Always be circadian."** A Remediation whose only job is to set
   the zone's CT to the circadian value each eval tick. Cool blue-
   white at solar noon, warm amber post-dusk, smooth transitions in
   between. Layered with a Scene that owns brightness, this gives
   "kitchen tracks the sun" with no additional CRDs.
2. **"Fade to circadian."** Composed with `fade` via the
   `_to_compute` argument grammar (forward extension from
   `fade-design.md`): at fade seed time, the evaluator dereferences
   `circadian` to a Kelvin value and pins it as the fade's
   endpoint. "At sunset, fade CT to whatever circadian says is
   right, over 20 minutes."
3. **"Drift with circadian over a window."** A Remediation with
   `active_compute: circadian` inside a long `time_intervals`
   window: every eval tick emits the current circadian Kelvin, so
   the zone smoothly tracks the sun across the entire afternoon.
   This is the closest analog to HomeKit Adaptive Lighting.

## Relationship to `sun_color_temperature`

The existing `sun_color_temperature` Computer
(`modules/conditioner/computer/sun.go`) does exactly the same job
this draft proposes, but quantized to the 5-step CT enum:

```
pre-dawn       (rise - 30m)              FIRSTLIGHT
morning ramp   (rise - 30m .. rise + 2h) MORNING
full day       (rise + 2h .. set - 2h)   DAY
late afternoon (set - 2h .. set + 30m)   LATEAFTERNOON
evening/night  (set + 30m onwards)       EVENING
```

`circadian` is the same machine with the quantization stripped off.
Boundaries become *anchor points* on a continuous curve rather than
step transitions. The lat/lon plumbing, the calendar-day handling
(see the "yesterday and today" loop at `sun.go:54-60`), and the
catch-all "outside daylight = evening" all carry over unchanged.

Two ways to ship the relationship:

1. **Coexist.** Keep `sun_color_temperature` as the discrete
   authoring-friendly Computer; add `circadian` as the continuous
   peer. Operators pick by feature need. Cost: two Computers doing
   nearly the same calculation; risk of drift if one is updated
   without the other.
2. **Supersede.** `circadian` replaces `sun_color_temperature`.
   The existing enum-emitting Computer becomes a thin wrapper that
   calls `circadian` and rounds to the nearest enum step. Cost:
   single source of truth; existing Remediations using
   `sun_color_temperature` keep working through the wrapper.

Option 2 is the recommendation. The cost is small (the wrapper is
~10 LOC), and we don't accidentally let the two diverge as the
curve is tuned.

## The curve

The continuous Kelvin curve is well-trodden territory; f.lux, Hue
Adaptive Lighting, HomeKit Adaptive Lighting, and Home Assistant's
`adaptive_lighting` integration all use variants of the same shape.
Proposed values (subject to operator tuning):

```
moment                              Kelvin    notes
─────────────────────────────────────────────────────────────────────
pre-dawn  (rise - 30m)              2200K     anchored at FIRSTLIGHT
sunrise   (rise)                    2700K     anchored at MORNING start
morning   (rise + 2h)               5000K     anchored at DAY start
solar noon                          5500K     peak
late-afternoon  (set - 2h)          4500K     anchored at LATEAFTERNOON
sunset    (set)                     3000K
dusk      (set + 30m)               2700K     anchored at EVENING
night                               2200K     held flat past dusk
```

Between the anchor points, the curve is a piecewise-linear
interpolation in Kelvin. Smoother shapes (cubic Bezier, logarithmic
in mireds) are possible follow-ups; piecewise-linear is enough for
the first release and aligns exactly with the existing
`sun_color_temperature` bucket transitions when sampled at the
boundaries — i.e. `circadian(rise+2h) == 5000K`, and rounding to
the nearest enum gives DAY (5500K canonical), preserving
backward compatibility with the discrete Computer.

Alternative anchor: solar altitude (angle above horizon) instead
of elapsed-time-from-rise. Physically more grounded — a 30°
altitude is the same "color of light" everywhere — but requires
fuller sun-position math than `nathan-osman/go-sunrise` provides
(it returns rise/set times, not the altitude curve). Out of scope
for the first pass; the time-anchored version composes correctly
with the existing `sun.go` bucket boundaries and is cheap.

## The Computer's interface

```yaml
active_compute: circadian
active_compute_args:
  # All args optional. Anchors and Kelvin targets default to the
  # values above; operator-tunable when a zone wants warmer-than-
  # average evenings or cooler-than-average noons.
  noon_kelvin: 5500
  evening_kelvin: 2700
  night_kelvin: 2200
  # Bias shifts the whole curve. Negative = warmer everywhere;
  # positive = cooler. Useful for bedroom zones that want a more
  # aggressive warm-shift at night without retuning each anchor.
  bias_kelvin: -200
```

Returns:

```go
ApplyValues{
    ColorTemperatureKelvin: <computed Kelvin>,
    ColorTemperature:       <nearest enum step for Status reporting>,
}
```

No brightness, no state, no color — single-axis Computer. Composes
with anything that owns the other axes (a Scene for brightness, a
fade for terminal_state). The enum field is set so today's consumers
(dashboards, Zone Status) keep reading something sensible during the
backward-compat window.

## Standalone use (ships without chaining)

The "always be circadian" use case needs only the Computer itself
and the continuous-CT representation. No `_to_compute` argument
grammar required. Deployment shape:

```yaml
- zone: kitchen
  active_compute: circadian
  time_intervals:
    - times: [{ start_time: "06:00", end_time: "23:00" }]
      location: America/Denver
```

The eval loop ticks; the Computer returns the current circadian
Kelvin; ZoneKeeper applies it; the zone tracks the sun. Same shape
as today's `sun_color_temperature` Remediations, with the smooth
output.

This means **circadian can ship as its own PR after fade lands, but
before the chaining work lands.** The chaining is the cherry on top;
the standalone Computer is independently useful.

## Composition with fade (chaining)

Once `_to_compute` is wired (per `fade-design.md`'s Modulation
section, forward extension #1):

```yaml
- zone: living-area
  active_compute: fade
  active_compute_args:
    color_temperature_to_compute: circadian
    duration: 20m
    anchor: window
    start_at: "18:00"
    timezone: America/Denver
```

At 18:00 the evaluator seeds the fade snapshot, dereferences
`circadian` to whatever Kelvin is appropriate at 18:00 *on this
date at this lat/lon*, pins it as `color_temperature_to_kelvin`,
and the fade runs from current to that value over 20 minutes. The
fade's endpoint depends on the day — winter and summer get
different curves automatically — but within a single envelope the
endpoint is stable.

Re-seeding mid-fade (a re-activation) would re-dereference
`circadian` against the new seed time, which is the right
behaviour: if the operator triggered "fade to circadian" again
five minutes later, they want the curve to target the *current*
circadian value, not the one captured at the first activation.

## Open questions

- **Day-of-year modulation.** Solstice and equinox affect not just
  the timing of rise/set (which the lat/lon math already handles)
  but also the *shape* of the curve — winter evenings linger
  longer in the warm range than summer evenings. Worth exploring
  whether the anchor offsets (the 30m, 2h, etc. from `sun.go`)
  should also be day-of-year-modulated. Defer to a follow-up if
  operators report the curve "feels off" at solstice.
- **Polar / extreme-latitude handling.** Above the Arctic Circle
  in summer, `rise` and `set` can be undefined or hours apart in
  ways the bucket math doesn't anticipate. `sun.go`'s current
  fallback is "outside any bucket → EVENING," which is
  defensible for now but produces a 24-hour stuck-warm zone
  during polar night. The 0.1% case; punt.
- **User warmth-bias override.** A Zone CRD field
  `Spec.CircadianBias` that pre-empts the `bias_kelvin` arg
  per-zone, so operators can tune individual rooms without
  copying the whole `active_compute_args` block across N
  Remediations. Forward extension; not in the first PR.
- **Smoothing across the night.** From dusk to pre-dawn the
  curve is flat at `night_kelvin` (2200K). That's correct for
  "the light is warm at night" but doesn't model the
  intermediate transitions through deep night. For zones that
  matter (bedside, hallway), a slow drift further into the warm
  end (e.g. 2200K at dusk → 1800K at deepest night → 2200K at
  pre-dawn) might be more pleasant. Operator-tunable curve
  shape is the answer; defer to v2.
- **Astronomical twilight vs civil twilight as the anchor.** The
  existing `sun.go` uses civil sunrise/sunset (sun crossing the
  horizon). Some adaptive-lighting implementations anchor on
  *civil twilight* (-6° altitude) or *astronomical twilight*
  (-18° altitude), which extend the warm-end transition further.
  Worth a one-off comparison once a real sensor is sampling the
  actual room light; the right anchor is "where the operator's
  eye stops perceiving daylight," which differs by room.

## Out of scope

- **Per-device CT range clamping.** Some bulbs only do 2700K-6500K;
  others 2200K-6500K; warm-only bulbs cap at 4000K. The
  device-facing apply layer is where clamping should happen, not
  here. The Computer always emits "what the sun is doing" and
  lets downstream truncate.
- **Custom curve shapes via operator-supplied piecewise tables.**
  Useful eventually; not first-cut. The default curve handles 95%
  of zones; `bias_kelvin` handles most of the rest.
- **Latency-aware sampling.** The 60s eval tick means circadian
  jumps in 60s increments rather than smoothly. For brightness,
  fade's interpolation already hides this; for circadian standalone
  use, the jumps are small (a few Kelvin per tick mid-curve) and
  imperceptible. If perceptibility becomes a concern, the answer is
  the `_to_compute` chaining path with a fade wrapping the circadian
  source, not finer-grained eval ticks.

## Migration & shipping order

1. **Continuous-CT representation lands** with fade
   (`fade-design.md` commits to this in the body). — shipped in
   v0.8.0.
2. **`circadian` Computer ships standalone** — peer to
   `sun_color_temperature` initially; supersede via the
   wrapper-rounding option once it has bedded in. New
   `active_compute: circadian` is operator-callable; no
   deployment_tools changes required to start using it. — shipped
   in v0.8.1.
3. **`_to_compute` argument grammar lands** (separate design;
   sketched in `fade-design.md`'s Modulation section).
4. **Operators migrate** "fade to circadian at sunset"
   Remediations as desired.

Each step is independently valuable. Step 2 alone gives "kitchen
tracks the sun" smoothly. Step 3 alone gives the broader
chaining grammar. Step 4 is purely deployment.

## Implementation notes — deviations from the draft

- **Dusk-to-night smoothing tail.** The table above anchors dusk at
  `evening_kelvin` (2700K default) and describes night as "held flat
  past dusk" at `night_kelvin` (2200K default). A strict reading
  produces a 500K step discontinuity at the dusk boundary, which is
  visually noticeable when downstream is a fade or any consumer that
  samples on a fast tick. The implementation adds a smoothing anchor
  at `set + 60m = night_kelvin`, so the dusk→night drop is a 30-min
  linear ramp rather than a step. The "held flat past dusk" semantic
  still applies past `set + 60m`. To recover the strict step
  behaviour, set `evening_kelvin = night_kelvin`.
- **Short-day handling.** For very short winter days (and polar
  edges), `rise + 2h` can fall after `set - 2h`, which would put
  `morning_end` later than `late_afternoon` in time. The
  implementation sorts anchors by time (stable sort) before
  interpolating, so the curve still walks anchors in time order and
  the day degrades into a monotonic-ish shape rather than a backward
  segment. Polar night still hits the night-floor fallback per the
  open-questions section.
- **Sanity clamp.** `bias_kelvin` plus an anchor value clamps to
  `[1500K, 10000K]` before emitting. Downstream device handlers
  saturate anyway; the clamp keeps a misconfigured bias from
  emitting nonsense Kelvin to logs and metrics.
