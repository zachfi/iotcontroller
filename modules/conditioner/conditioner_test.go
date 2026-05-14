package conditioner

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/mocks"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeKubeClient is a minimal kubeclient.Client for tests. List handles
// ConditionList; Get handles Zone (used by resolveToggleState). Other
// methods panic.
type fakeKubeClient struct {
	conditions []apiv1.Condition
	// zones[zoneName] = the Zone CR returned by Get for that name. If
	// the zone isn't in this map, Get returns a NotFound-style error.
	zones map[string]apiv1.Zone
}

func (f *fakeKubeClient) List(_ context.Context, list kubeclient.ObjectList, _ ...kubeclient.ListOption) error {
	if cl, ok := list.(*apiv1.ConditionList); ok {
		cl.Items = append(cl.Items, f.conditions...)
	}
	return nil
}

func (f *fakeKubeClient) Get(_ context.Context, key kubeclient.ObjectKey, obj kubeclient.Object, _ ...kubeclient.GetOption) error {
	if z, ok := obj.(*apiv1.Zone); ok {
		stored, found := f.zones[key.Name]
		if !found {
			return errExpectError
		}
		*z = stored
		return nil
	}
	panic("not implemented")
}
func (f *fakeKubeClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...kubeclient.ApplyOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Create(_ context.Context, _ kubeclient.Object, _ ...kubeclient.CreateOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Delete(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Update(_ context.Context, _ kubeclient.Object, _ ...kubeclient.UpdateOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Patch(_ context.Context, _ kubeclient.Object, _ kubeclient.Patch, _ ...kubeclient.PatchOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) DeleteAllOf(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteAllOfOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Status() kubeclient.SubResourceWriter              { panic("not implemented") }
func (f *fakeKubeClient) SubResource(_ string) kubeclient.SubResourceClient { panic("not implemented") }
func (f *fakeKubeClient) Scheme() *runtime.Scheme                           { panic("not implemented") }
func (f *fakeKubeClient) RESTMapper() meta.RESTMapper                       { panic("not implemented") }
func (f *fakeKubeClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	panic("not implemented")
}
func (f *fakeKubeClient) IsObjectNamespaced(_ runtime.Object) (bool, error) {
	panic("not implemented")
}

var errExpectError = errors.New("expect error")

// recordingZoneKeeper records SetState/SetScene/AdjustBrightness calls for tests.
type recordingZoneKeeper struct {
	mocks.ZoneKeeperClientMock
	mu                sync.Mutex
	setStateCalls     []*iotv1proto.SetStateRequest
	setSceneCalls     []*iotv1proto.SetSceneRequest
	adjustBrightCalls []*iotv1proto.AdjustBrightnessRequest
}

func (r *recordingZoneKeeper) SetState(ctx context.Context, req *iotv1proto.SetStateRequest, opts ...grpc.CallOption) (*iotv1proto.SetStateResponse, error) {
	r.mu.Lock()
	if req != nil {
		r.setStateCalls = append(r.setStateCalls, &iotv1proto.SetStateRequest{Name: req.Name, State: req.State})
	}
	r.mu.Unlock()
	return r.ZoneKeeperClientMock.SetState(ctx, req, opts...)
}

func (r *recordingZoneKeeper) SetScene(ctx context.Context, req *iotv1proto.SetSceneRequest, opts ...grpc.CallOption) (*iotv1proto.SetSceneResponse, error) {
	r.mu.Lock()
	if req != nil {
		r.setSceneCalls = append(r.setSceneCalls, &iotv1proto.SetSceneRequest{Name: req.Name, Scene: req.Scene})
	}
	r.mu.Unlock()
	return r.ZoneKeeperClientMock.SetScene(ctx, req, opts...)
}

func (r *recordingZoneKeeper) setStateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.setStateCalls)
}

func (r *recordingZoneKeeper) setSceneCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.setSceneCalls)
}

func (r *recordingZoneKeeper) AdjustBrightness(ctx context.Context, req *iotv1proto.AdjustBrightnessRequest, opts ...grpc.CallOption) (*iotv1proto.AdjustBrightnessResponse, error) {
	r.mu.Lock()
	if req != nil {
		r.adjustBrightCalls = append(r.adjustBrightCalls, &iotv1proto.AdjustBrightnessRequest{Name: req.Name, Delta: req.Delta})
	}
	r.mu.Unlock()
	return r.ZoneKeeperClientMock.AdjustBrightness(ctx, req, opts...)
}

func (r *recordingZoneKeeper) adjustBrightCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.adjustBrightCalls)
}

func (r *recordingZoneKeeper) firstSetState() (name string, state iotv1proto.ZoneState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.setStateCalls) == 0 {
		return "", iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED
	}
	req := r.setStateCalls[0]
	return req.Name, req.State
}

func Test_withinActiveWindow(t *testing.T) {
	var (
		cfg = Config{
			EpochTimeWindow: time.Hour,
		}
		testLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
		ctx        = context.Background()
	)

	cases := []struct {
		expected bool
		name     string
		now      string
		rem      apiv1.Remediation
		req      *iotv1proto.AlertRequest
	}{
		{
			expected: true,
			name:     "empty remediation",
			now:      "2025-12-12T04:23:28Z",
			rem:      apiv1.Remediation{},
		},
		{
			expected: true,
			name:     "time interval inside",
			now:      "2025-12-12T04:23:28Z",
			rem: apiv1.Remediation{
				TimeIntervals: []apiv1.TimeIntervalSpec{
					{
						Times: []apiv1.TimePeriod{
							{
								StartTime: "04:00",
								EndTime:   "05:00",
							},
						},
					},
				},
			},
		},
		{
			expected: false,
			name:     "time interval outside before",
			now:      "2025-12-12T03:30:00Z",
			rem: apiv1.Remediation{
				TimeIntervals: []apiv1.TimeIntervalSpec{
					{
						Times: []apiv1.TimePeriod{
							{StartTime: "04:00", EndTime: "05:00"},
						},
					},
				},
			},
		},
		{
			expected: false,
			name:     "time interval outside after",
			now:      "2025-12-12T05:30:00Z",
			rem: apiv1.Remediation{
				TimeIntervals: []apiv1.TimeIntervalSpec{
					{
						Times: []apiv1.TimePeriod{
							{StartTime: "04:00", EndTime: "05:00"},
						},
					},
				},
			},
		},
	}

	c, err := New(cfg, testLogger, nil, nil)
	require.NoError(t, err)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			require.NoError(t, err)

			require.Equal(t, tc.expected, c.withinActiveWindow(ctx, tc.rem, now))
		})
	}
}

func Test_matchConditio(t *testing.T) {
	cases := []struct {
		expected bool
		name     string
		labels   map[string]string
		req      apiv1.Condition
	}{
		{
			name: "not enabled",
			labels: map[string]string{
				"key1": "value1",
			},
			req:      apiv1.Condition{},
			expected: false,
		},
		{
			name: "enabled but with no matches",
			labels: map[string]string{
				"key1": "value1",
			},
			req: apiv1.Condition{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
				},
			},
			expected: false,
		},
		{
			name: "enabled with matches",
			labels: map[string]string{
				"key1": "value1",
			},
			req: apiv1.Condition{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{
							Labels: map[string]string{
								"key1": "value1",
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "enabled with value non-matches",
			labels: map[string]string{
				"key1": "value1",
			},
			req: apiv1.Condition{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{
							Labels: map[string]string{
								"key1": "value2",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "enabled with key non-matches",
			labels: map[string]string{
				"key1": "value1",
			},
			req: apiv1.Condition{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{
							Labels: map[string]string{
								"key2": "value1",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "multiple labels matching single",
			labels: map[string]string{
				"key1": "value1",
			},
			req: apiv1.Condition{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{
							Labels: map[string]string{
								"key1": "value1",
								"key2": "value2",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "multiple labels matching multiple",
			labels: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			req: apiv1.Condition{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{
							Labels: map[string]string{
								"key1": "value1",
								"key2": "value2",
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "multiple matches matching multiple labels",
			labels: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			req: apiv1.Condition{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{
							Labels: map[string]string{
								"key1": "value1",
							},
						},
						{
							Labels: map[string]string{
								"key2": "value2",
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "multiple matches not matching multiple labels",
			labels: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			req: apiv1.Condition{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{
							Labels: map[string]string{
								"key1": "value1",
							},
						},
						{
							Labels: map[string]string{
								"key2": "value3",
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	var (
		cfg        = Config{}
		testLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
		ctx        = context.Background()
	)

	c, err := New(cfg, testLogger, nil, nil)
	require.NoError(t, err)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, c.matchCondition(ctx, tc.labels, tc.req))
		})
	}
}

func Test_epochWindow(t *testing.T) {
	cases := []struct {
		name        string
		epochStr    string
		when        apiv1.When
		start, stop time.Time
		err         error
	}{
		{
			name:     "within window",
			epochStr: "2025-12-12T04:00:00Z",
			when: apiv1.When{
				Start: "-30m",
				Stop:  "30m",
			},
			start: time.Date(2025, 12, 12, 3, 30, 0, 0, time.UTC),
			stop:  time.Date(2025, 12, 12, 4, 30, 0, 0, time.UTC),
		},
		{
			name:     "within window without when",
			epochStr: "2025-12-12T04:00:00Z",
			when:     apiv1.When{},
			start:    time.Date(2025, 12, 12, 3, 59, 0, 0, time.UTC),
			stop:     time.Date(2025, 12, 12, 5, 0o0, 0, 0, time.UTC),
		},
		{
			name:     "invalid When Start duration",
			epochStr: "2025-12-12T04:00:00Z",
			when:     apiv1.When{Start: "not-a-duration"},
			err:      errExpectError,
		},
		{
			name:     "invalid When Stop duration",
			epochStr: "2025-12-12T04:00:00Z",
			when:     apiv1.When{Stop: "bad"},
			err:      errExpectError,
		},
	}

	var (
		cfg = Config{
			EpochTimeWindow: time.Hour,
		}
		testLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
		ctx        = context.Background()
	)

	c, err := New(cfg, testLogger, nil, nil)
	require.NoError(t, err)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			epoch, err := time.Parse(time.RFC3339, tc.epochStr)
			require.NoError(t, err)

			start, stop, err := c.epochWindow(ctx, epoch, tc.when)
			if tc.err != nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.start, start)
				require.Equal(t, tc.stop, stop)
			}
		})
	}
}

func Test_runAlertWindowCheckAt(t *testing.T) {
	rec := &recordingZoneKeeper{}
	cfg := Config{EpochTimeWindow: time.Hour}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, nil)
	require.NoError(t, err)

	rem := apiv1.Remediation{
		Zone:          "zone1",
		InactiveState: "off",
		TimeIntervals: []apiv1.TimeIntervalSpec{
			{
				Times: []apiv1.TimePeriod{
					{StartTime: "04:00", EndTime: "05:00"},
				},
			},
		},
	}
	c.trackAlertActive("cond1", rem)

	now, err := time.Parse(time.RFC3339, "2025-12-12T05:30:00Z")
	require.NoError(t, err)
	c.runAlertWindowCheckAt(ctx, now)

	require.Equal(t, 1, rec.setStateCount(), "expected one deactivate SetState call")
	name, state := rec.firstSetState()
	require.Equal(t, "zone1", name)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, state)
	require.Equal(t, 0, rec.setSceneCount())
	c.alertActiveMu.Lock()
	_, stillActive := c.alertActive[alertActiveKey("cond1", "zone1")]
	c.alertActiveMu.Unlock()
	require.False(t, stillActive, "remediation should be removed from alertActive")
}

// Test_runAlertWindowCheckAt_HeaterShape pins the regression that motivated
// the forceDeactivate split: when a low-temp heater Condition (active='on',
// no InactiveState, to preserve in-window hysteresis with a paired
// high-temp Condition) is alert-active and its window closes, the heater
// MUST be turned off. Otherwise the heater is stuck on until the next
// in-window high-temp firing, which may never happen if temp coasts in
// the hysteresis band overnight.
func Test_runAlertWindowCheckAt_HeaterShape_LowTempForcesOff(t *testing.T) {
	rec := &recordingZoneKeeper{}
	cfg := Config{EpochTimeWindow: time.Hour}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, logger, rec, nil)
	require.NoError(t, err)

	// Mirrors the deployed `zone-office-low-temp` Condition shape:
	// active='on', NO InactiveState (the helper's
	// `// inactive='off', // Only turn on the heater, let the high temp
	// alert turn it off.` line), and a TimeIntervals window.
	rem := apiv1.Remediation{
		Zone:        "office-heater",
		ActiveState: "on",
		TimeIntervals: []apiv1.TimeIntervalSpec{
			{Times: []apiv1.TimePeriod{{StartTime: "14:30", EndTime: "21:00"}}},
		},
	}
	c.trackAlertActive("zone-office-low-temp", rem)

	// 21:30 — 30 min past the window close while heater is still alert-active.
	now, err := time.Parse(time.RFC3339, "2026-05-14T21:30:00Z")
	require.NoError(t, err)
	c.runAlertWindowCheckAt(ctx, now)

	require.Equal(t, 1, rec.setStateCount(), "window close on heater-low shape MUST force the zone off")
	name, state := rec.firstSetState()
	require.Equal(t, "office-heater", name)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, state, "inferred OFF from non-OFF ActiveState")

	c.alertActiveMu.Lock()
	_, stillActive := c.alertActive[alertActiveKey("zone-office-low-temp", "office-heater")]
	c.alertActiveMu.Unlock()
	require.False(t, stillActive)
}

// Test_runAlertWindowCheckAt_HeaterShape_HighTempIsNoop pins the
// other half of the heater contract: the paired high-temp Condition
// (active='off', no InactiveState) is already in the safe state when
// firing — its window closing should produce zero SetState calls.
// Inverting "off" would be wrong: the heater is supposed to STAY off.
func Test_runAlertWindowCheckAt_HeaterShape_HighTempIsNoop(t *testing.T) {
	rec := &recordingZoneKeeper{}
	cfg := Config{EpochTimeWindow: time.Hour}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, logger, rec, nil)
	require.NoError(t, err)

	rem := apiv1.Remediation{
		Zone:        "office-heater",
		ActiveState: "off",
		TimeIntervals: []apiv1.TimeIntervalSpec{
			{Times: []apiv1.TimePeriod{{StartTime: "14:30", EndTime: "21:00"}}},
		},
	}
	c.trackAlertActive("zone-office-high-temp", rem)

	now, err := time.Parse(time.RFC3339, "2026-05-14T21:30:00Z")
	require.NoError(t, err)
	c.runAlertWindowCheckAt(ctx, now)

	require.Equal(t, 0, rec.setStateCount(), "active='off' window close must be a no-op (zone already safe)")
	require.Equal(t, 0, rec.setSceneCount())

	c.alertActiveMu.Lock()
	_, stillActive := c.alertActive[alertActiveKey("zone-office-high-temp", "office-heater")]
	c.alertActiveMu.Unlock()
	require.False(t, stillActive, "remediation should still be removed from alertActive even on no-op")
}

// Test_inferDefaultInactive covers each ActiveState value the operator
// might supply to make sure window-close inference picks a safe state
// in every case.
func Test_inferDefaultInactive(t *testing.T) {
	cases := []struct {
		active string
		want   iotv1proto.ZoneState
	}{
		{active: "on", want: iotv1proto.ZoneState_ZONE_STATE_OFF},
		{active: "color", want: iotv1proto.ZoneState_ZONE_STATE_OFF},
		{active: "randomcolor", want: iotv1proto.ZoneState_ZONE_STATE_OFF},
		{active: "offtimer", want: iotv1proto.ZoneState_ZONE_STATE_OFF},
		// "off" and empty are already-safe states — no-op (UNSPECIFIED).
		{active: "off", want: iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED},
		{active: "", want: iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED},
		// Unknown strings → UNSPECIFIED so we don't guess wrong.
		{active: "garbage", want: iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.active, func(t *testing.T) {
			got := inferDefaultInactive(apiv1.Remediation{ActiveState: tc.active})
			require.Equal(t, tc.want, got)
		})
	}
}

func Test_timeContains(t *testing.T) {
	cases := []struct {
		name           string
		expected       bool
		t, start, stop time.Time
	}{
		{
			name:     "inside interval",
			expected: true,
			t:        time.Date(2025, 12, 12, 4, 30, 0, 0, time.UTC),
			start:    time.Date(2025, 12, 12, 4, 0, 0, 0, time.UTC),
			stop:     time.Date(2025, 12, 12, 5, 0, 0, 0, time.UTC),
		},
		{
			name:     "outside interval before",
			expected: false,
			t:        time.Date(2025, 12, 12, 3, 30, 0, 0, time.UTC),
			start:    time.Date(2025, 12, 12, 4, 0, 0, 0, time.UTC),
			stop:     time.Date(2025, 12, 12, 5, 0, 0, 0, time.UTC),
		},
		{
			name:     "outside interval after",
			expected: false,
			t:        time.Date(2025, 12, 12, 5, 30, 0, 0, time.UTC),
			start:    time.Date(2025, 12, 12, 4, 0, 0, 0, time.UTC),
			stop:     time.Date(2025, 12, 12, 5, 0, 0, 0, time.UTC),
		},
		{
			name:     "on start",
			expected: true,
			t:        time.Date(2025, 12, 12, 4, 0, 0, 0, time.UTC),
			start:    time.Date(2025, 12, 12, 4, 0, 0, 0, time.UTC),
			stop:     time.Date(2025, 12, 12, 5, 0, 0, 0, time.UTC),
		},
		{
			name:     "on stop",
			expected: true,
			t:        time.Date(2025, 12, 12, 5, 0, 0, 0, time.UTC),
			start:    time.Date(2025, 12, 12, 4, 0, 0, 0, time.UTC),
			stop:     time.Date(2025, 12, 12, 5, 0, 0, 0, time.UTC),
		},
	}

	var (
		cfg        = Config{}
		testLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
		ctx        = context.Background()
	)

	c, err := New(cfg, testLogger, nil, nil)
	require.NoError(t, err)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, c.timeContains(ctx, tc.t, tc.start, tc.stop))
		})
	}
}

var testTargetZone = "test-target"

func Test_alertActiveKey(t *testing.T) {
	// Same inputs produce same key; different inputs produce different keys.
	require.Equal(t, "cond1/zone-a", alertActiveKey("cond1", "zone-a"))
	require.Equal(t, alertActiveKey("cond1", "zone-a"), alertActiveKey("cond1", "zone-a"))
	require.NotEqual(t, alertActiveKey("cond1", "zone-a"), alertActiveKey("cond2", "zone-a"))
	require.NotEqual(t, alertActiveKey("cond1", "zone-a"), alertActiveKey("cond1", "zone-b"))
}

func Test_trackAlertActive_untrackAlertActive(t *testing.T) {
	cfg := Config{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	c, err := New(cfg, testLogger, nil, nil)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "test-zone", ActiveState: "off"}
	condName := "test-condition"

	// Initially empty
	c.alertActiveMu.Lock()
	require.Len(t, c.alertActive, 0)
	c.alertActiveMu.Unlock()

	// Track adds entry
	c.trackAlertActive(condName, rem)
	c.alertActiveMu.Lock()
	require.Len(t, c.alertActive, 1)
	stored, ok := c.alertActive[alertActiveKey(condName, rem.Zone)]
	require.True(t, ok)
	require.Equal(t, rem.Zone, stored.Zone)
	c.alertActiveMu.Unlock()

	// Untrack removes entry
	c.untrackAlertActive(condName, rem)
	c.alertActiveMu.Lock()
	require.Len(t, c.alertActive, 0)
	c.alertActiveMu.Unlock()

	// Track two, untrack one
	rem2 := apiv1.Remediation{Zone: "other-zone", InactiveState: "on"}
	c.trackAlertActive(condName, rem)
	c.trackAlertActive("other-cond", rem2)
	c.alertActiveMu.Lock()
	require.Len(t, c.alertActive, 2)
	c.alertActiveMu.Unlock()
	c.untrackAlertActive(condName, rem)
	c.alertActiveMu.Lock()
	require.Len(t, c.alertActive, 1)
	_, ok = c.alertActive[alertActiveKey("other-cond", rem2.Zone)]
	require.True(t, ok)
	c.alertActiveMu.Unlock()
}

func Test_zoneState(t *testing.T) {
	tests := []struct {
		state    string
		expected iotv1proto.ZoneState
	}{
		{"on", iotv1proto.ZoneState_ZONE_STATE_ON},
		{"off", iotv1proto.ZoneState_ZONE_STATE_OFF},
		{"offtimer", iotv1proto.ZoneState_ZONE_STATE_OFFTIMER},
		{"color", iotv1proto.ZoneState_ZONE_STATE_COLOR},
		{"randomcolor", iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR},
		{"ZONE_STATE_ON", iotv1proto.ZoneState_ZONE_STATE_ON},
		{"unknown", iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED},
		{"", iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			got := zoneState(tt.state)
			require.Equal(t, tt.expected, got)
		})
	}
}

func Test_request(t *testing.T) {
	cases := []struct {
		name               string
		rem                apiv1.Remediation
		activateNil        bool
		activateSceneReq   *iotv1proto.SetSceneRequest
		activateStateReq   *iotv1proto.SetStateRequest
		deactivateNil      bool
		deactivateSceneReq *iotv1proto.SetSceneRequest
		deactivateStateReq *iotv1proto.SetStateRequest
	}{
		{
			name:          "empty remediation",
			rem:           apiv1.Remediation{},
			activateNil:   true,
			deactivateNil: true,
		},
		{
			name: "activate scene only",
			rem: apiv1.Remediation{
				Zone:        testTargetZone,
				ActiveScene: "scene1",
			},
			activateSceneReq: &iotv1proto.SetSceneRequest{
				Name:  testTargetZone,
				Scene: "scene1",
			},
			deactivateNil: true,
		},
		{
			name: "activate state only",
			rem: apiv1.Remediation{
				Zone:        testTargetZone,
				ActiveState: "on",
			},
			activateStateReq: &iotv1proto.SetStateRequest{
				Name:  testTargetZone,
				State: iotv1proto.ZoneState_ZONE_STATE_ON,
			},
			deactivateNil: true,
		},
		{
			name: "deactivate scene only",
			rem: apiv1.Remediation{
				Zone:          testTargetZone,
				InactiveScene: "scene1",
			},
			deactivateSceneReq: &iotv1proto.SetSceneRequest{
				Name:  testTargetZone,
				Scene: "scene1",
			},
			activateNil: true,
		},
		{
			name: "deactivate state only",
			rem: apiv1.Remediation{
				Zone:          testTargetZone,
				InactiveState: "off",
			},
			deactivateStateReq: &iotv1proto.SetStateRequest{
				Name:  testTargetZone,
				State: iotv1proto.ZoneState_ZONE_STATE_OFF,
			},
			activateNil: true,
		},
		{
			name: "test long name scene and state",
			rem: apiv1.Remediation{
				Zone:          testTargetZone,
				ActiveScene:   "long-scene-name-1234567890",
				ActiveState:   "ZONE_STATE_ON",
				InactiveScene: "long-scene-name-0987654321",
				InactiveState: "ZONE_STATE_OFF",
			},
			activateSceneReq: &iotv1proto.SetSceneRequest{
				Name:  testTargetZone,
				Scene: "long-scene-name-1234567890",
			},
			activateStateReq: &iotv1proto.SetStateRequest{
				Name:  testTargetZone,
				State: iotv1proto.ZoneState_ZONE_STATE_ON,
			},
			deactivateSceneReq: &iotv1proto.SetSceneRequest{
				Name:  testTargetZone,
				Scene: "long-scene-name-0987654321",
			},
			deactivateStateReq: &iotv1proto.SetStateRequest{
				Name:  testTargetZone,
				State: iotv1proto.ZoneState_ZONE_STATE_OFF,
			},
		},
	}

	ctx := context.Background()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := activateRequest(ctx, tc.rem)
			if tc.activateNil {
				require.Nil(t, req)
			} else {
				require.NotNil(t, req)
				require.Equal(t, tc.activateSceneReq, req.sceneReq)
				require.Equal(t, tc.activateStateReq, req.stateReq)
			}

			req = deactivateRequest(ctx, tc.rem)
			if tc.deactivateNil {
				require.Nil(t, req)
			} else {
				require.NotNil(t, req)
				require.Equal(t, tc.deactivateSceneReq, req.sceneReq)
				require.Equal(t, tc.deactivateStateReq, req.stateReq)
			}
		})
	}
}

// TestEpoch_schedulesDeactivation confirms that when the epoch event is in the
// future (now < start), both an activate and a deactivate event are added to
// the schedule. This exercises the Bug 1 fix (inverted nil-check on line 279).
func TestEpoch_schedulesDeactivation(t *testing.T) {
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{
		conditions: []apiv1.Condition{
			{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{Labels: map[string]string{"location": "home", "epoch": "sunset"}},
					},
					Remediations: []apiv1.Remediation{
						{
							Zone:          "zone1",
							ActiveScene:   "warm",
							InactiveState: "off",
							WhenGate:      apiv1.When{Start: "-30m", Stop: "+2h"},
						},
					},
				},
			},
		},
	}

	cfg := Config{EpochTimeWindow: time.Hour}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	// sunset is 2 hours from now; start = sunset-30m = ~90m from now (future)
	sunsetTime := time.Now().Add(2 * time.Hour)
	req := &iotv1proto.EpochRequest{
		Location: "home",
		Name:     "sunset",
		When:     sunsetTime.Unix(),
	}

	_, err = c.Epoch(ctx, req)
	require.NoError(t, err)

	require.Equal(t, 2, c.sched.len(), "expected both activate and deactivate events scheduled")
	require.Equal(t, 0, rec.setSceneCount(), "no immediate SetScene expected")
	require.Equal(t, 0, rec.setStateCount(), "no immediate SetState expected")
}

// TestEpoch_activatesWithinWindow confirms that when the current time falls
// inside the epoch window, the remediation is activated immediately via
// SetScene/SetState rather than being scheduled.
func TestEpoch_activatesWithinWindow(t *testing.T) {
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{
		conditions: []apiv1.Condition{
			{
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Matches: []apiv1.Match{
						{Labels: map[string]string{"location": "home", "epoch": "sunset"}},
					},
					Remediations: []apiv1.Remediation{
						{
							Zone:          "zone1",
							ActiveScene:   "warm",
							InactiveState: "off",
							WhenGate:      apiv1.When{Start: "-30m", Stop: "+2h"},
						},
					},
				},
			},
		},
	}

	cfg := Config{EpochTimeWindow: time.Hour}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	// sunset is right now; start = now-30m (past), stop = now+2h (future) → we are within the window
	sunsetTime := time.Now()
	req := &iotv1proto.EpochRequest{
		Location: "home",
		Name:     "sunset",
		When:     sunsetTime.Unix(),
	}

	_, err = c.Epoch(ctx, req)
	require.NoError(t, err)

	require.Equal(t, 1, rec.setSceneCount(), "expected immediate SetScene activation")
	require.Equal(t, 0, c.sched.len(), "no schedule events expected when activating immediately")
}

// TestApplyDesired_SuppressesRepeatedActivation: the most common cause
// of churn in production — alertmanager re-firing the same alert every
// group_interval. Repeated activateRemediation with the same desired
// (state, scene) for the same (condition, zone) must collapse to one
// SetState call. The cache invalidates only on real transition or
// after ApplyDesiredRefreshAge.
func TestApplyDesired_SuppressesRepeatedActivation(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office-heater", ActiveState: "on", InactiveState: "off"}

	for i := 0; i < 5; i++ {
		require.NoError(t, c.activateRemediation(ctx, "zone-office-low-temp", rem))
	}
	require.Equal(t, 1, rec.setStateCount(), "5 redundant activates should collapse to 1 SetState")
}

// TestApplyDesired_TransitionForcesApply: activate then deactivate
// (different desired) must emit two SetState calls. The cache stores
// the most-recent desired and any change re-invalidates.
func TestApplyDesired_TransitionForcesApply(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office-heater", ActiveState: "on", InactiveState: "off"}

	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.NoError(t, c.deactivateRemediation(ctx, "cond", rem))
	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.NoError(t, c.deactivateRemediation(ctx, "cond", rem))

	// Four genuine transitions → four SetStates.
	require.Equal(t, 4, rec.setStateCount(), "alternating activate/deactivate should not dedup")
}

// TestApplyDesired_RefreshAgeReapplies: a fresh cache entry suppresses;
// once it ages past ApplyDesiredRefreshAge the next call re-applies
// regardless. This is the drift-correction safety net for things that
// changed externally and that the cache doesn't know about.
func TestApplyDesired_RefreshAgeReapplies(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: 50 * time.Millisecond}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office-heater", ActiveState: "on"}

	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount())

	// Within refreshAge — suppressed.
	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount())

	time.Sleep(70 * time.Millisecond)

	// Past refreshAge — re-applies even though desired unchanged.
	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 2, rec.setStateCount(), "stale cache should force re-apply")
}

// TestApplyDesired_PerConditionZoneIsolation: two different
// (condition, zone) pairs have independent cache entries — activating
// one must not suppress the other.
func TestApplyDesired_PerConditionZoneIsolation(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	office := apiv1.Remediation{Zone: "office", ActiveState: "on"}
	main := apiv1.Remediation{Zone: "mainsuite", ActiveState: "on"}

	require.NoError(t, c.activateRemediation(ctx, "office-cond", office))
	require.NoError(t, c.activateRemediation(ctx, "main-cond", main))
	require.Equal(t, 2, rec.setStateCount(), "different (cond, zone) keys must not share cache")

	require.NoError(t, c.activateRemediation(ctx, "office-cond", office))
	require.NoError(t, c.activateRemediation(ctx, "main-cond", main))
	require.Equal(t, 2, rec.setStateCount(), "second round dedups both")
}

// TestAlert_FiringOutsideWindowDeactivates: the windowing-logic fix.
// A firing alert with TimeIntervals that do NOT contain `now` must
// deactivate (or stay deactivated), not activate. Before the fix, the
// override path forced active=true regardless of window membership,
// producing ON/OFF flip-flop with every webhook.
func TestAlert_FiringOutsideWindowDeactivates(t *testing.T) {
	// TimeInterval that covers a narrow past window — definitely not now.
	rem := apiv1.Remediation{
		Zone:          "office-heater",
		ActiveState:   "on",
		InactiveState: "off",
		// Years=1970 ensures the window can never contain "now".
		TimeIntervals: []apiv1.TimeIntervalSpec{
			{Years: []string{"1970"}},
		},
	}
	cond := apiv1.Condition{Spec: apiv1.ConditionSpec{
		Enabled:      true,
		Matches:      []apiv1.Match{{Labels: map[string]string{"alertname": "zoneTempLow", "zone": "office"}}},
		Remediations: []apiv1.Remediation{rem},
	}}
	cond.Name = "zone-office-low-temp"

	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{conditions: []apiv1.Condition{cond}}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	// Alert is firing, but we're outside the configured TimeInterval.
	// Pre-fix: this activated. Post-fix: should deactivate (active=false).
	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name:   "zoneTempLow",
		Zone:   "office",
		Status: "firing",
	})
	require.NoError(t, err)

	require.Equal(t, 1, rec.setStateCount(), "firing-outside-window should hit SetState once (deactivate)")
	name, state := rec.firstSetState()
	require.Equal(t, "office-heater", name)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, state, "firing-outside-window should deactivate")
}

// TestApplyDesired_ToggleResolvesByZoneStatus: a Condition with
// active_state=toggle resolves to ON or OFF at the conditioner before
// reaching the cache. Status=OFF → ON; Status=ON → OFF.
func TestApplyDesired_ToggleResolvesByZoneStatus(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{
		zones: map[string]apiv1.Zone{
			"office": {
				Status: apiv1.ZoneStatus{State: iotv1proto.ZoneState_ZONE_STATE_OFF.String()},
			},
		},
	}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "toggle"}

	// Status=OFF → toggle resolves to ON.
	require.NoError(t, c.activateRemediation(ctx, "office-toggle", rem))
	require.Equal(t, 1, rec.setStateCount())
	_, state := rec.firstSetState()
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, state, "toggle from OFF should resolve to ON")

	// Now flip the fake zone status to ON; next toggle should resolve to OFF.
	kube.zones["office"] = apiv1.Zone{Status: apiv1.ZoneStatus{State: iotv1proto.ZoneState_ZONE_STATE_ON.String()}}
	require.NoError(t, c.activateRemediation(ctx, "office-toggle", rem))
	require.Equal(t, 2, rec.setStateCount())
	last := rec.setStateCalls[1]
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, last.State, "toggle from ON should resolve to OFF")
}

// TestApplyDesired_ToggleAlternation: with the fake zone status flipped
// between presses to simulate the apiStatusUpdate roundtrip, three
// presses produce three alternating SetState calls (no dedup).
func TestApplyDesired_ToggleAlternation(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{
		zones: map[string]apiv1.Zone{
			"office": {Status: apiv1.ZoneStatus{State: iotv1proto.ZoneState_ZONE_STATE_OFF.String()}},
		},
	}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "toggle"}

	want := []iotv1proto.ZoneState{
		iotv1proto.ZoneState_ZONE_STATE_ON,  // status was OFF → ON
		iotv1proto.ZoneState_ZONE_STATE_OFF, // status flipped to ON → OFF
		iotv1proto.ZoneState_ZONE_STATE_ON,  // status flipped to OFF → ON
	}
	flipMap := map[iotv1proto.ZoneState]string{
		iotv1proto.ZoneState_ZONE_STATE_ON:  iotv1proto.ZoneState_ZONE_STATE_ON.String(),
		iotv1proto.ZoneState_ZONE_STATE_OFF: iotv1proto.ZoneState_ZONE_STATE_OFF.String(),
	}
	for i, exp := range want {
		require.NoError(t, c.activateRemediation(ctx, "office-toggle", rem))
		require.Equal(t, i+1, rec.setStateCount(), "press %d", i+1)
		require.Equal(t, exp, rec.setStateCalls[i].State, "press %d expected resolved state", i+1)
		// Simulate apiStatusUpdate reflecting the new state.
		kube.zones["office"] = apiv1.Zone{Status: apiv1.ZoneStatus{State: flipMap[exp]}}
	}
}

// TestApplyDesired_ToggleDoublePressDedup: two rapid toggle presses
// while the zone status hasn't caught up yet should produce only one
// effective SetState — applyDesired's cache absorbs the duplicate.
// This is the deliberate button-bounce debounce.
func TestApplyDesired_ToggleDoublePressDedup(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{
		zones: map[string]apiv1.Zone{
			"office": {Status: apiv1.ZoneStatus{State: iotv1proto.ZoneState_ZONE_STATE_OFF.String()}},
		},
	}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "toggle"}

	// Two presses, status NOT updated between them — both resolve to ON.
	require.NoError(t, c.activateRemediation(ctx, "office-toggle", rem))
	require.NoError(t, c.activateRemediation(ctx, "office-toggle", rem))

	require.Equal(t, 1, rec.setStateCount(), "rapid double-press without status update should dedup to 1 SetState")
}

// TestApplyDesired_ToggleReadFailureDefaultsOn: if the kube Get for the
// zone fails (e.g. zone CR doesn't exist), toggle defaults to "on" so
// the user isn't left in the dark with no clear recovery path.
func TestApplyDesired_ToggleReadFailureDefaultsOn(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{
		zones: map[string]apiv1.Zone{}, // empty → Get returns errExpectError
	}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "nonexistent", ActiveState: "toggle"}

	require.NoError(t, c.activateRemediation(ctx, "broken-toggle", rem))
	require.Equal(t, 1, rec.setStateCount())
	_, state := rec.firstSetState()
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, state, "toggle on read failure defaults to ON")
}

// TestActivateRemediation_TimeGated_OutsideWindow: a Remediation with
// TimeIntervals that don't contain "now" must be suppressed at
// activation time, regardless of where the activation came from
// (Alert RPC, Binding via ActivateCondition, etc.). This is Stage 1
// of the unified-evaluator plan: TimeIntervals become uniformly
// honored across all activation sources.
func TestActivateRemediation_TimeGated_OutsideWindow(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	// TimeInterval pinned to Years=1970 → never contains "now".
	rem := apiv1.Remediation{
		Zone:        "office",
		ActiveState: "on",
		TimeIntervals: []apiv1.TimeIntervalSpec{
			{Years: []string{"1970"}},
		},
	}

	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 0, rec.setStateCount(),
		"activation outside the window must not produce a SetState")
}

// TestActivateRemediation_TimeGated_InsideWindow: a Remediation whose
// TimeIntervals contain "now" activates as it always has.
func TestActivateRemediation_TimeGated_InsideWindow(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	// TimeInterval covering the current year. A bit of a hack to make
	// the test time-portable but reliable.
	thisYear := strconv.Itoa(time.Now().Year())
	rem := apiv1.Remediation{
		Zone:        "office",
		ActiveState: "on",
		TimeIntervals: []apiv1.TimeIntervalSpec{
			{Years: []string{thisYear}},
		},
	}

	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount(),
		"activation inside the window must fire SetState")
	_, state := rec.firstSetState()
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, state)
}

// TestActivateRemediation_TimeGated_NoIntervals: when TimeIntervals is
// empty, activation fires regardless. Preserves today's behaviour for
// every existing Condition that doesn't author windows.
func TestActivateRemediation_TimeGated_NoIntervals(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "on"}

	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount())
}

// TestDeactivateRemediation_NotTimeGated: deactivation must fire even
// when the Remediation's TimeIntervals don't contain "now". The
// semantic is "alert resolved; revert" — gating that would leave the
// zone stuck in the previous activation's state.
func TestDeactivateRemediation_NotTimeGated(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{
		Zone:          "office",
		ActiveState:   "on",
		InactiveState: "off",
		TimeIntervals: []apiv1.TimeIntervalSpec{
			{Years: []string{"1970"}}, // out of window
		},
	}

	require.NoError(t, c.deactivateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount(),
		"deactivation must fire regardless of TimeIntervals")
	_, state := rec.firstSetState()
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, state)
}

// TestActivateRemediation_BrightnessDelta_Positive: a Remediation
// with a positive ActiveBrightnessDelta routes to AdjustBrightness
// instead of applyDesired. The delta is propagated unchanged.
func TestActivateRemediation_BrightnessDelta_Positive(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveBrightnessDelta: 1}
	require.NoError(t, c.activateRemediation(ctx, "office-brighter", rem))

	require.Equal(t, 1, rec.adjustBrightCount())
	require.Equal(t, int32(1), rec.adjustBrightCalls[0].Delta)
	require.Equal(t, "office", rec.adjustBrightCalls[0].Name)
	require.Equal(t, 0, rec.setStateCount(),
		"delta path must NOT also call SetState — AdjustBrightness owns the state-side effect")
}

// TestActivateRemediation_BrightnessDelta_Negative: negative delta
// preserved as-is.
func TestActivateRemediation_BrightnessDelta_Negative(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveBrightnessDelta: -1}
	require.NoError(t, c.activateRemediation(ctx, "office-dimmer", rem))

	require.Equal(t, 1, rec.adjustBrightCount())
	require.Equal(t, int32(-1), rec.adjustBrightCalls[0].Delta)
}

// TestActivateRemediation_BrightnessDelta_BypassesCache: deltas are
// non-idempotent — N consecutive activations must produce N calls.
// This is the property the user explicitly asked for (each press
// of "hold" walks brightness one more step).
func TestActivateRemediation_BrightnessDelta_BypassesCache(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveBrightnessDelta: -1}
	for i := 0; i < 5; i++ {
		require.NoError(t, c.activateRemediation(ctx, "office-dimmer", rem))
	}
	require.Equal(t, 5, rec.adjustBrightCount(),
		"5 consecutive deltas must produce 5 RPCs (no dedup)")
}

// TestActivateRemediation_BrightnessDelta_TimeGated: time-gating
// (Stage 1) applies BEFORE the delta path. Out-of-window deltas
// don't fire.
func TestActivateRemediation_BrightnessDelta_TimeGated(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{
		Zone:                  "office",
		ActiveBrightnessDelta: 1,
		TimeIntervals: []apiv1.TimeIntervalSpec{
			{Years: []string{"1970"}}, // never contains "now"
		},
	}
	require.NoError(t, c.activateRemediation(ctx, "office-brighter", rem))
	require.Equal(t, 0, rec.adjustBrightCount(),
		"delta out of window must be suppressed by time-gating")
}

// TestActivateRemediation_BrightnessDelta_OverridesAbsolute: when
// both ActiveBrightnessDelta and ActiveState are set, the delta
// path wins and ActiveState is not applied via SetState. The
// underlying RPC handles "ensure on" as a side effect.
func TestActivateRemediation_BrightnessDelta_OverridesAbsolute(t *testing.T) {
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{
		Zone:                  "office",
		ActiveState:           "off", // contradictory; delta wins
		ActiveBrightnessDelta: 1,
	}
	require.NoError(t, c.activateRemediation(ctx, "office-brighter", rem))

	require.Equal(t, 1, rec.adjustBrightCount())
	require.Equal(t, 0, rec.setStateCount(),
		"delta path takes the activation; SetState is not called")
}
