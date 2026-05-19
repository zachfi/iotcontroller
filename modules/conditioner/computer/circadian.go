package computer

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nathan-osman/go-sunrise"

	pkgiot "github.com/zachfi/iotcontroller/pkg/iot"
)

// CircadianName is the registered name. Operators reference this via
// Remediation.ActiveCompute = CircadianName.
const CircadianName = "circadian"

// circadian is the continuous-Kelvin peer of sun_color_temperature.
// Same lat/lon plumbing, same calendar-crossing handling, but the
// daylight buckets become anchor points on a piecewise-linear curve
// rather than step transitions. Lives next to sun.go on purpose so
// future consolidation (the wrapper-supersede option in the design
// doc) stays a one-file change.
//
// Stateless: every Compute call recomputes the day's anchors from
// (now, lat, lon) and interpolates. No snapshot store, no init order
// concerns, can be Register()'d from package init().
//
// Output: continuous Kelvin (ColorTemperatureKelvin) is the truth;
// the enum field (ColorTemperature) is set to the nearest canonical
// step purely as a backward-compat hint for dashboards and Status
// writeback. Operators wiring circadian into a fade or comparing
// against a desired state should read the Kelvin field.
//
// Deviation from the strict reading of docs/circadian-design.md:
// the draft anchors dusk at evening_kelvin (2700K by default) and
// describes night as "held flat past dusk" at 2200K. A literal
// reading produces a 500K cliff at the dusk boundary. This
// implementation adds an extra anchor at set+60m = night_kelvin so
// the dusk→night drop is a 30-minute smooth fade rather than a
// step. The "held flat" semantic still applies past that smoothing
// window. Operators who want the strict step behavior can set
// evening_kelvin and night_kelvin equal.
type circadian struct{}

// Anchor Kelvin values per the design doc curve table. The
// "overridable" anchors (noon, evening/dusk, night/pre-dawn) live in
// circadianParams and default to these constants; the fixed anchors
// (sunrise, morning-end, late-afternoon, sunset) are inlined below.
const (
	defaultSunriseKelvin       int32 = 2700
	defaultMorningEndKelvin    int32 = 5000
	defaultNoonKelvin          int32 = 5500
	defaultLateAfternoonKelvin int32 = 4500
	defaultSunsetKelvin        int32 = 3000
	defaultDuskKelvin          int32 = 2700 // also evening_kelvin default
	defaultNightKelvin         int32 = 2200 // also pre-dawn anchor

	// Sanity clamp. Far outside this range, device handlers saturate
	// anyway; the clamp keeps a misconfigured bias from emitting
	// nonsense Kelvin to downstream consumers / metrics.
	minKelvin int32 = 1500
	maxKelvin int32 = 10000
)

type circadianParams struct {
	noonKelvin    int32
	eveningKelvin int32 // dusk anchor value
	nightKelvin   int32 // pre-dawn floor + night floor
	biasKelvin    int32 // shifts whole curve; negative = warmer
}

func (circadian) Compute(_ context.Context, now time.Time, loc Location, args map[string]string) (ApplyValues, error) {
	p, err := parseCircadianArgs(args)
	if err != nil {
		return ApplyValues{}, err
	}

	// sun.go calendar-crossing pattern: try today's anchor pair, then
	// yesterday's. For east-of-UTC (and far-west) timezones the local
	// evening hours can cross UTC midnight, so a `now` keyed to UTC
	// today may still belong to yesterday's daylight envelope.
	for _, offset := range []int{0, -1} {
		d := now.AddDate(0, 0, offset)
		rise, set := sunrise.SunriseSunset(loc.Lat, loc.Lon, d.Year(), d.Month(), d.Day())
		if k, ok := kelvinFor(now, rise, set, p); ok {
			return resultFor(k), nil
		}
	}

	// Outside any daylight envelope: night floor.
	return resultFor(clampKelvin(p.nightKelvin + p.biasKelvin)), nil
}

// resultFor packages a computed Kelvin value into the ApplyValues
// shape the eval loop expects, including the rounded enum hint.
func resultFor(k int32) ApplyValues {
	return ApplyValues{
		ColorTemperatureKelvin: k,
		ColorTemperature:       pkgiot.ColorTempNearestEnum(k),
	}
}

// kelvinFor returns the interpolated Kelvin if `now` falls inside the
// (pre-dawn..night) envelope of the given (rise, set) pair. ok=false
// is the caller's signal to try yesterday's anchors next, then fall
// through to the night-floor catch-all.
//
// Anchors (declared in chronological order for a typical
// mid-latitude day; sorted stably below to degrade gracefully for
// short winter days where rise+2h can land after set-2h):
//
//	pre-dawn     (rise - 30m)        nightKelvin
//	sunrise      (rise)              2700K
//	morning-end  (rise + 2h)         5000K
//	solar noon   (midpoint)          noonKelvin
//	late-aft     (set - 2h)          4500K
//	sunset       (set)               3000K
//	dusk         (set + 30m)         eveningKelvin
//	night        (set + 60m)         nightKelvin
//
// The trailing `night` anchor is the smoothing addition (see the
// type-level Deviation comment).
func kelvinFor(now, rise, set time.Time, p circadianParams) (int32, bool) {
	type anchor struct {
		t time.Time
		k float64
	}
	midpoint := rise.Add(set.Sub(rise) / 2)
	anchors := []anchor{
		{rise.Add(-30 * time.Minute), float64(p.nightKelvin)},
		{rise, float64(defaultSunriseKelvin)},
		{rise.Add(2 * time.Hour), float64(defaultMorningEndKelvin)},
		{midpoint, float64(p.noonKelvin)},
		{set.Add(-2 * time.Hour), float64(defaultLateAfternoonKelvin)},
		{set, float64(defaultSunsetKelvin)},
		{set.Add(30 * time.Minute), float64(p.eveningKelvin)},
		{set.Add(60 * time.Minute), float64(p.nightKelvin)},
	}
	// Short-day handling: for very short winter days (or polar
	// edges), rise+2h can come after set-2h. Stable sort keeps
	// declared order for any equal timestamps and reorders the rest
	// so the interpolation walks anchors in time order. The curve
	// degrades into something monotonic-ish rather than producing a
	// spike or a backwards segment.
	sort.SliceStable(anchors, func(i, j int) bool { return anchors[i].t.Before(anchors[j].t) })

	last := len(anchors) - 1
	if now.Before(anchors[0].t) || now.After(anchors[last].t) {
		return 0, false
	}
	for i := 0; i < last; i++ {
		a, b := anchors[i], anchors[i+1]
		if now.Before(b.t) {
			// `now` is in [a.t, b.t). Pre-condition holds:
			// the boundary check above guarantees now >= anchors[0].t,
			// and we iterate ascending, so the first segment with
			// now.Before(b.t) has now >= a.t as well.
			var k float64
			if dt := b.t.Sub(a.t); dt > 0 {
				frac := float64(now.Sub(a.t)) / float64(dt)
				k = a.k + (b.k-a.k)*frac
			} else {
				k = b.k
			}
			return clampKelvin(int32(math.Round(k)) + p.biasKelvin), true
		}
	}
	// now == anchors[last].t exactly (we excluded After above).
	return clampKelvin(int32(math.Round(anchors[last].k)) + p.biasKelvin), true
}

// parseCircadianArgs reads the four overridable knobs from args. All
// optional; unset keys take the defaults. Unparseable values return
// an error so the eval loop surfaces the misconfiguration in logs
// rather than silently dropping back to defaults.
func parseCircadianArgs(args map[string]string) (circadianParams, error) {
	p := circadianParams{
		noonKelvin:    defaultNoonKelvin,
		eveningKelvin: defaultDuskKelvin,
		nightKelvin:   defaultNightKelvin,
		biasKelvin:    0,
	}
	for _, override := range []struct {
		key string
		dst *int32
	}{
		{"noon_kelvin", &p.noonKelvin},
		{"evening_kelvin", &p.eveningKelvin},
		{"night_kelvin", &p.nightKelvin},
		{"bias_kelvin", &p.biasKelvin},
	} {
		s := strings.TrimSpace(args[override.key])
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return p, fmt.Errorf("circadian: parse %s %q: %w", override.key, s, err)
		}
		*override.dst = int32(n)
	}
	return p, nil
}

func clampKelvin(k int32) int32 {
	if k < minKelvin {
		return minKelvin
	}
	if k > maxKelvin {
		return maxKelvin
	}
	return k
}

func init() {
	Register(CircadianName, circadian{})
}
