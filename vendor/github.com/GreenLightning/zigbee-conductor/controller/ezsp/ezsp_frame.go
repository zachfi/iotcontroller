package ezsp

import (
	"encoding/binary"
	"fmt"
)

// EZSPFrameControl represents the frame control byte in an EZSP frame.
// Bit 0: Direction (0=command from host, 1=response from NCP)
// Bit 1-7: Reserved/Status
type EZSPFrameControl byte

const (
	EZSP_FRAME_CONTROL_COMMAND  EZSPFrameControl = 0x00
	EZSP_FRAME_CONTROL_RESPONSE EZSPFrameControl = 0x01
)

// EZSPFrameID represents the EZSP command/response ID.
type EZSPFrameID uint8

// Common EZSP frame IDs (from UG100)
const (
	EZSP_VERSION EZSPFrameID = 0x00
	EZSP_GET_CONFIGURATION_VALUE EZSPFrameID = 0x52
	EZSP_SET_CONFIGURATION_VALUE EZSPFrameID = 0x53
	EZSP_NETWORK_INIT EZSPFrameID = 0x17
	EZSP_START_SCAN EZSPFrameID = 0x1A
	EZSP_STOP_SCAN EZSPFrameID = 0x1B
	EZSP_FORM_NETWORK EZSPFrameID = 0x1E
	EZSP_LEAVE_NETWORK EZSPFrameID = 0x20
	EZSP_PERMIT_JOINING EZSPFrameID = 0x22
	EZSP_SEND_UNICAST EZSPFrameID = 0x34
	EZSP_SEND_MULTICAST EZSPFrameID = 0x38
	EZSP_MESSAGE_SENT_HANDLER EZSPFrameID = 0x3F
	EZSP_INCOMING_MESSAGE_HANDLER EZSPFrameID = 0x45
)

// EZSPFrame represents an EZSP command or response frame.
type EZSPFrame struct {
	Sequence  uint8
	Control   EZSPFrameControl
	FrameID   EZSPFrameID
	Parameters []byte
}

// ParseEZSPFrame parses EZSP frame data from an ASH DATA frame.
func ParseEZSPFrame(data []byte) (*EZSPFrame, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("EZSP frame too short: %d bytes", len(data))
	}
	
	return &EZSPFrame{
		Sequence:  data[0],
		Control:   EZSPFrameControl(data[1]),
		FrameID:   EZSPFrameID(data[2]),
		Parameters: data[3:],
	}, nil
}

// SerializeEZSPFrame serializes an EZSP frame to bytes.
func SerializeEZSPFrame(frame *EZSPFrame) []byte {
	data := make([]byte, 0, 3+len(frame.Parameters))
	data = append(data, frame.Sequence)
	data = append(data, byte(frame.Control))
	data = append(data, byte(frame.FrameID))
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
