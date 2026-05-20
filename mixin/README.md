# iotcontroller-mixin

Grafana dashboard mixin for the iotcontroller operator's exported
metrics. Lives in the same repo as the controller binary so a metric
addition / rename / removal ships in the same commit as the
corresponding dashboard panel.

## What's in here

- `mixin.libsonnet` — entry point.
- `config.libsonnet` — overridable knobs (`datasourceName`, `jobMatcher`,
  `refresh`, alert thresholds, etc.). Function-of-config pattern threads
  these through the dashboards and alerts at render time.
- `dashboards.libsonnet` — manifest mapping JSON output names to
  dashboard source files.
- `dashboards/controller.libsonnet` — operator-facing controller
  dashboard. Rows: HookReceiver, ZoneKeeper, Router, Conditioner,
  MQTT Client.
- `alerts.libsonnet` — manifest composing alert groups.
- `alerts/controller.libsonnet` — alert rules tied to the controller
  binary's metrics. Currently: `IOTZoneStateChurn` (zone-conflict
  detection via state-change rate).

## Consuming the mixin

The mixin emits the standard Grafana mixin shape:

```jsonnet
{
  grafanaDashboards: { '<name>.json': <dashboard> },
  prometheusAlerts: { ... },   // placeholder, not yet populated
  prometheusRules:  { ... },   // placeholder, not yet populated
}
```

In a deployment that uses jsonnet-bundler, add a dependency on this
mixin's subdir:

```json
{
  "source": {
    "git": {
      "remote": "https://github.com/zachfi/iotcontroller.git",
      "subdir": "mixin"
    }
  },
  "version": "main"
}
```

Then import and compose:

```jsonnet
local iotctl = (import 'iotcontroller-mixin/mixin.libsonnet') + {
  _config+:: {
    datasourceName: 'Prometheus',                      // your datasource var name
    jobMatcher:     'job=~"iot/controller-core"',      // scrape-label scoping
    // refresh, time range, tags, title all overridable too — see
    // config.libsonnet for the full set
  },
};
{
  // expose the dashboards to grafana-operator / kube-prometheus / etc.
  grafanaDashboards+:: iotctl.grafanaDashboards,
}
```

Defaults in `config.libsonnet` aim for a single-tenant standalone
deployment: empty `jobMatcher` (no scrape-label filter) and default
`datasource` variable name. Operators running inside kube-prometheus
or any multi-tenant Prometheus override `jobMatcher` to scope the
gRPC-route panels to the controller pod. The full list of knobs lives
in `config.libsonnet` with per-field doc comments.

Override propagation is verified locally with:

```bash
jsonnet -J <path-to-vendor> -e \
  '((import "mixin.libsonnet") + { _config+:: { jobMatcher: "job=foo" } }).grafanaDashboards["iotcontroller.json"].panels[12].targets[0].expr'
```

(panel index 12 is the per-route gRPC call-rate panel; the rendered
PromQL string should contain `job=foo` next to the route matcher.)

## Adding a panel

1. Pick the right dashboard (currently just `controller.libsonnet`).
2. Add a panel inside the right row, following the panel-builder
   helpers (`ts`, `tsSec`, `tsOps`, `statPanel`).
3. Comment the panel's *why* — what question does it answer, what
   shape is healthy steady-state, what's the headline alert signal.
4. If the metric is new in this PR, add it to
   `modules/.../metrics.go` in the same commit so the panel and its
   data source ship atomically.

## Adding a dashboard

1. Create `dashboards/<name>.libsonnet` with a single `dashboard:`
   field at the top level.
2. Add a one-liner to `dashboards.libsonnet` mapping the output JSON
   name to the new file.
3. Document the new dashboard's scope at the top of its file.
