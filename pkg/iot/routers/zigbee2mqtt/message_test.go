package zigbee2mqtt

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestZigbeeMessageFractionalNumerics covers the Third Reality 3RSP02028BZ
// smart plug payload, which reports fractional voltage/current/power and a
// non-integer ac_frequency. Fields that the controller stores must accept
// these as floats; previously Voltage was *int and the entire payload
// failed to decode mid-parse, blocking the device from being registered.
//
// See also TestExposeFractionalValueMax in devices_test.go for the
// matching fix on the bridge/devices definition side.
func TestZigbeeMessageFractionalNumerics(t *testing.T) {
	// Real payload captured from a 3RSP02028BZ shortly after pairing.
	payload := []byte(`{
		"ac_frequency":60,
		"countdown_to_turn_off":0,
		"countdown_to_turn_on":0,
		"current":0.024,
		"energy":0.001,
		"linkquality":51,
		"power":0.5,
		"power_factor":0.42,
		"state":"OFF",
		"voltage":120.8
	}`)

	m := ZigbeeMessage{}
	require.NoError(t, json.Unmarshal(payload, &m))
	require.NotNil(t, m.Voltage)
	require.InDelta(t, 120.8, *m.Voltage, 0.0001)
	require.NotNil(t, m.LinkQuality)
	require.Equal(t, 51, *m.LinkQuality)
	require.NotNil(t, m.State)
	require.Equal(t, "OFF", *m.State)
}

// TestZigbeeMessageFloatVOCAndIlluminance guards against the same regression
// for VOC and Illuminance, which historically were *int despite z2m sometimes
// reporting fractional values (calibrated sensors, derived metrics).
func TestZigbeeMessageFloatVOCAndIlluminance(t *testing.T) {
	payload := []byte(`{"voc":4.2,"illuminance":12.5}`)
	m := ZigbeeMessage{}
	require.NoError(t, json.Unmarshal(payload, &m))
	require.NotNil(t, m.VOC)
	require.InDelta(t, 4.2, *m.VOC, 0.0001)
	require.NotNil(t, m.Illuminance)
	require.InDelta(t, 12.5, *m.Illuminance, 0.0001)
}
