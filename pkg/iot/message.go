package iot

type MessageType int

const (
	MessageBridgeInfo MessageType = iota
	MessageBridgeDevices
	MessageBridgeLog
	MessageDeviceReport
)

// zigbee2mqtt/bridge/event {"data":{"definition":{"description":"Texas Instruments router","exposes":[{"access":7,"description":"Transmit power, supported from firmware 20221102. The max for CC1352 is 20 dBm and 5 dBm for CC2652 (any higher value is converted to 5dBm)","label":"Transmit power","name":"transmit_power","property":"transmit_power","type":"numeric","unit":"dBm","value_max":20,"value_min":-20,"value_step":1},{"access":1,"description":"Link quality (signal strength)","label":"Linkquality","name":"linkquality","property":"linkquality","type":"numeric","unit":"lqi","value_max":255,"value_min":0}],"model":"ti.router","options":[],"supports_ota":false,"vendor":"Custom devices (DiY)"},"friendly_name":"0x00124b00259bbd7f","ieee_address":"0x00124b00259bbd7f","status":"successful","supported":true},"type":"device_interview"}

// TODO: most of these types appear to be unused.

type wifiMessage struct {
	BSSID string `json:"bssid,omitempty"`
	IP    string `json:"ip,omitempty"`
	RSSI  int    `json:"rssi,omitempty"`
	SSID  string `json:"ssid,omitempty"`
}

type airMessage struct {
	Humidity    *float32 `json:"humidity,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
	HeatIndex   *float32 `json:"heatindex,omitempty"`
	TempCoef    *float64 `json:"tempcoef,omitempty"`
}

type waterMessage struct {
	Temperature *float32 `json:"temperature,omitempty"`
	TempCoef    *float64 `json:"tempcoef,omitempty"`
}

type lEDConfig struct {
	Schema       string   `json:"schema"`
	Brightness   bool     `json:"brightness"`
	Rgb          bool     `json:"rgb"`
	Effect       bool     `json:"effect"`
	EffectList   []string `json:"effect_list"`
	Name         string   `json:"name"`
	UniqueID     string   `json:"unique_id"`
	CommandTopic string   `json:"command_topic"`
	StateTopic   string   `json:"state_topic"`
	Device       struct {
		Identifiers  string     `json:"identifiers"`
		Manufacturer string     `json:"manufacturer"`
		Model        string     `json:"model"`
		Name         string     `json:"name"`
		SwVersion    string     `json:"sw_version"`
		Connections  [][]string `json:"connections"`
	} `json:"device"`
}

type lEDColor struct {
	State      string `json:"state"`
	Brightness int    `json:"brightness"`
	Effect     string `json:"effect"`
	Color      struct {
		R int `json:"r"`
		G int `json:"g"`
		B int `json:"b"`
	} `json:"color"`
}
