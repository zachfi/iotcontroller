// dashboards.libsonnet — manifest of dashboards the mixin emits.
//
// Each dashboard source file exports `dashboard(cfg)` as a function of
// the composed `_config` block. The `local root = self` capture below
// is the idiom that makes consumer overrides — `mixin + { _config+::
// { ... } }` — actually propagate: `root._config` is evaluated lazily,
// at JSON-emit time, so it sees the post-override config rather than
// a snapshot from import time.
//
// Adding a new dashboard:
//   1. Drop `dashboards/<name>.libsonnet` exporting `dashboard(cfg)`.
//   2. Add a one-liner here mapping the output JSON name to a call
//      against `root._config`.

{
  local root = self,
  grafanaDashboards+:: {
    'iotcontroller.json': (import 'dashboards/controller.libsonnet').dashboard(root._config),
  },
}
