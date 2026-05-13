package bindings

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metricBindingDebounced counts MinDuration-debounce events per Binding,
// labeled by outcome:
//
//	start       new debounce timer started (fresh entry or value change)
//	pending     event matched but dwell threshold not yet met
//	fired       dwell satisfied; ActivateCondition dispatched
//	suppressed  matched after fire; suppressed to keep one dispatch per
//	             stable-value-window
//
// Useful for diagnosing a flapping sensor (high rate of start +
// suppressed pairs without fired) or confirming the debounce engaged
// when expected.
var metricBindingDebounced = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_bindings_debounce_events_total",
	Help: "MinDuration debounce activity by Binding, labeled by outcome (start/pending/fired/suppressed).",
}, []string{"binding", "outcome"})
