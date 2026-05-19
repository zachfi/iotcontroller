package iot

import (
	"math"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// canonical.go is the one place that maps between the operator-facing
// discrete Brightness / ColorTemperature enums and the continuous
// internal representation that fade and (eventually) circadian
// produce. The fade-design doc commits this codebase to "continuous
// internally, discrete at the operator surface"; the helpers here are
// what keep that contract from leaking into every consumer.
//
// Canonical values are pinned to the existing device-facing maps in
// zone.go (defaultBrightnessMap, defaultColorTemperatureMap) so a
// fade ending at BRIGHTNESS_VERYLOW sends the same uint8 to the
// device as today's direct SetBrightness(VERYLOW) does. The new
// continuous path doesn't quietly recalibrate the curve.

// brightnessCanonical pairs each Brightness enum with its normalized
// [0, 1] value. Derived from defaultBrightnessMap (uint8 0..254) by
// dividing by 254. Ordered along the perceptual axis VERYLOW..FULL.
var brightnessCanonical = []struct {
	enum  iotv1proto.Brightness
	value float64
}{
	{iotv1proto.Brightness_BRIGHTNESS_VERYLOW, 70.0 / 254.0},
	{iotv1proto.Brightness_BRIGHTNESS_LOW, 90.0 / 254.0},
	{iotv1proto.Brightness_BRIGHTNESS_LOWPLUS, 95.0 / 254.0},
	{iotv1proto.Brightness_BRIGHTNESS_DIM, 110.0 / 254.0},
	{iotv1proto.Brightness_BRIGHTNESS_DIMPLUS, 125.0 / 254.0},
	{iotv1proto.Brightness_BRIGHTNESS_FULL, 1.0},
}

// colorTempCanonical pairs each ColorTemperature enum with its Kelvin
// value. Derived from defaultColorTemperatureMap (mireds 100..500)
// via Kelvin = 1_000_000 / mireds. Ordered along the warmth axis,
// FIRSTLIGHT (coolest, 10000K) → EVENING (warmest, 2000K).
var colorTempCanonical = []struct {
	enum   iotv1proto.ColorTemperature
	kelvin int32
}{
	{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_FIRSTLIGHT, 10000},   // 100 mireds
	{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING, 5000},       // 200 mireds
	{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY, 3333},           // 300 mireds
	{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON, 2500}, // 400 mireds
	{iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING, 2000},       // 500 mireds
}

// BrightnessCanonical returns the canonical normalized value [0, 1]
// for a Brightness enum. UNSPECIFIED (or any unknown enum) returns 0.
func BrightnessCanonical(b iotv1proto.Brightness) float64 {
	for _, p := range brightnessCanonical {
		if p.enum == b {
			return p.value
		}
	}
	return 0
}

// BrightnessNearestEnum returns the Brightness enum whose canonical
// value is closest to v. Used for Status writeback so dashboards and
// existing CRD consumers keep seeing enum labels even when the
// underlying apply was continuous. Out-of-range v clamps to the
// nearest endpoint (VERYLOW for v < 0, FULL for v > 1).
func BrightnessNearestEnum(v float64) iotv1proto.Brightness {
	if v <= 0 {
		return iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED
	}
	best := brightnessCanonical[0]
	bestDist := math.Abs(v - best.value)
	for _, p := range brightnessCanonical[1:] {
		d := math.Abs(v - p.value)
		if d < bestDist {
			best = p
			bestDist = d
		}
	}
	return best.enum
}

// ColorTempCanonical returns the canonical Kelvin value for a
// ColorTemperature enum. UNSPECIFIED returns 0.
func ColorTempCanonical(c iotv1proto.ColorTemperature) int32 {
	for _, p := range colorTempCanonical {
		if p.enum == c {
			return p.kelvin
		}
	}
	return 0
}

// ColorTempNearestEnum returns the ColorTemperature enum whose
// canonical Kelvin is closest to k. UNSPECIFIED for k <= 0.
func ColorTempNearestEnum(k int32) iotv1proto.ColorTemperature {
	if k <= 0 {
		return iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED
	}
	best := colorTempCanonical[0]
	bestDist := absInt32(k - best.kelvin)
	for _, p := range colorTempCanonical[1:] {
		d := absInt32(k - p.kelvin)
		if d < bestDist {
			best = p
			bestDist = d
		}
	}
	return best.enum
}

// KelvinToMireds converts a Kelvin color temperature to the
// device-native mired unit (1_000_000 / kelvin, rounded). Lives here
// alongside the canonical mappings so device handlers and Zone.Flush
// agree on the conversion. Returns 0 for k <= 0.
func KelvinToMireds(k int32) int32 {
	if k <= 0 {
		return 0
	}
	return int32(math.Round(1_000_000.0 / float64(k)))
}

func absInt32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
