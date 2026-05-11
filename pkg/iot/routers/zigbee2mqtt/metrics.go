package zigbee2mqtt

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// setBinaryMetric writes 1 / 0 for a binary sensor reading, but ONLY when
// the device actually reported the field (val != nil). The previous
// `if … else` pattern wrote a 0 even when the field was absent, which
// produced phantom series like iot_zigbee2mqtt_water_leak{device=…} for
// every device whose payload the controller processed — including devices
// (radiator switches, smart plugs, temperature probes) that have no leak
// sensor at all. That bloated cardinality and diluted the actual leak
// signal. Now: nil → no series.
func setBinaryMetric(g *prometheus.GaugeVec, val *bool, labels ...string) {
	if val == nil {
		return
	}
	v := float64(0)
	if *val {
		v = 1
	}
	g.WithLabelValues(labels...).Set(v)
}

var (
	metricsNamespace = "iot_zigbee2mqtt"

	metricIOTUnhandledReport = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "unhandled_object_report",
		Namespace: metricsNamespace,
		Help:      "The total number of notice calls that include an unhandled object ID.",
	}, []string{"device", "component"})

	// metricFallbackTotal counts events that flowed through the legacy
	// ActionHandler path because no Binding matched. Watch this drop to
	// zero per device as Bindings are migrated; once flat across all
	// devices, the legacy switch in zonekeeper.ActionHandler can be
	// removed. The device label is what makes this a per-button
	// migration thermometer.
	metricFallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "router_action_fallback_total",
		Namespace: "iotcontroller",
		Help:      "Number of action events handled via the legacy ActionHandler fallback (no matching Binding).",
	}, []string{"device", "action", "zone"})

	metricIOTReport = promauto.NewCounterVec(prometheus.CounterOpts{
		Name:      "object_report",
		Namespace: metricsNamespace,
		Help:      "The total number of notice calls for an object ID.",
	}, []string{"device", "component"})

	metricIOTBatteryPercent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "battery_percent",
		Namespace: metricsNamespace,
		Help:      "The reported batter percentage remaining.",
	}, []string{"device", "component", "zone", "type"})

	metricIOTLinkQuality = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "link_quality",
		Namespace: metricsNamespace,
		Help:      "The reported link quality",
	}, []string{"device", "component", "zone", "type"})

	metricIOTTransmitPower = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "transmit_power",
		Namespace: metricsNamespace,
		Help:      "The reported transmit power",
	}, []string{"device", "component", "zone", "type"})

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

	thingWirelessSignalStrength = promauto.NewGaugeVec(prometheus.GaugeOpts{
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

	// Power-metering plug gauges. Populated when a device reports
	// voltage/current/power/energy/power_factor/ac_frequency in its z2m
	// payload (e.g. Third Reality 3RSP02028BZ on the pond pump).
	metricIOTVoltage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "voltage",
		Namespace: metricsNamespace,
		Help:      "Mains voltage (V) reported by metering plug",
	}, []string{"device", "router", "zone"})

	metricIOTCurrent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "current",
		Namespace: metricsNamespace,
		Help:      "Load current (A) reported by metering plug",
	}, []string{"device", "router", "zone"})

	metricIOTPower = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "power",
		Namespace: metricsNamespace,
		Help:      "Instantaneous active power (W) reported by metering plug",
	}, []string{"device", "router", "zone"})

	metricIOTEnergy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "energy_kwh",
		Namespace: metricsNamespace,
		Help:      "Cumulative energy (kWh) reported by metering plug",
	}, []string{"device", "router", "zone"})

	metricIOTPowerFactor = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "power_factor",
		Namespace: metricsNamespace,
		Help:      "Power factor (0..1) reported by metering plug",
	}, []string{"device", "router", "zone"})

	metricIOTAcFrequency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "ac_frequency_hz",
		Namespace: metricsNamespace,
		Help:      "Mains AC frequency (Hz) reported by metering plug",
	}, []string{"device", "router", "zone"})
)
