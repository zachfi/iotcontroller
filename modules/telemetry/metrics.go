package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	telemetryIOTUnhandledReport = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_telemetry_unhandled_object_report",
		Help: "The total number of notice calls that include an unhandled object ID.",
	}, []string{"object_id", "component"})

	telemetryIOTReport = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rpc_telemetry_object_report",
		Help: "The total number of notice calls for an object ID.",
	}, []string{"object_id", "component"})

	telemetryIOTBatteryPercent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_battery_percent",
		Help: "The reported batter percentage remaining.",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTLinkQuality = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_link_quality",
		Help: "The reported link quality",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTBridgeState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_bridge_state",
		Help: "The reported bridge state",
	}, []string{})

	telemetryIOTOccupancy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_occupancy",
		Help: "Occupancy binary",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTWaterLeak = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_water_leak",
		Help: "Water leak binary",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_temperature",
		Help: "Sensor Temperature(C)",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTHumidity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_humidity",
		Help: "Sensor Humidity",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTFormaldehyde = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_formaldehyde",
		Help: "Sensor Formaldehyde",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTCo2 = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_co2",
		Help: "Sensor Humidity",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTVoc = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_voc",
		Help: "Sensor volatile organic compounds",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTTamper = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_tamper",
		Help: "Tamper binary",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTIlluminance = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_illuminance",
		Help: "Illuminance(LQI)",
	}, []string{"object_id", "component", "zone"})

	telemetryIOTState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rpc_telemetry_iot_state",
		Help: "State ON/OFF",
	}, []string{"object_id", "component", "zone"})

	waterTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "thing_water_temperature",
		Help: "Water Temperature",
	}, []string{"device"})

	airTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "thing_air_temperature",
		Help: "Temperature",
	}, []string{"device"})

	airHumidity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "thing_air_humidity",
		Help: "humidity",
	}, []string{"device"})

	airHeatindex = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "thing_air_heatindex",
		Help: "computed heat index",
	}, []string{"device"})

	thingWireless = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "thing_wireless",
		Help: "wireless information",
	}, []string{"device", "ssid", "bssid", "ip"})

	workQueueLength = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iotcontroller_telemetry_queue_length",
		Help: "The number of jobs in the work queue",
	}, []string{})

	// ispindel/brewHydroWhite/tilt 57.43604
	// ispindel/brewHydroWhite/temperature 5.125
	// ispindel/brewHydroWhite/temp_units C
	// ispindel/brewHydroWhite/battery 4.139729
	// ispindel/brewHydroWhite/gravity 1.056852
	// ispindel/brewHydroWhite/interval 900
	// ispindel/brewHydroWhite/RSSI -62

	metricSpecificGravity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iot_specific_gravity",
		Help: "Specific Gravity",
	}, []string{"device", "zone"})

	metricTiltAngle = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iot_tilt_angle",
		Help: "tilt angle",
	}, []string{"device", "zone"})

	metricTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iot_temperature",
		Help: "temperature",
	}, []string{"device", "zone"})

	metricBattery = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iot_battery",
		Help: "temperature",
	}, []string{"device", "zone"})

	metricRSSI = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "iot_rssi",
		Help: "Device wireless RSSI",
	}, []string{"device", "zone"})
)
