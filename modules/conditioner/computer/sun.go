package computer

import (
	"context"
	"time"

	"github.com/nathan-osman/go-sunrise"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// SunColorTemperatureName is the registered name. Conditions reference
// the computer via Remediation.ActiveCompute = SunColorTemperatureName.
const SunColorTemperatureName = "sun_color_temperature"

// sunColorTemperature maps the current solar position to one of the six
// ColorTemperature enum steps. The mapping is intentionally coarse — the
// zonekeeper's brightness/CT helpers already cap at the same enum
// granularity, so a finer-grained computer would have its output
// quantized at the apply step anyway.
//
// Mapping:
//
//	pre-dawn        (before sunrise - 30m)       FIRSTLIGHT
//	morning ramp    (sunrise - 30m .. +2h)       MORNING
//	full day        (sunrise+2h .. sunset-2h)    DAY
//	late afternoon  (sunset-2h .. sunset+30m)    LATEAFTERNOON
//	evening / night (sunset+30m onwards)         EVENING
//
// Returns ApplyValues with only ColorTemperature set; brightness, state,
// and color are left UNSPECIFIED so the eval loop's apply doesn't touch
// them. Callers that want to combine sun-CT with a fixed brightness
// stack ActiveScene with ActiveCompute on the same Remediation (Scene
// applies absolute brightness; ApplyValues layers CT over it).
type sunColorTemperature struct{}

func (sunColorTemperature) Compute(_ context.Context, now time.Time, loc Location, _ map[string]string) (ApplyValues, error) {
	// Daylight buckets are defined by a (sunrise, sunset) anchor pair.
	// At any given moment two anchor pairs are potentially relevant:
	// the calendar-UTC date that `now` falls within, and the day
	// before. The previous day's pair matters because for east-of-UTC
	// (or far-west) locations, local evening / late-afternoon /
	// post-sunset hours can cross UTC midnight and fall into the next
	// calendar day's `now`. Example: Denver in summer, sunset at
	// ~02:30 UTC the next calendar day; a tick at 01:30 UTC is "1h
	// before sunset" but the calendar-keyed sunrise/sunset call would
	// return the NEXT day's sun and bucket the tick as FIRSTLIGHT.
	//
	// Solution: check both yesterday's and today's bucket windows. If
	// `now` lands in any daylight bucket of either day, return that
	// bucket. Falling out the bottom is EVENING — the catch-all that
	// covers the long night between yesterday's evening-end and
	// today's pre-dawn.
	for _, offset := range []int{0, -1} {
		d := now.AddDate(0, 0, offset)
		rise, set := sunrise.SunriseSunset(loc.Lat, loc.Lon, d.Year(), d.Month(), d.Day())
		if ct, ok := bucketFor(now, rise, set); ok {
			return ApplyValues{ColorTemperature: ct}, nil
		}
	}
	return ApplyValues{ColorTemperature: iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING}, nil
}

// bucketFor reports the daylight bucket `now` falls into for the given
// (rise, set) anchor pair, or ok=false if `now` is outside all
// daylight windows for that day (i.e. before pre-dawn or after the
// post-sunset envelope). Caller folds ok=false into the EVENING
// catch-all.
//
// Bucket boundaries (relative to the day's rise/set):
//
//	[rise-30m, rise)       FIRSTLIGHT
//	[rise, rise+2h)        MORNING
//	[rise+2h, set-2h)      DAY
//	[set-2h, set+30m)      LATEAFTERNOON
//
// Outside all four → ok=false.
func bucketFor(now, rise, set time.Time) (iotv1proto.ColorTemperature, bool) {
	dawnStart := rise.Add(-30 * time.Minute)
	morningEnd := rise.Add(2 * time.Hour)
	lateStart := set.Add(-2 * time.Hour)
	eveningStart := set.Add(30 * time.Minute)

	switch {
	case !now.Before(dawnStart) && now.Before(rise):
		return iotv1proto.ColorTemperature_COLOR_TEMPERATURE_FIRSTLIGHT, true
	case !now.Before(rise) && now.Before(morningEnd):
		return iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING, true
	case !now.Before(morningEnd) && now.Before(lateStart):
		return iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY, true
	case !now.Before(lateStart) && now.Before(eveningStart):
		return iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON, true
	}
	return 0, false
}

func init() {
	Register(SunColorTemperatureName, sunColorTemperature{})
}
