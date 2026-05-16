package nativezigbee

import (
	"log/slog"
	"math"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

// Sensor metrics emitted from native-zigbee attribute reports. Naming
// mirrors the z2m router's `iot_zigbee2mqtt_*` family so dashboards and
// alert rules can keep a stable shape per-transport (matching by
// {zone=...} works across both, and the metric name encodes which
// transport saw the reading — useful when migrating devices).
//
// All values are scaled out of ZCL native units into human-friendly
// units BEFORE being written to the gauge:
//
//	temperature      → °C   (raw is signed int16 in 0.01°C units, scale ÷100)
//	humidity         → %    (raw is unsigned int16 in 0.01% units, scale ÷100)
//	pressure         → kPa  (raw is signed int16 in 0.1 kPa units, scale ÷10)
//	battery_percent  → %    (raw is uint8, 0–200 representing 0–100%, scale ÷2)
//	battery_voltage  → V    (raw is uint8 in 100mV units, scale ÷10)
//	illuminance      → lux  (raw is uint16, log10 scale: lux = 10^((raw−1)/10000))
//	link_quality     → 0–255 (taken directly from ZclMessage.link_quality)
//
// All metrics share labels {device, zone}. We deliberately leave router
// out of the label set — the metric name's namespace `iot_zigbee`
// already encodes the transport. Adding a router label would let
// PromQL aggregate across transports if needed; the name-based
// separation keeps cardinality predictable in the common case.
var attributeMetricsNamespace = "iot_zigbee"

var (
	metricTemperature = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "temperature",
		Namespace: attributeMetricsNamespace,
		Help:      "Sensor temperature in degrees Celsius. Source: ZCL cluster 0x0402 (Temperature Measurement) attribute 0x0000.",
	}, []string{"device", "zone"})

	metricHumidity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "humidity",
		Namespace: attributeMetricsNamespace,
		Help:      "Sensor relative humidity in percent. Source: ZCL cluster 0x0405 (Relative Humidity) attribute 0x0000.",
	}, []string{"device", "zone"})

	metricPressure = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "pressure_kpa",
		Namespace: attributeMetricsNamespace,
		Help:      "Atmospheric pressure in kilopascals. Source: ZCL cluster 0x0403 (Pressure Measurement) attribute 0x0000.",
	}, []string{"device", "zone"})

	metricBatteryPercent = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "battery_percent",
		Namespace: attributeMetricsNamespace,
		Help:      "Device battery level in percent. Source: ZCL cluster 0x0001 (Power Configuration) attribute 0x0021 BatteryPercentageRemaining.",
	}, []string{"device", "zone"})

	metricBatteryVoltage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "battery_voltage",
		Namespace: attributeMetricsNamespace,
		Help:      "Device battery voltage in volts. Source: ZCL cluster 0x0001 (Power Configuration) attribute 0x0020 BatteryVoltage.",
	}, []string{"device", "zone"})

	metricIlluminance = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "illuminance_lux",
		Namespace: attributeMetricsNamespace,
		Help:      "Sensor illuminance in lux. Source: ZCL cluster 0x0400 (Illuminance Measurement) attribute 0x0000.",
	}, []string{"device", "zone"})

	metricLinkQuality = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "link_quality",
		Namespace: attributeMetricsNamespace,
		Help:      "Most recent link quality indicator (0–255) reported by the dongle for a message from this device.",
	}, []string{"device", "zone"})
)

// ZCL well-known cluster IDs and attribute IDs we recognize. Anything
// outside this set is forwarded to the binding matcher (action events)
// or dropped at the metric layer — emitting a metric for every random
// attribute would balloon cardinality without giving anyone information
// they can act on.
const (
	clusterPowerConfiguration     = 0x0001
	clusterIlluminanceMeasurement = 0x0400
	clusterTemperatureMeasurement = 0x0402
	clusterPressureMeasurement    = 0x0403
	clusterRelativeHumidity       = 0x0405

	attrBatteryVoltage             = 0x0020
	attrBatteryPercentageRemaining = 0x0021
	attrMeasuredValue              = 0x0000
)

// emitAttributeMetrics walks a parsed ZclMessage's attribute records
// (from GlobalReportAttributes and successful GlobalReadAttributesResponse
// commands) and writes any recognized sensor reading to its Prometheus
// gauge. Anything we don't recognize is left for the binding matcher
// (action events) and the event router downstream — this function only
// writes metrics; it doesn't drop, swallow, or transform the message.
//
// Always called with a non-nil device whose iot/zone label has been
// resolved (or "" if the device is unzoned, in which case the metric
// still emits — pre-zone-label devices are useful to see in the
// dashboard before they're labeled).
//
// Link quality is recorded for every routed message regardless of
// command type, since the coordinator stamps it on the envelope. The
// other metrics are gated on the specific (cluster, attribute) pair.
func emitAttributeMetrics(logger *slog.Logger, msg *zclv1proto.ZclMessage, deviceName, zone string) {
	if msg == nil || deviceName == "" {
		return
	}

	// Link quality lives on the message envelope, not in an attribute
	// record. Emit per-message so we can correlate RF health with
	// individual sensor reports.
	if lqi := msg.GetLinkQuality(); lqi > 0 {
		metricLinkQuality.WithLabelValues(deviceName, zone).Set(float64(lqi))
	}

	cmd := msg.GetFrame().GetCommand()
	if cmd == nil {
		return
	}

	clusterID := msg.GetClusterId()

	if r := cmd.GetGlobalReportAttributes(); r != nil {
		for _, rec := range r.GetRecords() {
			emitAttributeMetric(logger, clusterID, rec.GetAttributeId(), rec.GetValue(), deviceName, zone)
		}
	}
	if r := cmd.GetGlobalReadAttributesResponse(); r != nil {
		for _, rec := range r.GetRecords() {
			// Only honor successful reads. A failed read carries no value.
			if rec.GetStatus() != zclv1proto.ZclStatus_ZCL_STATUS_SUCCESS {
				continue
			}
			emitAttributeMetric(logger, clusterID, rec.GetAttributeId(), rec.GetValue(), deviceName, zone)
		}
	}
}

// emitAttributeMetric resolves one (cluster, attribute, value) tuple
// to a gauge write — or no-op if we don't have a metric for this
// combination. ZCL unit scaling lives here, in one place, so a future
// dashboard or alert doesn't have to know the scaling: it reads the
// gauge in the human-friendly unit named by the metric.
//
// Returns silently on type mismatches (e.g. a manufacturer-specific
// extension repurposing an attribute id with a different data type) —
// the parser's data-type check is the source of truth for shape, but
// we're conservative here so a weird device doesn't poison the gauge.
func emitAttributeMetric(logger *slog.Logger, clusterID, attrID uint32, value *zclv1proto.AttributeValue, deviceName, zone string) {
	if value == nil {
		return
	}

	switch clusterID {
	case clusterTemperatureMeasurement:
		if attrID == attrMeasuredValue {
			// int16 in 0.01°C
			metricTemperature.WithLabelValues(deviceName, zone).Set(float64(value.GetInt16Value()) / 100.0)
		}

	case clusterRelativeHumidity:
		if attrID == attrMeasuredValue {
			// uint16 in 0.01%
			metricHumidity.WithLabelValues(deviceName, zone).Set(float64(value.GetUint16Value()) / 100.0)
		}

	case clusterPressureMeasurement:
		if attrID == attrMeasuredValue {
			// int16 in 0.1 kPa
			metricPressure.WithLabelValues(deviceName, zone).Set(float64(value.GetInt16Value()) / 10.0)
		}

	case clusterIlluminanceMeasurement:
		if attrID == attrMeasuredValue {
			// uint16, ZCL log scale: lux = 10^((raw-1)/10000); raw 0 = "too low"
			raw := value.GetUint16Value()
			if raw > 0 {
				lux := math.Pow(10, (float64(raw)-1.0)/10000.0)
				metricIlluminance.WithLabelValues(deviceName, zone).Set(lux)
			}
		}

	case clusterPowerConfiguration:
		switch attrID {
		case attrBatteryVoltage:
			// uint8 in 100mV units
			metricBatteryVoltage.WithLabelValues(deviceName, zone).Set(float64(value.GetUint8Value()) / 10.0)
		case attrBatteryPercentageRemaining:
			// uint8, 0–200 representing 0–100%
			metricBatteryPercent.WithLabelValues(deviceName, zone).Set(float64(value.GetUint8Value()) / 2.0)
		}
	}
}
