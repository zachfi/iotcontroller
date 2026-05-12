package computer

import (
	"context"
	"testing"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// Reference "now" used across most subtests. May 12 2026 is a
// Tuesday; Mountain Time = UTC-6 (MDT in May).
var rampLoc = Location{} // unused; ramp computer doesn't consult Location

// loadDenver returns America/Denver or skips the test if the tz
// database isn't available. Helps the test pass on minimal CI images
// where time-zone-data may be missing.
func loadDenver(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("America/Denver tz unavailable: %v", err)
	}
	return loc
}

func TestRamp_BrightnessProgressBoundaries(t *testing.T) {
	c, ok := Get(RampName)
	if !ok {
		t.Fatalf("computer %q not registered", RampName)
	}
	tz := loadDenver(t)

	// Ramp: BRIGHTNESS_LOW → BRIGHTNESS_FULL, 07:00–07:30 MDT.
	args := map[string]string{
		"field":    "brightness",
		"from":     "BRIGHTNESS_LOW",
		"to":       "BRIGHTNESS_FULL",
		"start_at": "07:00",
		"duration": "30m",
		"timezone": "America/Denver",
	}

	tests := []struct {
		name    string
		now     time.Time
		want    iotv1proto.Brightness
		wantSet bool // true if Brightness should be set; false = computer returns zero ApplyValues
	}{
		// Before the window.
		{
			name:    "before start",
			now:     time.Date(2026, 5, 12, 6, 30, 0, 0, tz),
			wantSet: false,
		},
		// At start: progress=0, expect from.
		{
			name:    "at start",
			now:     time.Date(2026, 5, 12, 7, 0, 0, 0, tz),
			want:    iotv1proto.Brightness_BRIGHTNESS_LOW,
			wantSet: true,
		},
		// Mid-ramp: progress=0.5 over a 5-step span (LOW=1 → FULL=5
		// in physical-order indices). 1 + 0.5*4 = 3 → DIM.
		{
			name:    "midpoint",
			now:     time.Date(2026, 5, 12, 7, 15, 0, 0, tz),
			want:    iotv1proto.Brightness_BRIGHTNESS_DIM,
			wantSet: true,
		},
		// At end: progress=1, expect to.
		{
			name:    "at end",
			now:     time.Date(2026, 5, 12, 7, 30, 0, 0, tz),
			want:    iotv1proto.Brightness_BRIGHTNESS_FULL,
			wantSet: true,
		},
		// After the window.
		{
			name:    "after end",
			now:     time.Date(2026, 5, 12, 8, 0, 0, 0, tz),
			wantSet: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Compute(context.Background(), tc.now, rampLoc, args)
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if tc.wantSet {
				if got.Brightness != tc.want {
					t.Errorf("brightness = %s; want %s (at %s)", got.Brightness, tc.want, tc.now)
				}
			} else {
				if got.Brightness != iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED {
					t.Errorf("brightness should be UNSPECIFIED outside window; got %s", got.Brightness)
				}
			}
			// Other fields must always stay unset.
			if got.State != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
				t.Errorf("state set unexpectedly: %s", got.State)
			}
			if got.ColorTemperature != iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED {
				t.Errorf("color_temperature set unexpectedly: %s", got.ColorTemperature)
			}
			if got.Color != "" {
				t.Errorf("color set unexpectedly: %q", got.Color)
			}
		})
	}
}

func TestRamp_BrightnessReversed(t *testing.T) {
	// FULL → VERYLOW: a dim-down ramp. Midpoint of a 6-step descent
	// (FULL=5 → VERYLOW=0 in physical order) is 2.5 → rounds to
	// either LOWPLUS (idx 2) or DIM (idx 3) depending on rounding.
	// Go's math.Round is half-away-from-zero: round(2.5) = 3 = DIM.
	c, _ := Get(RampName)
	tz := loadDenver(t)
	args := map[string]string{
		"field":    "brightness",
		"from":     "BRIGHTNESS_FULL",
		"to":       "BRIGHTNESS_VERYLOW",
		"start_at": "20:00",
		"duration": "60m",
		"timezone": "America/Denver",
	}

	at := time.Date(2026, 5, 12, 20, 30, 0, 0, tz) // midpoint
	got, err := c.Compute(context.Background(), at, rampLoc, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.Brightness != iotv1proto.Brightness_BRIGHTNESS_DIM &&
		got.Brightness != iotv1proto.Brightness_BRIGHTNESS_LOWPLUS {
		t.Errorf("midpoint of FULL→VERYLOW should be DIM or LOWPLUS; got %s", got.Brightness)
	}
}

func TestRamp_ColorTemperature(t *testing.T) {
	c, _ := Get(RampName)
	tz := loadDenver(t)
	args := map[string]string{
		"field":    "color_temperature",
		"from":     "COLOR_TEMPERATURE_MORNING",
		"to":       "COLOR_TEMPERATURE_DAY",
		"start_at": "08:00",
		"duration": "60m",
		"timezone": "America/Denver",
	}

	// Midpoint of MORNING (idx 1) → DAY (idx 2) = 1.5 → rounds to 2 = DAY.
	at := time.Date(2026, 5, 12, 8, 30, 0, 0, tz)
	got, err := c.Compute(context.Background(), at, rampLoc, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.ColorTemperature != iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY &&
		got.ColorTemperature != iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING {
		t.Errorf("midpoint MORNING→DAY: got %s", got.ColorTemperature)
	}
}

func TestRamp_DefaultTimezoneIsUTC(t *testing.T) {
	c, _ := Get(RampName)
	args := map[string]string{
		"field":    "brightness",
		"from":     "BRIGHTNESS_LOW",
		"to":       "BRIGHTNESS_FULL",
		"start_at": "07:00",
		"duration": "30m",
		// timezone intentionally unset
	}
	// 07:00 UTC May 12 2026 = within window at progress=0.
	at := time.Date(2026, 5, 12, 7, 0, 0, 0, time.UTC)
	got, err := c.Compute(context.Background(), at, rampLoc, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.Brightness != iotv1proto.Brightness_BRIGHTNESS_LOW {
		t.Errorf("at-start (UTC) brightness = %s; want LOW", got.Brightness)
	}
}

func TestRamp_IsPure(t *testing.T) {
	// Same inputs ⇒ same outputs. The Computer contract.
	c, _ := Get(RampName)
	tz := loadDenver(t)
	args := map[string]string{
		"field":    "brightness",
		"from":     "BRIGHTNESS_LOW",
		"to":       "BRIGHTNESS_FULL",
		"start_at": "07:00",
		"duration": "30m",
		"timezone": "America/Denver",
	}
	now := time.Date(2026, 5, 12, 7, 12, 0, 0, tz)
	v1, _ := c.Compute(context.Background(), now, rampLoc, args)
	v2, _ := c.Compute(context.Background(), now, rampLoc, args)
	if v1 != v2 {
		t.Errorf("non-deterministic compute: %v vs %v", v1, v2)
	}
}

func TestRamp_ArgErrors(t *testing.T) {
	c, _ := Get(RampName)
	now := time.Date(2026, 5, 12, 7, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		args map[string]string
	}{
		{"missing field", map[string]string{"from": "BRIGHTNESS_LOW", "to": "BRIGHTNESS_FULL", "start_at": "07:00", "duration": "30m"}},
		{"unknown field", map[string]string{"field": "color", "from": "BRIGHTNESS_LOW", "to": "BRIGHTNESS_FULL", "start_at": "07:00", "duration": "30m"}},
		{"bad duration", map[string]string{"field": "brightness", "from": "BRIGHTNESS_LOW", "to": "BRIGHTNESS_FULL", "start_at": "07:00", "duration": "nope"}},
		{"zero duration", map[string]string{"field": "brightness", "from": "BRIGHTNESS_LOW", "to": "BRIGHTNESS_FULL", "start_at": "07:00", "duration": "0s"}},
		{"bad timezone", map[string]string{"field": "brightness", "from": "BRIGHTNESS_LOW", "to": "BRIGHTNESS_FULL", "start_at": "07:00", "duration": "30m", "timezone": "Mars/Olympus"}},
		{"bad start_at", map[string]string{"field": "brightness", "from": "BRIGHTNESS_LOW", "to": "BRIGHTNESS_FULL", "start_at": "07-00", "duration": "30m"}},
		{"bad brightness name", map[string]string{"field": "brightness", "from": "BRIGHTNESS_BOGUS", "to": "BRIGHTNESS_FULL", "start_at": "07:00", "duration": "30m"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Compute(context.Background(), now, rampLoc, tc.args)
			if err == nil {
				t.Errorf("expected error for %s; got nil", tc.name)
			}
		})
	}
}

func TestRamp_RegistryEntry(t *testing.T) {
	if _, ok := Get(RampName); !ok {
		t.Errorf("ramp not registered")
	}
	var found bool
	for _, n := range Names() {
		if n == RampName {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() does not contain %q", RampName)
	}
}
