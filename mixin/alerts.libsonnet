// alerts.libsonnet — manifest of alert groups the mixin emits.
//
// Same function-of-config pattern as dashboards.libsonnet — captures
// the composed _config via `local root = self` so consumer overrides
// (e.g. higher zoneStateChurnRatePerMin for a busy multi-occupant
// household) propagate to the rule expressions.
//
// Consumer side (deployment_tools) sees a standard
// prometheusAlerts.groups list it can concat with its own
// non-mixin-owned alerts; the mimir ruler picks them up via
// environments/mimir/util.libsonnet.

{
  local root = self,
  prometheusAlerts+:: {
    groups+: [
      (import 'alerts/controller.libsonnet')(root._config),
    ],
  },
}
