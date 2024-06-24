package zigbee2mqtt

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsNamespace = "iot_zigbee2mqtt"

	metricIOTUnhandledReport = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "unhandled_object_report",
		Namespace: metricsNamespace,
		Help:      "The total number of notice calls that include an unhandled object ID.",
	}, []string{"device", "component"})

	metricIOTReport = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "object_report",
		Namespace: metricsNamespace,
		Help:      "The total number of notice calls for an object ID.",
	}, []string{"device", "component"})

	metricIOTBatteryPercent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "battery_percent",
		Namespace: metricsNamespace,
		Help:      "The reported batter percentage remaining.",
	}, []string{"device", "component", "zone"})

	metricIOTLinkQuality = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "link_quality",
		Namespace: metricsNamespace,
		Help:      "The reported link quality",
	}, []string{"device", "component", "zone"})

	metricIOTBridgeState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "bridge_state",
		Namespace: metricsNamespace,
		Help:      "The reported bridge state",
	}, []string{})

	metricIOTOccupancy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "occupancy",
		Namespace: metricsNamespace,
		Help:      "Occupancy binary",
	}, []string{"device", "router", "zone"})

	metricIOTWaterLeak = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "water_leak",
		Namespace: metricsNamespace,
		Help:      "Water leak binary",
	}, []string{"device", "router", "zone"})

	metricIOTTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "temperature",
		Namespace: metricsNamespace,
		Help:      "Sensor Temperature(C)",
	}, []string{"device", "router", "zone"})

	metricIOTSoilMoisture = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "soil_moisture",
		Namespace: metricsNamespace,
		Help:      "Soil moisture percent",
	}, []string{"device", "router", "zone"})

	metricIOTHumidity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "humidity",
		Namespace: metricsNamespace,
		Help:      "Sensor Humidity",
	}, []string{"device", "router", "zone"})

	metricIOTFormaldehyde = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "formaldehyde",
		Namespace: metricsNamespace,
		Help:      "Sensor Formaldehyde",
	}, []string{"device", "router", "zone"})

	metricIOTCo2 = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "co2",
		Namespace: metricsNamespace,
		Help:      "Sensor Humidity",
	}, []string{"device", "router", "zone"})

	metricIOTVoc = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "voc",
		Namespace: metricsNamespace,
		Help:      "Sensor volatile organic compounds",
	}, []string{"device", "router", "zone"})

	metricIOTTamper = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "tamper",
		Namespace: metricsNamespace,
		Help:      "Tamper binary",
	}, []string{"device", "router", "zone"})

	metricIOTIlluminance = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "illuminance",
		Namespace: metricsNamespace,
		Help:      "Illuminance(LQI)",
	}, []string{"device", "router", "zone"})

	metricIOTState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "state",
		Namespace: metricsNamespace,
		Help:      "State ON/OFF",
	}, []string{"device", "router", "zone"})

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
		Name:      "specific_gravity",
		Namespace: metricsNamespace,
		Help:      "Specific Gravity (SG)",
	}, []string{"device", "zone"})

	metricTiltAngle = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "tilt_angle",
		Namespace: metricsNamespace,
		Help:      "Tilt angle",
	}, []string{"device", "zone"})

	metricBattery = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "battery",
		Namespace: metricsNamespace,
		Help:      "Device battery level",
	}, []string{"device", "zone"})

	metricRSSI = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "rssi",
		Namespace: metricsNamespace,
		Help:      "Device wireless RSSI",
	}, []string{"device", "zone"})
)
