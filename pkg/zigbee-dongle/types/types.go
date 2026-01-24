package types

import "fmt"

// AddressMode specifies how a Zigbee address should be interpreted.
type AddressMode byte

const (
	AddressModeNone     AddressMode = 0x00
	AddressModeGroup    AddressMode = 0x01
	AddressModeNWK      AddressMode = 0x02
	AddressModeIEEE     AddressMode = 0x03
	AddressModeCombined AddressMode = 0x04
)

func (m AddressMode) String() string {
	switch m {
	case AddressModeNone:
		return "None"
	case AddressModeGroup:
		return "Group"
	case AddressModeNWK:
		return "NWK"
	case AddressModeIEEE:
		return "IEEE"
	case AddressModeCombined:
		return "Combined"
	default:
		return "Unknown"
	}
}

// MACAddress represents a 64-bit IEEE MAC address.
type MACAddress uint64

func (a MACAddress) String() string {
	return fmt.Sprintf("%016x", uint64(a))
}

// Address represents a Zigbee network address.
type Address struct {
	Mode     AddressMode
	Short    uint16
	Extended MACAddress
}

// IncomingMessage represents a message received from a device on the network.
type IncomingMessage struct {
	Source              Address
	SourceEndpoint      uint8
	DestinationEndpoint uint8
	ClusterID           uint16
	LinkQuality         uint8
	Data                []byte
}

// OutgoingMessage represents a message to be sent to a device on the network.
type OutgoingMessage struct {
	Destination         Address
	DestinationEndpoint uint8
	SourceEndpoint      uint8
	ClusterID           uint16
	Radius              uint8
	Data                []byte
}

// ProfileID represents a Zigbee application profile identifier.
type ProfileID uint16

const (
	// ProfileDevice is the Zigbee Device Profile
	ProfileDevice                       ProfileID = 0x0000
	ProfileIndustrialPlantMonitoring    ProfileID = 0x0101
	ProfileHomeAutomation               ProfileID = 0x0104
	ProfileCommercialBuildingAutomation ProfileID = 0x0105
	ProfileTelecomApplications          ProfileID = 0x0107
	ProfilePersonalHomeAndHospitalCare  ProfileID = 0x0108
	ProfileAdvancedMeteringInitialtive  ProfileID = 0x0109
)

func (id ProfileID) String() string {
	switch id {
	case ProfileDevice:
		return "ZDP"
	case ProfileIndustrialPlantMonitoring:
		return "IPM"
	case ProfileHomeAutomation:
		return "HA"
	case ProfileCommercialBuildingAutomation:
		return "CBA"
	case ProfileTelecomApplications:
		return "TA"
	case ProfilePersonalHomeAndHospitalCare:
		return "PHHC"
	case ProfileAdvancedMeteringInitialtive:
		return "AMI"
	default:
		return fmt.Sprintf("0x%04x", uint16(id))
	}
}

// NetworkInfo contains information about the current network state.
type NetworkInfo struct {
	ShortAddress          uint16
	PanID                 uint16
	ParentAddress         uint16
	ExtendedPanID         uint64
	ExtendedParentAddress uint64
	Channel               uint16
	State                 string
}

// NetworkParameters defines the parameters for forming or joining a network.
type NetworkParameters struct {
	PanID         uint16  // 16-bit PAN ID
	ExtendedPanID uint64  // 64-bit Extended PAN ID
	Channel       uint8   // Radio channel (11-26 for 2.4GHz)
	NetworkKey    [16]byte // 128-bit Network Key for encryption
}

// NetworkState represents the persisted network state that can be saved/loaded.
// This allows swapping devices while maintaining the same network.
type NetworkState struct {
	PanID         uint16
	ExtendedPanID uint64
	Channel       uint8
	NetworkKey    [16]byte
}
