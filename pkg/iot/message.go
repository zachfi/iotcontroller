package iot

type MessageType int

const (
	MessageBridgeInfo MessageType = iota
	MessageBridgeDevices
	MessageBridgeLog
	MessageDeviceReport
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

type WifiMessage struct {
	BSSID string `json:"bssid,omitempty"`
	IP    string `json:"ip,omitempty"`
	RSSI  int    `json:"rssi,omitempty"`
	SSID  string `json:"ssid,omitempty"`
}

type AirMessage struct {
	Humidity    *float32 `json:"humidity,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
	HeatIndex   *float32 `json:"heatindex,omitempty"`
	TempCoef    *float64 `json:"tempcoef,omitempty"`
}

type WaterMessage struct {
	Temperature *float32 `json:"temperature,omitempty"`
	TempCoef    *float64 `json:"tempcoef,omitempty"`
}

type LEDConfig struct {
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

type LEDColor struct {
	State      string `json:"state"`
	Brightness int    `json:"brightness"`
	Effect     string `json:"effect"`
	Color      struct {
		R int `json:"r"`
		G int `json:"g"`
		B int `json:"b"`
	} `json:"color"`
}
