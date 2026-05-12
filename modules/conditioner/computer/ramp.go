package computer

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// RampName is the registered name for the ramp computer. Conditions
// reference it via Remediation.ActiveCompute = RampName.
const RampName = "ramp"

// ramp linearly interpolates a single zone field from `from` to `to`
// over a daily time window. Outside the window the computer returns
// an empty ApplyValues so other layers (buttons, scenes, alerts) own
// the zone.
//
// Args (Remediation.ActiveComputeArgs):
//
//	field       brightness | color_temperature
//	from        enum-name string for the starting value
//	to          enum-name string for the ending value
//	start_at    HH:MM (24-hour) in the given timezone
//	duration    Go duration string ("30m", "1h", "1h30m")
//	timezone    IANA timezone for start_at. Defaults to UTC.
//
// Example — fade the office to full brightness over 30 min before
// the workday starts:
//
//	active_compute: ramp
//	active_compute_args:
//	  field: brightness
//	  from: BRIGHTNESS_LOW
//	  to: BRIGHTNESS_FULL
//	  start_at: "07:00"
//	  duration: "30m"
//	  timezone: America/Denver
//
// Behaviour:
//   - now < today's start_at:   returns ApplyValues{} (no apply)
//   - now in [start_at, start_at+duration]:   interpolated step
//   - now > today's end:        returns ApplyValues{} (no apply)
//
// The ramp resets every day — same window fires Mon/Tue/Wed/... The
// Remediation's time_intervals layer is what restricts which calendar
// days the eval loop touches the Remediation at all (eg. weekdays).
//
// Interpolation respects *physical* order rather than enum integer
// order. Brightness physical order (dim→bright) is VERYLOW, LOW,
// LOWPLUS, DIM, DIMPLUS, FULL; ColorTemperature physical order
// (cool→warm) happens to match the enum order. Going from a higher-
// index to a lower-index endpoint reverses the ramp (e.g. FULL → DIM).
type ramp struct{}

// brightnessPhysical is the physical-order index used by ramp's
// brightness interpolation. The proto enum order (FULL=1, DIM=2, …,
// VERYLOW=6) is roughly the *reverse* of perceptual order, so we
// can't interpolate on the proto integer directly without reversing
// the operator's apparent intent.
var brightnessPhysical = []iotv1proto.Brightness{
	iotv1proto.Brightness_BRIGHTNESS_VERYLOW,
	iotv1proto.Brightness_BRIGHTNESS_LOW,
	iotv1proto.Brightness_BRIGHTNESS_LOWPLUS,
	iotv1proto.Brightness_BRIGHTNESS_DIM,
	iotv1proto.Brightness_BRIGHTNESS_DIMPLUS,
	iotv1proto.Brightness_BRIGHTNESS_FULL,
}

// colorTempPhysical is the physical-order index (cool→warm) used by
// ramp's color_temperature interpolation. Matches the proto enum
// order, but we keep the table explicit so a future enum reorder
// doesn't silently change the ramp's interpolation curve.
var colorTempPhysical = []iotv1proto.ColorTemperature{
	iotv1proto.ColorTemperature_COLOR_TEMPERATURE_FIRSTLIGHT,
	iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING,
	iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY,
	iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON,
	iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING,
}

func (ramp) Compute(_ context.Context, now time.Time, _ Location, args map[string]string) (ApplyValues, error) {
	field := args["field"]
	fromStr := args["from"]
	toStr := args["to"]
	startAtStr := args["start_at"]
	durationStr := args["duration"]
	tzStr := args["timezone"]
	if tzStr == "" {
		tzStr = "UTC"
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return ApplyValues{}, fmt.Errorf("ramp: parse duration %q: %w", durationStr, err)
	}
	if duration <= 0 {
		return ApplyValues{}, fmt.Errorf("ramp: duration must be > 0, got %s", duration)
	}

	loc, err := time.LoadLocation(tzStr)
	if err != nil {
		return ApplyValues{}, fmt.Errorf("ramp: load timezone %q: %w", tzStr, err)
	}

	hh, mm, err := parseHHMM(startAtStr)
	if err != nil {
		return ApplyValues{}, fmt.Errorf("ramp: parse start_at %q: %w", startAtStr, err)
	}

	// Today's ramp window, anchored to midnight-local + start_at HH:MM.
	// Use the calendar date of `now` *as seen in the configured
	// timezone* so the daily reset aligns with the operator's local
	// midnight, not UTC midnight.
	nowLocal := now.In(loc)
	startToday := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), hh, mm, 0, 0, loc)
	end := startToday.Add(duration)

	if now.Before(startToday) || now.After(end) {
		return ApplyValues{}, nil
	}

	// Linear progress in [0, 1]. Clamp guards against floating-point
	// drift at the boundaries.
	progress := float64(now.Sub(startToday)) / float64(duration)
	if progress < 0 {
		progress = 0
	} else if progress > 1 {
		progress = 1
	}

	switch field {
	case "brightness":
		from, ok := parseBrightness(fromStr)
		if !ok {
			return ApplyValues{}, fmt.Errorf("ramp: unknown brightness %q (want one of %s)", fromStr, brightnessNames())
		}
		to, ok := parseBrightness(toStr)
		if !ok {
			return ApplyValues{}, fmt.Errorf("ramp: unknown brightness %q (want one of %s)", toStr, brightnessNames())
		}
		return ApplyValues{Brightness: interpolateBrightness(from, to, progress)}, nil

	case "color_temperature":
		from, ok := parseColorTemp(fromStr)
		if !ok {
			return ApplyValues{}, fmt.Errorf("ramp: unknown color_temperature %q (want one of %s)", fromStr, colorTempNames())
		}
		to, ok := parseColorTemp(toStr)
		if !ok {
			return ApplyValues{}, fmt.Errorf("ramp: unknown color_temperature %q (want one of %s)", toStr, colorTempNames())
		}
		return ApplyValues{ColorTemperature: interpolateColorTemp(from, to, progress)}, nil

	case "":
		return ApplyValues{}, fmt.Errorf("ramp: args.field is required (brightness | color_temperature)")
	default:
		return ApplyValues{}, fmt.Errorf("ramp: unknown field %q (want brightness | color_temperature)", field)
	}
}

// parseHHMM parses "HH:MM" (24-hour). Whitespace and surrounding
// quotes from YAML parsing are tolerated so an operator writing
// `start_at: "07:00"` in deployment_tools doesn't need to think about
// quoting.
func parseHHMM(s string) (hour, minute int, err error) {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", s)
	}
	hour, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("hour: %w", err)
	}
	if hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("hour out of range: %d", hour)
	}
	minute, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("minute: %w", err)
	}
	if minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("minute out of range: %d", minute)
	}
	return hour, minute, nil
}

// parseBrightness looks up a Brightness enum by its proto name. The
// proto integer values are also accepted for ergonomics.
func parseBrightness(s string) (iotv1proto.Brightness, bool) {
	s = strings.TrimSpace(s)
	if v, ok := iotv1proto.Brightness_value[s]; ok {
		return iotv1proto.Brightness(v), true
	}
	if n, err := strconv.Atoi(s); err == nil {
		if _, ok := iotv1proto.Brightness_name[int32(n)]; ok {
			return iotv1proto.Brightness(n), true
		}
	}
	return 0, false
}

func brightnessNames() string {
	names := make([]string, 0, len(brightnessPhysical))
	for _, b := range brightnessPhysical {
		names = append(names, b.String())
	}
	return strings.Join(names, ", ")
}

// parseColorTemp looks up a ColorTemperature enum by its proto name.
// As with parseBrightness, the proto integer is also accepted.
func parseColorTemp(s string) (iotv1proto.ColorTemperature, bool) {
	s = strings.TrimSpace(s)
	if v, ok := iotv1proto.ColorTemperature_value[s]; ok {
		return iotv1proto.ColorTemperature(v), true
	}
	if n, err := strconv.Atoi(s); err == nil {
		if _, ok := iotv1proto.ColorTemperature_name[int32(n)]; ok {
			return iotv1proto.ColorTemperature(n), true
		}
	}
	return 0, false
}

func colorTempNames() string {
	names := make([]string, 0, len(colorTempPhysical))
	for _, c := range colorTempPhysical {
		names = append(names, c.String())
	}
	return strings.Join(names, ", ")
}

// interpolateBrightness picks the brightness step closest to a linear
// interpolation between `from` and `to` at the given progress in
// [0, 1]. Indices are taken from brightnessPhysical so the curve
// follows perceptual order, not the proto enum integers.
//
// If either endpoint isn't in brightnessPhysical (e.g. UNSPECIFIED),
// the function returns `to` — the most useful degenerate behavior
// is "end up at the requested target."
func interpolateBrightness(from, to iotv1proto.Brightness, progress float64) iotv1proto.Brightness {
	fromIdx, fromOK := indexOfBrightness(from)
	toIdx, toOK := indexOfBrightness(to)
	if !fromOK || !toOK {
		return to
	}
	pos := float64(fromIdx) + progress*float64(toIdx-fromIdx)
	idx := int(math.Round(pos))
	if idx < 0 {
		idx = 0
	} else if idx >= len(brightnessPhysical) {
		idx = len(brightnessPhysical) - 1
	}
	return brightnessPhysical[idx]
}

func indexOfBrightness(b iotv1proto.Brightness) (int, bool) {
	for i, v := range brightnessPhysical {
		if v == b {
			return i, true
		}
	}
	return 0, false
}

// interpolateColorTemp is the CT analog of interpolateBrightness.
func interpolateColorTemp(from, to iotv1proto.ColorTemperature, progress float64) iotv1proto.ColorTemperature {
	fromIdx, fromOK := indexOfColorTemp(from)
	toIdx, toOK := indexOfColorTemp(to)
	if !fromOK || !toOK {
		return to
	}
	pos := float64(fromIdx) + progress*float64(toIdx-fromIdx)
	idx := int(math.Round(pos))
	if idx < 0 {
		idx = 0
	} else if idx >= len(colorTempPhysical) {
		idx = len(colorTempPhysical) - 1
	}
	return colorTempPhysical[idx]
}

func indexOfColorTemp(c iotv1proto.ColorTemperature) (int, bool) {
	for i, v := range colorTempPhysical {
		if v == c {
			return i, true
		}
	}
	return 0, false
}

func init() {
	Register(RampName, ramp{})
}
