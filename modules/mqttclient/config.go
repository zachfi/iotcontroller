package mqttclient

import (
	"flag"

	"github.com/zachfi/iotcontroller/pkg/iot"
)

type Config struct {
	MQTT iot.MQTTConfig `yaml:"mqtt,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(_ string, f *flag.FlagSet) {
	f.StringVar(&cfg.MQTT.Topic, "topic", "#", "Topic to subscribe to")
}
