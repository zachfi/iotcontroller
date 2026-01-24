package zigbeedongle

// StackType identifies which Zigbee stack implementation to use.
type StackType string

const (
	// StackTypeZNP is the Z-Stack Network Processor protocol (Texas Instruments CC253X)
	StackTypeZNP StackType = "znp"
	// StackTypeEmber is the Ember stack (uses EZSP protocol, Silicon Labs coordinators)
	StackTypeEmber StackType = "ember"
)

// Config holds configuration for a zigbee dongle.
type Config struct {
	// StackType specifies which Zigbee stack implementation to use.
	// Defaults to "znp" if not specified.
	StackType StackType `yaml:"stack_type,omitempty"`

	// Port is the serial port path (e.g., "/dev/ttyUSB0")
	Port string `yaml:"port,omitempty"`

	// BaudRate is the serial communication baud rate (default: 115200)
	BaudRate int `yaml:"baud_rate,omitempty"`

	// DisableFlowControl disables RTS/CTS hardware flow control
	// Some Z-Stack 3.x devices have flow control disabled by default
	DisableFlowControl bool `yaml:"disable_flow_control,omitempty"`

	// LogCommands enables logging of all commands sent/received
	LogCommands bool `yaml:"log_commands,omitempty"`

	// LogErrors enables logging of protocol errors
	LogErrors bool `yaml:"log_errors,omitempty"`
}
