# zigbee-dongle Package

This package provides a unified interface for interacting with Zigbee coordinator dongles, abstracting away the differences between various Zigbee stack implementations.

## Architecture

The package is organized in layers:

### Layer 1: Serial Protocol (Stack-Specific)

Each Zigbee stack uses a different serial protocol to communicate with the USB dongle:

- **Z-Stack (ZNP)**: Uses Z-Stack Network Processor protocol
  - Frame format: SOF + Length + Command + Data + FCS
  - Commands organized by subsystem (SYS, AF, ZDO, etc.)
  - Used by: Texas Instruments CC253X-based dongles, Sonoff Dongle Plus P

- **Ember**: Uses EmberZNet Serial Protocol (EZSP) over ASH framing
  - ASH (Asynchronous Serial Host) framing layer
  - EZSP frames with sequence numbers and frame IDs
  - Used by: Silicon Labs coordinators, Sonoff Dongle Max, Sonoff ZBDongle-E

- **Future stacks**: ConBee, etc.

### Layer 2: Zigbee Network Layer (Stack-Agnostic)

Once we extract the APS (Application Support) payload from the serial protocol, we arrive at standard Zigbee network frames containing:
- Source and destination addresses
- Endpoints
- Cluster IDs
- Link quality
- Application payload data

### Layer 3: Application Data Objects (Stack-Agnostic)

The `IncomingMessage` and `OutgoingMessage` types represent the same Zigbee application data regardless of which stack produced them. These are the structures that can be:
- Used directly in application code
- Wrapped in protobuf messages for gRPC
- Serialized for storage or transmission

## Package Structure

```
pkg/zigbee-dongle/
├── dongle.go          # Core interface and stack-agnostic types
├── config.go          # Configuration structure
├── factory.go         # Factory function to create dongles
├── scf/               # Simple Command Format serialization (used by ZNP)
│   └── scf.go
├── znp/               # Z-Stack implementation
│   ├── frame.go       # ZNP frame serialization
│   ├── command.go     # Command registration system
│   ├── port.go        # Serial port communication
│   ├── commands.go    # ZNP command definitions
│   └── controller.go  # ZNP controller implementing Dongle interface
└── ember/             # Ember stack implementation
    ├── frame.go       # ASH frame serialization
    ├── ezsp_frame.go  # EZSP frame parsing
    ├── port.go        # Serial port communication
    └── controller.go  # Ember controller implementing Dongle interface
```

## Usage

```go
import "github.com/zachfi/iotcontroller/pkg/zigbee-dongle"

// Create configuration
cfg := zigbeedongle.Config{
    Port: "/dev/ttyUSB0",
    LogCommands: false,
    LogErrors: true,
}

// Factory creates the appropriate implementation based on config
dongle, err := zigbeedongle.NewDongle(cfg)
if err != nil {
    log.Fatal(err)
}
defer dongle.Close()

// Start receiving messages (stack-agnostic interface)
messages, err := dongle.Start(ctx)
if err != nil {
    log.Fatal(err)
}

// All operations use the same interface regardless of stack
for msg := range messages {
    // Handle IncomingMessage - same structure for ZNP, Ember, etc.
    fmt.Printf("Received from %v: cluster=%d, data=%x\n",
        msg.Source, msg.ClusterID, msg.Data)
}

// Send message
err = dongle.Send(ctx, zigbeedongle.OutgoingMessage{
    Destination: zigbeedongle.Address{
        Mode:  zigbeedongle.AddressModeNWK,
        Short: 0x1234,
    },
    DestinationEndpoint: 1,
    SourceEndpoint:      1,
    ClusterID:           0x0006, // OnOff cluster
    Data:                []byte{0x01}, // Turn on
})

// Permit joining
err = dongle.PermitJoining(ctx, true)

// Get network info
info, err := dongle.GetNetworkInfo(ctx)
```

## Adding New Stack Implementations

To add support for a new Zigbee stack (e.g., Ember):

1. Create a new subdirectory (e.g., `ember/`)
2. Implement the stack-specific serial protocol handling
3. Implement the `Dongle` interface, converting stack-specific frames to `IncomingMessage`/`OutgoingMessage`
4. Update `factory.go` to support the new stack type

The key is that all implementations produce the same `IncomingMessage` and `OutgoingMessage` structures, making the rest of the application stack-agnostic.
