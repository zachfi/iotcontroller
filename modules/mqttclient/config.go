package mqttclient

import (
	"flag"

	"github.com/zachfi/iotcontroller/pkg/iot"
)

type Config struct {
	MQTT iot.MQTTConfig `yaml:"mqtt,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	// f.StringVar(
	// 	&cfg.ServerAddress,
	// 	util.PrefixConfig(prefix, "server-address"),
	// 	":9090",
	// 	"The address of the server to connect to",
	// )
}
