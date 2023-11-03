package iot

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zachfi/iotcontroller/pkg/iot/handlers/mock"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

func TestHasDevices(t *testing.T) {
	cases := []struct {
		zone    string
		devices []*iotv1proto.Device
	}{
		{
			zone: "one",
			devices: []*iotv1proto.Device{
				{
					Name: "asd",
					Type: iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT,
				},
				{
					Name: "DSA",
					Type: iotv1proto.DeviceType_DEVICE_TYPE_ISPINDEL,
				},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	for _, tc := range cases {
		z, err := NewZone(tc.zone, logger)
		require.NoError(t, err)

		for _, d := range tc.devices {
			err := z.SetDevice(d, mock.MockHandler{})
			require.NoError(t, err)
		}

		for _, d := range tc.devices {
			require.True(t, z.HasDevice(d.Name))
		}
	}
}
