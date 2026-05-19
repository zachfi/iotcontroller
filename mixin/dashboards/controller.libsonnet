// controller.libsonnet — IOT Controller (operator-facing) dashboard.
//
// Panels track the controller binary's exported metrics: HookReceiver
// alert pipeline, ZoneKeeper flush rate / state changes, Router queue
// + unhandled routes, Conditioner activity (apply rate, compute
// success / suppression / errors), MQTT harvester queue depth.
//
// Originally lived in
// deployment_tools/tk/lib/iot/dashboards/controller/v1.libsonnet;
// moved here in iotcontroller v0.8.2 so dashboard panels evolve in
// lockstep with the underlying metrics. The deployment_tools
// equivalent is now a thin importer that vendors this mixin via
// jsonnet-bundler.
//
// Datasource var name and the job matcher used to scope /iot.v1.*
// gRPC traces are hardcoded for the znet deployment shape. A future
// follow-up will wire mixin/config.libsonnet's overridable knobs into
// these literals so external consumers can re-skin without forking.

local g = import 'github.com/grafana/grafonnet/gen/grafonnet-latest/main.libsonnet';

local row = g.panel.row;
local timeSeries = g.panel.timeSeries;
local stat = g.panel.stat;
local var = g.dashboard.variable;

local custom = timeSeries.fieldConfig.defaults.custom;
local options = timeSeries.options;

// Reusable panel builders. Each returns a timeSeries / stat configured
// with the legend layout and rendering style this dashboard prefers,
// taking just (title, targets) — keeps the panel grid below readable
// as a flat list rather than a wall of grafonnet incantations.
local ts(title, targets) =
  timeSeries.new(title)
  + timeSeries.queryOptions.withTargets(targets)
  + timeSeries.queryOptions.withInterval('1m')
  + options.legend.withDisplayMode('table')
  + options.legend.withCalcs(['lastNotNull', 'max'])
  + custom.withFillOpacity(10)
  + custom.withShowPoints('never');

local tsSec(title, targets) =
  ts(title, targets)
  + timeSeries.standardOptions.withUnit('s')
  + custom.scaleDistribution.withType('log')
  + custom.scaleDistribution.withLog(10);

local tsOps(title, targets) =
  ts(title, targets)
  + timeSeries.standardOptions.withUnit('ops');

local statPanel(title, targets) =
  stat.new(title)
  + stat.queryOptions.withTargets(targets)
  + stat.options.withColorMode('background')
  + stat.options.withGraphMode('area')
  + stat.gridPos.withW(4)
  + stat.gridPos.withH(4);

local datasource = var.datasource.new('datasource', 'prometheus');

local promTarget(expr, legendFormat='') =
  g.query.prometheus.new('$datasource', expr)
  + g.query.prometheus.withLegendFormat(legendFormat);

{
  dashboard:
    g.dashboard.new('IOT Controller')
    + g.dashboard.withDescription('IOT Controller module health and automation activity')
    + g.dashboard.withTags(['iot', 'controller'])
    + g.dashboard.withVariables([datasource])
    + g.dashboard.time.withFrom('now-3h')
    + g.dashboard.time.withTo('now')
    + g.dashboard.graphTooltip.withSharedCrosshair()
    + g.dashboard.withRefresh('1m')
    + g.dashboard.withPanels(
      g.util.grid.wrapPanels([

        // ── HookReceiver ──────────────────────────────────────────────────
        row.new('HookReceiver'),

        ts('Alert Pipeline Errors', [
          promTarget(
            'rate(iotcontroller_hookreceiver_alerts_total{result="error"}[5m])',
            'errors/s'
          ),
          promTarget(
            'rate(iotcontroller_hookreceiver_alerts_total{result="success"}[5m])',
            'success/s'
          ),
        ]),

        tsSec('gRPC Latency', [
          promTarget(
            'histogram_quantile(0.99, rate(iotcontroller_hookreceiver_grpc_duration_seconds[5m]))',
            'P99'
          ),
          promTarget(
            'histogram_quantile(0.50, rate(iotcontroller_hookreceiver_grpc_duration_seconds[5m]))',
            'P50'
          ),
        ]),

        // ── ZoneKeeper ────────────────────────────────────────────────────
        row.new('ZoneKeeper'),

        ts('Zone Flush Rate', [
          promTarget(
            'rate(iotcontroller_zonekeeper_flush_total[5m])',
            '{{zone}}'
          ),
        ]),

        ts('State Changes (1h rate)', [
          promTarget(
            'rate(iotcontroller_zonekeeper_state_changes_total[1h])',
            '{{zone}}/{{state}}'
          ),
        ]),

        // ── Router ────────────────────────────────────────────────────────
        row.new('Router'),

        statPanel('Queue Length', [
          promTarget(
            'iotcontroller_router_queue_length',
            'queue'
          ),
        ]),

        ts('Unhandled Routes', [
          promTarget(
            'rate(iotcontroller_router_unhandled_route[5m])',
            'unhandled/s'
          ),
        ]),

        ts('Send Errors', [
          promTarget(
            'rate(iotcontroller_router_message_send_errors[5m])',
            'errors/s'
          ),
        ]),

        // Stage 3 migration thermometer. Per-device rate of action events
        // that fell through to the legacy ActionHandler because no Binding
        // matched. Watch this drain to zero per device as Bindings are
        // rolled out; once flat we can retire the ActionHandler switch.
        // Action label exposes which vocab strings are still unbound.
        ts('Action Fallback (legacy ActionHandler, per device)', [
          promTarget(
            'sum by (device, action, zone) (rate(iotcontroller_router_action_fallback_total[5m]))',
            '{{zone}}/{{device}} action={{action}}'
          ),
        ]),

        // ── Conditioner ──────────────────────────────────────────────────
        row.new('Conditioner'),

        // Per-route gRPC handling rate. ActivateCondition vs ActionHandler
        // shows whether the binding path or the legacy switch is doing the
        // work. After the Stage 3 migration completes, ActivateCondition
        // should be the dominant call rate among ZoneKeeper-targeted RPCs.
        tsOps('gRPC Call Rate (by route)', [
          promTarget(
            'sum by (route) (rate(iot_request_duration_seconds_count{job=~"iot/controller-core",route=~"/iot.v1\\\\..*"}[5m]))',
            '{{route}}'
          ),
        ]),

        // Per-route p99 server-side latency. The big-picture answer to
        // "is this slow?" — spikes on Conditioner.ActivateCondition or
        // ZoneKeeperService.SetState mean a press took more than 250ms
        // somewhere inside the controller pod. Pair with traces for
        // span-level breakdown.
        tsSec('gRPC P99 Latency (by route)', [
          promTarget(
            'histogram_quantile(0.99, sum by (le, route) (rate(iot_request_duration_seconds_bucket{job=~"iot/controller-core",route=~"/iot.v1\\\\..*"}[5m])))',
            '{{route}}'
          ),
        ]),

        // applyDesired idempotency cache hits, split by direction.
        //  * activate   — re-fires of the same Condition collapsed to no-op
        //  * deactivate — alert-resolve repeat fires
        //  * time-gated — TimeIntervals window suppressed the activation
        // High activate/deactivate rate vs low state_changes_total proves
        // the cache is doing its job (Zigbee amplification fix landed).
        // High time-gated rate proves TimeIntervals are honored as
        // designed by the Stage 1 work.
        tsOps('Conditioner Apply Suppressed (by direction)', [
          promTarget(
            'sum by (direction) (rate(iotcontroller_conditioner_apply_suppressed_total[5m]))',
            '{{direction}}'
          ),
        ]),

        // Per-condition suppression breakdown. Useful when one Condition
        // dominates the apply rate (alert flapping, scheduled re-fire) to
        // pinpoint which one to investigate.
        ts('Conditioner Suppression by Condition (top 10)', [
          promTarget(
            'topk(10, sum by (condition, direction) (rate(iotcontroller_conditioner_apply_suppressed_total[15m])))',
            '{{condition}} ({{direction}})'
          ),
        ]),

        // active_compute success rate. The counter increments once per
        // successful Computer tick (Compute returned no error AND the
        // ApplyValues RPC succeeded). Per-computer breakdown answers
        // "is fade firing? circadian? sun_color_temperature?" without
        // resorting to log grep or status spelunking. Steady-state
        // value should track (conditioner.evaluation_total × number
        // of in-window active_compute Remediations).
        tsOps('Conditioner Compute Applied (by computer)', [
          promTarget(
            'sum by (compute) (rate(iotcontroller_conditioner_evaluation_compute_applied_total[5m]))',
            '{{compute}}'
          ),
        ]),

        // Per-Condition success breakdown for canary deploys. Newly
        // added active_compute Conditions show up here within one
        // tick; absence after a deployment is the headline signal
        // that the operator's CRD didn't reach the conditioner's
        // informer or its time_intervals don't cover `now`.
        ts('Conditioner Compute Applied by Condition (top 10)', [
          promTarget(
            'topk(10, sum by (condition, zone, compute) (rate(iotcontroller_conditioner_evaluation_compute_applied_total[15m])))',
            '{{condition}} → {{zone}} ({{compute}})'
          ),
        ]),

        // Computer failure paths. Three thin lines that should all
        // sit at zero on a healthy deployment. Non-zero on
        // compute_unknown is an operator typo or a metric referencing
        // a not-yet-compiled Computer; compute_error is internal to
        // the Computer (e.g. query's PromQL HTTP failure); apply_error
        // is downstream of ZoneKeeper.ApplyValues.
        ts('Conditioner Compute Failures', [
          promTarget(
            'sum by (compute) (rate(iotcontroller_conditioner_evaluation_compute_unknown_total[5m]))',
            '{{compute}} (unknown)'
          ),
          promTarget(
            'sum by (compute) (rate(iotcontroller_conditioner_evaluation_compute_error_total[5m]))',
            '{{compute}} (compute err)'
          ),
          promTarget(
            'sum by (compute) (rate(iotcontroller_conditioner_evaluation_apply_error_total[5m]))',
            '{{compute}} (apply err)'
          ),
        ]),

        // applyDesired cache invalidations driven by out-of-band Zone
        // Status changes — somebody other than this conditioner (a
        // button press, alert from a second origin, direct SetState)
        // moved the zone. Steady-state should be near zero; bursts
        // correlate with manual operator activity or fade
        // Computer key-up events.
        tsOps('Apply Cache Invalidations (out-of-band)', [
          promTarget(
            'sum by (zone, reason) (rate(iotcontroller_conditioner_apply_cache_invalidations_total[5m]))',
            '{{zone}} ({{reason}})'
          ),
        ]),

        // ── MQTT Client ───────────────────────────────────────────────────
        row.new('MQTT Client'),

        ts('Harvester Message Rate', [
          promTarget(
            'rate(iot_harvester_message_total[3m])',
            'messages/s'
          ),
        ]),

        ts('Harvester Route Errors', [
          promTarget(
            'rate(iot_harvester_message_error[5m])',
            'errors/s'
          ),
        ]),

        ts('MQTT Client Replacement Errors', [
          promTarget(
            'rate(iotcontroller_mqttclient_replacement_errors[5m])',
            'errors/s'
          ),
        ]),

        // Harvester queue depth — the headline cold-start signal. With
        // the single-goroutine consumer this could climb to hundreds
        // during a controller-core restart (1.7s queue waits per item
        // observed in traces). With bounded fan-out (default 16
        // workers) it should hold at 0 except during the cold-start
        // window or a downstream Router stall.
        ts('Harvester Queue Depth', [
          promTarget(
            'iot_harvester_queue_depth',
            'depth'
          ),
        ]),

        // Active fan-out workers inside routeClient.Send. At-saturation
        // values (≥ -harvester.concurrency) mean every worker is
        // blocked and items are queueing; correlate with apiserver
        // write latency and grpc client redial events.
        ts('Harvester Active Workers', [
          promTarget(
            'iot_harvester_active_workers',
            'in-flight'
          ),
        ]),

      ], panelWidth=12, panelHeight=8)
    ),
}
