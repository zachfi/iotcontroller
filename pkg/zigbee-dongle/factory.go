package zigbeedongle

import (
	"fmt"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/ember"
	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/znp"
)

// NewDongle creates a new Dongle implementation based on the provided config.
// The stack type is determined by cfg.StackType (defaults to ZNP if not specified).
//
// Each stack implementation handles its own serial protocol:
//   - ZNP: Z-Stack Network Processor protocol
//   - Ember: Ember stack (uses EZSP protocol)
//
// All implementations produce the same stack-agnostic IncomingMessage and OutgoingMessage types.
func NewDongle(cfg Config) (Dongle, error) {
	stackType := cfg.StackType
	if stackType == "" {
		stackType = StackTypeZNP // Default to ZNP
	}

	switch stackType {
	case StackTypeZNP:
		settings := znp.Settings{
			Port:               cfg.Port,
			BaudRate:           cfg.BaudRate,
			DisableFlowControl: cfg.DisableFlowControl,
			LogCommands:        cfg.LogCommands,
			LogErrors:          cfg.LogErrors,
		}

		controller, err := znp.NewController(settings)
		if err != nil {
			return nil, fmt.Errorf("failed to create ZNP controller: %w", err)
		}

		return controller, nil

	case StackTypeEmber:
		settings := ember.Settings{
			Port:               cfg.Port,
			BaudRate:           cfg.BaudRate,
			DisableFlowControl: cfg.DisableFlowControl,
			LogCommands:        cfg.LogCommands,
			LogErrors:          cfg.LogErrors,
		}

		controller, err := ember.NewController(settings)
		if err != nil {
			return nil, fmt.Errorf("failed to create Ember controller: %w", err)
		}

		return controller, nil

	default:
		return nil, fmt.Errorf("unknown stack type: %q (supported: %q, %q)", stackType, StackTypeZNP, StackTypeEmber)
	}
}
