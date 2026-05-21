package conditioner

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// TestShadow_SingleContributorComposes — one Remediation, in window,
// claiming state=on. Shadow's target should be ON with exactly one
// contributor recorded.
func TestShadow_SingleContributorComposes(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("foyer-on"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "foyer",
				ActiveState:   "on",
				TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
			}},
		},
	}}

	c, err := New(Config{}, logger, &recordingZoneKeeper{}, &listKubeClient{items: conds})
	require.NoError(t, err)

	trace, err := c.computeZoneTarget(ctx, "foyer", time.Now())
	require.NoError(t, err)

	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, trace.target.State,
		"single state=on Remediation should set target state to ON")
	require.False(t, trace.hasConflict(),
		"single contributor on each axis is not a conflict")
	require.Len(t, trace.contributors[axisState], 1)
	require.Equal(t, "foyer-on", trace.contributors[axisState][0].condition)
}

// TestShadow_MultipleContributorsDetectConflict — two Remediations
// disagree on state for the same zone (state=on and state=off, both
// in window). Shadow should detect a conflict on the state axis and
// surface both contributors. The "winner" by last-write-wins is the
// last-declared one (matching today's imperative behavior), but the
// loser is preserved in the contributor list for diagnosis.
func TestShadow_MultipleContributorsDetectConflict(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	conds := []apiv1.Condition{
		{
			ObjectMeta: metav1Meta("foyer-on"),
			Spec: apiv1.ConditionSpec{
				Enabled: true,
				Remediations: []apiv1.Remediation{{
					Zone:          "foyer",
					ActiveState:   "on",
					TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
				}},
			},
		},
		{
			ObjectMeta: metav1Meta("foyer-off"),
			Spec: apiv1.ConditionSpec{
				Enabled: true,
				Remediations: []apiv1.Remediation{{
					Zone:          "foyer",
					ActiveState:   "off",
					TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
				}},
			},
		},
	}

	c, err := New(Config{}, logger, &recordingZoneKeeper{}, &listKubeClient{items: conds})
	require.NoError(t, err)

	trace, err := c.computeZoneTarget(ctx, "foyer", time.Now())
	require.NoError(t, err)

	require.True(t, trace.hasConflict(),
		"two Remediations claiming state on the same zone is a conflict")
	require.ElementsMatch(t, []string{"state"}, trace.conflictAxes())
	require.Len(t, trace.contributors[axisState], 2,
		"both Remediations should appear in the contributor list")
}

// TestShadow_TimeGatedRemediationOutOfScope — a Remediation whose
// time_intervals don't cover `now` must not contribute. The classic
// case the imperative path already gates correctly; shadow must too.
func TestShadow_TimeGatedRemediationOutOfScope(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// 24:00-24:00 is an empty window — Prometheus treats start>=end
	// as "never matches". Same pattern the evaluator's own
	// out-of-window test uses.
	never := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "24:00", EndTime: "24:00"}},
	}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("foyer-never"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "foyer",
				ActiveState:   "on",
				TimeIntervals: []apiv1.TimeIntervalSpec{never},
			}},
		},
	}}

	c, err := New(Config{}, logger, &recordingZoneKeeper{}, &listKubeClient{items: conds})
	require.NoError(t, err)

	trace, err := c.computeZoneTarget(ctx, "foyer", time.Now())
	require.NoError(t, err)

	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED, trace.target.State,
		"out-of-window Remediation must not contribute to the target")
	require.Empty(t, trace.contributors[axisState],
		"out-of-window Remediation must not appear in contributors")
}

// TestShadow_AlertDrivenSkipped — a Condition with Matches set is
// alert-driven; activation depends on alert history we don't model
// in v1. Confirm the shadow skips these even if they have
// time_intervals (heater Conditions look like this).
func TestShadow_AlertDrivenSkipped(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("prop-house-low-temp"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Matches: []apiv1.Match{{Labels: map[string]string{
				"alertname": "zoneTempLow:prop-house",
				"zone":      "prop-house",
			}}},
			Remediations: []apiv1.Remediation{{
				Zone:          "prop-house-heater",
				ActiveState:   "on",
				TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
			}},
		},
	}}

	c, err := New(Config{}, logger, &recordingZoneKeeper{}, &listKubeClient{items: conds})
	require.NoError(t, err)

	trace, err := c.computeZoneTarget(ctx, "prop-house-heater", time.Now())
	require.NoError(t, err)

	require.Empty(t, trace.contributors[axisState],
		"alert-driven Remediations are out-of-scope for v1 shadow")
}

// TestShadow_ActiveComputeSkipped — Remediations with active_compute
// are skipped in v1 (Computer side-effect safety). Confirm the
// circadian-style Condition doesn't appear in the contributor list.
func TestShadow_ActiveComputeSkipped(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("office-circadian"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "office",
				ActiveCompute: "circadian",
				TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
			}},
		},
	}}

	c, err := New(Config{}, logger, &recordingZoneKeeper{}, &listKubeClient{items: conds})
	require.NoError(t, err)

	trace, err := c.computeZoneTarget(ctx, "office", time.Now())
	require.NoError(t, err)

	require.Empty(t, trace.contributors[axisColorTemperature],
		"active_compute Remediations are out-of-scope for v1 shadow")
	require.Empty(t, trace.contributors[axisState])
}

// TestShadow_DisabledConditionIgnored — Spec.Enabled=false means no
// contribution, even when the Remediation otherwise would qualify.
func TestShadow_DisabledConditionIgnored(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("foyer-on-disabled"),
		Spec: apiv1.ConditionSpec{
			Enabled: false,
			Remediations: []apiv1.Remediation{{
				Zone:          "foyer",
				ActiveState:   "on",
				TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
			}},
		},
	}}

	c, err := New(Config{}, logger, &recordingZoneKeeper{}, &listKubeClient{items: conds})
	require.NoError(t, err)

	trace, err := c.computeZoneTarget(ctx, "foyer", time.Now())
	require.NoError(t, err)

	require.Empty(t, trace.contributors[axisState],
		"disabled Condition must not contribute")
}

// TestShadow_ConflictMetricIncrements — running the per-tick hook
// against two-Remediation conflict should increment the conflict
// counter on the state axis exactly once.
func TestShadow_ConflictMetricIncrements(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	conds := []apiv1.Condition{
		{
			ObjectMeta: metav1Meta("foyer-pingpong-on"),
			Spec: apiv1.ConditionSpec{
				Enabled: true,
				Remediations: []apiv1.Remediation{{
					Zone:          "foyer-pingpong",
					ActiveState:   "on",
					TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
				}},
			},
		},
		{
			ObjectMeta: metav1Meta("foyer-pingpong-off"),
			Spec: apiv1.ConditionSpec{
				Enabled: true,
				Remediations: []apiv1.Remediation{{
					Zone:          "foyer-pingpong",
					ActiveState:   "off",
					TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
				}},
			},
		},
	}

	c, err := New(Config{}, logger, &recordingZoneKeeper{}, &listKubeClient{items: conds})
	require.NoError(t, err)

	before := testutil.ToFloat64(metricShadowConflict.WithLabelValues("foyer-pingpong", "state"))
	c.runShadow(ctx)
	after := testutil.ToFloat64(metricShadowConflict.WithLabelValues("foyer-pingpong", "state"))

	require.Equal(t, before+1, after,
		"runShadow should increment the conflict counter by 1 for the affected (zone, axis)")
}
