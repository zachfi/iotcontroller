package zigbeecoordinator

import (
	"flag"
	"log/slog"

	"github.com/zachfi/iotcontroller/internal/common"
	zigbeedongle "github.com/zachfi/iotcontroller/pkg/zigbee-dongle"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	StackType            string              `yaml:"stack_type,omitempty"`              // "znp" or "ember"
	Port                 string              `yaml:"port,omitempty"`                    // Serial port (e.g. /dev/ttyUSB0). TODO: avoid requiring root; add udev rule so dialout group can access the device.
	BaudRate             int                 `yaml:"baud_rate,omitempty"`               // Default: 115200
	DisableFlowControl   bool                `yaml:"disable_flow_control,omitempty"`    // Disable RTS/CTS for Z-Stack 3.x
	EmberSkipForcedLeave bool                `yaml:"ember_skip_forced_leave,omitempty"` // Ember: skip LEAVE before NETWORK_INIT (match zigbee-herdsman order; try if 0x58 persists)
	SkipHardwareReset    bool                `yaml:"skip_hardware_reset,omitempty"`     // Skip DTR reset before magic byte (match old/znp; try if device unresponsive after reset)
	LogCommands          bool                `yaml:"log_commands,omitempty"`
	LogErrors            bool                `yaml:"log_errors,omitempty"`
	StateFile            string              `yaml:"state_file,omitempty"`    // Path to network state file (default: zigbee-network-state.yaml)
	ForceForm            bool                `yaml:"force_form,omitempty"`    // If true, leave current network (if any) and form from config or generate new params; use to get a secure key (devices must re-join).
	RouterClient         common.ClientConfig `yaml:"router_client,omitempty"` // gRPC client config for router connection
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.StackType, util.PrefixConfig(prefix, "stack-type"), "znp", "The Zigbee stack type: 'znp' (Z-Stack) or 'ember' (Ember/EZSP)")
	f.StringVar(&cfg.Port, util.PrefixConfig(prefix, "port"), "/dev/ttyUSB0", "The serial port for the Zigbee coordinator")
	f.IntVar(&cfg.BaudRate, util.PrefixConfig(prefix, "baud-rate"), 115200, "The baud rate for serial communication (default: 115200)")
	f.BoolVar(&cfg.DisableFlowControl, util.PrefixConfig(prefix, "disable-flow-control"), false, "Disable RTS/CTS flow control (needed for some Z-Stack 3.x devices)")
	f.BoolVar(&cfg.EmberSkipForcedLeave, util.PrefixConfig(prefix, "ember-skip-forced-leave"), false, "Ember: skip LEAVE before NETWORK_INIT (match zigbee-herdsman; try if formation returns 0x58)")
	f.BoolVar(&cfg.SkipHardwareReset, util.PrefixConfig(prefix, "skip-hardware-reset"), false, "Skip DTR hardware reset before magic byte (old/znp does not reset; try if device unresponsive)")
	f.BoolVar(&cfg.LogCommands, util.PrefixConfig(prefix, "log-commands"), false, "Enable logging of all Zigbee commands sent/received")
	f.BoolVar(&cfg.LogErrors, util.PrefixConfig(prefix, "log-errors"), true, "Enable logging of Zigbee protocol errors")
	f.StringVar(&cfg.StateFile, util.PrefixConfig(prefix, "state-file"), "network.yaml", "Path to network state file for persistence across device swaps")
	f.BoolVar(&cfg.ForceForm, util.PrefixConfig(prefix, "force-form"), false, "Form network from config (or generate) even if device already on a network; use to establish a secure key (devices must re-join)")
	cfg.RouterClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "router-client"), f)
}

// ToDongleConfig builds the dongle config from module config. Pass the module's logger so zigbee/znp logs have context.
func (cfg *Config) ToDongleConfig(logger *slog.Logger) zigbeedongle.Config {
	stackType := zigbeedongle.StackType(cfg.StackType)
	if stackType == "" {
		stackType = zigbeedongle.StackTypeZNP // Default to ZNP
	}

	return zigbeedongle.Config{
		StackType:            stackType,
		Port:                 cfg.Port,
		BaudRate:             cfg.BaudRate,
		DisableFlowControl:   cfg.DisableFlowControl,
		EmberSkipForcedLeave: cfg.EmberSkipForcedLeave,
		SkipHardwareReset:    cfg.SkipHardwareReset,
		LogCommands:          cfg.LogCommands,
		LogErrors:            cfg.LogErrors,
		Logger:               logger,
	}
}
