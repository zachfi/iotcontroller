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
