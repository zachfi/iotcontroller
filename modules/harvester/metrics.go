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
	harvesterMessageErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_message_error",
		Help: "The the total number of messages failed to process by the harvester",
	}, []string{})
	harvesterRouteSendErrorTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_route_send_error_total",
		Help: "The the total number of messages failed to route",
	}, []string{})
	harvesterRouteSendTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_route_send_total",
		Help: "The the total number of messages routed",
	}, []string{})
)
