package conditioner

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/mocks"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	"google.golang.org/grpc"
)

var errExpectError = errors.New("expect error")

// recordingZoneKeeper records SetState/SetScene calls for tests.
type recordingZoneKeeper struct {
	mocks.ZoneKeeperClientMock
	mu            sync.Mutex
	setStateCalls []*iotv1proto.SetStateRequest
	setSceneCalls []*iotv1proto.SetSceneRequest
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
