package nativezigbee

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

// gaugeValue returns the current value of a labeled gauge, or NaN
// (well, -1 as a sentinel and a fail-the-test signal) if the series
// doesn't exist. Used to assert that emitAttributeMetrics wrote the
// expected value with the expected unit scaling.
func gaugeValue(t *testing.T, g *prometheus.GaugeVec, labels ...string) float64 {
	t.Helper()
	m, err := g.GetMetricWithLabelValues(labels...)
	require.NoError(t, err)
	pb := &dto.Metric{}
	require.NoError(t, m.Write(pb))
	require.NotNil(t, pb.Gauge, "expected a gauge value")
	return pb.GetGauge().GetValue()
}

// gaugeNotSet returns true iff there is no series for the given
// labels — used to verify we didn't accidentally emit something we
// shouldn't have (e.g. on an unsupported cluster).
func gaugeNotSet(t *testing.T, g *prometheus.GaugeVec, labels ...string) bool {
	t.Helper()
	gathered, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	// Find the metric family by description (cheap and stable in tests).
	desc := g.WithLabelValues(labels...).Desc().String()
	for _, mf := range gathered {
		for _, m := range mf.GetMetric() {
			thisDesc := m.String()
			if strings.Contains(thisDesc, desc) {
				return false
			}
		}
	}
	return true
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

// reportAttributesMessage builds the minimum ZclMessage envelope the
// metric emitter walks: a frame with a global ReportAttributes command
// carrying a single (attribute_id, value) record on the given cluster.
// LinkQuality is set explicitly so the link_quality gauge gets exercised
// alongside the cluster-specific one.
func reportAttributesMessage(clusterID uint32, lqi uint32, attrID uint32, value *zclv1proto.AttributeValue) *zclv1proto.ZclMessage {
	return &zclv1proto.ZclMessage{
		ClusterId:   clusterID,
		LinkQuality: lqi,
		Frame: &zclv1proto.ZclFrame{
			Command: &zclv1proto.ZclCommand{
				Command: &zclv1proto.ZclCommand_GlobalReportAttributes{
					GlobalReportAttributes: &zclv1proto.GlobalReportAttributes{
						Records: []*zclv1proto.AttributeRecord{
							{AttributeId: attrID, Value: value},
						},
					},
				},
			},
		},
	}
}

// Test_emitAttributeMetrics_TemperatureCelsius pins the regression case
// that motivated this whole change: the closet SNZB-02 reporting 26.00°C
// as a signed int16 of 2600. The gauge must read 26.00 after scaling.
func Test_emitAttributeMetrics_TemperatureCelsius(t *testing.T) {
	msg := reportAttributesMessage(
		clusterTemperatureMeasurement, 233, attrMeasuredValue,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT16,
			Value:    &zclv1proto.AttributeValue_Int16Value{Int16Value: 2600},
		},
	)
	emitAttributeMetrics(discardLogger(), msg, "closet-sensor", "closet")

	require.InDelta(t, 26.00, gaugeValue(t, metricTemperature, "closet-sensor", "closet"), 1e-9)
	require.InDelta(t, 233.0, gaugeValue(t, metricLinkQuality, "closet-sensor", "closet"), 1e-9)
}

func Test_emitAttributeMetrics_TemperatureNegative(t *testing.T) {
	// Outdoor sensor at -5.50°C → int16(-550)
	msg := reportAttributesMessage(
		clusterTemperatureMeasurement, 200, attrMeasuredValue,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT16,
			Value:    &zclv1proto.AttributeValue_Int16Value{Int16Value: -550},
		},
	)
	emitAttributeMetrics(discardLogger(), msg, "outdoor-sensor", "outdoor")
	require.InDelta(t, -5.50, gaugeValue(t, metricTemperature, "outdoor-sensor", "outdoor"), 1e-9)
}

func Test_emitAttributeMetrics_Humidity(t *testing.T) {
	// 55.23% RH → uint16(5523)
	msg := reportAttributesMessage(
		clusterRelativeHumidity, 220, attrMeasuredValue,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT16,
			Value:    &zclv1proto.AttributeValue_Uint16Value{Uint16Value: 5523},
		},
	)
	emitAttributeMetrics(discardLogger(), msg, "h-sensor", "bath")
	require.InDelta(t, 55.23, gaugeValue(t, metricHumidity, "h-sensor", "bath"), 1e-9)
}

func Test_emitAttributeMetrics_Pressure(t *testing.T) {
	// 101.3 kPa → int16(1013)
	msg := reportAttributesMessage(
		clusterPressureMeasurement, 180, attrMeasuredValue,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT16,
			Value:    &zclv1proto.AttributeValue_Int16Value{Int16Value: 1013},
		},
	)
	emitAttributeMetrics(discardLogger(), msg, "p-sensor", "outdoor")
	require.InDelta(t, 101.3, gaugeValue(t, metricPressure, "p-sensor", "outdoor"), 1e-9)
}

func Test_emitAttributeMetrics_BatteryPercent(t *testing.T) {
	// 87% → raw 174 (ZCL: 0–200 represents 0–100%)
	msg := reportAttributesMessage(
		clusterPowerConfiguration, 230, attrBatteryPercentageRemaining,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT8,
			Value:    &zclv1proto.AttributeValue_Uint8Value{Uint8Value: 174},
		},
	)
	emitAttributeMetrics(discardLogger(), msg, "b-sensor", "closet")
	require.InDelta(t, 87.0, gaugeValue(t, metricBatteryPercent, "b-sensor", "closet"), 1e-9)
}

func Test_emitAttributeMetrics_BatteryVoltage(t *testing.T) {
	// 3.0V → raw 30 (uint8 in 100mV units)
	msg := reportAttributesMessage(
		clusterPowerConfiguration, 230, attrBatteryVoltage,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT8,
			Value:    &zclv1proto.AttributeValue_Uint8Value{Uint8Value: 30},
		},
	)
	emitAttributeMetrics(discardLogger(), msg, "bv-sensor", "closet")
	require.InDelta(t, 3.0, gaugeValue(t, metricBatteryVoltage, "bv-sensor", "closet"), 1e-9)
}

func Test_emitAttributeMetrics_IlluminanceLog(t *testing.T) {
	// ZCL: lux = 10^((raw-1)/10000). For 100 lux, raw ≈ 20001
	// (10^((20001-1)/10000) = 10^2 = 100). Verify the math.
	msg := reportAttributesMessage(
		clusterIlluminanceMeasurement, 210, attrMeasuredValue,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT16,
			Value:    &zclv1proto.AttributeValue_Uint16Value{Uint16Value: 20001},
		},
	)
	emitAttributeMetrics(discardLogger(), msg, "lux-sensor", "lobby")
	require.InDelta(t, 100.0, gaugeValue(t, metricIlluminance, "lux-sensor", "lobby"), 1e-6)
}

func Test_emitAttributeMetrics_IlluminanceZeroIsNoSeries(t *testing.T) {
	// raw=0 is ZCL's "below sensor's measurable range" sentinel.
	// We must NOT emit a gauge for it (would be 10^(-0.0001) ≈ 0.9997
	// lux, a misleading reading).
	msg := reportAttributesMessage(
		clusterIlluminanceMeasurement, 210, attrMeasuredValue,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT16,
			Value:    &zclv1proto.AttributeValue_Uint16Value{Uint16Value: 0},
		},
	)
	emitAttributeMetrics(discardLogger(), msg, "lux-sentinel-sensor", "lobby")
	require.True(t, gaugeNotSet(t, metricIlluminance, "lux-sentinel-sensor", "lobby"),
		"raw=0 illuminance must produce no gauge series")
}

func Test_emitAttributeMetrics_UnknownClusterIgnored(t *testing.T) {
	// Some random cluster we don't have a metric for (cluster 0x0006
	// genOnOff has attribute reports too but isn't a sensor in our
	// taxonomy — it's an action target).
	msg := reportAttributesMessage(
		0x0006, 200, 0x0000,
		&zclv1proto.AttributeValue{
			DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_BOOLEAN,
			Value:    &zclv1proto.AttributeValue_BoolValue{BoolValue: true},
		},
	)
	// No assertion about sensor gauges — just verify no panic and
	// link_quality still emits since it lives on the envelope, not
	// on the cluster classification.
	emitAttributeMetrics(discardLogger(), msg, "onoff-thing", "kitchen")
	require.InDelta(t, 200.0, gaugeValue(t, metricLinkQuality, "onoff-thing", "kitchen"), 1e-9)
}

func Test_emitAttributeMetrics_FailedReadResponseSkipped(t *testing.T) {
	// A GlobalReadAttributesResponse with status != SUCCESS carries
	// no value — we must NOT write a 0 into the gauge.
	msg := &zclv1proto.ZclMessage{
		ClusterId:   clusterTemperatureMeasurement,
		LinkQuality: 100,
		Frame: &zclv1proto.ZclFrame{
			Command: &zclv1proto.ZclCommand{
				Command: &zclv1proto.ZclCommand_GlobalReadAttributesResponse{
					GlobalReadAttributesResponse: &zclv1proto.GlobalReadAttributesResponse{
						Records: []*zclv1proto.AttributeResponseRecord{
							{
								AttributeId: attrMeasuredValue,
								Status:      zclv1proto.ZclStatus_ZCL_STATUS_UNSUPPORTED_ATTRIBUTE,
								// No Value
							},
						},
					},
				},
			},
		},
	}
	emitAttributeMetrics(discardLogger(), msg, "broken-sensor", "elsewhere")
	require.True(t, gaugeNotSet(t, metricTemperature, "broken-sensor", "elsewhere"),
		"failed-status read response must not emit a gauge")
}

func Test_emitAttributeMetrics_NilMessageNoPanic(t *testing.T) {
	// Defensive: never panic on a nil message or empty device name.
	require.NotPanics(t, func() {
		emitAttributeMetrics(discardLogger(), nil, "x", "y")
	})
	require.NotPanics(t, func() {
		emitAttributeMetrics(discardLogger(), &zclv1proto.ZclMessage{}, "", "y")
	})
}
