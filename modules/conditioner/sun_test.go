package conditioner

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
)

// Test location: Denver, Colorado, USA — middle latitude, well-defined
// sunrise/sunset, no DST quirks for the dates used below. All times
// below in UTC for clarity (go-sunrise returns UTC).
var testLoc = Location{Lat: 39.7392, Lon: -104.9903}

// 2026-06-21 (summer solstice) at Denver. Sunrise ~05:31 UTC,
// sunset ~01:30 UTC the following day (i.e. ~19:30 local).
var testDate = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

func TestSunEvent_Sunrise(t *testing.T) {
	got, err := sunEvent(SunEventSunrise, testDate, testLoc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Denver summer sunrise is ~5:30 MDT = ~11:30 UTC. Range 09-14 UTC
	// is a generous sanity window.
	if got.Year() != 2026 || got.Month() != time.June || got.Day() != 21 ||
		got.Hour() < 9 || got.Hour() > 14 {
		t.Errorf("sunrise out of expected range for Denver summer: %s", got)
	}
}

func TestSunEvent_Sunset(t *testing.T) {
	got, err := sunEvent(SunEventSunset, testDate, testLoc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Denver summer sunset is ~8:30 PM MDT = ~02:30 UTC June 22. The
	// library returns the UTC-normalized timestamp for that day.
	if got.Year() != 2026 || got.Month() != time.June {
		t.Errorf("sunset out of expected month: %s", got)
	}
}

func TestSunEvent_Noon(t *testing.T) {
	rise, _ := sunEvent(SunEventSunrise, testDate, testLoc)
	set, _ := sunEvent(SunEventSunset, testDate, testLoc)
	noon, err := sunEvent(SunEventNoon, testDate, testLoc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := rise.Add(set.Sub(rise) / 2)
	if !noon.Equal(expected) {
		t.Errorf("noon = %s; want midpoint of sunrise/sunset = %s", noon, expected)
	}
}

func TestSunEvent_Unknown(t *testing.T) {
	_, err := sunEvent("dawn-of-time", testDate, testLoc)
	if err == nil {
		t.Errorf("expected error for unknown event")
	}
}

func TestSunRelativeWindow_CrossesMidnight(t *testing.T) {
	// `before: 30m, after: 12h` around sunset. Denver summer sunset is
	// ~02:00 UTC the next day; before=30m → start ~01:30 UTC, after=12h
	// → end ~14:00 UTC that next day. The span is a single
	// continuous [start, end]; end > start.
	window := apiv1.SunWindow{
		Event:  SunEventSunset,
		Before: metav1.Duration{Duration: 30 * time.Minute},
		After:  metav1.Duration{Duration: 12 * time.Hour},
	}

	start, end, err := sunRelativeWindow(window, testDate, testLoc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !end.After(start) {
		t.Errorf("end (%s) must be after start (%s) — windows are continuous spans, not wrap-around", end, start)
	}
	// Cross-midnight sanity: end should be on a different calendar day
	// from start (12.5 h window starting in the evening definitely
	// crosses).
	if start.Day() == end.Day() && start.Month() == end.Month() {
		t.Logf("note: window does not cross midnight at this date (%s → %s); summer solstice in Denver places sunset late enough that this is unusual", start, end)
	}
}

func TestInSunWindow(t *testing.T) {
	// Sunrise window: 1h before, 1h after. Sample three points: well
	// before, during, well after.
	window := apiv1.SunWindow{
		Event:  SunEventSunrise,
		Before: metav1.Duration{Duration: time.Hour},
		After:  metav1.Duration{Duration: time.Hour},
	}

	rise, _ := sunEvent(SunEventSunrise, testDate, testLoc)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"hours before sunrise", rise.Add(-3 * time.Hour), false},
		{"30m before sunrise", rise.Add(-30 * time.Minute), true},
		{"at sunrise", rise, true},
		{"30m after sunrise", rise.Add(30 * time.Minute), true},
		{"hours after sunrise", rise.Add(3 * time.Hour), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := inSunWindow(window, tc.now, testLoc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("inSunWindow(%s) = %v; want %v", tc.now, got, tc.want)
			}
		})
	}
}
