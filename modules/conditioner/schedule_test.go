package conditioner

import (
	"testing"

	"github.com/stretchr/testify/require"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

func Test_matched(t *testing.T) {
	cases := []struct {
		a      request
		b      request
		expect bool
	}{
		{
			expect: true,
		},
		// Simple scene match
		{
			a: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
			},
			b: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
			},
			expect: true,
		},
		{
			a: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
			},
			b: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "office",
				},
			},
			expect: false,
		},
		{
			a: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "office",
				},
			},
			b:      request{},
			expect: true,
		},
		{
			a: request{},
			b: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "office",
				},
			},
			expect: false,
		},
		{
			a: request{},
			b: request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			expect: false,
		},
		// mismatched zone
		{
			a: request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			b: request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "office",
				},
			},
			expect: false,
		},
		// empty b
		{
			a: request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			b:      request{},
			expect: false,
		},
		// empty a
		{
			a: request{},
			b: request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			expect: false,
		},
		// Large match
		{
			a: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "porch",
				},
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			b: request{
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
