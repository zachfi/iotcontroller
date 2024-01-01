package router

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricQueueLength = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iotcontroller_router_queue_length",
		Help: "The number of jobs in the route queue",
	}, []string{})

	metricUnhandledRoute = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iotcontroller_router_unhandled_route",
		Help: "The total number of notice calls that include an unhandled object ID.",
	}, []string{"route"})
)
