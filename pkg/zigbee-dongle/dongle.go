// Package zigbeedongle provides a unified interface for interacting with Zigbee coordinator dongles.
//
// Architecture Layers:
//
// 1. Serial Protocol Layer (stack-specific):
//   - Z-Stack uses ZNP (Z-Stack Network Processor) protocol
//   - Ember uses EZSP (EmberZNet Serial Protocol) over ASH framing
//   - Each stack has its own serial framing, command structure, and handshake
//
// 2. Zigbee Network Layer (stack-agnostic):
//   - Once we extract the APS (Application Support) payload from the serial protocol,
//     we arrive at standard Zigbee network frames
//   - These contain: source/destination addresses, endpoints, cluster IDs, and payload data
//
// 3. Application Data Objects (stack-agnostic):
//   - IncomingMessage and OutgoingMessage represent the same Zigbee application data
//   - Regardless of whether it came from ZNP, EZSP, or any other stack
//   - These are the data structures that can be wrapped in protobuf for gRPC
//
// This package abstracts away the serial protocol differences, providing a unified
// interface that works with any Zigbee stack implementation.
package zigbeedongle

import (
	"context"
	"io"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// Dongle is the interface for interacting with a Zigbee coordinator dongle.
// Implementations handle the stack-specific serial protocol (ZNP, Ember, etc.)
// and present a unified view of Zigbee network operations.
type Dongle interface {
	io.Closer

	// Start initializes the controller and returns a channel of incoming messages.
	// The channel will be closed when the controller stops or encounters an error.
	Start(ctx context.Context) (<-chan types.IncomingMessage, error)

	// Send sends a message to a device on the Zigbee network.
	Send(ctx context.Context, msg types.OutgoingMessage) error

	// PermitJoining enables or disables device joining on the network.
	PermitJoining(ctx context.Context, enabled bool) error

	// GetNetworkInfo returns information about the current network state.
	GetNetworkInfo(ctx context.Context) (*types.NetworkInfo, error)

	// FormNetwork creates a new Zigbee network with the specified parameters.
	// This should be called before Start() if the device is not already part of a network.
	// The network parameters should be persisted so the same network can be restored
	// when swapping devices.
	FormNetwork(ctx context.Context, params types.NetworkParameters) error
}

// Re-export types for convenience
type (
	AddressMode       = types.AddressMode
	MACAddress        = types.MACAddress
	Address           = types.Address
	IncomingMessage   = types.IncomingMessage
	OutgoingMessage   = types.OutgoingMessage
	ProfileID         = types.ProfileID
	NetworkInfo       = types.NetworkInfo
	NetworkParameters = types.NetworkParameters
	NetworkState      = types.NetworkState
)

// Re-export network state constants
const (
	NetworkStateUnknown   = types.NetworkStateUnknown
	NetworkStateUp        = types.NetworkStateUp
	NetworkStateDown      = types.NetworkStateDown
	NetworkStateNotJoined = types.NetworkStateNotJoined
)

// Re-export constants
const (
	AddressModeNone     = types.AddressModeNone
	AddressModeGroup    = types.AddressModeGroup
	AddressModeNWK      = types.AddressModeNWK
	AddressModeIEEE     = types.AddressModeIEEE
	AddressModeCombined = types.AddressModeCombined

	ProfileDevice                       = types.ProfileDevice
	ProfileIndustrialPlantMonitoring    = types.ProfileIndustrialPlantMonitoring
	ProfileHomeAutomation               = types.ProfileHomeAutomation
	ProfileCommercialBuildingAutomation = types.ProfileCommercialBuildingAutomation
	ProfileTelecomApplications          = types.ProfileTelecomApplications
	ProfilePersonalHomeAndHospitalCare  = types.ProfilePersonalHomeAndHospitalCare
	ProfileAdvancedMeteringInitialtive  = types.ProfileAdvancedMeteringInitialtive
)
