package ezsp

import (
	"encoding/binary"
	"fmt"
)

const (
	ASH_FLAG  = 0x7E // Frame delimiter
	ASH_ESC   = 0x7D // Escape character
	ASH_ESC_XOR = 0x20 // XOR value for escaped bytes
)

// ASHFrameType represents the type of ASH frame.
type ASHFrameType int

const (
	ASH_FRAME_DATA ASHFrameType = iota
	ASH_FRAME_ACK
	ASH_FRAME_NAK
	ASH_FRAME_RST
	ASH_FRAME_RSTACK
	ASH_FRAME_ERROR
)

// ASHFrame represents a parsed ASH frame.
type ASHFrame struct {
	Type     ASHFrameType
	Control  byte
	FrameNum uint8  // 3 bits (DATA only)
	AckNum   uint8  // 3 bits
	ReTx     bool   // DATA only
	NRdy     bool   // ACK/NAK only
	Reserved bool   // ACK/NAK only
	Data     []byte // DATA, RSTACK, ERROR
	CRC      uint16
	Raw      []byte // Raw frame bytes (for debugging)
}

// unescapeASHFrame removes byte stuffing from ASH frame data.
// ASH uses 0x7D as escape character, followed by XORed byte.
func unescapeASHFrame(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	
	result := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == ASH_ESC && i+1 < len(raw) {
			// Escaped byte: XOR with 0x20
			result = append(result, raw[i+1]^ASH_ESC_XOR)
			i++ // Skip the escaped byte
		} else {
			result = append(result, raw[i])
		}
	}
	return result
}

// ParseASHFrame parses raw bytes into an ASHFrame struct.
// The raw bytes should include the flag delimiters but not the escaped bytes.
// This function expects the frame to already be unescaped.
func ParseASHFrame(raw []byte) (*ASHFrame, error) {
	// Remove flag bytes if present
	if len(raw) > 0 && raw[0] == ASH_FLAG {
		raw = raw[1:]
	}
	if len(raw) > 0 && raw[len(raw)-1] == ASH_FLAG {
		raw = raw[:len(raw)-1]
	}
	
	// Unescape the frame
	unescaped := unescapeASHFrame(raw)
	
	// Minimum frame size: control byte (1) + CRC (2) = 3 bytes
	// RST and RSTACK frames can be this minimal
	if len(unescaped) < 3 {
		return nil, fmt.Errorf("frame too short: %d bytes (minimum 3)", len(unescaped))
	}
	
	control := unescaped[0]
	frame := &ASHFrame{
		Control: control,
		Raw:     raw, // Keep original raw bytes for debugging
	}

	switch {
	case control&0x80 == 0:
		// DATA frame - requires at least control + CRC = 3 bytes, but typically has data
		if len(unescaped) < 3 {
			return nil, fmt.Errorf("DATA frame too short: %d bytes", len(unescaped))
		}
		frame.Type = ASH_FRAME_DATA
		frame.FrameNum = (control >> 4) & 0x07
		frame.ReTx = (control>>3)&0x01 == 1
		frame.AckNum = control & 0x07
		if len(unescaped) > 3 {
			frame.Data = unescaped[1 : len(unescaped)-2] // Exclude control, CRC
		}
	case control&0xE0 == 0x80:
		// ACK or NAK - minimal frame: control + CRC = 3 bytes
		if len(unescaped) < 3 {
			return nil, fmt.Errorf("ACK/NAK frame too short: %d bytes", len(unescaped))
		}
		if control&0x20 == 0 {
			frame.Type = ASH_FRAME_ACK
		} else {
			frame.Type = ASH_FRAME_NAK
		}
		frame.Reserved = (control>>4)&0x01 == 1
		frame.NRdy = (control>>3)&0x01 == 1
		frame.AckNum = control & 0x07
	case control == 0xC0:
		// RST frame - minimal: control + CRC = 3 bytes
		if len(unescaped) < 3 {
			return nil, fmt.Errorf("RST frame too short: %d bytes", len(unescaped))
		}
		frame.Type = ASH_FRAME_RST
	case control == 0xC1:
		// RSTACK frame - minimal: control + CRC = 3 bytes, may have optional data
		if len(unescaped) < 3 {
			return nil, fmt.Errorf("RSTACK frame too short: %d bytes", len(unescaped))
		}
		frame.Type = ASH_FRAME_RSTACK
		if len(unescaped) > 3 {
			frame.Data = unescaped[1 : len(unescaped)-2] // Exclude control, CRC
		}
	case control == 0xC2:
		// ERROR frame - minimal: control + CRC = 3 bytes, may have optional data
		if len(unescaped) < 3 {
			return nil, fmt.Errorf("ERROR frame too short: %d bytes", len(unescaped))
		}
		frame.Type = ASH_FRAME_ERROR
		if len(unescaped) > 3 {
			frame.Data = unescaped[1 : len(unescaped)-2] // Exclude control, CRC
		}
	default:
		return nil, fmt.Errorf("unknown frame type: 0x%02X", control)
	}

	// CRC is always last 2 bytes before flag
	if len(unescaped) >= 3 {
		frame.CRC = binary.BigEndian.Uint16(unescaped[len(unescaped)-2:])
	}
	return frame, nil
}

// DebugString returns a human-readable string for inspecting the frame.
func (f *ASHFrame) DebugString() string {
	return fmt.Sprintf(
		"Type: %v\nControl: 0x%02X\nFrameNum: %d\nAckNum: %d\nReTx: %v\nNRdy: %v\nReserved: %v\nData: %X\nCRC: 0x%04X\nRaw: %X",
		f.Type, f.Control, f.FrameNum, f.AckNum, f.ReTx, f.NRdy, f.Reserved, f.Data, f.CRC, f.Raw,
	)
}

// escapeASHFrame adds byte stuffing to ASH frame data.
// Bytes 0x7E, 0x7D, 0x11, 0x13 are escaped.
func escapeASHFrame(data []byte) []byte {
	result := make([]byte, 0, len(data)*2)
	for _, b := range data {
		switch b {
		case ASH_FLAG, ASH_ESC, 0x11, 0x13:
			result = append(result, ASH_ESC, b^ASH_ESC_XOR)
		default:
			result = append(result, b)
		}
	}
	return result
}

// BuildRSTFrame creates a raw ASH RST frame with proper escaping.
func buildRSTFrame() []byte {
	control := byte(0xC0) // RST control byte
	crc := crcCCITT([]byte{control})
	
	// Build frame: control + CRC (high byte) + CRC (low byte)
	frameData := []byte{control, byte(crc >> 8), byte(crc & 0xFF)}
	
	// Escape the frame data
	escaped := escapeASHFrame(frameData)
	
	// Add flag delimiters
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, ASH_FLAG)
	result = append(result, escaped...)
	result = append(result, ASH_FLAG)
	
	return result
}

// BuildRSTACKFrame creates a raw ASH RSTACK frame with proper escaping.
func BuildRSTACKFrame() []byte {
	control := byte(0xC1) // RSTACK control byte
	crc := crcCCITT([]byte{control})
	
	// Build frame: control + CRC (high byte) + CRC (low byte)
	frameData := []byte{control, byte(crc >> 8), byte(crc & 0xFF)}
	
	// Escape the frame data
	escaped := escapeASHFrame(frameData)
	
	// Add flag delimiters
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, ASH_FLAG)
	result = append(result, escaped...)
	result = append(result, ASH_FLAG)
	
	return result
}

// BuildASHDataFrame creates an ASH DATA frame with proper escaping and CRC.
func BuildASHDataFrame(control byte, data []byte) []byte {
	// Build frame: control + data
	frameData := make([]byte, 0, 1+len(data))
	frameData = append(frameData, control)
	frameData = append(frameData, data...)
	
	// Calculate CRC over control + data
	crc := crcCCITT(frameData)
	frameData = append(frameData, byte(crc>>8), byte(crc&0xFF))
	
	// Escape the frame data
	escaped := escapeASHFrame(frameData)
	
	// Add flag delimiters
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, ASH_FLAG)
	result = append(result, escaped...)
	result = append(result, ASH_FLAG)
	
	return result
}

// crcCCITT computes CRC-CCITT (poly 0x1021, init 0xFFFF) for ASH.
func crcCCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
