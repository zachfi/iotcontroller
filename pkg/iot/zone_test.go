package iot

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

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

// countingHandler is a Handler that records per-method call counts so
// the idempotent-flush tests can assert "called once, suppressed
// thereafter."
type countingHandler struct {
	on            atomic.Int64
	off           atomic.Int64
	setBrightness atomic.Int64
	setColorTemp  atomic.Int64
	setColor      atomic.Int64
}

func (h *countingHandler) On(context.Context, *iotv1proto.Device) error {
	h.on.Add(1)
	return nil
}
func (h *countingHandler) Off(context.Context, *iotv1proto.Device) error {
	h.off.Add(1)
	return nil
}
func (h *countingHandler) Alert(context.Context, *iotv1proto.Device) error { return nil }
func (h *countingHandler) SetBrightness(context.Context, *iotv1proto.Device, uint8) error {
	h.setBrightness.Add(1)
	return nil
}
func (h *countingHandler) RandomColor(context.Context, *iotv1proto.Device, []string) error {
	return nil
}
func (h *countingHandler) SetColor(context.Context, *iotv1proto.Device, string) error {
	h.setColor.Add(1)
	return nil
}
func (h *countingHandler) SetColorTemp(context.Context, *iotv1proto.Device, int32) error {
	h.setColorTemp.Add(1)
	return nil
}

// TestFlush_SuppressesRedundantOn: after the first Flush sends On to
// the device, a second Flush (same desired state) within refreshAge
// must not call h.On again. The cache hit is what saves Zigbee traffic.
func TestFlush_SuppressesRedundantOn(t *testing.T) {
	z, err := NewZone("test", testlogger)
	require.NoError(t, err)

	h := &countingHandler{}
	dev := &iotv1proto.Device{Name: "plug-1", Type: iotv1proto.DeviceType_DEVICE_TYPE_RELAY}
	require.NoError(t, z.SetDevice(ctx, dev, h))

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)

	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.on.Load(), "first flush should call On once")

	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.on.Load(), "second flush within refreshAge should suppress On")
}

// TestFlush_RedundantOffAlsoSuppressed: same shape as the On test but
// for the off path, which is the pond-pump-style hot path
// (SelfAnnounce → Flush(limiter=just-this-relay) → handleOff).
func TestFlush_RedundantOffAlsoSuppressed(t *testing.T) {
	z, err := NewZone("test", testlogger)
	require.NoError(t, err)

	h := &countingHandler{}
	dev := &iotv1proto.Device{Name: "plug-1", Type: iotv1proto.DeviceType_DEVICE_TYPE_RELAY}
	require.NoError(t, z.SetDevice(ctx, dev, h))

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_OFF)

	for i := 0; i < 5; i++ {
		require.NoError(t, z.Flush(ctx, nil))
	}
	require.Equal(t, int64(1), h.off.Load(), "5 redundant Off flushes should collapse to 1 handler call")
}

// TestFlush_StateChangeBreaksCache: when the desired state changes
// (OFF → ON), the cache must NOT suppress the new direction even
// though the device was recently asserted with the old direction.
func TestFlush_StateChangeBreaksCache(t *testing.T) {
	z, err := NewZone("test", testlogger)
	require.NoError(t, err)

	h := &countingHandler{}
	dev := &iotv1proto.Device{Name: "plug-1", Type: iotv1proto.DeviceType_DEVICE_TYPE_RELAY}
	require.NoError(t, z.SetDevice(ctx, dev, h))

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_OFF)
	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.off.Load())

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)
	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.on.Load(), "state change should reach the device")
	require.Equal(t, int64(1), h.off.Load(), "Off should not be re-issued")
}

// TestFlush_RefreshAgeReassertsDrift: after refreshAge expires, the
// next Flush must re-send the desired value even when the cache
// claims it's already there. This is the drift-correction safety net
// for devices that were power-cycled or briefly fell off the network.
func TestFlush_RefreshAgeReassertsDrift(t *testing.T) {
	z, err := NewZone("test", testlogger)
	require.NoError(t, err)
	z.SetRefreshAge(50 * time.Millisecond)

	h := &countingHandler{}
	dev := &iotv1proto.Device{Name: "plug-1", Type: iotv1proto.DeviceType_DEVICE_TYPE_RELAY}
	require.NoError(t, z.SetDevice(ctx, dev, h))

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_OFF)
	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.off.Load())

	// within refreshAge — suppressed
	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.off.Load())

	time.Sleep(70 * time.Millisecond)

	// past refreshAge — handler re-runs
	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(2), h.off.Load(), "stale cache entry should force re-send")
}

// TestFlush_SuppressesBrightnessAndColorTemp: lights get
// handleBrightness + handleColorTemperature on every non-OFF flush, so
// the cache must skip those too or the light bus stays hot.
func TestFlush_SuppressesBrightnessAndColorTemp(t *testing.T) {
	z, err := NewZone("test", testlogger)
	require.NoError(t, err)

	h := &countingHandler{}
	dev := &iotv1proto.Device{Name: "bulb-1", Type: iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT}
	require.NoError(t, z.SetDevice(ctx, dev, h))

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)
	z.SetBrightness(ctx, iotv1proto.Brightness_BRIGHTNESS_FULL)
	z.SetColorTemperature(ctx, iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY)

	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.on.Load())
	require.Equal(t, int64(1), h.setBrightness.Load())
	require.Equal(t, int64(1), h.setColorTemp.Load())

	for i := 0; i < 4; i++ {
		require.NoError(t, z.Flush(ctx, nil))
	}
	require.Equal(t, int64(1), h.on.Load(), "5x On flushes deduped to 1")
	require.Equal(t, int64(1), h.setBrightness.Load(), "5x brightness flushes deduped to 1")
	require.Equal(t, int64(1), h.setColorTemp.Load(), "5x colorTemp flushes deduped to 1")
}

// TestFlush_OffTimerCanonicalizedToOn: ZONE_STATE_OFFTIMER routes
// through handleOn (lights stay on while the timer is pending). The
// cache must treat OFFTIMER and ON identically so flipping between
// them doesn't re-issue On.
func TestFlush_OffTimerCanonicalizedToOn(t *testing.T) {
	z, err := NewZone("test", testlogger)
	require.NoError(t, err)

	h := &countingHandler{}
	dev := &iotv1proto.Device{Name: "plug-1", Type: iotv1proto.DeviceType_DEVICE_TYPE_RELAY}
	require.NoError(t, z.SetDevice(ctx, dev, h))

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)
	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.on.Load())

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_OFFTIMER)
	require.NoError(t, z.Flush(ctx, nil))
	require.Equal(t, int64(1), h.on.Load(), "OFFTIMER ↔ ON should be cache-equivalent")
}
