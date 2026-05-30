package computer

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// prom_scalar.go — continuous-output PromQL Computer. Sibling to the
// discrete `query` Computer: instead of thresholding the result and
// returning one of two operator-supplied ApplyValues tuples, it
// LINEARLY MAPS the scalar onto a single ApplyValues axis (the
// continuous BrightnessValue or ColorTemperatureKelvin field).
//
// Use cases:
//
//   - Cloud coverage (0-100%) → brightness (0.5 - 0.9): brighter
//     lights when the sky's cloudier, smooth transitions across the
//     day.
//
//   - Solar irradiance / sunset proximity → color_temperature
//     (2200K-5000K): smooth warming toward sunset without the
//     discrete COLOR_TEMPERATURE enum buckets.
//
// Args:
//
//	query         PromQL expression. Required.
//	output_axis   "brightness" | "color_temperature". Required.
//	in_min        Input lower bound. Required.
//	in_max        Input upper bound. Required (must differ from in_min).
//	out_min       Output lower bound. For brightness: 0.0-1.0
//	              continuous BrightnessValue. For color_temperature:
//	              integer Kelvin (e.g. 2200). Required.
//	out_max       Output upper bound. Required.
//	clamp         "true" (default) clamps values outside [in_min, in_max]
//	              to the corresponding output bound. "false" extrapolates
//	              linearly past the bounds (rarely what you want).
//	on_error.brightness_value         Optional fail-safe BrightnessValue
//	on_error.color_temperature_kelvin Optional fail-safe Kelvin
//
// Behaviour:
//
//	Linear interpolation: out = out_min + (in - in_min) * (out_max
//	  - out_min) / (in_max - in_min). Clamped to [out_min, out_max]
//	  by default.
//	Empty vector / Mimir error: same resolution order as query —
//	  declared on_error.* > cached last-known-good > error.
//
// Each Compute returns ApplyValues with EXACTLY ONE continuous axis
// populated (the configured output_axis); other fields stay at their
// zero values so the stack composition's per-axis switch leaves
// other axes alone.

const PromScalarName = "prom_scalar"

// outputAxis enumerates the continuous axes prom_scalar can drive.
// Discrete axes (state, color string) make no sense for a linear map.
type outputAxis int

const (
	axisUnknown outputAxis = iota
	axisBrightnessValue
	axisColorTemperatureKelvin
)

func parseOutputAxis(s string) (outputAxis, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "brightness", "brightness_value":
		return axisBrightnessValue, nil
	case "color_temperature", "color_temperature_kelvin":
		return axisColorTemperatureKelvin, nil
	case "":
		return axisUnknown, fmt.Errorf("output_axis is required")
	default:
		return axisUnknown, fmt.Errorf("unknown output_axis %q (want brightness or color_temperature)", s)
	}
}

// promScalar implements the Computer interface for the continuous
// PromQL-driven case. Holds its own promClient and per-args cache;
// caches by hashArgs so two Conditions with identical args share one
// entry (same as query).
type promScalar struct {
	pc     *promClient
	logger *slog.Logger

	cacheMu sync.Mutex
	cache   map[string]ApplyValues
}

// NewPromScalar builds a continuous-output PromQL Computer.
// Registered alongside NewQuery by the conditioner module when
// QueryConfig.Endpoint is non-empty.
func NewPromScalar(cfg QueryConfig) Computer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &promScalar{
		pc:     newPromClient(cfg),
		logger: logger.With("computer", PromScalarName),
		cache:  map[string]ApplyValues{},
	}
}

type promScalarParams struct {
	promql string
	axis   outputAxis
	inMin  float64
	inMax  float64
	outMin float64
	outMax float64
	clamp  bool
}

// parsePromScalarParams validates the args and returns the parsed
// mapping. Errors here surface at Compute time so operator
// misconfigurations are caught on first eval rather than silently
// producing zero-axis output.
func parsePromScalarParams(args map[string]string) (promScalarParams, error) {
	p := promScalarParams{clamp: true}

	p.promql = strings.TrimSpace(args["query"])
	if p.promql == "" {
		return p, fmt.Errorf("args.query is required")
	}

	axis, err := parseOutputAxis(args["output_axis"])
	if err != nil {
		return p, err
	}
	p.axis = axis

	var ferr error
	if p.inMin, ferr = parseRequiredFloat(args, "in_min"); ferr != nil {
		return p, ferr
	}
	if p.inMax, ferr = parseRequiredFloat(args, "in_max"); ferr != nil {
		return p, ferr
	}
	if p.outMin, ferr = parseRequiredFloat(args, "out_min"); ferr != nil {
		return p, ferr
	}
	if p.outMax, ferr = parseRequiredFloat(args, "out_max"); ferr != nil {
		return p, ferr
	}
	if p.inMin == p.inMax {
		return p, fmt.Errorf("in_min == in_max (%g): mapping has zero input range", p.inMin)
	}

	if s := strings.ToLower(strings.TrimSpace(args["clamp"])); s != "" {
		switch s {
		case "true", "yes", "1":
			p.clamp = true
		case "false", "no", "0":
			p.clamp = false
		default:
			return p, fmt.Errorf("clamp %q: want true or false", s)
		}
	}
	return p, nil
}

// parseRequiredFloat reads a numeric arg, returning an explicit error
// when the arg is missing or unparseable. Strict by design — silent
// defaults to zero would produce surprising linear maps.
func parseRequiredFloat(args map[string]string, key string) (float64, error) {
	s := strings.TrimSpace(args[key])
	if s == "" {
		return 0, fmt.Errorf("args.%s is required", key)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("args.%s = %q: not a number: %w", key, s, err)
	}
	return f, nil
}

// interpolate applies the parsed linear map. The math is the textbook
// shape: shift by in_min, scale to out, shift by out_min. Clamping
// happens against the OUTPUT bounds because the operator declared
// those — extrapolation past out_min/out_max could yield negative
// brightness or sub-Kelvin temperatures that downstream apply layers
// would silently reject.
func (p promScalarParams) interpolate(in float64) float64 {
	t := (in - p.inMin) / (p.inMax - p.inMin)
	out := p.outMin + t*(p.outMax-p.outMin)
	if !p.clamp {
		return out
	}
	lo, hi := p.outMin, p.outMax
	if lo > hi {
		lo, hi = hi, lo
	}
	if out < lo {
		return lo
	}
	if out > hi {
		return hi
	}
	return out
}

// assignAxis writes the interpolated value into the right field of
// the ApplyValues tuple based on the configured output_axis.
func (p promScalarParams) assignAxis(v float64) ApplyValues {
	var out ApplyValues
	switch p.axis {
	case axisBrightnessValue:
		out.BrightnessValue = v
	case axisColorTemperatureKelvin:
		out.ColorTemperatureKelvin = int32(v)
	}
	return out
}

// parseOnErrorContinuous reads the optional fail-safe values for the
// continuous axes. Returns ok=true if at least one is set so the
// caller can route through the fail-safe path on fetch error
// (mirroring query's hasOnErrorArgs semantics).
func parseOnErrorContinuous(args map[string]string) (ApplyValues, bool, error) {
	var (
		out ApplyValues
		ok  bool
	)
	if s := strings.TrimSpace(args["on_error.brightness_value"]); s != "" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return out, false, fmt.Errorf("on_error.brightness_value %q: %w", s, err)
		}
		out.BrightnessValue = f
		ok = true
	}
	if s := strings.TrimSpace(args["on_error.color_temperature_kelvin"]); s != "" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return out, false, fmt.Errorf("on_error.color_temperature_kelvin %q: %w", s, err)
		}
		out.ColorTemperatureKelvin = int32(f)
		ok = true
	}
	return out, ok, nil
}

func (s *promScalar) Compute(ctx context.Context, now time.Time, _ Location, args map[string]string) (ApplyValues, error) {
	p, err := parsePromScalarParams(args)
	if err != nil {
		return ApplyValues{}, fmt.Errorf("prom_scalar: %w", err)
	}

	cacheKey := hashArgs(args)
	condLabel := args["_condition"]
	zoneLabel := args["_zone"]

	value, err := s.pc.Fetch(ctx, p.promql, now)
	if err != nil {
		failSafe, hasFailSafe, perr := parseOnErrorContinuous(args)
		if perr != nil {
			return ApplyValues{}, fmt.Errorf("prom_scalar: on_error parse: %w", perr)
		}
		if hasFailSafe {
			metricQueryFailSafe.WithLabelValues(condLabel, zoneLabel).Inc()
			s.logger.Debug("prom_scalar: fetch failed, returning declared on_error fail-safe",
				slog.String("promql", p.promql),
				slog.String("error", err.Error()),
			)
			return failSafe, nil
		}
		s.cacheMu.Lock()
		cached, ok := s.cache[cacheKey]
		s.cacheMu.Unlock()
		if ok {
			s.logger.Debug("prom_scalar: HTTP/parse failure, returning cached last-known-good",
				slog.String("promql", p.promql),
				slog.String("error", err.Error()),
			)
			return cached, nil
		}
		return ApplyValues{}, fmt.Errorf("prom_scalar: %w", err)
	}

	mapped := p.interpolate(value)
	vals := p.assignAxis(mapped)

	metricQueryValue.WithLabelValues(condLabel, zoneLabel).Set(value)

	s.cacheMu.Lock()
	s.cache[cacheKey] = vals
	s.cacheMu.Unlock()

	return vals, nil
}
