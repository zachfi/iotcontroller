# iotcontroller docs

Curated architecture documentation for the operator. The top-level
[`../README.md`](../README.md) is the entry point; the docs in this
directory go deeper on specific subsystems.

## Index

- [`architecture.md`](architecture.md) — module map, event flow,
  transport-agnostic design, CRD model.
- [`bindings.md`](bindings.md) — Binding schema, the matcher,
  selector specificity, `MinDuration` debounce, authoring patterns.
- [`conditioner.md`](conditioner.md) — periodic evaluator,
  applyDesired idempotency cache, time-window gating, the four
  trigger sources (Alert, ActivateCondition, eval-loop, deprecated
  Schedule).
- [`computers.md`](computers.md) — Computer interface, registry,
  the built-ins (`sun_color_temperature`, `ramp`,
  `rotate_colors`, `query`, `fade`, `circadian`), how to add a new
  computer.
- [`circadian-design.md`](circadian-design.md) — the `circadian`
  Computer: continuous-Kelvin peer of `sun_color_temperature` driven
  by piecewise-linear interpolation between sun-anchored points.
  Operator-tunable noon / evening / night Kelvin and a whole-curve
  bias. Standalone use shipped in v0.8.1; chaining via
  `_to_compute` is forward work tracked in `fade-design.md`.

## Proposals

Drafted feature requests, not yet implemented. Each describes the
operator behaviour the proposal would unlock, what already composes
from existing primitives, and what's missing.

- [`fade-design.md`](fade-design.md) — unified `fade` Computer for
  time-bounded interpolation of brightness, color temperature, and
  color. Framed as an attack/sustain/release envelope: the on-side
  and off-side Conditions for a zone are two halves of one
  envelope, parameterized via `withMotionEnvelope` (event-anchored)
  or `withFadeAutoOff` (window-anchored). Commits to a continuous
  internal representation (the discrete brightness/CT enums become
  authoring sugar on top of normalized [0,1] and Kelvin) so that
  forward extensions — LFO-style modulation chaining, a `circadian`
  Computer driving sun-position CT, percentage-based Scene
  authoring — don't force second migrations of every CT/brightness
  consumer. `ramp` collapses into a deprecated alias. Supersedes
  earlier fade-to-off and motion-light drafts.
- [`binding-only-design.md`](binding-only-design.md) — small
  controller-side primitive: a `binding_only: true` flag on
  Remediation that suppresses eval-loop firing while keeping
  `withinActiveWindow` as a gate for explicit activations
  (Binding match, `ActivateCondition` RPC, alert, epoch). Unblocks
  motion-during-window-only patterns (bedside lamp on after
  `sunset−15m` only if motion) and time-of-day-aware button
  presses (re-enables the disabled `-full`/`-dim` block in
  deployment_tools). Independent of and composes with the fade
  Computer.

## Reference material

- [`reference/ember/`](reference/ember/) — Silicon Labs EmberZNet
  EZSP-UART specifications. Used by the native Zigbee coordinator's
  Ember stack implementation.

## Scratch

In-progress design notes that aren't part of the published
documentation live in `scratch/` (gitignored). When a scratch
document settles into something worth maintaining, promote it to
this directory's root.
