package iot

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

func testLoggerContinuous() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestZone_SetBrightnessDualWrites(t *testing.T) {
	// SetBrightness(enum) must populate both fields. The continuous
	// representation is what the fade Computer and other continuous
	// consumers read; the enum field is what Status and
	// IncrementBrightness/DecrementBrightness read. Skewed dual-state
	// would let the two paths drift apart.
	z, err := NewZone("test", testLoggerContinuous())
	require.NoError(t, err)

	z.SetBrightness(context.Background(), iotv1proto.Brightness_BRIGHTNESS_DIM)

	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_DIM, z.Brightness())
	require.InDelta(t, 110.0/254.0, z.brightnessValue, 1e-9, "continuous tracks canonical-of-enum")
}

func TestZone_SetBrightnessValueDualWrites(t *testing.T) {
	// SetBrightnessValue(continuous) must populate both fields. The
	// enum field gets the nearest enum for Status writeback.
	z, err := NewZone("test", testLoggerContinuous())
	require.NoError(t, err)

	// Exactly the canonical for DIM.
	z.SetBrightnessValue(context.Background(), 110.0/254.0)

	require.Equal(t, 110.0/254.0, z.brightnessValue)
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_DIM, z.Brightness(), "enum tracks nearest of continuous")
}

func TestZone_SetBrightnessValueArbitraryContinuous(t *testing.T) {
	// Arbitrary continuous (mid-fade) writes the exact value and the
	// nearest enum for Status. The fade-tick scenario: progress = 0.65
	// during a FULL→VERYLOW fade emits some interpolated value that
	// doesn't sit exactly on a canonical anchor.
	z, err := NewZone("test", testLoggerContinuous())
	require.NoError(t, err)

	z.SetBrightnessValue(context.Background(), 0.65)
	require.Equal(t, 0.65, z.brightnessValue)
	// The nearest enum at 0.65 is... DIMPLUS=125/254≈0.492 vs FULL=1.0;
	// 0.65 is closer to DIMPLUS. Either way the enum must come from
	// the canonical comparison, not from a stale prior write.
	require.NotEqual(t, iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED, z.Brightness())
}

func TestZone_SetColorTemperatureDualWrites(t *testing.T) {
	z, err := NewZone("test", testLoggerContinuous())
	require.NoError(t, err)

	z.SetColorTemperature(context.Background(), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING)
	require.Equal(t, iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING, z.ColorTemperature())
	require.Equal(t, int32(2000), z.colorTempKelvin)
}

func TestZone_SetColorTemperatureKelvinDualWrites(t *testing.T) {
	z, err := NewZone("test", testLoggerContinuous())
	require.NoError(t, err)

	z.SetColorTemperatureKelvin(context.Background(), 2000)
	require.Equal(t, int32(2000), z.colorTempKelvin)
	require.Equal(t, iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING, z.ColorTemperature())
}

func TestZone_SetColorTemperatureKelvinArbitrary(t *testing.T) {
	// 2700K is a common adaptive-lighting "warm white" value that
	// doesn't match any of our 5 canonical enum points exactly. It
	// should pick the nearest (EVENING=2000K vs LATEAFTERNOON=2500K
	// vs DAY=3333K — 2700K is closest to LATEAFTERNOON at 2500K).
	z, err := NewZone("test", testLoggerContinuous())
	require.NoError(t, err)

	z.SetColorTemperatureKelvin(context.Background(), 2700)
	require.Equal(t, int32(2700), z.colorTempKelvin)
	require.Equal(t, iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON, z.ColorTemperature())
}
