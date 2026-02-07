package zigbeecoordinator

import (
	"flag"

	"github.com/zachfi/iotcontroller/internal/common"
	zigbeedongle "github.com/zachfi/iotcontroller/pkg/zigbee-dongle"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	StackType          string              `yaml:"stack_type,omitempty"` // "znp" or "ember"
	Port               string              `yaml:"port,omitempty"`
	BaudRate           int                 `yaml:"baud_rate,omitempty"`            // Default: 115200
	DisableFlowControl bool                `yaml:"disable_flow_control,omitempty"` // Disable RTS/CTS for Z-Stack 3.x
	LogCommands        bool                `yaml:"log_commands,omitempty"`
	LogErrors          bool                `yaml:"log_errors,omitempty"`
	StateFile          string              `yaml:"state_file,omitempty"`    // Path to network state file (default: zigbee-network-state.yaml)
	RouterClient       common.ClientConfig `yaml:"router_client,omitempty"` // gRPC client config for router connection
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.StackType, util.PrefixConfig(prefix, "stack-type"), "znp", "The Zigbee stack type: 'znp' (Z-Stack) or 'ember' (Ember/EZSP)")
	f.StringVar(&cfg.Port, util.PrefixConfig(prefix, "port"), "/dev/ttyUSB0", "The serial port for the Zigbee coordinator")
	f.IntVar(&cfg.BaudRate, util.PrefixConfig(prefix, "baud-rate"), 115200, "The baud rate for serial communication (default: 115200)")
	f.BoolVar(&cfg.DisableFlowControl, util.PrefixConfig(prefix, "disable-flow-control"), false, "Disable RTS/CTS flow control (needed for some Z-Stack 3.x devices)")
	f.BoolVar(&cfg.LogCommands, util.PrefixConfig(prefix, "log-commands"), false, "Enable logging of all Zigbee commands sent/received")
	f.BoolVar(&cfg.LogErrors, util.PrefixConfig(prefix, "log-errors"), true, "Enable logging of Zigbee protocol errors")
	f.StringVar(&cfg.StateFile, util.PrefixConfig(prefix, "state-file"), "network.yaml", "Path to network state file for persistence across device swaps")
	cfg.RouterClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "router-client"), f)
}

func (cfg *Config) ToDongleConfig() zigbeedongle.Config {
	stackType := zigbeedongle.StackType(cfg.StackType)
	if stackType == "" {
		stackType = zigbeedongle.StackTypeZNP // Default to ZNP
	}

	return zigbeedongle.Config{
		StackType:          stackType,
		Port:               cfg.Port,
		BaudRate:           cfg.BaudRate,
		DisableFlowControl: cfg.DisableFlowControl,
		LogCommands:        cfg.LogCommands,
		LogErrors:          cfg.LogErrors,
	}
}
