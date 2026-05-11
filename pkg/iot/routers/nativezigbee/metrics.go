package nativezigbee

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metricFallbackTotal counts events that flowed through the legacy
// ActionHandler path because no Binding matched. Watch this drop to zero
// per device as Bindings are migrated; once flat across all devices, the
// legacy switch in zonekeeper.ActionHandler can be removed. The device
// label is what makes this a per-button migration thermometer.
var metricFallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name:      "router_action_fallback_total",
	Namespace: "iotcontroller_nativezigbee",
	Help:      "Number of action events handled via the legacy ActionHandler fallback (no matching Binding).",
}, []string{"device", "action", "zone"})
