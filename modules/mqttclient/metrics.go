package mqttclient

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsNamespace = "iotcontroller"
	metricsSubsystem = "mqttclient"

	metricMQTTClientReplaced = promauto.NewCounter(prometheus.CounterOpts{
		Name:      "replacement",
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Help:      "The number of times the MQTT client has been replaced",
	})
	metricMQTTClientReplacementError = promauto.NewCounter(prometheus.CounterOpts{
		Name:      "replacement_errors",
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Help:      "The number of times the MQTT failed to replace the existing connection",
	})
)
