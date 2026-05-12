package computer

import (
	"context"
	"testing"
	"time"

	"github.com/nathan-osman/go-sunrise"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// Denver, Colorado. Mid-latitude, long summer days.
var testLoc = Location{Lat: 39.7392, Lon: -104.9903}

// TestSunColorTemperature_Buckets samples the computer at offsets from
// the day's actual sunrise/sunset and verifies each bucket boundary.
// Fetches the anchor times from go-sunrise directly so the test stays
// honest about what the library reports rather than relying on
// hardcoded wall-clock times.
func TestSunColorTemperature_Buckets(t *testing.T) {
	c, ok := Get(SunColorTemperatureName)
	if !ok {
		t.Fatalf("computer %q not registered", SunColorTemperatureName)
	}

	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	rise, set := sunrise.SunriseSunset(testLoc.Lat, testLoc.Lon, date.Year(), date.Month(), date.Day())

	tests := []struct {
		name string
		now  time.Time
		want iotv1proto.ColorTemperature
	}{
		// Deep-night maps to EVENING, not FIRSTLIGHT, by design.
		// FIRSTLIGHT is the narrow "first hint of dawn" window — a
		// cool / blue CT that mimics actual dawn sky. Two hours before
		// sunrise is dead-of-night for indoor purposes: someone getting
		// up at 3am wants warm and dim, not blue dawn light.
		{"two hours before sunrise (deep night, warm)", rise.Add(-2 * time.Hour), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING},
		{"15m before sunrise (in the pre-dawn window)", rise.Add(-15 * time.Minute), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_FIRSTLIGHT},
		{"at sunrise", rise, iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING},
		{"one hour after sunrise", rise.Add(time.Hour), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING},
		{"three hours after sunrise (full day)", rise.Add(3 * time.Hour), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY},
		{"midday", rise.Add(set.Sub(rise) / 2), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY},
		{"one hour before sunset (late afternoon)", set.Add(-time.Hour), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON},
		{"one hour after sunset (evening)", set.Add(time.Hour), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING},
		{"middle of night (evening)", set.Add(6 * time.Hour), iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Compute(context.Background(), tc.now, testLoc, nil)
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if got.ColorTemperature != tc.want {
				t.Errorf("at %s: CT = %s; want %s", tc.now, got.ColorTemperature, tc.want)
			}
			// Computer should leave other fields unset so the eval-loop
			// apply only touches color temperature.
			if got.State != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
				t.Errorf("state set unexpectedly: %s", got.State)
			}
			if got.Brightness != iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED {
				t.Errorf("brightness set unexpectedly: %s", got.Brightness)
			}
			if got.Color != "" {
				t.Errorf("color set unexpectedly: %q", got.Color)
			}
		})
	}
}

func TestSunColorTemperature_IsPure(t *testing.T) {
	// Two calls with identical inputs must produce identical outputs —
	// the Computer interface specifies pure functions of (now, loc,
	// args). No hidden state.
	c, _ := Get(SunColorTemperatureName)
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

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

func TestRegistry_GetUnknown(t *testing.T) {
	_, ok := Get("nope")
	if ok {
		t.Errorf("unknown computer reported as registered")
	}
}

func TestRegistry_Names(t *testing.T) {
	names := Names()
	var found bool
	for _, n := range names {
		if n == SunColorTemperatureName {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() does not contain %q; got %v", SunColorTemperatureName, names)
	}
}
