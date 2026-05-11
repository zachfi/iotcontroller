package conditioner

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metricApplySuppressed counts activate/deactivate calls that were
// suppressed by applyDesired because the desired (state, scene) for the
// (condition, zone) pair already matches what we last successfully sent.
//
// High rate on this metric is the migration thermometer for the
// conditioner-side dedup work: it represents Alert RPC re-firings,
// runAlertWindowCheck repeat deactivations, and direct ActivateCondition
// re-presses that no longer reach the ZoneKeeper because they were
// already in the desired state.
//
// Compared against
// rate(iotcontroller_hookreceiver_alerts_total{status="success"}[5m])
// this tells you what fraction of inbound alerts produced a real state
// change vs were collapsed to no-ops.
var metricApplySuppressed = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_conditioner_apply_suppressed_total",
	Help: "Number of conditioner activate/deactivate calls suppressed because (condition, zone) desired state/scene matches the last applied value.",
}, []string{"condition", "zone", "direction"})
