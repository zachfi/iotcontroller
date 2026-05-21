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

    {
      // Shadow resolver's per-axis conflict signal. The shadow
      // composes a declarative target from all TimeInterval-driven
      // Conditions; a (zone, axis) entry with >1 contributor means
      // structural overlap — two Conditions on the same zone
      // claiming the same axis (state / brightness / color_temperature
      // / color / scene) in the same eval tick. The imperative path
      // resolves this by last-write-wins; the shadow surfaces it as a
      // metric.
      //
      // Boundary cases (e.g. one Condition's time_intervals ending
      // exactly where another's begins) can produce a single tick of
      // overlap and are cosmetic. Sustained overlap is the structural
      // bug — at last sustained overlap (foyer-off vs
      // foyer-motion-nightvision overnight) we saw ~7h of every-minute
      // increments. Threshold > %(threshold)d increments over 15m
      // separates boundary from sustained.
      //
      // Catches structural overlap BEFORE the IOTZoneStateChurn alert
      // above does: state churn is the downstream symptom (state flips
      // visible on the lamp); per-axis conflict is the upstream cause
      // (Condition windows overlap).
      //
      // Action: open the dashboard's "Shadow Resolver Conflicts"
      // panel, filter to the firing zone+axis, identify the two
      // Conditions that overlap, decide which one should narrow.
      alert: 'IOTConditionConflict',
      expr: |||
        sum by (zone, axis) (
          increase(iotcontroller_conditioner_shadow_conflicts_total[15m])
        ) > %(threshold)d
      ||| % { threshold: cfg.conditionConflictPerWindow },
      'for': cfg.conditionConflictFor,
      labels: {
        severity: 'warning',
        team: 'home-automation',
      },
      annotations: {
        summary: '{{ $labels.zone }}/{{ $labels.axis }} has overlapping Conditions ({{ $value | printf "%.0f" }} conflicts in 15m)',
        description: |||
          The shadow resolver found %(threshold)d+ structural conflicts on zone={{ $labels.zone }}
          axis={{ $labels.axis }} over the last 15 minutes, sustained for %(for)s. This means
          two Conditions in their TimeIntervals windows are both claiming the same axis on the
          same zone, every eval tick. The imperative path is resolving via last-write-wins;
          the lamp's behavior depends on Condition ordering, not operator intent.

          Open the IOT Controller dashboard's "Shadow Resolver Conflicts (multi-contributor axes)"
          panel, filter to zone={{ $labels.zone }}, and inspect the controller logs for
          'shadow: multi-contributor conflict on zone' messages — they name the conflicting
          Conditions in the contributors field.
        ||| % { threshold: cfg.conditionConflictPerWindow, 'for': cfg.conditionConflictFor },
      },
    },
  ],
}
