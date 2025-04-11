package mqttclient

import (
	"flag"

	// "github.com/grafana/dskit/backoff"
	"github.com/zachfi/iotcontroller/pkg/iot"
)

type Config struct {
	MQTT iot.MQTTConfig `yaml:"mqtt,omitempty"`
	// Backoff backoff.Config `yaml:"backoff,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(_ string, f *flag.FlagSet) {
	f.StringVar(&cfg.MQTT.Topic, "topic", "#", "Topic to subscribe to")
	// cfg.Backoff.RegisterFlagsWithPrefix("mqttclient", f)
}
