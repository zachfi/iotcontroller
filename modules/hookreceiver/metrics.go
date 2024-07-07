package hookreceiver

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var hookreceiverReceivedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_hookreceiver_alerts_received_total",
	Help: "The total number of alerts received by the hookreceiver",
}, []string{"name", "zone"})

var hookreceiverReceiverErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "iotcontroller_hookreceiver_errors_total",
	Help: "The total number of errors in the receiver",
}, []string{"name", "zone"})
