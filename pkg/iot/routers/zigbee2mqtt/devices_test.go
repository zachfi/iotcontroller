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
