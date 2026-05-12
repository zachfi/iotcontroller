package harvester

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	harvesterMessageTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_message_total",
		Help: "The the total number of messages seen by the harvester",
	}, []string{"topic"})
	// harvesterMessageErrors was unreferenced; kept here as a placeholder
	// for the per-message-decode-error counter we never wired. Intentionally
	// not registered — promauto would panic on duplicate names if we leave
	// the literal in place. Re-add when the decode path produces errors
	// the operator should see.
	harvesterRouteSendErrorTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_route_send_error_total",
		Help: "The the total number of messages failed to route",
	}, []string{})
	harvesterRouteSendTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_route_send_total",
		Help: "The the total number of messages routed",
	}, []string{})

	// harvesterQueueDepth is the live in-channel depth of itemCh. The
	// channel is buffered to 1000; sustained non-zero depth means the
	// fan-out workers can't drain as fast as MQTT delivers. With concurrency
	// 16 and 7 msg/s steady-state this should sit at 0 except during the
	// cold-start window or downstream-stall events.
	harvesterQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "iot_harvester_queue_depth",
		Help: "Current depth of the harvester item channel (pre-Send queue).",
	})

	// harvesterActiveWorkers tracks how many fan-out goroutines are
	// currently blocked inside routeClient.Send. Tail this against
	// cfg.Concurrency: at-saturation gauge values mean we're at the
	// configured cap and items are queueing.
	harvesterActiveWorkers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "iot_harvester_active_workers",
		Help: "Number of harvester fan-out goroutines currently inside a RouteService/Send call.",
	})
)
