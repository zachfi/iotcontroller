package zigbee2mqtt

import "encoding/json"

type MessageType int

const (
	MessageBridgeInfo MessageType = iota
	MessageBridgeDevices
	MessageBridgeLog
	MessageDeviceReport
)

const (
	MessageDevices = "devices"
)

type ZigbeeMessage struct {
	Action       *string  `json:"action,omitempty"`
	Battery      *float64 `json:"battery,omitempty"`
	Illuminance  *int     `json:"illuminance,omitempty"`
	LinkQuality  *int     `json:"linkquality,omitempty"`
	State        *string  `json:"state,omitempty"`
	Occupancy    *bool    `json:"occupancy,omitempty"`
	Tamper       *bool    `json:"tamper,omitempty"`
	Temperature  *float64 `json:"temperature,omitempty"`
	Humidity     *float64 `json:"humidity,omitempty"`
	Voltage      *int     `json:"voltage,omitempty"`
	WaterLeak    *bool    `json:"water_leak,omitempty"`
	Co2          *float64 `json:"co2,omitempty"`
	Formaldehyde *float64 `json:"formaldehyd,omitempty"`
	VOC          *int     `json:"voc,omitempty"`
}

type Message struct {
	Type    string   `json:"type,omitempty"`
	Devices *Devices `json:"devices,omitempty"`
}

func (m *Message) UnmarshalJSON(data []byte) error {
	return nil
}

// ZigbeeBridgeLogMessage
// https://www.zigbee2mqtt.io/information/mqtt_topics_and_message_structure.html#zigbee2mqttbridgelog
// zigbee2mqtt/bridge/log
// {"type":"device_announced","message":"announce","meta":{"friendly_name":"0x0017880104650857"}}
type BridgeLog struct {
	Type    string                 `json:"type,omitempty"`
	Message interface{}            `json:"message,omitempty"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
}

func (z *BridgeLog) UnmarshalJSON(data []byte) error {
	var v map[string]interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	z.Type, _ = v["type"].(string)
	message := v["message"]

	switch z.Type {
	case "device_announced":
		z.Message = v["message"].(string)
		z.Meta = v["meta"].(map[string]interface{})
	case "devices":
		j, err := json.Marshal(message)
		if err != nil {
			return err
		}

		m := Devices{}
		err = json.Unmarshal(j, &m)
		if err != nil {
			return err
		}

		z.Message = m
	case "ota_update":
		z.Meta = v["meta"].(map[string]interface{})
		z.Message = v["message"].(string)
	case "pairing":
		z.Meta = v["meta"].(map[string]interface{})
		z.Message = v["message"].(string)
	}

	return nil
}
