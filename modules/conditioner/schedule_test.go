package conditioner

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zachfi/iotcontroller/pkg/mocks"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

var testlogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

func Test_schedule(t *testing.T) {
	cases := []struct {
		name   string
		events int
		err    string
		req    *request
	}{
		{
			name:   "test1",
			err:    "empty request",
			events: 0,
		},
		{
			name:   "test1",
			events: 1,
			req: &request{
				stateReq: &iotv1proto.SetStateRequest{
					Name:  "zone1",
					State: iotv1proto.ZoneState_ZONE_STATE_OFF,
				},
			},
		},
		{
			name:   "test1",
			events: 1,
			req: &request{
				stateReq: &iotv1proto.SetStateRequest{
					Name:  "zone1",
					State: iotv1proto.ZoneState_ZONE_STATE_OFF,
				},
			},
		},
		{
			name:   "test2",
			events: 2,
			req: &request{
				stateReq: &iotv1proto.SetStateRequest{
					Name:  "zone2",
					State: iotv1proto.ZoneState_ZONE_STATE_ON,
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := newSchedule(testlogger)
	require.NotNil(t, s)

	go s.run(ctx, &mocks.ZoneKeeperClientMock{})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := time.Now().Add(1 * time.Second)

			err := s.add(ctx, tc.name, next, tc.req)
			if tc.err != "" {
				require.Error(t, err)
				require.EqualError(t, err, tc.err, "expected error for test case %s", tc.name)
			} else {
				require.NoError(t, err, "unexpected error for test case %s", tc.name)
			}

			require.Equal(t, tc.events, s.len(), "unexpected number of events for test case %s", tc.name)
		})
	}

	time.Sleep(3 * time.Second)
	require.Equal(t, 0, s.len(), "expected all vents to be processed")
}

func Test_matched(t *testing.T) {
	cases := map[string]struct {
		a      *request
		b      *request
		expect bool
	}{
		"default": {
			expect: true,
		},
		"only one non-nil": {
			expect: false,
			a:      &request{},
		},
		"simple scene match": {
			a: &request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
			},
			b: &request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
			},
			expect: true,
		},
		"simple scene not match": {
			a: &request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
			},
			b: &request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "office",
				},
			},
			expect: false,
		},
		"simple scene not match empty": {
			a: &request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "office",
				},
			},
			b:      &request{},
			expect: false,
		},
		"simple scene not match empty alt": {
			a: &request{},
			b: &request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "office",
				},
			},
			expect: false,
		},
		// mismatched zone
		"simple state not match": {
			a: &request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			b: &request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "office",
				},
			},
			expect: false,
		},
		"simple state not match empty": {
			a: &request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			b:      &request{},
			expect: false,
		},
		// empty a
		"simple state not match empty alt": {
			a: &request{},
			b: &request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			expect: false,
		},
		"large match": {
			a: &request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			b: &request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			expect: true,
		},
	}

	for _, tc := range cases {
		require.Equal(t, tc.expect, matched(tc.a, tc.b))
	}
}
