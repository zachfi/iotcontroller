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
