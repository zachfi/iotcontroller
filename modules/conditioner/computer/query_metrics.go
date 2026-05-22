package computer

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metricQueryValue exposes the raw PromQL result the query Computer
// is seeing each tick. Inspired by the KEDA Prometheus scaler
// observability — the operator can see in Grafana exactly what the
// Computer is computing without re-running the PromQL by hand. The
// value reflects whatever the operator wrote in `args.query` — if
// the PromQL ends in `> 0.5`, the gauge shows 1 (true) or absent
// (false). If the operator wrote a raw aggregate, the gauge shows
// the aggregate value.
//
// Labels:
//
//	condition  — the Condition CR name the Remediation belongs to
//	zone       — the Remediation's target zone
//
// Set only on successful HTTP+parse; transient failures don't update
// the gauge so a stale value is visible during outages (paired with
// the IOTConditionerComputeErrors alert).
var metricQueryValue = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "iotcontroller_conditioner_query_value",
	Help: "Latest scalar result of the query Computer's PromQL expression, per (condition, zone). Reflects whatever shape the operator's args.query produces — boolean (1/absent) when the PromQL contains a comparator, or the raw aggregate value otherwise.",
}, []string{"condition", "zone"})

// metricQueryOutcome is a companion gauge that records the Computer's
// threshold decision as 1 (on_true fired) or 0 (on_false fired).
// Useful when args.query is a raw aggregate and the comparison is
// implicit in the Computer's "value > 0" check — the operator can
// read this gauge directly without knowing the threshold semantic.
var metricQueryOutcome = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "iotcontroller_conditioner_query_outcome",
	Help: "Whether the query Computer's threshold check returned on_true (1) or on_false (0) on the last successful evaluation, per (condition, zone).",
}, []string{"condition", "zone"})

// metricQueryFailSafe counts evaluations where the PromQL fetch
// errored AND the operator's Condition declared explicit on_error.*
// fail-safe args, so the Computer returned those values instead of
// falling back to the cache. Non-zero rate is the operator's signal
// that a safety-critical zone (pump, heater) is currently running on
// its declared fail-safe direction rather than fresh data.
//
// Pair this with iotcontroller_conditioner_evaluation_compute_error_total
// to distinguish "fetch is broken AND operator declared a safe
// fallback" (this metric) from "fetch is broken AND no fallback, eval
// loop counted an error" (the eval_compute_error counter).
var metricQueryFailSafe = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_conditioner_query_fail_safe_total",
	Help: "Number of PromQL fetch failures where the operator's declared on_error.* fail-safe ApplyValues was returned in place of the cached or errored result.",
}, []string{"condition", "zone"})
