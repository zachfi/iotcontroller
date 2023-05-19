package mqttclient

import "github.com/zachfi/iotcontroller/pkg/iot"

type Config struct {
	MQTT iot.MQTTConfig `yaml:"mqtt,omitempty"`
}
