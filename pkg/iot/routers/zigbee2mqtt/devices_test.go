package zigbee2mqtt

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// TestDeviceType exercises the deviceType classifier with payloads
// representative of real zigbee devices, especially edge cases where the
// classification order matters.
func TestDeviceType(t *testing.T) {
	cases := []struct {
		name string
		// payload is a single Device JSON object; wrapped in [] for Devices decode.
		payload string
		want    iotv1proto.DeviceType
	}{
		{
			// SONOFF SNZB-02LD: IP65 temperature probe. Exposes only
			// temperature + battery + linkquality (no humidity, no occupancy,
			// no leak). Must classify as TEMPERATURE rather than UNSPECIFIED.
			name: "sonoff snzb-02ld pure temperature probe",
			payload: `{"definition":{"vendor":"SONOFF","model":"SNZB-02LD","exposes":[
				{"type":"numeric","name":"battery","property":"battery"},
				{"type":"numeric","name":"temperature","property":"temperature"},
				{"type":"numeric","name":"linkquality","property":"linkquality"}
			]}}`,
			want: iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE,
		},
		{
			// Temp + humidity combo sensor — humidity branch wins (and itself
			// returns TEMPERATURE today; either way must not be UNSPECIFIED).
			name: "temp+humidity combo sensor",
			payload: `{"definition":{"exposes":[
				{"type":"numeric","name":"temperature","property":"temperature"},
				{"type":"numeric","name":"humidity","property":"humidity"}
			]}}`,
			want: iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE,
		},
		{
			// Motion sensor that also reports its internal temperature must
			// stay classified as MOTION, not get reclassified as TEMPERATURE.
			name: "motion sensor with temperature must stay motion",
			payload: `{"definition":{"exposes":[
				{"type":"binary","name":"occupancy","property":"occupancy"},
				{"type":"numeric","name":"temperature","property":"temperature"}
			]}}`,
			want: iotv1proto.DeviceType_DEVICE_TYPE_MOTION,
		},
		{
			// Water leak sensor with temperature must stay LEAK.
			name: "leak sensor with temperature must stay leak",
			payload: `{"definition":{"exposes":[
				{"type":"binary","name":"water_leak","property":"water_leak"},
				{"type":"numeric","name":"temperature","property":"temperature"}
			]}}`,
			want: iotv1proto.DeviceType_DEVICE_TYPE_LEAK,
		},
		{
			// Air quality sensor (VOC) with temperature must stay AIR_QUALITY.
			name: "air quality sensor with temperature must stay air quality",
			payload: `{"definition":{"exposes":[
				{"type":"numeric","name":"voc","property":"voc"},
				{"type":"numeric","name":"temperature","property":"temperature"}
			]}}`,
			want: iotv1proto.DeviceType_DEVICE_TYPE_AIR_QUALITY,
		},
		{
			// No identifying exposures at all → UNSPECIFIED.
			name:    "empty definition",
			payload: `{"definition":{"exposes":[]}}`,
			want:    iotv1proto.DeviceType_DEVICE_TYPE_UNSPECIFIED,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := Devices{}
			require.NoError(t, json.Unmarshal([]byte("["+tc.payload+"]"), &obj))
			require.Len(t, obj, 1)
			got := deviceType(obj[0])
			require.Equal(t, tc.want, got, "got %s want %s", got, tc.want)
		})
	}
}

// TestExposeFractionalValueMax ensures the bridge/devices decoder accepts
// fractional value_max / value_min values (e.g. a timer numeric expose with
// half-second resolution: "value_max":3599.5). Regression test for an entire
// devices payload aborting mid-decode and no devices being identified.
func TestExposeFractionalValueMax(t *testing.T) {
	payload := []byte(`[{"definition":{"exposes":[{"type":"numeric","name":"timer","property":"timer","value_max":3599.5,"value_min":0.5}]}}]`)
	obj := Devices{}
	require.NoError(t, json.Unmarshal(payload, &obj))
	require.Len(t, obj, 1)
	require.Len(t, obj[0].Definition.Exposes, 1)
	require.InDelta(t, 3599.5, obj[0].Definition.Exposes[0].ValueMax, 0.0001)
	require.InDelta(t, 0.5, obj[0].Definition.Exposes[0].ValueMin, 0.0001)
}

func TestDevices(t *testing.T) {
	cases := []struct {
		name string
		file string
	}{
		{
			name: "parse from test capture",
			file: "../../../../testdata/devices.json",
		},
		{
			name: "parse from manual capture",
			file: "../../../iot/devices.json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jsonFile, err := os.Open(tc.file)
			require.NoError(t, err)
			defer jsonFile.Close()

			byteValue, err := io.ReadAll(jsonFile)
			require.NoError(t, err)
			obj := Devices{}
			err = json.Unmarshal(byteValue, &obj)
			require.NoError(t, err)
			require.Greater(t, len(obj), 0)

			for _, d := range obj {
				x := deviceType(d)
				require.Greater(t, x, iotv1proto.DeviceType_DEVICE_TYPE_UNSPECIFIED,
					fmt.Sprintf("device: %+v", d),
				)
			}
		})
	}
}
