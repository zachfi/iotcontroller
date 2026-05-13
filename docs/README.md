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
  the four built-ins (`sun_color_temperature`, `ramp`,
  `rotate_colors`, `query`), how to add a new computer.

## Reference material

- [`reference/ember/`](reference/ember/) — Silicon Labs EmberZNet
  EZSP-UART specifications. Used by the native Zigbee coordinator's
  Ember stack implementation.

## Scratch

In-progress design notes that aren't part of the published
documentation live in `scratch/` (gitignored). When a scratch
document settles into something worth maintaining, promote it to
this directory's root.
