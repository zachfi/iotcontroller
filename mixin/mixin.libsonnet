// mixin.libsonnet — entry point for the iotcontroller mixin.
//
// Bundles dashboards (and, eventually, alerts and recording rules)
// for the iotcontroller binary's exported metrics. Co-located with
// the metric definitions in `modules/*/metrics.go` so a metric change
// and its observability panel ship in the same commit.
//
// Consumption pattern (deployment_tools and any external operator):
//
//   local iotctl = (import 'iotcontroller-mixin/mixin.libsonnet') + {
//     _config+:: { datasourceName: 'Prometheus' },
//   };
//   { grafanaDashboards+:: iotctl.grafanaDashboards }
//
// The mixin exposes its output the same way Grafana's own mixins do:
// `grafanaDashboards`, `prometheusAlerts`, `prometheusRules`. Today
// only the first is populated; the others are placeholders so
// consumers can compose against a stable shape.

(import 'config.libsonnet') +
(import 'dashboards.libsonnet') +
{
  prometheusAlerts+:: {},
  prometheusRules+:: {},
}
