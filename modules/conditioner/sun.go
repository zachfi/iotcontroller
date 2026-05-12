package conditioner

import (
	"fmt"
	"time"

	"github.com/nathan-osman/go-sunrise"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
)

// SunEvent names the supported anchor points for a SunWindow. The strings
// match the go-sunrise terminology where applicable.
const (
	SunEventSunrise  = "sunrise"
	SunEventSunset   = "sunset"
	SunEventNoon     = "noon"
	SunEventMidnight = "midnight"
)

// Location is the configured (lat, lon) used for all solar calculations.
// A single value is shared across the conditioner.
type Location struct {
	Lat float64
	Lon float64
}

// sunEvent returns the time of the named solar event for `date` at
// `loc`. For "noon" we use the midpoint between sunrise and sunset; for
// "midnight" we use the midpoint between today's sunset and tomorrow's
// sunrise (solar midnight, which can drift up to ~16m from clock midnight
// across the year — the operator-visible difference is small).
func sunEvent(event string, date time.Time, loc Location) (time.Time, error) {
	switch event {
	case SunEventSunrise:
		rise, _ := sunrise.SunriseSunset(loc.Lat, loc.Lon, date.Year(), date.Month(), date.Day())
		return rise, nil
	case SunEventSunset:
		_, set := sunrise.SunriseSunset(loc.Lat, loc.Lon, date.Year(), date.Month(), date.Day())
		return set, nil
	case SunEventNoon:
		rise, set := sunrise.SunriseSunset(loc.Lat, loc.Lon, date.Year(), date.Month(), date.Day())
		return rise.Add(set.Sub(rise) / 2), nil
	case SunEventMidnight:
		_, set := sunrise.SunriseSunset(loc.Lat, loc.Lon, date.Year(), date.Month(), date.Day())
		tomorrow := date.AddDate(0, 0, 1)
		nextRise, _ := sunrise.SunriseSunset(loc.Lat, loc.Lon, tomorrow.Year(), tomorrow.Month(), tomorrow.Day())
		return set.Add(nextRise.Sub(set) / 2), nil
	default:
		return time.Time{}, fmt.Errorf("unknown sun event %q", event)
	}
}

// sunRelativeWindow returns the [start, end] span of a SunWindow anchored
// at the named solar event for the date `now` falls within. The window is
// treated as a single continuous span — `Before: 30m, After: 12h` around
// sunset yields a 12.5 h span that typically crosses midnight, returned
// here as one [start, end] pair with end > start.
//
// `now` is used to anchor the date — the window is computed for "today's"
// event from the perspective of `now`. For a sunrise window that opened
// at 5:30 this morning and closed at 6:30, calling sunRelativeWindow at
// 9:00 returns those bounds; at 4:00 (before today's sunrise), it
// likewise returns today's bounds (and the caller observes `now < start`,
// meaning the window hasn't opened yet).
func sunRelativeWindow(window apiv1.SunWindow, now time.Time, loc Location) (start, end time.Time, err error) {
	event, err := sunEvent(window.Event, now, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start = event.Add(-window.Before.Duration)
	end = event.Add(window.After.Duration)
	return start, end, nil
}

// inSunWindow returns true if `now` is within the SunWindow at `loc`.
// Boundary inclusive on both ends.
func inSunWindow(window apiv1.SunWindow, now time.Time, loc Location) (bool, error) {
	start, end, err := sunRelativeWindow(window, now, loc)
	if err != nil {
		return false, err
	}
	return !now.Before(start) && !now.After(end), nil
}
