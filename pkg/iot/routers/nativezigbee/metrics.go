package nativezigbee

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metricFallbackTotal counts action events from zoned devices that no
// Binding matched. With the ActionHandler switch retired this is an
// observability-only signal — recorded for visibility but otherwise no
// behaviour follows. A sustained rate on an unexpected (device, action)
// pair indicates a missing Binding the operator probably wants to author.
var metricFallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name:      "router_action_fallback_total",
	Namespace: "iotcontroller_nativezigbee",
	Help:      "Number of action events from zoned devices that matched no Binding. Observability-only since ActionHandler retirement.",
}, []string{"device", "action", "zone"})
