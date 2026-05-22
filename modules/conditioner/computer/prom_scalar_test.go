package computer

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// prom_scalar_test.go — coverage for the continuous PromQL Computer.
// The query-shape tests in query_test.go already cover the underlying
// promClient (auth header, tenant, empty vector, etc.) so this file
// focuses on:
//
//   - The linear interpolation math (anchors, midpoint, clamping,
//     inverted ranges).
//   - Axis routing — brightness vs color_temperature populate only
//     the corresponding ApplyValues field.
//   - Arg validation — required fields, parse failures.
//   - The on_error.* fail-safe path for continuous axes.

// scalarServer returns an httptest server that replies with a single
// scalar PromQL result wrapping `value`. handler hook lets tests
// override the response for failure-path coverage.
func scalarServer(t *testing.T, value string) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	fail := &atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"%s"]}]}}`, value)
	}))
	t.Cleanup(srv.Close)
	return srv, fail
}

func newTestPromScalar(t *testing.T, endpoint string) Computer {
	t.Helper()
	return NewPromScalar(QueryConfig{
		Endpoint: endpoint,
		Timeout:  2 * time.Second,
	})
}

// TestPromScalar_BrightnessAxis_LinearMap is the canonical cloud-coverage
// → brightness mapping from the design doc. cloud_coverage_percent
// of 50 should map to the midpoint of [out_min, out_max].
func TestPromScalar_BrightnessAxis_LinearMap(t *testing.T) {
	srv, _ := scalarServer(t, "50")
	q := newTestPromScalar(t, srv.URL)

	args := map[string]string{
		"query":       "avg_over_time(cloud_coverage_percent[10m])",
		"output_axis": "brightness",
		"in_min":      "0",
		"in_max":      "100",
		"out_min":     "0.5",
		"out_max":     "0.9",
	}
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	require.NoError(t, err)

	// (50 - 0) / (100 - 0) = 0.5  ;  0.5 + 0.5 * (0.9 - 0.5) = 0.7
	require.InDelta(t, 0.7, out.BrightnessValue, 1e-9,
		"50%% cloud-coverage at midpoint maps to midpoint brightness")
	// Other axes must stay at zero — the linear map drives ONE axis.
	require.Zero(t, out.ColorTemperatureKelvin)
	require.Equal(t, "", out.Color)
}

// TestPromScalar_BrightnessAxis_BoundaryAnchors locks the endpoints.
// in == in_min → out_min ; in == in_max → out_max.
func TestPromScalar_BrightnessAxis_BoundaryAnchors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		promVal string
		want    float64
	}{
		{"at in_min", "0", 0.5},
		{"at in_max", "100", 0.9},
		{"quarter", "25", 0.6},
		{"three-quarter", "75", 0.8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := scalarServer(t, tc.promVal)
			q := newTestPromScalar(t, srv.URL)
			args := map[string]string{
				"query": "anything", "output_axis": "brightness",
				"in_min": "0", "in_max": "100",
				"out_min": "0.5", "out_max": "0.9",
			}
			out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
			require.NoError(t, err)
			require.InDelta(t, tc.want, out.BrightnessValue, 1e-9)
		})
	}
}

// TestPromScalar_Clamping verifies the default clamp=true behavior.
// Input below in_min clamps to out_min; above in_max clamps to out_max.
// Operators get bounded output regardless of metric outliers.
func TestPromScalar_Clamping(t *testing.T) {
	cases := []struct {
		name    string
		promVal string
		want    float64
	}{
		{"below in_min clamps to out_min", "-25", 0.5},
		{"above in_max clamps to out_max", "200", 0.9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := scalarServer(t, tc.promVal)
			q := newTestPromScalar(t, srv.URL)
			args := map[string]string{
				"query": "x", "output_axis": "brightness",
				"in_min": "0", "in_max": "100",
				"out_min": "0.5", "out_max": "0.9",
			}
			out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
			require.NoError(t, err)
			require.InDelta(t, tc.want, out.BrightnessValue, 1e-9)
		})
	}
}

// TestPromScalar_ClampDisabled_Extrapolates confirms the opt-out
// path. clamp=false lets the linear map extrapolate past the
// bounds, which is rarely what operators want — but the contract
// is documented and testable.
func TestPromScalar_ClampDisabled_Extrapolates(t *testing.T) {
	srv, _ := scalarServer(t, "200")
	q := newTestPromScalar(t, srv.URL)
	args := map[string]string{
		"query": "x", "output_axis": "brightness",
		"in_min": "0", "in_max": "100",
		"out_min": "0.5", "out_max": "0.9",
		"clamp": "false",
	}
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	require.NoError(t, err)
	// (200 - 0) / 100 * 0.4 + 0.5 = 1.3
	require.InDelta(t, 1.3, out.BrightnessValue, 1e-9)
}

// TestPromScalar_InvertedOutputRange — clamp range works correctly
// when out_min > out_max (operator wants "more cloud → DIMMER" for
// some aesthetic). The clamp still pins to [min(out_min,out_max),
// max(out_min,out_max)].
func TestPromScalar_InvertedOutputRange(t *testing.T) {
	srv, _ := scalarServer(t, "0")
	q := newTestPromScalar(t, srv.URL)
	args := map[string]string{
		"query": "x", "output_axis": "brightness",
		"in_min": "0", "in_max": "100",
		"out_min": "0.9", "out_max": "0.5", // inverted
	}
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	require.NoError(t, err)
	require.InDelta(t, 0.9, out.BrightnessValue, 1e-9,
		"inverted range: in_min still anchors to out_min (which is the larger value here)")
}

// TestPromScalar_ColorTemperatureAxis routes the linear map onto the
// continuous Kelvin field. Solar irradiance 0-1000 → 2200K-5000K is
// a representative use case.
func TestPromScalar_ColorTemperatureAxis(t *testing.T) {
	srv, _ := scalarServer(t, "500")
	q := newTestPromScalar(t, srv.URL)
	args := map[string]string{
		"query":       "solar_irradiance_w_per_m2",
		"output_axis": "color_temperature",
		"in_min":      "0",
		"in_max":      "1000",
		"out_min":     "2200",
		"out_max":     "5000",
	}
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	require.NoError(t, err)
	// 500/1000 * 2800 + 2200 = 3600
	require.Equal(t, int32(3600), out.ColorTemperatureKelvin)
	require.Zero(t, out.BrightnessValue, "brightness axis must stay untouched")
}

// TestPromScalar_AxisAcceptedAliases — operators may write either
// "brightness" or "brightness_value", either "color_temperature" or
// "color_temperature_kelvin". Documented friendliness for the
// continuous-vs-enum field naming.
func TestPromScalar_AxisAcceptedAliases(t *testing.T) {
	for _, name := range []string{"brightness", "brightness_value", "BRIGHTNESS"} {
		t.Run(name, func(t *testing.T) {
			srv, _ := scalarServer(t, "50")
			q := newTestPromScalar(t, srv.URL)
			out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, map[string]string{
				"query": "x", "output_axis": name,
				"in_min": "0", "in_max": "100", "out_min": "0", "out_max": "1",
			})
			require.NoError(t, err)
			require.InDelta(t, 0.5, out.BrightnessValue, 1e-9)
		})
	}
	for _, name := range []string{"color_temperature", "color_temperature_kelvin", "Color_Temperature"} {
		t.Run(name, func(t *testing.T) {
			srv, _ := scalarServer(t, "0.5")
			q := newTestPromScalar(t, srv.URL)
			out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, map[string]string{
				"query": "x", "output_axis": name,
				"in_min": "0", "in_max": "1", "out_min": "2000", "out_max": "6500",
			})
			require.NoError(t, err)
			require.Equal(t, int32(4250), out.ColorTemperatureKelvin)
		})
	}
}

// TestPromScalar_RequiredArgs — every required arg surfaces a parse
// error when omitted. Operators should learn about misconfigurations
// at first eval, not via silent zero-valued ApplyValues.
func TestPromScalar_RequiredArgs(t *testing.T) {
	srv, _ := scalarServer(t, "1")
	q := newTestPromScalar(t, srv.URL)
	base := map[string]string{
		"query": "x", "output_axis": "brightness",
		"in_min": "0", "in_max": "100",
		"out_min": "0", "out_max": "1",
	}
	for _, key := range []string{"query", "output_axis", "in_min", "in_max", "out_min", "out_max"} {
		t.Run("missing "+key, func(t *testing.T) {
			args := make(map[string]string, len(base))
			for k, v := range base {
				if k == key {
					continue
				}
				args[k] = v
			}
			_, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
			require.Error(t, err, "missing %s must error", key)
		})
	}
}

// TestPromScalar_ZeroInputRange catches the divide-by-zero shape.
// in_min == in_max produces a 0/0 map — surface this as a config
// error rather than NaN-propagate into the apply layer.
func TestPromScalar_ZeroInputRange(t *testing.T) {
	srv, _ := scalarServer(t, "1")
	q := newTestPromScalar(t, srv.URL)
	args := map[string]string{
		"query": "x", "output_axis": "brightness",
		"in_min": "50", "in_max": "50", // zero range
		"out_min": "0", "out_max": "1",
	}
	_, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	require.Error(t, err)
	require.Contains(t, err.Error(), "in_min == in_max")
}

// TestPromScalar_FailSafe_OnFetchError_DeclaredWins — same precedence
// contract as query.go's on_error.*: declared fail-safe overrides
// the cache fallback when the fetch errors.
func TestPromScalar_FailSafe_OnFetchError_DeclaredWins(t *testing.T) {
	srv, failNow := scalarServer(t, "50")
	q := newTestPromScalar(t, srv.URL)
	args := map[string]string{
		"query": "x", "output_axis": "brightness",
		"in_min": "0", "in_max": "100",
		"out_min": "0.5", "out_max": "0.9",
		"on_error.brightness_value": "0.1", // dim to a safe minimum on outage
	}
	// First call primes the cache to 0.7 (midpoint).
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	require.NoError(t, err)
	require.InDelta(t, 0.7, out.BrightnessValue, 1e-9)

	// Mimir flips to 500. Cache is 0.7; fail-safe is 0.1.
	failNow.Store(true)
	out, err = q.Compute(context.Background(), time.Unix(1060, 0), Location{}, args)
	require.NoError(t, err, "fail-safe path must not error")
	require.InDelta(t, 0.1, out.BrightnessValue, 1e-9,
		"declared on_error.brightness_value (0.1) must override cached value (0.7)")
}

// TestPromScalar_FailSafe_CacheFallback — without on_error declared,
// fetch error falls back to the cached previous value. Inherited from
// the underlying promClient + Compute path.
func TestPromScalar_FailSafe_CacheFallback(t *testing.T) {
	srv, failNow := scalarServer(t, "100")
	q := newTestPromScalar(t, srv.URL)
	args := map[string]string{
		"query": "x", "output_axis": "brightness",
		"in_min": "0", "in_max": "100",
		"out_min": "0.5", "out_max": "0.9",
	}
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	require.NoError(t, err)
	require.InDelta(t, 0.9, out.BrightnessValue, 1e-9)

	failNow.Store(true)
	out, err = q.Compute(context.Background(), time.Unix(1060, 0), Location{}, args)
	require.NoError(t, err)
	require.InDelta(t, 0.9, out.BrightnessValue, 1e-9,
		"no on_error: cache fallback returns the prior 0.9")
}

// TestPromScalar_FailSafe_FirstCallError — no cache, no fail-safe: the
// error surfaces up.
func TestPromScalar_FailSafe_FirstCallError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	q := newTestPromScalar(t, srv.URL)
	args := map[string]string{
		"query": "x", "output_axis": "brightness",
		"in_min": "0", "in_max": "100",
		"out_min": "0", "out_max": "1",
	}
	_, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	require.Error(t, err)
}

// TestPromScalar_Interpolate_Math is a pure-function test for the
// linear map. Independent of the HTTP plumbing — catches a refactor
// that breaks the math.
func TestPromScalar_Interpolate_Math(t *testing.T) {
	p := promScalarParams{
		inMin: 0, inMax: 100, outMin: 0.5, outMax: 0.9, clamp: true,
	}
	cases := []struct {
		in   float64
		want float64
	}{
		{0, 0.5},
		{100, 0.9},
		{50, 0.7},
		{-50, 0.5}, // clamped
		{200, 0.9}, // clamped
	}
	for _, tc := range cases {
		if got := p.interpolate(tc.in); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("interpolate(%g) = %g; want %g", tc.in, got, tc.want)
		}
	}
}
