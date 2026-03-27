package ember

import (
	"encoding/binary"
	"fmt"
)

const (
	ASH_FLAG    = 0x7E // Frame delimiter
	ASH_ESC     = 0x7D // Escape character
	ASH_ESC_XOR = 0x20 // XOR value for escaped bytes
	ASH_CANCEL  = 0x1A // Cancel character (used before RST to wake device)
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
	case control&0xC0 == 0x80:
		// ACK (0x8n) or NAK (0xAn) - minimal frame: control + CRC = 3 bytes
		// ACK: bits[7:5] = 100. NAK: bits[7:5] = 101. Both: bits[7:6] = 10.
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

// buildRSTFrame creates a raw ASH RST frame with proper escaping.
// According to bellows and zigbee-herdsman, we should send CANCEL bytes (0x1A) before RST
// to wake up the device. bellows sends 32 CANCEL bytes, zigbee-herdsman sends at least 1.
// The CANCEL bytes are sent BEFORE the frame (before the FLAG byte).
func buildRSTFrame() []byte {
	control := byte(0xC0) // RST control byte
	crc := crcCCITT([]byte{control})

	// Build frame: control + CRC (high byte) + CRC (low byte)
	frameData := []byte{control, byte(crc >> 8), byte(crc & 0xFF)}

	// Escape the frame data
	escaped := escapeASHFrame(frameData)

	// Build the frame with flag delimiters
	frame := make([]byte, 0, len(escaped)+2)
	frame = append(frame, ASH_FLAG)
	frame = append(frame, escaped...)
	frame = append(frame, ASH_FLAG)

	// Prepend CANCEL bytes to wake device (bellows sends 32)
	// Some devices need these to wake up or clear state before RST
	cancelBytes := make([]byte, 32)
	for i := range cancelBytes {
		cancelBytes[i] = ASH_CANCEL
	}

	// Return: CANCEL bytes + frame
	result := make([]byte, 0, len(cancelBytes)+len(frame))
	result = append(result, cancelBytes...)
	result = append(result, frame...)

	return result
}

// buildRSTACKFrame creates a raw ASH RSTACK frame with proper escaping.
func buildRSTACKFrame() []byte {
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

// generatePseudoRandomSequence generates the ASH pseudo-random sequence for data randomization.
// The sequence is reinitialized at the start of every Data Field.
// Algorithm: 8-bit linear feedback shift register
// - rand0 = 0x42
// - if bit 0 of randi is 0, randi+1 = randi >> 1
// - if bit 0 of randi is 1, randi+1 = (randi >> 1) ^ 0xB8
func generatePseudoRandomSequence(length int) []byte {
	if length == 0 {
		return nil
	}
	sequence := make([]byte, length)
	rand := uint8(0x42) // Initial value
	for i := 0; i < length; i++ {
		sequence[i] = rand
		if rand&0x01 == 0 {
			rand = rand >> 1
		} else {
			rand = (rand >> 1) ^ 0xB8
		}
	}
	return sequence
}

// randomizeData XORs the data with the pseudo-random sequence.
func randomizeData(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	sequence := generatePseudoRandomSequence(len(data))
	result := make([]byte, len(data))
	for i := range data {
		result[i] = data[i] ^ sequence[i]
	}
	return result
}

// derandomizeData XORs the data with the pseudo-random sequence to restore original data.
// This is the same operation as randomizeData (XOR is its own inverse).
func derandomizeData(data []byte) []byte {
	return randomizeData(data)
}

// buildASHDataFrame creates an ASH DATA frame with proper escaping and CRC.
// According to UG101, the Data Field must be XORed with a pseudo-random sequence
// before byte stuffing to reduce the likelihood of reserved bytes.
func buildASHDataFrame(control byte, data []byte) []byte {
	// Step 1: Randomize the Data Field (XOR with pseudo-random sequence)
	randomizedData := randomizeData(data)

	// Step 2: Build frame: control + randomized data
	frameData := make([]byte, 0, 1+len(randomizedData))
	frameData = append(frameData, control)
	frameData = append(frameData, randomizedData...)

	// Step 3: Calculate CRC over control + randomized data
	crc := crcCCITT(frameData)
	frameData = append(frameData, byte(crc>>8), byte(crc&0xFF))

	// Step 4: Escape the frame data (byte stuffing)
	escaped := escapeASHFrame(frameData)

	// Step 5: Add flag delimiters
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, ASH_FLAG)
	result = append(result, escaped...)
	result = append(result, ASH_FLAG)

	return result
}

// buildASHACKFrame creates an ASH ACK frame with proper escaping and CRC.
// ACK frame format: control byte (0x80 + ackNum) + CRC
func buildASHACKFrame(ackNum uint8) []byte {
	// ACK control byte: 0x80 (ACK type) + ackNum in lower 3 bits
	control := byte(0x80) | (ackNum & 0x07)
	crc := crcCCITT([]byte{control})

	// Build frame: control + CRC
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
