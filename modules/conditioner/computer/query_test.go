package computer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
