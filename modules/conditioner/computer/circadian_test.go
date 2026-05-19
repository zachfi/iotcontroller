package computer

import (
	"context"
	"testing"
	"time"

	"github.com/nathan-osman/go-sunrise"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// Anchor exactness: every anchor time should produce its anchor's
// Kelvin value (no interpolation rounding error). Fetches rise/set
// from go-sunrise directly so the test stays honest about what the
// library reports rather than relying on hardcoded clock times. Uses
// the same Denver fixture as sun_test.go.
func TestCircadian_AtAnchors(t *testing.T) {
	c, ok := Get(CircadianName)
	if !ok {
		t.Fatalf("computer %q not registered", CircadianName)
	}

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rise, set := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())
	midpoint := rise.Add(set.Sub(rise) / 2)

	tests := []struct {
		name string
		at   time.Time
		want int32
	}{
		{"pre-dawn anchor (night floor)", rise.Add(-30 * time.Minute), defaultNightKelvin},
		{"sunrise anchor", rise, defaultSunriseKelvin},
		{"morning-end anchor", rise.Add(2 * time.Hour), defaultMorningEndKelvin},
		{"solar noon anchor (peak)", midpoint, defaultNoonKelvin},
		{"late-afternoon anchor", set.Add(-2 * time.Hour), defaultLateAfternoonKelvin},
		{"sunset anchor", set, defaultSunsetKelvin},
		{"dusk anchor", set.Add(30 * time.Minute), defaultDuskKelvin},
		{"night anchor (smoothing tail)", set.Add(60 * time.Minute), defaultNightKelvin},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Compute(context.Background(), tc.at, testLoc, nil)
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if got.ColorTemperatureKelvin != tc.want {
				t.Errorf("at %s: Kelvin = %d; want %d", tc.at, got.ColorTemperatureKelvin, tc.want)
			}
			// The enum hint should also be set — even if it rounds to
			// something approximate, it must not be UNSPECIFIED when
			// Kelvin is non-zero.
			if got.ColorTemperature == iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED {
				t.Errorf("at %s: enum hint left UNSPECIFIED for Kelvin=%d", tc.at, got.ColorTemperatureKelvin)
			}
			// Computer must not touch the other axes.
			if got.State != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
				t.Errorf("state set unexpectedly: %s", got.State)
			}
			if got.Brightness != iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED {
				t.Errorf("brightness set unexpectedly: %s", got.Brightness)
			}
			if got.BrightnessValue != 0 {
				t.Errorf("brightness value set unexpectedly: %f", got.BrightnessValue)
			}
			if got.Color != "" {
				t.Errorf("color set unexpectedly: %q", got.Color)
			}
		})
	}
}

// Interpolation: midway between two adjacent anchors should sit
// midway between their Kelvin values. Uses the sunrise→morning-end
// segment since it's the steepest (2300K spread over 2h), which makes
// off-by-one interpolation bugs visible.
func TestCircadian_Interpolation(t *testing.T) {
	c, _ := Get(CircadianName)

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rise, _ := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())

	midpoint := rise.Add(time.Hour) // halfway between rise (2700) and rise+2h (5000)
	got, err := c.Compute(context.Background(), midpoint, testLoc, nil)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	wantMid := (defaultSunriseKelvin + defaultMorningEndKelvin) / 2 // 3850
	if abs := absInt32(got.ColorTemperatureKelvin - wantMid); abs > 1 {
		t.Errorf("midpoint Kelvin = %d; want ~%d (±1 rounding)", got.ColorTemperatureKelvin, wantMid)
	}
}

// Night floor: a tick well past dusk+60m on the same calendar UTC
// day must drop to night_kelvin via the envelope-exit fallback.
func TestCircadian_NightAfterSmoothingTail(t *testing.T) {
	c, _ := Get(CircadianName)

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	_, set := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())

	// 4 hours past sunset is well past the dusk+60m smoothing tail.
	got, err := c.Compute(context.Background(), set.Add(4*time.Hour), testLoc, nil)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.ColorTemperatureKelvin != defaultNightKelvin {
		t.Errorf("Kelvin = %d; want %d (night floor)", got.ColorTemperatureKelvin, defaultNightKelvin)
	}
}

// Pre-dawn cutoff: a tick well before pre-dawn must drop to
// night_kelvin via the envelope-entry fallback (and the
// yesterday-loop must NOT pull yesterday's daylight anchor in).
func TestCircadian_NightBeforePreDawn(t *testing.T) {
	c, _ := Get(CircadianName)

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rise, _ := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())

	// 4 hours before sunrise (well before pre-dawn) — yesterday's
	// dusk+60m has long since passed.
	got, err := c.Compute(context.Background(), rise.Add(-4*time.Hour), testLoc, nil)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.ColorTemperatureKelvin != defaultNightKelvin {
		t.Errorf("Kelvin = %d; want %d (night floor)", got.ColorTemperatureKelvin, defaultNightKelvin)
	}
}

// Calendar crossing: for Denver in summer, sunset on June 20 falls
// after UTC midnight on June 21. A `now` keyed to early June 21 UTC
// must locate its anchor in yesterday's envelope, not today's
// pre-dawn — otherwise the catch-all wrongly serves night.
func TestCircadian_CalendarCrossing(t *testing.T) {
	c, _ := Get(CircadianName)

	// Yesterday's sunset for the June 21 keying.
	prev := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	_, prevSet := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, prev.Year(), prev.Month(), prev.Day())

	// 15 minutes after prevSet — squarely inside the (set, set+30m)
	// segment, so the result must interpolate between 3000K and
	// the dusk anchor (default 2700K). 15m / 30m = 0.5 → 2850K.
	at := prevSet.Add(15 * time.Minute)
	got, err := c.Compute(context.Background(), at, testLoc, nil)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	want := (defaultSunsetKelvin + defaultDuskKelvin) / 2 // 2850
	if abs := absInt32(got.ColorTemperatureKelvin - want); abs > 1 {
		t.Errorf("calendar-crossed tick: Kelvin = %d; want ~%d (yesterday's anchors)", got.ColorTemperatureKelvin, want)
	}
}

// bias_kelvin shifts the whole curve uniformly. A negative bias
// makes everywhere warmer; sunrise should land below its anchor by
// exactly |bias|.
func TestCircadian_Bias(t *testing.T) {
	c, _ := Get(CircadianName)

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rise, _ := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())

	got, err := c.Compute(context.Background(), rise, testLoc, map[string]string{"bias_kelvin": "-200"})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	want := defaultSunriseKelvin - 200
	if got.ColorTemperatureKelvin != want {
		t.Errorf("biased sunrise Kelvin = %d; want %d", got.ColorTemperatureKelvin, want)
	}
}

// noon_kelvin override pins the peak to a different Kelvin and
// re-derives the surrounding interpolation. Sets noon to 4000 and
// checks that the noon anchor returns exactly 4000.
func TestCircadian_NoonOverride(t *testing.T) {
	c, _ := Get(CircadianName)

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rise, set := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())
	midpoint := rise.Add(set.Sub(rise) / 2)

	got, err := c.Compute(context.Background(), midpoint, testLoc, map[string]string{"noon_kelvin": "4000"})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.ColorTemperatureKelvin != 4000 {
		t.Errorf("overridden noon Kelvin = %d; want 4000", got.ColorTemperatureKelvin)
	}
}

// Clamping: a huge bias must saturate at the configured min/max
// rather than emit nonsense Kelvin downstream.
func TestCircadian_BiasClamps(t *testing.T) {
	c, _ := Get(CircadianName)

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rise, _ := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())

	got, err := c.Compute(context.Background(), rise, testLoc, map[string]string{"bias_kelvin": "999999"})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.ColorTemperatureKelvin != maxKelvin {
		t.Errorf("max-bias Kelvin = %d; want maxKelvin %d", got.ColorTemperatureKelvin, maxKelvin)
	}

	got, err = c.Compute(context.Background(), rise, testLoc, map[string]string{"bias_kelvin": "-999999"})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.ColorTemperatureKelvin != minKelvin {
		t.Errorf("min-bias Kelvin = %d; want minKelvin %d", got.ColorTemperatureKelvin, minKelvin)
	}
}

// Unparseable override surfaces as an error rather than silently
// dropping back to defaults.
func TestCircadian_BadArg(t *testing.T) {
	c, _ := Get(CircadianName)

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rise, _ := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())

	_, err := c.Compute(context.Background(), rise, testLoc, map[string]string{"noon_kelvin": "kinda warm"})
	if err == nil {
		t.Errorf("expected error for unparseable noon_kelvin; got nil")
	}
}

// Pure function: identical (now, loc, args) → identical output. The
// Computer interface contract requires no hidden state.
func TestCircadian_IsPure(t *testing.T) {
	c, _ := Get(CircadianName)
	now := time.Date(2026, 6, 21, 18, 30, 0, 0, time.UTC)
	v1, err := c.Compute(context.Background(), now, testLoc, nil)
	if err != nil {
		t.Fatalf("first compute: %v", err)
	}
	v2, err := c.Compute(context.Background(), now, testLoc, nil)
	if err != nil {
		t.Fatalf("second compute: %v", err)
	}
	if v1 != v2 {
		t.Errorf("non-deterministic compute: first=%+v second=%+v", v1, v2)
	}
}

// Registry: CircadianName must be discoverable alongside the other
// computers — Names() is used at startup for diagnostic logging.
func TestCircadian_Registered(t *testing.T) {
	names := Names()
	var found bool
	for _, n := range names {
		if n == CircadianName {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() does not contain %q; got %v", CircadianName, names)
	}
}

func absInt32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
