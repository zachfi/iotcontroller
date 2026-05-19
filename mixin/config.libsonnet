// config.libsonnet — overridable knobs for the iotcontroller mixin.
//
// Consumers wrap the mixin and override _config to fit their cluster:
//
//   (import 'iotcontroller-mixin/mixin.libsonnet') + {
//     _config+:: {
//       datasourceName: 'Prometheus',
//       jobMatcher: 'job=~"iot/controller-core"',
//       refresh: '30s',
//     },
//   }
//
// Defaults aim for the most common case (kube-prometheus-style scrape
// labels, datasource var named "datasource"). Override sparingly.
{
  _config+:: {
    // Name of the dashboard variable that selects the Prometheus
    // datasource. The mixin's panels target `$<datasourceName>`.
    datasourceName: 'datasource',

    // Label-matcher fragment that scopes /iot.v1.* gRPC metrics to
    // the controller pods. Used to filter the per-route gRPC panels
    // away from any other process exposing /iot.v1.* labels. Inserted
    // verbatim into PromQL inside `{...}`.
    jobMatcher: 'job=~"iot/controller-core"',

    // Dashboard auto-refresh cadence. The controller's evaluator
    // ticks at 60s, so a 1m refresh aligns visible updates with the
    // smallest unit of change.
    refresh: '1m',

    // Tags applied to every dashboard the mixin emits. Lets operators
    // filter the dashboard browser to "things this mixin owns" without
    // hand-curating folders.
    dashboardTags: ['iot', 'controller'],

    // Default time-range when the dashboard first loads. Aligned with
    // the operator's expected attention span for a deployment review.
    timeFrom: 'now-3h',
    timeTo: 'now',
  },
}
