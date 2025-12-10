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

	metricMessagesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "router_messages_received",
		Namespace: metricsNamespace,
		Help:      "The number of messages received by the router",
	}, []string{})

	metricMessagesSendErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "router_message_send_errors",
		Namespace: metricsNamespace,
		Help:      "The number of messages which failed to process",
	}, []string{})

	metricActiveReceiverRoutines = promauto.NewGauge(prometheus.GaugeOpts{
		Name:      "router_active_receiver_routines",
		Namespace: metricsNamespace,
		Help:      "The number of messages received by the router",
	})
)
