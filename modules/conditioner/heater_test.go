package conditioner

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// heater_test.go — Phase D prerequisite. Locks in the heater safety
// invariants on the CURRENT alert-driven imperative path so reviewers
// of any future migration (Phase D moves the heater zone into
// cfg.ReconcileZones) have a baseline to compare against.
//
// The production pattern: a Condition declares Matches against the
// zoneTempLow / zoneTempHigh alert labels and pairs a low Remediation
// (active_state=on, inactive_state=on) with a high Remediation
// (active_state=off, inactive_state=off). The "withHeater" alert
// pair in deployment_tools is the canonical case; this file
// reconstructs that shape in-process so the invariants are testable.
//
// What we assert here:
//
//  1. zoneTempLow firing → heater zone gets SetState(ON)
//  2. zoneTempHigh firing → heater zone gets SetState(OFF)
//  3. Resolve preserves hysteresis when InactiveState is empty (the
//     "withHeater pattern" — resolved low must NOT turn the heater
//     off, otherwise a brief overshoot short-cycles the relay).
//  4. zoneTempLow firing-outside-window must force-deactivate. The
//     window-close safety contract that v0.6.5 introduced.

// heaterCondition assembles the Condition shape the deployment_tools'
// `withHeater(zoneName, lowThreshold, highThreshold)` builder
// produces. Two Remediations, one per direction; both target the
// heater zone (not the temperature-sensor zone).
func heaterCondition(name, heaterZone, alertName, alertZone, state string, inactive string) apiv1.Condition {
	return apiv1.Condition{
		ObjectMeta: metav1Meta(name),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Matches: []apiv1.Match{{
				Labels: map[string]string{
					"alertname": alertName,
					"zone":      alertZone,
				},
			}},
			Remediations: []apiv1.Remediation{{
				Zone:          heaterZone,
				ActiveState:   state,
				InactiveState: inactive,
			}},
		},
	}
}

func TestHeater_TempLowFiring_TurnsOn(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cond := heaterCondition(
		"office-heater-low",
		"office-heater", // target zone (the relay)
		"zoneTempLow",
		"office", // sensor zone (where the alert label points)
		"on",
		// Empty inactive: resolved low preserves hysteresis. The
		// pairing condition (office-heater-high with inactive_state=off)
		// drives the off direction.
		"",
	)

	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{conditions: []apiv1.Condition{cond}}
	c, err := New(Config{ApplyDesiredRefreshAge: time.Hour}, logger, rec, kube)
	require.NoError(t, err)

	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name:   "zoneTempLow",
		Zone:   "office",
		Status: "firing",
	})
	require.NoError(t, err)

	require.Equal(t, 1, rec.setStateCount(), "low-temp firing must fire exactly one SetState")
	name, state := rec.firstSetState()
	require.Equal(t, "office-heater", name, "SetState targets the heater zone, not the sensor zone")
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, state, "low-temp must turn the heater ON")
}

func TestHeater_TempHighFiring_TurnsOff(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cond := heaterCondition(
		"office-heater-high",
		"office-heater",
		"zoneTempHigh",
		"office",
		"off",
		"off",
	)

	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{conditions: []apiv1.Condition{cond}}
	c, err := New(Config{ApplyDesiredRefreshAge: time.Hour}, logger, rec, kube)
	require.NoError(t, err)

	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name:   "zoneTempHigh",
		Zone:   "office",
		Status: "firing",
	})
	require.NoError(t, err)

	require.Equal(t, 1, rec.setStateCount())
	name, state := rec.firstSetState()
	require.Equal(t, "office-heater", name)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, state, "high-temp must turn the heater OFF")
}

// TestHeater_HysteresisPreservedOnLowResolve — the canonical
// short-cycling regression. Low-temp resolves when temp briefly
// crosses the threshold; if Remediation.InactiveState were "off",
// the heater would shut off mid-warmup and the cycle would repeat.
// The withHeater pattern leaves InactiveState empty for the low
// Condition so resolve produces a no-op deactivate (deactivateRequest
// returns nil with no inactive state declared).
func TestHeater_HysteresisPreservedOnLowResolve(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cond := heaterCondition(
		"office-heater-low",
		"office-heater",
		"zoneTempLow",
		"office",
		"on",
		"", // critical: empty inactive_state preserves hysteresis
	)

	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{conditions: []apiv1.Condition{cond}}
	c, err := New(Config{ApplyDesiredRefreshAge: time.Hour}, logger, rec, kube)
	require.NoError(t, err)

	// Fire then resolve.
	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name: "zoneTempLow", Zone: "office", Status: "firing",
	})
	require.NoError(t, err)
	require.Equal(t, 1, rec.setStateCount(), "firing → ON")

	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name: "zoneTempLow", Zone: "office", Status: "resolved",
	})
	require.NoError(t, err)

	// Resolve with empty InactiveState should NOT have produced a
	// second SetState. The withHeater pattern relies on this:
	// resolved-low is a no-op; the paired high Condition's firing
	// drives the off direction.
	require.Equal(t, 1, rec.setStateCount(),
		"resolved low with empty InactiveState must NOT short-cycle the heater (still 1 SetState total)")
}

// TestHeater_FiringOutsideWindow_ForcesOff — when an alert fires
// while the Remediation's TimeInterval is closed, forceDeactivate
// infers OFF from the active_state. v0.6.5's contract: outside
// window must reach a safe state. This is the second-line safety
// net for "alert started firing inside window, kept firing as window
// closed."
func TestHeater_FiringOutsideWindow_ForcesOff(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Build a Condition with a TimeInterval that never matches "now"
	// (Years=1970). Alert firing against this Condition must drive
	// the heater OFF via forceDeactivate's inferDefaultInactive path.
	rem := apiv1.Remediation{
		Zone:        "office-heater",
		ActiveState: "on",
		// No InactiveState — forceDeactivate infers OFF from active=on.
		TimeIntervals: []apiv1.TimeIntervalSpec{{Years: []string{"1970"}}},
	}
	cond := apiv1.Condition{
		ObjectMeta: metav1Meta("office-heater-low"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Matches: []apiv1.Match{{
				Labels: map[string]string{"alertname": "zoneTempLow", "zone": "office"},
			}},
			Remediations: []apiv1.Remediation{rem},
		},
	}

	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{conditions: []apiv1.Condition{cond}}
	c, err := New(Config{ApplyDesiredRefreshAge: time.Hour}, logger, rec, kube)
	require.NoError(t, err)

	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name: "zoneTempLow", Zone: "office", Status: "firing",
	})
	require.NoError(t, err)

	require.Equal(t, 1, rec.setStateCount(), "firing-outside-window must produce one SetState")
	name, state := rec.firstSetState()
	require.Equal(t, "office-heater", name)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, state,
		"firing-outside-window forces OFF — heater safe state when the window is closed")
}

// TestHeater_PairedConditionsCommutate — the full withHeater pattern
// reproduces the production heater behavior: low fires ON, high fires
// OFF, both conditions enabled simultaneously. Alerts can arrive in
// any order; each direction is asserted by its own Condition.
func TestHeater_PairedConditionsCommutate(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	low := heaterCondition("h-low", "office-heater", "zoneTempLow", "office", "on", "")
	high := heaterCondition("h-high", "office-heater", "zoneTempHigh", "office", "off", "off")

	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{conditions: []apiv1.Condition{low, high}}
	c, err := New(Config{ApplyDesiredRefreshAge: time.Hour}, logger, rec, kube)
	require.NoError(t, err)

	// Sequence: low fires → ON. High fires → OFF. Low resolves
	// (hysteresis, no-op). High resolves (deactivate to off — same as
	// active for high, so no change).
	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{Name: "zoneTempLow", Zone: "office", Status: "firing"})
	require.NoError(t, err)
	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{Name: "zoneTempHigh", Zone: "office", Status: "firing"})
	require.NoError(t, err)
	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{Name: "zoneTempLow", Zone: "office", Status: "resolved"})
	require.NoError(t, err)

	// Two SetState transitions: firing-low → ON, firing-high → OFF.
	// Resolved-low is no-op (no InactiveState). No short-cycle.
	require.Equal(t, 2, rec.setStateCount(),
		"paired conditions produce exactly two SetStates (low→ON, high→OFF) and the resolved-low no-ops")
}
