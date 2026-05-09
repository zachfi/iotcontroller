package zigbee2mqtt

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func ptrTrue() *bool  { v := true; return &v }
func ptrFalse() *bool { v := false; return &v }

// TestSetBinaryMetric_NilDoesNotEmit guards the phantom-zero regression.
// Before the fix, devices with no occupancy/water_leak/tamper sensor still
// got a 0-valued series in their respective gauge because the old code
// always called Set(0) in the else branch. Now nil means "no series".
func TestSetBinaryMetric_NilDoesNotEmit(t *testing.T) {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "test_binary",
		Help: "test gauge",
	}, []string{"device", "router", "zone"})

	setBinaryMetric(g, nil, "0xtemperature_only_probe", "zigbee2mqtt", "tunnel")
	require.Equal(t, 0, testutil.CollectAndCount(g),
		"a nil reading must not register a series")
}

// TestSetBinaryMetric_TruePtrEmitsOne and false-ptr-emits-zero verify
// the on-the-wire values for actual leak/occupancy/tamper events.
func TestSetBinaryMetric_TruePtrEmitsOne(t *testing.T) {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "test_binary",
		Help: "test gauge",
	}, []string{"device", "router", "zone"})

	setBinaryMetric(g, ptrTrue(), "0xleak_sensor", "zigbee2mqtt", "pond")
	require.Equal(t, 1, testutil.CollectAndCount(g))
	require.Equal(t, 1.0, testutil.ToFloat64(g.WithLabelValues(
		"0xleak_sensor", "zigbee2mqtt", "pond")))
}

func TestSetBinaryMetric_FalsePtrEmitsZero(t *testing.T) {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "test_binary",
		Help: "test gauge",
	}, []string{"device", "router", "zone"})

	setBinaryMetric(g, ptrFalse(), "0xleak_sensor", "zigbee2mqtt", "pond")
	require.Equal(t, 1, testutil.CollectAndCount(g))
	require.Equal(t, 0.0, testutil.ToFloat64(g.WithLabelValues(
		"0xleak_sensor", "zigbee2mqtt", "pond")))
}
