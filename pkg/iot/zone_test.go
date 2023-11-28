package iot

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zachfi/iotcontroller/pkg/iot/handlers/mock"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

var (
	testlogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	ctx        = context.Background()
)

func TestNewZone(t *testing.T) {
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

	for _, tc := range cases {
		z, err := NewZone(tc.zone, testlogger)
		require.NoError(t, err)

		z.SetBrightness(ctx, iotv1proto.Brightness_BRIGHTNESS_FULL)
		z.SetColorTemperature(ctx, iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY)
		z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_COLOR)
		z.IncrementBrightness(ctx)
		z.DecrementBrightness(ctx)
		err = z.Flush(ctx, nil)
		require.NoError(t, err)

		for _, d := range tc.devices {
			err := z.SetDevice(ctx, d, mock.MockHandler{})
			require.NoError(t, err)
		}

		require.Equal(t, len(tc.devices), len(z.devices))
	}
}

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

	for _, tc := range cases {
		z, err := NewZone(tc.zone, testlogger)
		require.NoError(t, err)

		for _, d := range tc.devices {
			err := z.SetDevice(ctx, d, mock.MockHandler{})
			require.NoError(t, err)
		}

		for _, d := range tc.devices {
			require.True(t, z.HasDevice(d.Name))
		}
	}
}
