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

// metricEvalTotal counts evaluator ticks. With cfg.EvaluationInterval at
// 60s steady state this should be ~1/min. Compare against
// rate(...{direction="time-gated"}[5m]) to see what fraction of ticks
// produce work; a high ratio of gated to applied means most computer-
// driven Remediations are sleeping outside their window.
var metricEvalTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "iotcontroller_conditioner_evaluation_total",
	Help: "Number of times the conditioner evaluator has ticked.",
})

// metricEvalDuration records the wall-clock time for one full evaluator
// pass (List + walk + apply). Should stay well under 1s — if it climbs
// the eval loop is competing with itself.
var metricEvalDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "iotcontroller_conditioner_evaluation_duration_seconds",
	Help:    "Wall-clock time for one evaluator pass.",
	Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
})

// metricEvalComputeUnknown counts evaluations that resolved to a
// computer name not in the registry. Should be flat at zero; non-zero
// means an operator authored a Condition referencing a misspelled
// computer name (or one that hasn't been compiled in yet).
var metricEvalComputeUnknown = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_conditioner_evaluation_compute_unknown_total",
	Help: "Number of Remediations whose active_compute name doesn't match any registered Computer.",
}, []string{"compute"})

// metricEvalComputeError counts errors returned from Computer.Compute.
// Per-computer label so an outage of one (e.g. PromQL endpoint for the
// query computer once it lands) doesn't get lost in aggregate.
var metricEvalComputeError = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_conditioner_evaluation_compute_error_total",
	Help: "Number of errors returned from Computer.Compute, by computer name.",
}, []string{"compute"})

// metricEvalApplyError counts ApplyValues RPC failures during eval. Per-
// computer label points the operator at the source.
var metricEvalApplyError = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_conditioner_evaluation_apply_error_total",
	Help: "Number of ApplyValues RPC failures during evaluator ticks, by computer name.",
}, []string{"compute"})

// metricEvalComputeApplied counts successful active_compute applies — a
// Computer's Compute returned without error AND the resulting
// ApplyValues was accepted by ZoneKeeper. Counterpart to the three
// failure metrics above (compute_unknown / compute_error / apply_error)
// so an operator can observe "this Condition is alive and firing" from
// metrics alone, without resorting to grepping logs or reading the Zone
// Status.
//
// Per-(condition, zone, compute) label: the condition label is the
// one most useful for canary deploys (did the new fade/circadian
// Condition I just added actually fire?); zone scopes blast radius
// when the same Computer drives multiple zones; compute lets the
// operator drill into "how busy is fade vs sun_color_temperature vs
// circadian" in aggregate.
//
// Steady state on a healthy deployment should be roughly
// rate(iotcontroller_conditioner_evaluation_total) × <count of
// active_compute Remediations inside their TimeIntervals window>.
var metricEvalComputeApplied = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_conditioner_evaluation_compute_applied_total",
	Help: "Number of successful active_compute applies (Compute returned no error AND ApplyValues RPC succeeded), labeled by condition, zone, and computer name.",
}, []string{"condition", "zone", "compute"})

// metricApplyCacheInvalidated counts applyDesired cache entries dropped
// because the Zone CR's Status disagreed with what the cache thought
// was the last-applied value — i.e. somebody other than this
// conditioner moved the zone (button press, alert from a second
// origin, direct SetState). The fade Computer's "key-up while
// attacking" semantics rely on this: a mid-envelope button press
// blows the snapshot so the next tick re-seeds from the new current
// instead of overwriting the operator's intent.
//
// Steady-state should be near zero. Per-zone label scopes the noise
// floor — a single chatty zone shouldn't be drowned out by the rest.
var metricApplyCacheInvalidated = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_conditioner_apply_cache_invalidations_total",
	Help: "Number of applyDesired cache entries invalidated due to out-of-band Zone state changes.",
}, []string{"zone", "reason"})
