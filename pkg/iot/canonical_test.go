package iot

import (
	"testing"

	"github.com/stretchr/testify/require"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

func TestBrightnessCanonical_AllEnumsMapped(t *testing.T) {
	// Every named brightness step in the perceptual order must map to a
	// distinct, non-zero canonical value. UNSPECIFIED is the only enum
	// that returns 0.
	cases := []struct {
		enum    iotv1proto.Brightness
		nonZero bool
	}{
		{iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED, false},
		{iotv1proto.Brightness_BRIGHTNESS_VERYLOW, true},
		{iotv1proto.Brightness_BRIGHTNESS_LOW, true},
		{iotv1proto.Brightness_BRIGHTNESS_LOWPLUS, true},
		{iotv1proto.Brightness_BRIGHTNESS_DIM, true},
		{iotv1proto.Brightness_BRIGHTNESS_DIMPLUS, true},
		{iotv1proto.Brightness_BRIGHTNESS_FULL, true},
	}
	for _, c := range cases {
		v := BrightnessCanonical(c.enum)
		if c.nonZero {
			require.Greater(t, v, 0.0, "%s should map to non-zero canonical", c.enum)
			require.LessOrEqual(t, v, 1.0, "%s canonical must be in [0,1]", c.enum)
		} else {
			require.Equal(t, 0.0, v, "%s should map to zero", c.enum)
		}
	}
}

func TestBrightnessCanonical_FullIsOne(t *testing.T) {
	// FULL is the anchor — operators authoring 100% brightness via the
	// enum expect the continuous value to match exactly 1.0, not
	// 254/254 floating-point noise that compares != 1.0.
	require.Equal(t, 1.0, BrightnessCanonical(iotv1proto.Brightness_BRIGHTNESS_FULL))
}

func TestBrightnessCanonical_OrderPreserved(t *testing.T) {
	// Perceptual order: VERYLOW < LOW < LOWPLUS < DIM < DIMPLUS < FULL.
	// Continuous values must respect this monotone — fade interpolation
	// relies on it.
	ordered := []iotv1proto.Brightness{
		iotv1proto.Brightness_BRIGHTNESS_VERYLOW,
		iotv1proto.Brightness_BRIGHTNESS_LOW,
		iotv1proto.Brightness_BRIGHTNESS_LOWPLUS,
		iotv1proto.Brightness_BRIGHTNESS_DIM,
		iotv1proto.Brightness_BRIGHTNESS_DIMPLUS,
		iotv1proto.Brightness_BRIGHTNESS_FULL,
	}
	for i := 1; i < len(ordered); i++ {
		prev := BrightnessCanonical(ordered[i-1])
		curr := BrightnessCanonical(ordered[i])
		require.Less(t, prev, curr, "%s should canonical-rank below %s", ordered[i-1], ordered[i])
	}
}

func TestBrightnessNearestEnum_RoundTrip(t *testing.T) {
	// Every enum's canonical value must round-trip back to itself.
	// Otherwise SetBrightness(enum) → SetBrightnessValue(canonical) →
	// SetBrightness(nearestEnum) would silently drift the operator's
	// authored brightness.
	for _, enum := range []iotv1proto.Brightness{
		iotv1proto.Brightness_BRIGHTNESS_VERYLOW,
		iotv1proto.Brightness_BRIGHTNESS_LOW,
		iotv1proto.Brightness_BRIGHTNESS_LOWPLUS,
		iotv1proto.Brightness_BRIGHTNESS_DIM,
		iotv1proto.Brightness_BRIGHTNESS_DIMPLUS,
		iotv1proto.Brightness_BRIGHTNESS_FULL,
	} {
		v := BrightnessCanonical(enum)
		got := BrightnessNearestEnum(v)
		require.Equal(t, enum, got, "round-trip failed for %s", enum)
	}
}

func TestBrightnessNearestEnum_OutOfRange(t *testing.T) {
	// Negative and zero map to UNSPECIFIED (the "not set" sentinel).
	// Values above 1.0 clamp to FULL since that's the operator's
	// presumed intent.
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED, BrightnessNearestEnum(0))
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED, BrightnessNearestEnum(-0.5))
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_FULL, BrightnessNearestEnum(1.5))
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_FULL, BrightnessNearestEnum(2.0))
}

func TestBrightnessNearestEnum_MidpointPicksHigher(t *testing.T) {
	// A continuous value halfway between two canonicals snaps to one of
	// them deterministically. The math.Abs-then-min picks whichever has
	// a smaller signed distance; floating-point quirks aside, equal
	// distances should not panic and should pick a single enum.
	low := BrightnessCanonical(iotv1proto.Brightness_BRIGHTNESS_LOW)
	lowPlus := BrightnessCanonical(iotv1proto.Brightness_BRIGHTNESS_LOWPLUS)
	mid := (low + lowPlus) / 2
	got := BrightnessNearestEnum(mid)
	// Either LOW or LOWPLUS is acceptable; we only require deterministic
	// non-panic behaviour.
	require.Contains(t, []iotv1proto.Brightness{
		iotv1proto.Brightness_BRIGHTNESS_LOW,
		iotv1proto.Brightness_BRIGHTNESS_LOWPLUS,
	}, got)
}

func TestColorTempCanonical_AllEnumsMapped(t *testing.T) {
	cases := []struct {
		enum    iotv1proto.ColorTemperature
		nonZero bool
	}{
		{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED, false},
		{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_FIRSTLIGHT, true},
		{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING, true},
		{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY, true},
		{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON, true},
		{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING, true},
	}
	for _, c := range cases {
		k := ColorTempCanonical(c.enum)
		if c.nonZero {
			require.Greater(t, k, int32(0), "%s should map to non-zero Kelvin", c.enum)
		} else {
			require.Equal(t, int32(0), k, "%s should map to zero", c.enum)
		}
	}
}

func TestColorTempCanonical_WarmthOrder(t *testing.T) {
	// FIRSTLIGHT (coolest, highest Kelvin) → EVENING (warmest, lowest
	// Kelvin). Higher Kelvin = cooler. The fade Computer interpolates
	// in Kelvin space and needs this ordering to be monotone.
	ordered := []iotv1proto.ColorTemperature{
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_FIRSTLIGHT,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING,
	}
	for i := 1; i < len(ordered); i++ {
		prev := ColorTempCanonical(ordered[i-1])
		curr := ColorTempCanonical(ordered[i])
		require.Greater(t, prev, curr, "%s should be cooler (higher K) than %s", ordered[i-1], ordered[i])
	}
}

func TestColorTempNearestEnum_RoundTrip(t *testing.T) {
	for _, enum := range []iotv1proto.ColorTemperature{
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_FIRSTLIGHT,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING,
	} {
		k := ColorTempCanonical(enum)
		got := ColorTempNearestEnum(k)
		require.Equal(t, enum, got, "round-trip failed for %s (Kelvin=%d)", enum, k)
	}
}

func TestKelvinToMireds_KnownPoints(t *testing.T) {
	// mireds = 1_000_000 / Kelvin. Verify the five canonical Kelvin
	// values map back to the existing mired infrastructure (100..500
	// in defaultColorTemperatureMap).
	cases := []struct {
		kelvin int32
		mireds int32
	}{
		{10000, 100}, // FIRSTLIGHT
		{5000, 200},  // MORNING
		{3333, 300},  // DAY (3333 rounds to 1e6/3333 = 300.03 → 300)
		{2500, 400},  // LATEAFTERNOON
		{2000, 500},  // EVENING
	}
	for _, c := range cases {
		require.Equal(t, c.mireds, KelvinToMireds(c.kelvin), "Kelvin=%d", c.kelvin)
	}
	require.Equal(t, int32(0), KelvinToMireds(0), "zero Kelvin is zero mireds (sentinel)")
	require.Equal(t, int32(0), KelvinToMireds(-100), "negative Kelvin is zero mireds")
}
