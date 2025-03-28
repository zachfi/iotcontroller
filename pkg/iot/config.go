package iot

import (
	"flag"

	"github.com/grafana/dskit/backoff"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	MQTT MQTTConfig `yaml:"mqtt,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.MQTT.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "mqtt"), f)
}

type MQTTConfig struct {
	URL      string         `yaml:"url,omitempty"`
	Topic    string         `yaml:"topic,omitempty"`
	Username string         `yaml:"username,omitempty"`
	Password string         `yaml:"password,omitempty"`
	Backoff  backoff.Config `yaml:"backoff,omitempty"`
}

func (cfg *MQTTConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.URL, util.PrefixConfig(prefix, "url"), "tcp://localhost:1883", "The URL of the MQTT broker")
	f.StringVar(&cfg.Topic, util.PrefixConfig(prefix, "topic"), "iot", "The MQTT topic to subscribe to")
	f.StringVar(&cfg.Username, util.PrefixConfig(prefix, "username"), "", "The username to authenticate with the MQTT broker")
	f.StringVar(&cfg.Password, util.PrefixConfig(prefix, "password"), "", "The password to authenticate with the MQTT broker")
	cfg.Backoff.RegisterFlagsWithPrefix(util.PrefixConfig(prefix, "backoff"), f)
}
