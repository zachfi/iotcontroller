package conditioner

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// pump_test.go — Phase D prerequisite. Locks in the pond-pump safety
// invariants on BOTH today's alert-driven path and the future
// active_compute=query path the design plan will migrate to.
//
// The pump's two-direction invariant (memory: pond-pump shape):
//
//	water present (PromQL > 0.5)  → state=on  (pump runs, removes water)
//	water absent  (PromQL <= 0.5) → state=off (pump stops; running dry damages it)
//
// On the alert-driven path, the pondLeak alert maps to active_state=on
// and resolves to inactive_state=off (no hysteresis-preserve here:
// running the pump dry is worse than briefly cycling). On the
// query-driven path, the on_false bundle drives the off direction
// directly, with on_error.state=ZONE_STATE_OFF making Mimir outages
// fail safe.

// pumpAlertCondition is the production-shape alert Condition for the
// pump. Mirrors the heater pair but with no hysteresis preserve and
// a single Condition driving both directions via firing/resolved.
func pumpAlertCondition() apiv1.Condition {
	return apiv1.Condition{
		ObjectMeta: metav1Meta("pump-on-leak"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Matches: []apiv1.Match{{
				Labels: map[string]string{
					"alertname": "pondLeak",
					"zone":      "pond",
				},
			}},
			Remediations: []apiv1.Remediation{{
				Zone:          "pond-pump",
				ActiveState:   "on",
				InactiveState: "off", // resolved → off (NOT hysteresis-preserve)
			}},
		},
	}
}

func TestPump_LeakFiring_TurnsOn(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{conditions: []apiv1.Condition{pumpAlertCondition()}}
	c, err := New(Config{ApplyDesiredRefreshAge: time.Hour}, logger, rec, kube)
	require.NoError(t, err)

	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name: "pondLeak", Zone: "pond", Status: "firing",
	})
	require.NoError(t, err)

	require.Equal(t, 1, rec.setStateCount())
	name, state := rec.firstSetState()
	require.Equal(t, "pond-pump", name)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, state,
		"pondLeak firing must turn the pump ON")
}

func TestPump_LeakResolved_TurnsOff(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{conditions: []apiv1.Condition{pumpAlertCondition()}}
	c, err := New(Config{ApplyDesiredRefreshAge: time.Hour}, logger, rec, kube)
	require.NoError(t, err)

	// Fire then resolve.
	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name: "pondLeak", Zone: "pond", Status: "firing",
	})
	require.NoError(t, err)
	_, err = c.Alert(ctx, &iotv1proto.AlertRequest{
		Name: "pondLeak", Zone: "pond", Status: "resolved",
	})
	require.NoError(t, err)

	require.Equal(t, 2, rec.setStateCount(), "firing→ON then resolved→OFF must produce 2 SetStates")
	last := rec.setStateCalls[len(rec.setStateCalls)-1]
	require.Equal(t, "pond-pump", last.Name)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, last.State,
		"resolved must turn the pump OFF (running dry is dangerous; no hysteresis preserve)")
}

// pumpQueryServer is a minimal Prometheus-shape HTTP server used by
// the active_compute=query pump tests. waterPresent toggles between
// "PromQL returns 1" and "PromQL returns 0"; failNow flips the server
// to a 500 to test the on_error path.
type pumpQueryServer struct {
	waterPresent atomic.Bool
	failNow      atomic.Bool
	srv          *httptest.Server
}

func newPumpQueryServer(t *testing.T) *pumpQueryServer {
	t.Helper()
	pqs := &pumpQueryServer{}
	pqs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pqs.failNow.Load() {
			http.Error(w, "mimir down", http.StatusInternalServerError)
			return
		}
		val := "0"
		if pqs.waterPresent.Load() {
			val = "1"
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1000,"%s"]}]}}`, val)
	}))
	t.Cleanup(pqs.srv.Close)
	return pqs
}

// pumpQueryComputer constructs an isolated query Computer that hits
// the test's pumpQueryServer. Each test gets its own Computer to
// avoid the package-wide registry leaking cache state between tests.
func pumpQueryComputer(t *testing.T, endpoint string) computer.Computer {
	t.Helper()
	return computer.NewQuery(computer.QueryConfig{
		Endpoint: endpoint,
		Timeout:  2 * time.Second,
	})
}

// pumpQueryArgs returns the production-shape args for a pond-pump
// query Condition: the OR-of-two-sensors with 2-minute smoothing, and
// the safety-critical on_error.state=OFF declaration.
func pumpQueryArgs() map[string]string {
	return map[string]string{
		"query":          `max(avg_over_time(iot_zigbee2mqtt_water_leak{zone="pond"}[2m])) > 0.5`,
		"on_true.state":  "ZONE_STATE_ON",
		"on_false.state": "ZONE_STATE_OFF",
		// SAFETY-CRITICAL: declared fail-safe direction overrides the
		// query Computer's default cache fallback so a Mimir outage
		// cannot leave the pump running dry.
		"on_error.state": "ZONE_STATE_OFF",
	}
}

// TestPump_Query_WaterPresent_ReturnsOn — the active_compute=query
// path's on direction. PromQL > 0.5 returns 1 → on_true → ON.
func TestPump_Query_WaterPresent_ReturnsOn(t *testing.T) {
	pqs := newPumpQueryServer(t)
	pqs.waterPresent.Store(true)
	q := pumpQueryComputer(t, pqs.srv.URL)

	out, err := q.Compute(context.Background(), time.Unix(1000, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, out.State,
		"water present → pump runs")
}

// TestPump_Query_WaterAbsent_ReturnsOff — the off direction.
// Critical: this is what stops the pump when the leak is gone.
func TestPump_Query_WaterAbsent_ReturnsOff(t *testing.T) {
	pqs := newPumpQueryServer(t)
	pqs.waterPresent.Store(false)
	q := pumpQueryComputer(t, pqs.srv.URL)

	out, err := q.Compute(context.Background(), time.Unix(1000, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, out.State,
		"water absent → pump stops (no running dry)")
}

// TestPump_Query_FailSafeOnMimirOutage_AfterRunning — the canonical
// safety scenario: the pump was on (water was present), then Mimir
// goes down. Without on_error.state=OFF the cache fallback returns
// ON, and the pump runs dry. With it, the pump goes OFF on the next
// reconcile tick after the outage starts.
func TestPump_Query_FailSafeOnMimirOutage_AfterRunning(t *testing.T) {
	pqs := newPumpQueryServer(t)
	pqs.waterPresent.Store(true)
	q := pumpQueryComputer(t, pqs.srv.URL)

	// First call: water present, cache primes to ON.
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, out.State)

	// Mimir falls over. Cache says ON; fail-safe must override.
	pqs.failNow.Store(true)
	out, err = q.Compute(context.Background(), time.Unix(1060, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err, "fail-safe path must NOT error")
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, out.State,
		"Mimir down: pump MUST go OFF (fail-safe overrides cached ON to prevent running dry)")
}

// TestPump_Query_FailSafeOnFirstCall — the colder scenario: Mimir is
// down BEFORE the pump has ever computed a value. Without on_error
// the Computer would return an error and the eval loop wouldn't apply
// anything — leaving the pump's actual state to whatever was last
// imperatively set. With on_error.state=OFF, the first-time
// fail-safe applies OFF, which is the safe direction whether or not
// the pump was previously running.
func TestPump_Query_FailSafeOnFirstCall(t *testing.T) {
	pqs := newPumpQueryServer(t)
	pqs.failNow.Store(true) // Mimir already broken when conditioner starts
	q := pumpQueryComputer(t, pqs.srv.URL)

	out, err := q.Compute(context.Background(), time.Unix(1000, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, out.State,
		"first-call fail-safe: no cache yet, no fresh data; declared safe direction wins")
}

// TestPump_Query_OutageRecovery_ReturnsToFreshData — once Mimir
// recovers, subsequent ticks consult the live PromQL again. The
// on_error path is a transient safety override, not a permanent
// pin to OFF.
func TestPump_Query_OutageRecovery_ReturnsToFreshData(t *testing.T) {
	pqs := newPumpQueryServer(t)
	pqs.waterPresent.Store(true)
	q := pumpQueryComputer(t, pqs.srv.URL)

	// Healthy: ON.
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, out.State)

	// Outage: fail-safe OFF.
	pqs.failNow.Store(true)
	out, err = q.Compute(context.Background(), time.Unix(1060, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, out.State)

	// Recovery: pump goes back to live-data behavior.
	pqs.failNow.Store(false)
	out, err = q.Compute(context.Background(), time.Unix(1120, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, out.State,
		"after recovery the Computer reads fresh data again, not the fail-safe direction")
}

// TestPump_Query_EmptyVectorIsOff — the most common
// metric-missing shape. A PromQL whose result is an empty vector
// (e.g. sensor down, scrape target unhealthy) thresholds to 0 → on_false
// → OFF. This holds independently of on_error.* — empty-vector is a
// successful query that returned no data, not an error.
func TestPump_Query_EmptyVectorIsOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	}))
	t.Cleanup(srv.Close)

	q := pumpQueryComputer(t, srv.URL)
	out, err := q.Compute(context.Background(), time.Unix(1000, 0), computer.Location{}, pumpQueryArgs())
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, out.State,
		"empty vector (e.g. sensors offline) treats as no-water → pump OFF")
}
