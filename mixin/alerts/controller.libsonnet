// controller.libsonnet — alert rules for the iotcontroller binary.
//
// Each rule's metric is one the controller emits today (see
// modules/*/metrics.go). Rules emit a `team: home-automation` label
// so Alertmanager routes them to the operator without needing a
// route-table change at deploy time.
//
// Function-of-config pattern: returns a group, consumer composes via
// prometheusAlerts.groups. Config knobs let operators tune
// thresholds per-deployment (a busy household with motion in every
// room has a higher noise floor than a single-occupant zone).

function(cfg) {
  name: 'iotcontroller-zone-state',
  rules: [
    {
      // Zone state changes per minute, per zone. Healthy zones do
      // single-digit transitions per hour (motion → on, off-dwell →
      // off, repeat across the day). Sustained > 0.5/min for 10
      // minutes is a conflict signature — two Conditions on the
      // same zone disagreeing on what state to apply, each fighting
      // the other every eval tick.
      //
      // Real case that motivated this rule: foyer overnight
      // 2026-05-19 → 2026-05-20. foyer-off (eval-loop, state=off,
      // 22:00-07:00 MDT) overlapped foyer-motion-nightvision
      // (motion → state=on red). 286 ON + 296 OFF transitions in
      // 12h vs ~10 expected.
      //
      // Action: open the dashboard's "Condition Activation Density
      // by zone" panel for the zone label; the two top contributors
      // are the conflicting Conditions.
      alert: 'IOTZoneStateChurn',
      expr: |||
        sum by (zone) (
          rate(iotcontroller_zonekeeper_state_changes_total[5m])
        ) * 60 > %(threshold)s
      ||| % { threshold: cfg.zoneStateChurnRatePerMin },
      'for': cfg.zoneStateChurnFor,
      labels: {
        severity: 'warning',
        team: 'home-automation',
      },
      annotations: {
        summary: '{{ $labels.zone }} flipping {{ $value | printf "%.1f" }} state changes/min',
        description: |||
          Zone {{ $labels.zone }} has sustained state-change rate above %(threshold)s/min for %(for)s,
          which suggests two Conditions are fighting over the zone's state. Open the IOT
          Controller dashboard's "Condition Activation Density by zone" panel and filter by
          zone={{ $labels.zone }} — the two top contributors are the conflict.
        ||| % { threshold: cfg.zoneStateChurnRatePerMin, 'for': cfg.zoneStateChurnFor },
      },
    },
  ],
}
