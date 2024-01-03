package router

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsNamespace = "iotcontroller"

	metricQueueLength = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "router_queue_length",
		Namespace: metricsNamespace,
		Help:      "The number of jobs in the route queue",
	}, []string{})

	metricUnhandledRoute = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "router_unhandled_route",
		Namespace: metricsNamespace,
		Help:      "The total number of notice calls that include an unhandled object ID.",
	}, []string{"route"})
)
