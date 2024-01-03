package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsNamespace = "telemetry"

	telemetryIOTUnhandledReport = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "rpc_telemetry_unhandled_object_report",
		Namespace: metricsNamespace,
		Help:      "The total number of notice calls that include an unhandled object ID.",
	}, []string{"object_id", "component"})

	telemetryIOTReport = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "rpc_telemetry_object_report",
		Namespace: metricsNamespace,
		Help:      "The total number of notice calls for an object ID.",
	}, []string{"object_id", "component"})

	telemetryIOTBatteryPercent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_battery_percent",
		Namespace: metricsNamespace,
		Help:      "The reported batter percentage remaining.",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTLinkQuality = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_link_quality",
		Namespace: metricsNamespace,
		Help:      "The reported link quality",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTBridgeState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_bridge_state",
		Namespace: metricsNamespace,
		Help:      "The reported bridge state",
	}, []string{})

	telemetryIOTOccupancy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_occupancy",
		Namespace: metricsNamespace,
		Help:      "Occupancy binary",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTWaterLeak = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_water_leak",
		Namespace: metricsNamespace,
		Help:      "Water leak binary",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_temperature",
		Namespace: metricsNamespace,
		Help:      "Sensor Temperature(C)",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTHumidity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_humidity",
		Namespace: metricsNamespace,
		Help:      "Sensor Humidity",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTFormaldehyde = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_formaldehyde",
		Namespace: metricsNamespace,
		Help:      "Sensor Formaldehyde",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTCo2 = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_co2",
		Namespace: metricsNamespace,
		Help:      "Sensor Humidity",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTVoc = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_voc",
		Namespace: metricsNamespace,
		Help:      "Sensor volatile organic compounds",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTTamper = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_tamper",
		Namespace: metricsNamespace,
		Help:      "Tamper binary",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTIlluminance = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_illuminance",
		Namespace: metricsNamespace,
		Help:      "Illuminance(LQI)",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rpc_telemetry_iot_state",
		Namespace: metricsNamespace,
		Help:      "State ON/OFF",
	}, []string{"object_id", "component", "zone"})

	waterTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "thing_water_temperature",
		Namespace: metricsNamespace,
		Help:      "Water Temperature",
	}, []string{"device"})

	airTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "thing_air_temperature",
		Namespace: metricsNamespace,
		Help:      "Temperature",
	}, []string{"device"})

	airHumidity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "thing_air_humidity",
		Namespace: metricsNamespace,
		Help:      "humidity",
	}, []string{"device"})

	airHeatindex = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "thing_air_heatindex",
		Namespace: metricsNamespace,
		Help:      "computed heat index",
	}, []string{"device"})

	thingWireless = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "thing_wireless",
		Namespace: metricsNamespace,
		Help:      "wireless information",
	}, []string{"device", "ssid", "bssid", "ip"})

	queueLength = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "iotcontroller_telemetry_queue_length",
		Namespace: metricsNamespace,
		Help:      "The number of jobs in the work queue",
	}, []string{})

	// ispindel/brewHydroWhite/tilt 57.43604
	// ispindel/brewHydroWhite/temperature 5.125
	// ispindel/brewHydroWhite/temp_units C
	// ispindel/brewHydroWhite/battery 4.139729
	// ispindel/brewHydroWhite/gravity 1.056852
	// ispindel/brewHydroWhite/interval 900
	// ispindel/brewHydroWhite/RSSI -62

	metricSpecificGravity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "iot_specific_gravity",
		Namespace: metricsNamespace,
		Help:      "Specific Gravity",
	}, []string{"device", "zone"})

	metricTiltAngle = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "iot_tilt_angle",
		Namespace: metricsNamespace,
		Help:      "tilt angle",
	}, []string{"device", "zone"})

	metricBattery = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "iot_battery",
		Namespace: metricsNamespace,
		Help:      "temperature",
	}, []string{"device", "zone"})

	metricRSSI = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "iot_rssi",
		Namespace: metricsNamespace,
		Help:      "Device wireless RSSI",
	}, []string{"device", "zone"})
)
