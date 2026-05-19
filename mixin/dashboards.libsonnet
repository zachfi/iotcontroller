// dashboards.libsonnet — manifest of dashboards the mixin emits.
//
// Each entry imports a self-contained dashboard file. New dashboards
// land here as one-line additions; the dashboard files themselves
// own their internal structure (rows, panels, target queries).
//
// Today: just the controller dashboard. Eventually: zone telemetry
// (currently in deployment_tools) once the operator-facing dashboards
// migrate over too.

{
  grafanaDashboards+:: {
    'iotcontroller.json': (import 'dashboards/controller.libsonnet').dashboard,
  },
}
