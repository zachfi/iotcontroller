package ember

import (
	"encoding/binary"
	"fmt"
)

// EZSPFrameControl represents the frame control byte in an EZSP frame.
// In legacy format (protocol < 8): 1 byte. Bit 7 = direction.
// In extended format (protocol >= 8): 2 bytes. Bit 0 = frame type (0=cmd, 1=resp).
type EZSPFrameControl byte

const (
	EZSP_FRAME_CONTROL_COMMAND  EZSPFrameControl = 0x00 // legacy: cmd (bit7=0); extended: frame type command (bit0=0)
	EZSP_FRAME_CONTROL_RESPONSE EZSPFrameControl = 0x80 // legacy: response (bit7=1); extended: frame type response (bit0=1)
)

// EZSPFrameID represents the EZSP command/response ID.
type EZSPFrameID uint8

// Common EZSP frame IDs (from UG100)
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
)

// EZSPConfigId identifies an NCP configuration parameter (EZSP_SET_CONFIGURATION_VALUE).
type EZSPConfigId uint8

// Configuration IDs (from UG100 / AN706 Table 4).
const (
	EZSP_CONFIG_STACK_PROFILE                   EZSPConfigId = 0x0C // ZigBee Pro = 2
	EZSP_CONFIG_SECURITY_LEVEL                  EZSPConfigId = 0x0D // AES-128-ENC+MIC-32 = 5
	EZSP_CONFIG_SUPPORTED_NETWORKS              EZSPConfigId = 0x3E // 1 network
	EZSP_CONFIG_TRUST_CENTER_ADDRESS_CACHE_SIZE EZSPConfigId = 0x19 // 2
	EZSP_CONFIG_MAX_HOPS                        EZSPConfigId = 0x20 // 30
)

// EZSPPolicyId identifies an NCP policy (EZSP_SET_POLICY).
type EZSPPolicyId uint8

const (
	EZSP_POLICY_TRUST_CENTER    EZSPPolicyId = 0x00
	EZSP_POLICY_APP_KEY_REQUEST EZSPPolicyId = 0x04
)

// Trust center decision bitmask values (EZSP protocol 9+, EmberZNet 7.x).
// Used as the decision byte in EZSP_SET_POLICY for EZSP_POLICY_TRUST_CENTER.
const (
	EZSP_TC_DECISION_ALLOW_JOINS             uint8 = 0x01
	EZSP_TC_DECISION_ALLOW_UNSECURED_REJOINS uint8 = 0x02
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
func ParseEZSPFrameExtended(data []byte) (*EZSPFrame, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("extended EZSP frame too short: %d bytes", len(data))
	}
	// frameControlLo bit 0: 0=command, 1=response. Map to our EZSPFrameControl.
	var ctrl EZSPFrameControl
	if data[1]&0x01 != 0 {
		ctrl = EZSP_FRAME_CONTROL_RESPONSE
	}
	return &EZSPFrame{
		Sequence:   data[0],
		Control:    ctrl,
		FrameID:    EZSPFrameID(data[3]), // frameIDLo (frameIDHi at data[4] is always 0x00)
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
// Extended format is used for EZSP protocol v8+ after the VERSION handshake.
// Header: [sequence (1)] [frameControlLo (1)] [frameControlHi=0x00 (1)] [frameIDLo (1)] [frameIDHi=0x00 (1)]
func SerializeEZSPFrameExtended(frame *EZSPFrame) []byte {
	// frameControlLo: bit 0 = frame type (0=command, 1=response)
	var fcLo byte
	if frame.Control == EZSP_FRAME_CONTROL_RESPONSE {
		fcLo = 0x01
	}
	data := make([]byte, 0, 5+len(frame.Parameters))
	data = append(data, frame.Sequence)
	data = append(data, fcLo)
	data = append(data, 0x00)              // frameControlHi
	data = append(data, byte(frame.FrameID)) // frameIDLo
	data = append(data, 0x00)              // frameIDHi (always 0 for standard commands)
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
