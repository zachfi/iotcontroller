package zigbeedongle

// StackType identifies which Zigbee stack implementation to use.
type StackType string

const (
	// StackTypeZNP is the Z-Stack Network Processor protocol (Texas Instruments CC253X)
	StackTypeZNP StackType = "znp"
	// StackTypeEZSP is the EmberZNet Serial Protocol (Silicon Labs coordinators)
	// Not yet implemented
	StackTypeEZSP StackType = "ezsp"
)

// Config holds configuration for a zigbee dongle.
type Config struct {
	// StackType specifies which Zigbee stack implementation to use.
	// Defaults to "znp" if not specified.
	StackType StackType `yaml:"stack_type,omitempty"`

	// Port is the serial port path (e.g., "/dev/ttyUSB0")
	Port string `yaml:"port,omitempty"`

	// LogCommands enables logging of all commands sent/received
	LogCommands bool `yaml:"log_commands,omitempty"`

	// LogErrors enables logging of protocol errors
	LogErrors bool `yaml:"log_errors,omitempty"`
}
