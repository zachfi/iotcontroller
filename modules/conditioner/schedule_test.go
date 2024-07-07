package conditioner

import (
	"testing"

	"github.com/stretchr/testify/require"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

func Test_matched(t *testing.T) {
	cases := map[string]struct {
		a      request
		b      request
		expect bool
	}{
		"default": {
			expect: true,
		},
		"simple scene match": {
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
		"simple scene not match": {
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
		"simple scene not match empty": {
			a: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "office",
				},
			},
			b:      request{},
			expect: false,
		},
		"simple scene not match empty alt": {
			a: request{},
			b: request{
				sceneReq: &iotv1proto.SetSceneRequest{
					Scene: "dawn",
					Name:  "office",
				},
			},
			expect: false,
		},
		// mismatched zone
		"simple state not match": {
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
		"simple state not match empty": {
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
		"simple state not match empty alt": {
			a: request{},
			b: request{
				stateReq: &iotv1proto.SetStateRequest{
					State: iotv1proto.ZoneState_ZONE_STATE_COLOR,
					Name:  "porch",
				},
			},
			expect: false,
		},
		"large match": {
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
