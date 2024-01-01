package iot

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trace "go.opentelemetry.io/otel/trace"

	"github.com/zachfi/iotcontroller/pkg/iot/messages/zigbee2mqtt"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

func TestParseTopicPath(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		Topic  string
		Should TopicPath
	}

	samples := []testStruct{
		{
			Topic: "stat/ff3e8a21dc12507d3a159b4792403f01/tempcoef",
			Should: TopicPath{
				Component: "stat",
				ObjectID:  "tempcoef",
				NodeID:    "ff3e8a21dc12507d3a159b4792403f01",
				Endpoints: []string{},
			},
		},

		{
			Topic: "stat/ff3e8a21dc12507d3a159b4792403f01/water/tempcoef",
			Should: TopicPath{
				Component: "stat",
				NodeID:    "ff3e8a21dc12507d3a159b4792403f01",
				ObjectID:  "water",
				Endpoints: []string{"tempcoef"},
			},
		},

		{
			Topic: "homeassistant/binary_sensor/garden/config",
			Should: TopicPath{
				Component: "homeassistant",
				ObjectID:  "binary_sensor",
				Endpoints: []string{"garden", "config"},
			},
		},

		{
			Topic: "homeassistant/binary_sensor/garden/state",
			Should: TopicPath{
				Component: "homeassistant",
				ObjectID:  "binary_sensor",
				Endpoints: []string{"garden", "state"},
			},
		},

		{
			Topic: "workgroup/92696ed2ae92b430f4e9447583936628/wifi/ssid",
			Should: TopicPath{
				Component: "workgroup",
				NodeID:    "92696ed2ae92b430f4e9447583936628",
				ObjectID:  "wifi",
				Endpoints: []string{"ssid"},
			},
		},

		{
			Topic: "homeassistant/light/18c114ad3dec7c1d29bc888e4e748f89/led1/config",
			Should: TopicPath{
				DiscoveryPrefix: "homeassistant",
				Component:       "light",
				NodeID:          "18c114ad3dec7c1d29bc888e4e748f89",
				ObjectID:        "led1",
				Endpoints:       []string{"config"},
			},
		},

		{
			Topic: "zigbee2mqtt/0x00158d0004238a81",
			Should: TopicPath{
				Component: "zigbee2mqtt",
				ObjectID:  "0x00158d0004238a81",
				Endpoints: []string{},
			},
		},

		{
			Topic: "zigbee2mqtt/0x00124b00257c5f24/set",
			Should: TopicPath{
				Component: "zigbee2mqtt",
				ObjectID:  "0x00124b00257c5f24",
				Endpoints: []string{"set"},
			},
		},

		{
			Topic: "ispindel/brewHydroWhite/RSSI",
			Should: TopicPath{
				Component: "ispindel",
				ObjectID:  "brewHydroWhite",
				Endpoints: []string{"RSSI"},
			},
		},

		// "workgroup/92696ed2ae92b430f4e9447583936628/wifi/bssid",

		// "stat/92696ed2ae92b430f4e9447583936628/tempcoef",
		// "stat/92696ed2ae92b430f4e9447583936628/water/tempcoef",
		// "stat/f51d958dbc60b0519d7e64f14cc733ab/tempcoef",
		// "stat/f51d958dbc60b0519d7e64f14cc733ab/water/tempcoef",
		// "stat/18c114ad3dec7c1d29bc888e4e748f89/led1/color",
		// "stat/18c114ad3dec7c1d29bc888e4e748f89/led1/power",
		// "stat/18c114ad3dec7c1d29bc888e4e748f89/led2/color",
		// "stat/18c114ad3dec7c1d29bc888e4e748f89/led2/power",
		// "workgroup/92696ed2ae92b430f4e9447583936628/wifi/ssid",
		// "workgroup/92696ed2ae92b430f4e9447583936628/wifi/bssid",
		// "workgroup/92696ed2ae92b430f4e9447583936628/wifi/rssi",
		// "workgroup/92696ed2ae92b430f4e9447583936628/wifi/ip",
		// "workgroup/92696ed2ae92b430f4e9447583936628/device",
		// "workgroup/92696ed2ae92b430f4e9447583936628/sketch",
		// "workgroup/92696ed2ae92b430f4e9447583936628/air/temperature",
		// "workgroup/92696ed2ae92b430f4e9447583936628/air/humidity",
		// "workgroup/92696ed2ae92b430f4e9447583936628/air/heatindex",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/air/temperature",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/air/humidity",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/air/heatindex",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/wifi/ssid",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/wifi/bssid",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/wifi/rssi",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/wifi/ip",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/device",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/sketch",
		// "workgroup/ff3e8a21dc12507d3a159b4792403f01/light",
		// "workgroup/f51d958dbc60b0519d7e64f14cc733ab/wifi/ssid",
		// "workgroup/f51d958dbc60b0519d7e64f14cc733ab/wifi/bssid",
		// "workgroup/f51d958dbc60b0519d7e64f14cc733ab/wifi/rssi",
		// "workgroup/f51d958dbc60b0519d7e64f14cc733ab/wifi/ip",
		// "workgroup/f51d958dbc60b0519d7e64f14cc733ab/device",
		// "workgroup/f51d958dbc60b0519d7e64f14cc733ab/sketch",
		// "workgroup/f51d958dbc60b0519d7e64f14cc733ab/light",
		// "workgroup/18c114ad3dec7c1d29bc888e4e748f89/wifi/ssid",
		// "workgroup/18c114ad3dec7c1d29bc888e4e748f89/wifi/bssid",
		// "workgroup/18c114ad3dec7c1d29bc888e4e748f89/wifi/rssi",
		// "workgroup/18c114ad3dec7c1d29bc888e4e748f89/wifi/ip",
		// "workgroup/18c114ad3dec7c1d29bc888e4e748f89/sketch",
		// "homeassistant/light/18c114ad3dec7c1d29bc888e4e748f89/led1/config",
		// "homeassistant/light/18c114ad3dec7c1d29bc888e4e748f89/led2/config",
	}

	for _, s := range samples {
		result, err := ParseTopicPath(s.Topic)
		assert.Nil(t, err)
		require.Equal(t, s.Should, result)
	}
}

func testFloat(f float32) *float32 {
	return &f
}

func TestReadMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ObjectID  string
		Payload   []byte
		Endpoints []string
		Obj       interface{}
		Err       error
	}{
		{
			ObjectID:  "wifi",
			Payload:   []byte(`{"ssid":"testaroo"}`),
			Endpoints: []string{"ssid"},
			Obj: WifiMessage{
				SSID: "testaroo",
			},
		},
		{
			ObjectID:  "air",
			Payload:   []byte(`{"temperature": 17.28}`),
			Endpoints: []string{"temperature"},
			Obj: AirMessage{
				Temperature: testFloat(17.28),
			},
		},
		{
			ObjectID:  "air",
			Payload:   []byte(`{"humidity":50.5}`),
			Endpoints: []string{"humidity"},
			Obj: AirMessage{
				Humidity: testFloat(50.5),
			},
		},
		{
			ObjectID:  "water",
			Payload:   []byte(`{"temperature":50.5}`),
			Endpoints: []string{"temperature"},
			Obj: WaterMessage{
				Temperature: testFloat(50.5),
			},
		},
		{
			ObjectID:  "led",
			Payload:   []byte(`{"name":"test"}`),
			Endpoints: []string{"config"},
			Obj: LEDConfig{
				Name: "test",
			},
		},
		{
			ObjectID:  "led",
			Payload:   []byte(`{"state":"on"}`),
			Endpoints: []string{"color"},
			Obj: LEDColor{
				State: "on",
			},
		},
	}

	for _, tc := range cases {
		result, err := ReadMessage(tc.ObjectID, tc.Payload, tc.Endpoints...)
		assert.NoError(t, err)
		assert.Equal(t, tc.Obj, result)
	}
}

func TestReadZigbeeMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		discovery *iotv1proto.DeviceDiscovery
		Obj       interface{}
		Err       error
	}{
		{
			discovery: &iotv1proto.DeviceDiscovery{
				ObjectId:  "bridge",
				Message:   []byte(`online`),
				Endpoints: []string{"state"},
			},
			Obj: zigbee2mqtt.BridgeState("online"),
		},
		{
			discovery: &iotv1proto.DeviceDiscovery{
				ObjectId: "bridge",
				Message: []byte(`{
				"message":"Update available for '0x001777090899e9c9'",
				"meta":{
					"device":"0x001777090899e9c9",
					"status":"available"
				},
				"type":"ota_update"
			}`),
				Endpoints: []string{"log"},
			},
			Obj: zigbee2mqtt.BridgeLog{
				Message: "Update available for '0x001777090899e9c9'",
				Meta: map[string]interface{}{
					"device": "0x001777090899e9c9",
					"status": "available",
				},
				Type: "ota_update",
			},
		},
		{
			discovery: &iotv1proto.DeviceDiscovery{
				ObjectId:  "bridge",
				Message:   []byte(`online`),
				Endpoints: []string{"config"},
			},
			Obj: nil,
		},
	}

	ctx := context.Background()
	tracer := trace.NewNoopTracerProvider().Tracer("test")

	for _, tc := range cases {

		result, err := ReadZigbeeMessage(ctx, tracer, tc.discovery)
		assert.NoError(t, err)
		assert.Equal(t, tc.Obj, result)
	}
}

func TestZigbeeDeviceType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		bridgeDevice zigbee2mqtt.Device
		devType      iotv1proto.DeviceType
	}{
		{
			bridgeDevice: zigbee2mqtt.Device{
				Type: "Coordinator",
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_COORDINATOR,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor: "Philips",
				},
				ModelID: "LCA003",
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor: "Philips",
				},
				ModelID: "LCB002",
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor: "SONOFF",
					Model:  "S31ZB",
				},
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_RELAY,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor: "SONOFF",
					Model:  "S40ZBTPB",
				},
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_RELAY,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor: "SONOFF",
					Model:  "SNZB-01",
				},
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_BUTTON,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor:      "Philips",
					Description: "Hue dimmer switch",
				},
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_BUTTON,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor:      "Philips",
					Description: "Hue Tap dial switch",
				},
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_BUTTON,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor:      "Third Reality",
					Description: "Temperature and humidity sensor",
					Model:       "3RTHS24BZ",
				},
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor:      "Third Reality",
					Description: "Smart button",
					Model:       "3RSB22BZ",
				},
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_BUTTON,
		},
		{
			bridgeDevice: zigbee2mqtt.Device{
				Definition: zigbee2mqtt.Definition{
					Vendor: "TuYa",
					Model:  "TS0601_air_quality_sensor",
				},
			},
			devType: iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE,
		},
	}

	for _, tc := range cases {
		x := zigbee2mqtt.DeviceType(tc.bridgeDevice)
		require.Equal(t, tc.devType, x)
	}
}

func TestZigbee_DevicesJson(t *testing.T) {
	content, err := os.ReadFile("/home/zach/go/src/github.com/zachfi/iotcontroller/pkg/iot/devices.json")
	require.NoError(t, err)

	var devices zigbee2mqtt.Devices
	err = json.Unmarshal(content, &devices)
	require.NoError(t, err)

	for _, d := range devices {
		if d.FriendlyName == "0x001788010d3803ad" {
			t.Logf("device: %+v", d)
		}
	}
}
