package ember

import (
	"encoding/binary"
	"fmt"
)

// EZSPFrameControl represents the frame control byte in an EZSP frame.
// In both legacy and extended formats, bit 7 of fcLo = direction:
//
//	0 = host→NCP (command), 1 = NCP→host (response or async callback).
type EZSPFrameControl byte

const (
	EZSP_FRAME_CONTROL_COMMAND  EZSPFrameControl = 0x00 // bit 7 = 0: host→NCP
	EZSP_FRAME_CONTROL_RESPONSE EZSPFrameControl = 0x80 // bit 7 = 1: NCP→host
)

// EZSP_EXTENDED_FRAME_FORMAT_VERSION is the value that must be in bits[0:1] of
// the frameControlHi (fcHi, byte[2]) byte of all extended-format frames.
// The NCP uses this to detect extended vs legacy format: fcHi & 0x03 == 0x01.
const EZSP_EXTENDED_FRAME_FORMAT_VERSION byte = 0x01

// EZSPFrameID represents the EZSP command/response ID.
// Extended format supports 2-byte frame IDs (uint16); legacy supports only 1-byte.
type EZSPFrameID uint16

// Common EZSP frame IDs (from UG100 / Silicon Labs EZSP spec).
// Commands 0x0100+ require extended format (2-byte frame ID).
const (
	EZSP_VERSION                       EZSPFrameID = 0x00
	EZSP_ADD_ENDPOINT                  EZSPFrameID = 0x02
	EZSP_GET_CONFIGURATION_VALUE       EZSPFrameID = 0x52
	EZSP_SET_CONFIGURATION_VALUE       EZSPFrameID = 0x53
	EZSP_SET_POLICY                    EZSPFrameID = 0x55
	EZSP_GET_POLICY                    EZSPFrameID = 0x56
	EZSP_SET_MANUFACTURER_CODE         EZSPFrameID = 0x15
	EZSP_NETWORK_INIT                  EZSPFrameID = 0x17
	EZSP_TRUST_CENTER_JOIN_HANDLER     EZSPFrameID = 0x24
	EZSP_NETWORK_STATE                 EZSPFrameID = 0x18
	EZSP_STACK_STATUS_HANDLER          EZSPFrameID = 0x19
	EZSP_SET_EXTENDED_SECURITY_BITMASK EZSPFrameID = 0x6A
	EZSP_START_SCAN                    EZSPFrameID = 0x1A
	EZSP_STOP_SCAN                     EZSPFrameID = 0x1B
	EZSP_FORM_NETWORK                  EZSPFrameID = 0x1E
	EZSP_LEAVE_NETWORK                 EZSPFrameID = 0x20
	EZSP_PERMIT_JOINING                EZSPFrameID = 0x22
	EZSP_GET_EUI64                     EZSPFrameID = 0x26
	EZSP_GET_NODE_ID                   EZSPFrameID = 0x27
	EZSP_GET_NETWORK_PARAMETERS        EZSPFrameID = 0x28
	EZSP_GET_PARENT_CHILD_PARAMETERS   EZSPFrameID = 0x29
	EZSP_SET_INITIAL_SECURITY_STATE    EZSPFrameID = 0x68
	EZSP_CLEAR_KEY_TABLE               EZSPFrameID = 0xB1
	EZSP_SEND_UNICAST                  EZSPFrameID = 0x34
	EZSP_SEND_MULTICAST                EZSPFrameID = 0x38
	EZSP_MESSAGE_SENT_HANDLER          EZSPFrameID = 0x3F
	EZSP_INCOMING_MESSAGE_HANDLER      EZSPFrameID = 0x45

	EZSP_SET_VALUE                 EZSPFrameID = 0xAB // Set an NCP value
	EZSP_SET_CONCENTRATOR          EZSPFrameID = 0x10 // Configure concentrator mode
	EZSP_SET_SOURCE_ROUTE_DISC     EZSPFrameID = 0x5A // Set source route discovery mode
	EZSP_SET_MULTICAST_TABLE_ENTRY EZSPFrameID = 0x64 // Set multicast table entry

	// Extended-format-only commands (frame ID > 0xFF, requires 2-byte ID in 5-byte header).
	EZSP_IMPORT_TRANSIENT_KEY      EZSPFrameID = 0x0111 // Add transient link key for joining
	EZSP_CLEAR_TRANSIENT_LINK_KEYS EZSPFrameID = 0x006B // Remove all transient link keys
)

// EZSPConfigId identifies an NCP configuration parameter (EZSP_SET_CONFIGURATION_VALUE).
type EZSPConfigId uint8

// Configuration IDs (from UG100 / AN706 Table 4).
const (
	EZSP_CONFIG_STACK_PROFILE                   EZSPConfigId = 0x0C // ZigBee Pro = 2
	EZSP_CONFIG_SECURITY_LEVEL                  EZSPConfigId = 0x0D // AES-128-ENC+MIC-32 = 5
	EZSP_CONFIG_INDIRECT_TRANSMISSION_TIMEOUT   EZSPConfigId = 0x12 // MAC indirect timeout (ms)
	EZSP_CONFIG_SUPPORTED_NETWORKS              EZSPConfigId = 0x3E // 1 network
	EZSP_CONFIG_TRUST_CENTER_ADDRESS_CACHE_SIZE EZSPConfigId = 0x19 // 2
	EZSP_CONFIG_MAX_HOPS                        EZSPConfigId = 0x20 // 30
	EZSP_CONFIG_MAX_END_DEVICE_CHILDREN         EZSPConfigId = 0x03 // max sleepy end devices
	EZSP_CONFIG_END_DEVICE_POLL_TIMEOUT         EZSPConfigId = 0x44 // end device poll timeout (seconds exponent)
	EZSP_CONFIG_TRANSIENT_KEY_TIMEOUT_S         EZSPConfigId = 0x36 // transient key timeout
)

// EZSPPolicyId identifies an NCP policy (EZSP_SET_POLICY).
type EZSPPolicyId uint8

const (
	EZSP_POLICY_TRUST_CENTER    EZSPPolicyId = 0x00
	EZSP_POLICY_TC_KEY_REQUEST  EZSPPolicyId = 0x05 // TC_KEY_REQUEST_POLICY
	EZSP_POLICY_APP_KEY_REQUEST EZSPPolicyId = 0x06 // APP_KEY_REQUEST_POLICY
	EZSP_POLICY_MSG_CONTENTS_CB EZSPPolicyId = 0x03 // MESSAGE_CONTENTS_IN_CALLBACK_POLICY
)

// Trust center decision bitmask values (EZSP protocol 8+, EmberZNet 7.x).
// Used as the decision byte in EZSP_SET_POLICY for EZSP_POLICY_TRUST_CENTER.
// Authoritative source: zigbee-herdsman EzspDecisionBitmask enum.
const (
	EZSP_TC_DECISION_ALLOW_JOINS              uint8 = 0x01 // bit 0: send network key to all joining devices
	EZSP_TC_DECISION_ALLOW_UNSECURED_REJOINS  uint8 = 0x02 // bit 1: send network key to all rejoining devices
	EZSP_TC_DECISION_SEND_KEY_IN_CLEAR        uint8 = 0x04 // bit 2: send the network key in the clear
	EZSP_TC_DECISION_IGNORE_UNSECURED_REJOINS uint8 = 0x08 // bit 3: do nothing for unsecured rejoins
	EZSP_TC_DECISION_JOINS_USE_INSTALL_CODES  uint8 = 0x10 // bit 4: allow joins if entry in transient key table
	EZSP_TC_DECISION_DEFER_JOINS              uint8 = 0x20 // bit 5: delay sending network key to new joining device
)

// EZSPFrame represents an EZSP command or response frame.
type EZSPFrame struct {
	Sequence   uint8
	Control    EZSPFrameControl
	FrameID    EZSPFrameID
	Parameters []byte
}

// ParseEZSPFrame parses a legacy (3-byte header) EZSP frame.
// Used only for the initial VERSION command/response before version negotiation.
func ParseEZSPFrame(data []byte) (*EZSPFrame, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("EZSP frame too short: %d bytes", len(data))
	}

	return &EZSPFrame{
		Sequence:   data[0],
		Control:    EZSPFrameControl(data[1]),
		FrameID:    EZSPFrameID(data[2]),
		Parameters: data[3:],
	}, nil
}

// ParseEZSPFrameExtended parses an extended-format (5-byte header) EZSP frame.
// Extended format is used for EZSP protocol v8+ (EmberZNet 7.x uses protocol 13).
// Header: [sequence (1)] [frameControlLo (1)] [frameControlHi (1)] [frameIDLo (1)] [frameIDHi (1)]
// Detection: frameControlHi & 0x03 == EZSP_EXTENDED_FRAME_FORMAT_VERSION (0x01).
func ParseEZSPFrameExtended(data []byte) (*EZSPFrame, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("extended EZSP frame too short: %d bytes", len(data))
	}
	// frameControlLo bit 7: 0=host→NCP (command), 1=NCP→host (response or async callback).
	var ctrl EZSPFrameControl
	if data[1]&0x80 != 0 {
		ctrl = EZSP_FRAME_CONTROL_RESPONSE
	}
	return &EZSPFrame{
		Sequence:   data[0],
		Control:    ctrl,
		FrameID:    EZSPFrameID(data[3]) | EZSPFrameID(data[4])<<8,
		Parameters: data[5:],
	}, nil
}

// SerializeEZSPFrame serializes a legacy (3-byte header) EZSP frame.
// Used only for the initial VERSION command before extended format is negotiated.
func SerializeEZSPFrame(frame *EZSPFrame) []byte {
	data := make([]byte, 0, 3+len(frame.Parameters))
	data = append(data, frame.Sequence)
	data = append(data, byte(frame.Control))
	data = append(data, byte(frame.FrameID))
	data = append(data, frame.Parameters...)
	return data
}

// SerializeEZSPFrameExtended serializes an extended-format (5-byte header) EZSP frame.
// All commands after VERSION handshake use this format.
// Header: [sequence (1)] [frameControlLo=0x00 (1)] [frameControlHi=0x01 (1)] [frameIDLo (1)] [frameIDHi (1)]
// frameControlHi must be EZSP_EXTENDED_FRAME_FORMAT_VERSION (0x01) so the NCP detects extended format.
func SerializeEZSPFrameExtended(frame *EZSPFrame) []byte {
	// fcLo = 0x00 for host commands (bit 7 = 0 = host→NCP direction).
	// fcHi = EZSP_EXTENDED_FRAME_FORMAT_VERSION (0x01): bits[0:1] = 0x01 signals extended format to NCP.
	data := make([]byte, 0, 5+len(frame.Parameters))
	data = append(data, frame.Sequence)
	data = append(data, 0x00)                               // frameControlLo: command direction
	data = append(data, EZSP_EXTENDED_FRAME_FORMAT_VERSION) // frameControlHi: 0x01 = extended format
	data = append(data, byte(frame.FrameID))                // frameIDLo
	data = append(data, byte(frame.FrameID>>8))             // frameIDHi
	data = append(data, frame.Parameters...)
	return data
}

// EZSPVersionResponse represents the response to EZSP_VERSION command.
type EZSPVersionResponse struct {
	ProtocolVersion uint8
	StackType       uint8
	StackVersion    uint16
}

// ParseVersionResponse parses the version response parameters.
func ParseVersionResponse(params []byte) (*EZSPVersionResponse, error) {
	if len(params) < 4 {
		return nil, fmt.Errorf("version response too short: %d bytes", len(params))
	}

	return &EZSPVersionResponse{
		ProtocolVersion: params[0],
		StackType:       params[1],
		StackVersion:    binary.LittleEndian.Uint16(params[2:4]),
	}, nil
}
