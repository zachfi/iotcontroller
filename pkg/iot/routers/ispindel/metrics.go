package ispindel

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsNamespace = "iot_ispindel"

	metricTiltAngle = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "tilt_angle",
		Namespace: metricsNamespace,
		Help:      "tilt angle",
	}, []string{"device", "zone"})

	metricSpecificGravity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "specific_gravity",
		Namespace: metricsNamespace,
		Help:      "Specific Gravity",
	}, []string{"device", "zone"})

	metricBattery = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "battery",
		Namespace: metricsNamespace,
		Help:      "temperature",
	}, []string{"device", "zone"})

	metricRSSI = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rssi",
		Namespace: metricsNamespace,
		Help:      "Device wireless RSSI",
	}, []string{"device", "zone"})

	metricTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "temperature",
		Namespace: metricsNamespace,
		Help:      "Sensor Temperature(C)",
	}, []string{"device", "zone"})
)
