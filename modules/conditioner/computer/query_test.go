package computer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// promServer returns an httptest server that serves a configurable
// Prometheus /api/v1/query response. handler is invoked per request
// so the test can return different bodies or status codes per call.
func promServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(s.Close)
	return s
}

func newTestQuery(t *testing.T, endpoint string) Computer {
	t.Helper()
	return NewQuery(QueryConfig{
		Endpoint: endpoint,
		Timeout:  time.Second,
	})
}

func TestQuery_VectorAboveZero_PicksOnTrue(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"3.14"]}]}}`)
	})

	q := newTestQuery(t, srv.URL)
	args := map[string]string{
		"query":              "up",
		"on_true.state":      "ZONE_STATE_ON",
		"on_true.brightness": "BRIGHTNESS_FULL",
		"on_false.state":     "ZONE_STATE_OFF",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_ON {
		t.Errorf("state = %s; want ZONE_STATE_ON (on_true)", got.State)
	}
	if got.Brightness != iotv1proto.Brightness_BRIGHTNESS_FULL {
		t.Errorf("brightness = %s; want BRIGHTNESS_FULL", got.Brightness)
	}
}

func TestQuery_VectorZero_PicksOnFalse(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"0"]}]}}`)
	})
	q := newTestQuery(t, srv.URL)
	args := map[string]string{
		"query":          "up",
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_OFF {
		t.Errorf("state = %s; want ZONE_STATE_OFF (on_false)", got.State)
	}
}

func TestQuery_EmptyVector_TreatsAsZero(t *testing.T) {
	// No instances matched the selector — that's "false" in the
	// computer's threshold model. Operator can use on_false to drive
	// the "no instances up" zone state.
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})
	q := newTestQuery(t, srv.URL)
	args := map[string]string{
		"query":          "up{job=\"nope\"}",
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_OFF {
		t.Errorf("state = %s; want ZONE_STATE_OFF for empty vector", got.State)
	}
}

func TestQuery_ScalarResultType(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"scalar","result":[1000,"42"]}}`)
	})
	q := newTestQuery(t, srv.URL)
	args := map[string]string{
		"query":         "scalar(sum(up))",
		"on_true.state": "ZONE_STATE_ON",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_ON {
		t.Errorf("scalar 42 should pick on_true; got %s", got.State)
	}
}

func TestQuery_HTTPError_FirstCall_ReturnsError(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	q := newTestQuery(t, srv.URL)
	args := map[string]string{"query": "up", "on_true.state": "ZONE_STATE_ON"}

	// First call: no cache, error surfaces.
	_, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err == nil {
		t.Errorf("expected error on first-call HTTP 500 with empty cache; got nil")
	}
}

func TestQuery_HTTPError_AfterSuccess_ReturnsCached(t *testing.T) {
	// First call succeeds, second call fails: should return the
	// cached on_true ApplyValues with err==nil so the eval loop
	// applies it.
	var failNext bool
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		if failNext {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"1"]}]}}`)
	})
	q := newTestQuery(t, srv.URL)
	args := map[string]string{"query": "up", "on_true.state": "ZONE_STATE_ON", "on_false.state": "ZONE_STATE_OFF"}

	first, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("first Compute: %v", err)
	}
	if first.State != iotv1proto.ZoneState_ZONE_STATE_ON {
		t.Fatalf("first call should populate cache with on_true; got %s", first.State)
	}

	failNext = true
	second, err := q.Compute(context.Background(), time.Unix(2000, 0), Location{}, args)
	if err != nil {
		t.Errorf("second Compute (cached fallback): err = %v; want nil", err)
	}
	if second.State != iotv1proto.ZoneState_ZONE_STATE_ON {
		t.Errorf("second Compute (cached fallback): state = %s; want cached on_true ZONE_STATE_ON", second.State)
	}
}

func TestQuery_RespectsTenantAndAuth(t *testing.T) {
	var sawTenant, sawAuth string
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		sawTenant = r.Header.Get("X-Scope-OrgID")
		sawAuth = r.Header.Get("Authorization")
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"1"]}]}}`)
	})

	t.Setenv("TEST_BEARER", "swordfish")
	q := NewQuery(QueryConfig{
		Endpoint:        srv.URL,
		Tenant:          "ops",
		Timeout:         time.Second,
		AuthTokenEnvVar: "TEST_BEARER",
	})
	args := map[string]string{"query": "up"}
	if _, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args); err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if sawTenant != "ops" {
		t.Errorf("X-Scope-OrgID = %q; want ops", sawTenant)
	}
	if !strings.HasPrefix(sawAuth, "Bearer ") || !strings.HasSuffix(sawAuth, "swordfish") {
		t.Errorf("Authorization = %q; want Bearer swordfish", sawAuth)
	}
}

func TestQuery_MissingQueryArg(t *testing.T) {
	q := newTestQuery(t, "http://unused")
	_, err := q.Compute(context.Background(), time.Unix(0, 0), Location{}, map[string]string{})
	if err == nil {
		t.Errorf("expected error for missing query arg")
	}
}

func TestQuery_BadOnTrueBrightness(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"1"]}]}}`)
	})
	q := newTestQuery(t, srv.URL)
	_, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, map[string]string{
		"query":              "up",
		"on_true.brightness": "BRIGHTNESS_BOGUS",
	})
	if err == nil {
		t.Errorf("expected error for unknown brightness enum")
	}
}

// ───────────────────────────────────────────────────────────────────
// Safety invariant tests — Phase B prerequisite per
// .claude/plans/humble-foraging-pike.md
//
// The reconcile architecture migration must not regress two
// production invariants the query Computer powers today:
//
//   * "temp too low → turn on heater" / "temp not low → turn off"
//   * "water present → turn on pump" / "no water → turn off"
//
// These tests lock in the discrete-threshold semantics for both
// invariants in their actual production args shape. Failures here
// are blocking — heater + pump are safety-critical zones (heater
// keeps plants alive in winter; pump damaged if it runs dry).
// ───────────────────────────────────────────────────────────────────

// TestQuery_HeaterLowTemp_ReturnsOn locks in the heater's on-side
// invariant. A PromQL expression returning >0 (e.g. "1" because
// `temp < threshold` is true) must produce on_true → state=on.
// Today's production heater zones use an alert-driven path rather than
// active_compute=query, but if/when the migration ever wires the
// heater through query, this is the contract.
func TestQuery_HeaterLowTemp_ReturnsOn(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		// `temp_celsius < 5` returns 1 when temp is below threshold.
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"1"]}]}}`)
	})
	q := newTestQuery(t, srv.URL)

	args := map[string]string{
		"query":          `iot_zigbee2mqtt_temperature{zone="heated-zone"} < 5`,
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_ON {
		t.Errorf("heater cold: state = %s; want ZONE_STATE_ON (heater fires)", got.State)
	}
}

// TestQuery_HeaterHighTemp_ReturnsOff locks in the heater's off-side
// invariant. PromQL returning 0 (temp ≥ threshold) produces on_false
// → state=off. This is the hysteresis turn-off; without it the heater
// cycles forever.
func TestQuery_HeaterHighTemp_ReturnsOff(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		// `temp_celsius < 5` returns 0 (empty vector OR scalar 0) when
		// the inequality fails. Test the scalar=0 path; the empty-vector
		// path is covered by TestQuery_EmptyVector_TreatsAsZero.
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"0"]}]}}`)
	})
	q := newTestQuery(t, srv.URL)

	args := map[string]string{
		"query":          `iot_zigbee2mqtt_temperature{zone="heated-zone"} < 5`,
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_OFF {
		t.Errorf("heater warm: state = %s; want ZONE_STATE_OFF (heater stops)", got.State)
	}
}

// TestQuery_PumpWaterPresent_ReturnsOn locks in the pump's on-side
// invariant. A representative production PromQL for the pump:
//
//	max(avg_over_time(iot_zigbee2mqtt_water_leak{zone="pumped-zone"}[2m])) > 0.5
//
// Returns 1 when ANY sensor's 2-min smoothed signal exceeds 0.5
// (OR-of-two-sensors, redundancy against single-sensor 2.4GHz dropouts).
// This must produce on_true → state=on.
func TestQuery_PumpWaterPresent_ReturnsOn(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		// `max(...) > 0.5` returns 1 when water is detected.
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"1"]}]}}`)
	})
	q := newTestQuery(t, srv.URL)

	args := map[string]string{
		"query":          `max(avg_over_time(iot_zigbee2mqtt_water_leak{zone="pumped-zone"}[2m])) > 0.5`,
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_ON {
		t.Errorf("pump water-present: state = %s; want ZONE_STATE_ON (pump runs)", got.State)
	}
}

// TestQuery_PumpWaterAbsent_ReturnsOff locks in the pump's off-side
// invariant — the *safety-critical* direction. If the water signal
// drops below 0.5 (sensors no longer see water), the pump MUST go
// off; running dry damages the upgraded pump.
func TestQuery_PumpWaterAbsent_ReturnsOff(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		// `max(...) > 0.5` returns 0 when smoothed water signal is below
		// threshold.
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"0"]}]}}`)
	})
	q := newTestQuery(t, srv.URL)

	args := map[string]string{
		"query":          `max(avg_over_time(iot_zigbee2mqtt_water_leak{zone="pumped-zone"}[2m])) > 0.5`,
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_OFF {
		t.Errorf("pump water-absent: state = %s; want ZONE_STATE_OFF (pump stops to avoid running dry)", got.State)
	}
}

// TestQuery_EmptyVector_HeaterStopsAndPumpStops confirms that an
// empty PromQL result (the most common failure-adjacent shape — the
// query returned no series, e.g. metric is missing) fails to the
// off-side bundle. Both heater and pump treat "no data" as off, by
// the threshold-against-zero contract. For the pump this is the
// desired fail-safe (no water signal → pump off → can't run dry).
// For the heater this is debatable (plants might freeze if the
// metric is missing during a cold snap) — see the TODO test below
// for the architectural follow-up.
func TestQuery_EmptyVector_HeaterStopsAndPumpStops(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})
	q := newTestQuery(t, srv.URL)

	args := map[string]string{
		"query":          `nonexistent_metric > 0`,
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_OFF {
		t.Errorf("empty result: state = %s; want ZONE_STATE_OFF (both heater + pump fail to off-side)", got.State)
	}
}

// TestQuery_PumpFailSafeOnFetchError_OnErrorArgs locks in the pump's
// fail-safe direction: with `on_error.state = ZONE_STATE_OFF` declared,
// a PromQL fetch error returns OFF regardless of what the previous
// successful query produced. Without this, a cached "water present →
// on" result during a Mimir outage would keep the pump running while
// the water drains — running the pump dry damages it.
//
// Sequence:
//
//  1. First call against a healthy server returns "water present → on"
//     and caches state=on.
//  2. Server then 500s; the cached on is on the table.
//  3. Without on_error.state, the cache fallback would return on
//     (the dangerous direction, asserted by the sibling test below).
//     With on_error.state=OFF declared, the Computer returns OFF.
func TestQuery_PumpFailSafeOnFetchError_OnErrorArgs(t *testing.T) {
	var failNow atomic.Bool
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		if failNow.Load() {
			http.Error(w, "Mimir transiently down", http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"1"]}]}}`)
	})
	q := newTestQuery(t, srv.URL)

	args := map[string]string{
		"query":          `max(avg_over_time(iot_zigbee2mqtt_water_leak{zone="pumped-zone"}[2m])) > 0.5`,
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
		"on_error.state": "ZONE_STATE_OFF", // pump runs dry if it stays on without water signal
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("first Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_ON {
		t.Fatalf("first Compute (server healthy, water present): state = %s; want ON", got.State)
	}

	// Server flips to 500. Cached value is ON; declared fail-safe is OFF.
	// Fail-safe must win.
	failNow.Store(true)
	got, err = q.Compute(context.Background(), time.Unix(1060, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute under fetch error with on_error declared should not error: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_OFF {
		t.Errorf("Mimir down + on_error.state=OFF: state = %s; want ZONE_STATE_OFF (pump must NOT run dry on fetch failure)", got.State)
	}
}

// TestQuery_FailSafe_TakesPrecedenceOverCache is the architectural
// sibling: it asserts the resolution order explicitly. Without
// on_error.* the cache wins (existing lighting-zone behavior);
// with on_error.* declared, the fail-safe wins. Catches a future
// refactor that accidentally swaps the order.
func TestQuery_FailSafe_TakesPrecedenceOverCache(t *testing.T) {
	t.Run("no on_error declared: cache fallback returns the previously-applied value", func(t *testing.T) {
		var failNow atomic.Bool
		srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
			if failNow.Load() {
				http.Error(w, "outage", http.StatusInternalServerError)
				return
			}
			fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"1"]}]}}`)
		})
		q := newTestQuery(t, srv.URL)
		args := map[string]string{
			"query":         `up{zone="lighting-zone"}`,
			"on_true.state": "ZONE_STATE_ON",
			// No on_error.* — lighting-zone semantics
		}
		if _, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args); err != nil {
			t.Fatalf("warm cache: %v", err)
		}
		failNow.Store(true)
		got, err := q.Compute(context.Background(), time.Unix(1060, 0), Location{}, args)
		if err != nil {
			t.Fatalf("cached fallback should not error: %v", err)
		}
		if got.State != iotv1proto.ZoneState_ZONE_STATE_ON {
			t.Errorf("no on_error declared: cache should return ON; got %s", got.State)
		}
	})

	t.Run("on_error declared: fail-safe wins over cache", func(t *testing.T) {
		var failNow atomic.Bool
		srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
			if failNow.Load() {
				http.Error(w, "outage", http.StatusInternalServerError)
				return
			}
			fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"1"]}]}}`)
		})
		q := newTestQuery(t, srv.URL)
		args := map[string]string{
			"query":          `pump_should_run{}`,
			"on_true.state":  "ZONE_STATE_ON",
			"on_false.state": "ZONE_STATE_OFF",
			"on_error.state": "ZONE_STATE_OFF",
		}
		if _, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args); err != nil {
			t.Fatalf("warm cache: %v", err)
		}
		failNow.Store(true)
		got, err := q.Compute(context.Background(), time.Unix(1060, 0), Location{}, args)
		if err != nil {
			t.Fatalf("fail-safe path should not error: %v", err)
		}
		if got.State != iotv1proto.ZoneState_ZONE_STATE_OFF {
			t.Errorf("on_error declared: fail-safe should return OFF (overriding cached ON); got %s", got.State)
		}
	})

	t.Run("on_error declared and no cache: fail-safe wins over error", func(t *testing.T) {
		srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "always down", http.StatusInternalServerError)
		})
		q := newTestQuery(t, srv.URL)
		args := map[string]string{
			"query":          `pump_should_run{}`,
			"on_true.state":  "ZONE_STATE_ON",
			"on_false.state": "ZONE_STATE_OFF",
			"on_error.state": "ZONE_STATE_OFF",
		}
		got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
		if err != nil {
			t.Fatalf("first-call fail-safe should not error: %v", err)
		}
		if got.State != iotv1proto.ZoneState_ZONE_STATE_OFF {
			t.Errorf("first-call fail-safe: state = %s; want OFF", got.State)
		}
	})

	t.Run("malformed on_error.state surfaces parse error", func(t *testing.T) {
		srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "down", http.StatusInternalServerError)
		})
		q := newTestQuery(t, srv.URL)
		args := map[string]string{
			"query":          `whatever{}`,
			"on_error.state": "NOT_A_REAL_ZONE_STATE",
		}
		_, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
		if err == nil {
			t.Fatalf("expected parse error for malformed on_error.state")
		}
	})
}

// TestQuery_FailSafeCovers_AllAxes confirms the fail-safe path
// supports all four ApplyValues axes (state, brightness,
// color_temperature, color) not just state. Useful for "outage →
// turn the office lamp red as a warning" patterns where the operator
// wants a multi-axis fail-safe.
func TestQuery_FailSafeCovers_AllAxes(t *testing.T) {
	srv := promServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	})
	q := newTestQuery(t, srv.URL)
	args := map[string]string{
		"query":                      `whatever{}`,
		"on_error.state":             "ZONE_STATE_ON",
		"on_error.brightness":        "BRIGHTNESS_FULL",
		"on_error.color_temperature": "COLOR_TEMPERATURE_DAY",
		"on_error.color":             "#FF0000",
	}
	got, err := q.Compute(context.Background(), time.Unix(1000, 0), Location{}, args)
	if err != nil {
		t.Fatalf("fail-safe Compute: %v", err)
	}
	if got.State != iotv1proto.ZoneState_ZONE_STATE_ON {
		t.Errorf("state: got %s, want ON", got.State)
	}
	if got.Brightness != iotv1proto.Brightness_BRIGHTNESS_FULL {
		t.Errorf("brightness: got %s, want FULL", got.Brightness)
	}
	if got.ColorTemperature != iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY {
		t.Errorf("color_temperature: got %s, want DAY", got.ColorTemperature)
	}
	if got.Color != "#FF0000" {
		t.Errorf("color: got %q, want #FF0000", got.Color)
	}
}

// TestQuery_HasOnErrorArgs_Detection asserts the helper's edge cases.
// Empty values must NOT count as "declared" — operators templating in
// scaffolding shouldn't accidentally route through fail-safe with a
// zero-value ApplyValues.
func TestQuery_HasOnErrorArgs_Detection(t *testing.T) {
	cases := []struct {
		name string
		args map[string]string
		want bool
	}{
		{"none set", map[string]string{"query": "x"}, false},
		{"only empty string", map[string]string{"on_error.state": "  "}, false},
		{"state declared", map[string]string{"on_error.state": "ZONE_STATE_OFF"}, true},
		{"brightness only", map[string]string{"on_error.brightness": "BRIGHTNESS_OFF"}, true},
		{"color only", map[string]string{"on_error.color": "#000000"}, true},
		{"ct only", map[string]string{"on_error.color_temperature": "COLOR_TEMPERATURE_FIRST_LIGHT"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasOnErrorArgs(tc.args); got != tc.want {
				t.Errorf("hasOnErrorArgs(%v) = %v; want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestQuery_HashArgsStableAcrossMapIterations(t *testing.T) {
	// Map iteration order varies between Compute() calls but the
	// cache key must not. Compute the hash a few times for identical
	// content and compare.
	args := map[string]string{
		"query":          "up",
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
	}
	want := hashArgs(args)
	for range 20 {
		got := hashArgs(args)
		if got != want {
			t.Errorf("hashArgs not stable across iterations: %q vs %q", got, want)
		}
	}
}
