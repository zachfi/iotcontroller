package bindings

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metricBindingDebounced counts Binding match outcomes, labeled by
// outcome. Despite the historical name (`_debounce_events_total`) the
// counter covers both the slow path (Bindings with min_duration > 0
// — the debounce engine) AND the fast path (Bindings with no
// min_duration — fires immediately on match). The outcome values
// distinguish what happened:
//
//	start       new debounce timer started (fresh entry or value change)
//	pending     event matched but dwell threshold not yet met
//	fired       Condition was dispatched — either dwell satisfied
//	             (slow path) or first-and-only match (fast path)
//	suppressed  matched after fire; suppressed to keep one dispatch per
//	             stable-value-window (slow path only)
//
// Useful for diagnosing a flapping sensor (high rate of start +
// suppressed pairs without fired), confirming the debounce engaged
// when expected, or just answering "did Binding X fire today?"
// uniformly without knowing whether it has a dwell configured.
var metricBindingDebounced = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_bindings_debounce_events_total",
	Help: "Binding match outcomes by Binding name. Covers both debounced (slow-path) and immediate (fast-path) bindings. Outcome: start/pending/fired/suppressed.",
}, []string{"binding", "outcome"})

// metricBindingDispatch counts matcher dispatch fan-out by shape. The
// `dispatched` label is the number of Conditions the matcher returned
// for a single event ("0", "1", "many"). Pre-fan-out the matcher
// always returned at most one; the "many" bucket grows post-fix when
// operator-authored sibling bindings (e.g. motion-evening +
// motion-nightvision on one sensor) co-dispatch and Condition-side
// time-windows pick the right one.
var metricBindingDispatch = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_bindings_dispatch_total",
	Help: "Matcher dispatch outcomes. dispatched=number of Conditions returned for an event (bucketed: 0, 1, many).",
}, []string{"dispatched"})
