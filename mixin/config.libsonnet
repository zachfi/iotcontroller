// config.libsonnet — overridable knobs for the iotcontroller mixin.
//
// Defaults are chosen for a standalone iotcontroller deployment with
// no extra Prometheus label cardinality (single-tenant scrape, default
// "datasource" variable in Grafana). Consumers running inside a richer
// observability stack (kube-prometheus, Grafana Cloud, multi-cluster
// federation) override per-knob to match their scrape shape.
//
// Override pattern at the consumer site:
//
//   (import 'iotcontroller-mixin/mixin.libsonnet') + {
//     _config+:: {
//       jobMatcher: 'job=~"iot/controller-core"',
//       datasourceName: 'Prometheus',
//     },
//   }
//
// Every knob below is referenced from dashboards/*.libsonnet via a
// passed-in cfg argument (the function-of-config pattern; see
// dashboards.libsonnet). Adding a new knob means: (a) add the default
// here with a doc comment, (b) reference it as cfg.fooBar in the
// dashboard, (c) update mixin/README.md's consumption section so
// downstream operators discover it.

{
  _config+:: {
    // Name of the Grafana dashboard variable that selects the
    // Prometheus datasource. Dashboards reference query targets as
    // `$<datasourceName>`. Grafana installations using a non-default
    // variable name (e.g. "Prometheus", "ds_metrics") set this to
    // align the rendered queries with their template.
    datasourceName: 'datasource',

    // PromQL label-matcher fragment that scopes the gRPC/route
    // panels to the controller pod (i.e. distinguishes
    // iotcontroller's /iot.v1.* counters from any other process
    // exporting metrics under similar names in the same Prometheus
    // tenant). Inserted verbatim inside `{...}` next to other
    // label matchers. Empty string (the default) disables the
    // filter — appropriate for single-tenant deployments where
    // iotcontroller is the only series source.
    //
    // Example: 'job=~"iot/controller-core"' (kube-prometheus shape)
    jobMatcher: '',

    // PromQL label-matcher fragment scoping zone-axis panels to a
    // specific zone selection. Drives the per-zone breakdowns that
    // would otherwise spam the legend with every zone in the
    // cluster. Empty string disables the filter; pair with a
    // `zone` template variable on the dashboard for "filter by
    // selected zone" behavior.
    //
    // Reserved for future zone-facing dashboards (today the
    // controller dashboard doesn't use per-zone filtering, but
    // when iot/v1 and zone/v1 migrate here this knob takes effect).
    //
    // Example: 'zone=~"$zone"' (interactive template variable)
    zoneMatcher: '',

    // Dashboard auto-refresh cadence. The conditioner ticks every
    // 60 s, so '1m' aligns visible updates with the smallest unit
    // of change. Operators wanting a slower refresh (e.g.
    // dashboards on a shared TV) lengthen to '5m' or longer.
    refresh: '1m',

    // Default time-range when the dashboard first loads. 'now-3h'
    // covers a single working window without scrolling.
    timeFrom: 'now-3h',
    timeTo: 'now',

    // Dashboard metadata. `dashboardUid` is empty by default so
    // Grafana auto-generates a UID from the title; operators
    // pinning external links to a specific UID set this explicitly
    // to a stable value before first publish.
    dashboardTitle: 'IOT Controller',
    dashboardTags: ['iot', 'controller'],
    dashboardUid: '',
  },
}
