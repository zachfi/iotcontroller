package iot

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metricFlushSuppressed counts per-device handler calls that the
// idempotent-flush cache skipped because the desired value already
// matches the last value we successfully sent (and the entry hasn't
// aged out of refreshAge). Compare against
// iotcontroller_zonekeeper_flush_total{result="success"} to see what
// fraction of flush volume is being deduped — should be the vast
// majority for the chatty zones (pond-pump, mainsuite, office) where
// SelfAnnounce drives many flushes per second but the desired state
// rarely changes.
//
// Labels:
//
//	zone   — the Zone.name
//	device — device.Name (the CR name / IEEE-prefixed for z2m)
//	kind   — which field was suppressed: state, brightness, color_temp, color
var metricFlushSuppressed = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_zone_flush_suppressed_total",
	Help: "Number of per-device handler calls suppressed by the idempotent-flush cache (desired == last-applied within refreshAge).",
}, []string{"zone", "device", "kind"})
