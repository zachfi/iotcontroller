package zigbeecoordinator

import (
	"flag"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	Port        string `yaml:"port,omitempty"`
	LogCommands bool   `yaml:"log_commands,omitempty"`
	LogErrors   bool   `yaml:"log_errors,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.Port, util.PrefixConfig(prefix, "port"), "/dev/ttyUSB0", "The serial port for the Zigbee coordinator")
	f.BoolVar(&cfg.LogCommands, util.PrefixConfig(prefix, "log-commands"), false, "Enable logging of all Zigbee commands sent/received")
	f.BoolVar(&cfg.LogErrors, util.PrefixConfig(prefix, "log-errors"), true, "Enable logging of Zigbee protocol errors")
}

func (cfg *Config) ToDongleConfig() zigbeedongle.Config {
	return zigbeedongle.Config{
		StackType:   zigbeedongle.StackTypeZNP, // Default to ZNP for now
		Port:        cfg.Port,
		LogCommands: cfg.LogCommands,
		LogErrors:   cfg.LogErrors,
	}
}
