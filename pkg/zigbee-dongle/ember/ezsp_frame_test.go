package ember

import (
	"bytes"
	"testing"
)

// TestParseEZSPFrameRoundTrip verifies legacy EZSP frame serialize/parse round-trip.
func TestParseEZSPFrameRoundTrip(t *testing.T) {
	original := &EZSPFrame{
		Sequence:   0x42,
		Control:    EZSP_FRAME_CONTROL_COMMAND,
		FrameID:    EZSP_VERSION,
		Parameters: []byte{0x0D}, // request version 13
	}
	serialized := SerializeEZSPFrame(original)
	parsed, err := ParseEZSPFrame(serialized)
	if err != nil {
		t.Fatalf("ParseEZSPFrame: %v", err)
	}
	if parsed.Sequence != original.Sequence {
		t.Errorf("Sequence = %d, want %d", parsed.Sequence, original.Sequence)
	}
	if parsed.Control != original.Control {
		t.Errorf("Control = %v, want %v", parsed.Control, original.Control)
	}
	if parsed.FrameID != original.FrameID {
		t.Errorf("FrameID = 0x%04X, want 0x%04X", parsed.FrameID, original.FrameID)
	}
	if !bytes.Equal(parsed.Parameters, original.Parameters) {
		t.Errorf("Parameters = %v, want %v", parsed.Parameters, original.Parameters)
	}
}

// TestParseEZSPFrameTooShort verifies that short input returns an error.
func TestParseEZSPFrameTooShort(t *testing.T) {
	_, err := ParseEZSPFrame([]byte{0x00, 0x00})
	if err == nil {
		t.Error("expected error for frame too short, got nil")
	}
}

// TestParseEZSPFrameExtendedRoundTrip verifies extended EZSP frame serialize/parse round-trip.
// SerializeEZSPFrameExtended is host→NCP only (always writes fcLo=0x00, command direction).
func TestParseEZSPFrameExtendedRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		frame *EZSPFrame
	}{
		{
			name: "get network parameters",
			frame: &EZSPFrame{
				Sequence:   0x01,
				Control:    EZSP_FRAME_CONTROL_COMMAND,
				FrameID:    EZSP_GET_NETWORK_PARAMETERS,
				Parameters: []byte{},
			},
		},
		{
			name: "permit joining",
			frame: &EZSPFrame{
				Sequence:   0x05,
				Control:    EZSP_FRAME_CONTROL_COMMAND,
				FrameID:    EZSP_PERMIT_JOINING,
				Parameters: []byte{0x3C}, // 60 seconds
			},
		},
		{
			name: "extended frame id (>0xFF)",
			frame: &EZSPFrame{
				Sequence:   0xFF,
				Control:    EZSP_FRAME_CONTROL_COMMAND,
				FrameID:    EZSP_IMPORT_TRANSIENT_KEY,
				Parameters: []byte{0xAA, 0xBB},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serialized := SerializeEZSPFrameExtended(tt.frame)
			parsed, err := ParseEZSPFrameExtended(serialized)
			if err != nil {
				t.Fatalf("ParseEZSPFrameExtended: %v", err)
			}
			if parsed.Sequence != tt.frame.Sequence {
				t.Errorf("Sequence = %d, want %d", parsed.Sequence, tt.frame.Sequence)
			}
			// SerializeEZSPFrameExtended always produces command direction (fcLo=0x00)
			if parsed.Control != EZSP_FRAME_CONTROL_COMMAND {
				t.Errorf("Control = %v, want COMMAND", parsed.Control)
			}
			if parsed.FrameID != tt.frame.FrameID {
				t.Errorf("FrameID = 0x%04X, want 0x%04X", parsed.FrameID, tt.frame.FrameID)
			}
			if !bytes.Equal(parsed.Parameters, tt.frame.Parameters) {
				t.Errorf("Parameters = %v, want %v", parsed.Parameters, tt.frame.Parameters)
			}
		})
	}
}

// TestParseEZSPFrameExtendedResponse verifies that a response frame (bit7=1 in fcLo) is parsed correctly.
// This simulates parsing a frame received from the NCP, not one serialized by our code.
func TestParseEZSPFrameExtendedResponse(t *testing.T) {
	// Manually construct a response frame: fcLo=0x80 (bit7=1 = NCP→host)
	// [seq=0x03] [fcLo=0x80] [fcHi=0x01] [frameIDLo=NETWORK_STATE] [frameIDHi=0x00] [0x02]
	data := []byte{0x03, 0x80, 0x01, byte(EZSP_NETWORK_STATE), 0x00, 0x02}
	parsed, err := ParseEZSPFrameExtended(data)
	if err != nil {
		t.Fatalf("ParseEZSPFrameExtended: %v", err)
	}
	if parsed.Control != EZSP_FRAME_CONTROL_RESPONSE {
		t.Errorf("Control = %v, want RESPONSE", parsed.Control)
	}
	if parsed.FrameID != EZSP_NETWORK_STATE {
		t.Errorf("FrameID = 0x%04X, want 0x%04X", parsed.FrameID, EZSP_NETWORK_STATE)
	}
}

// TestParseEZSPFrameExtendedTooShort verifies that short input returns an error.
func TestParseEZSPFrameExtendedTooShort(t *testing.T) {
	_, err := ParseEZSPFrameExtended([]byte{0x00, 0x00, 0x01, 0x00})
	if err == nil {
		t.Error("expected error for extended frame too short, got nil")
	}
}

// TestParseVersionResponse verifies parsing of the EZSP VERSION response.
func TestParseVersionResponse(t *testing.T) {
	// protocolVersion=13, stackType=2, stackVersion=0x07B4
	params := []byte{0x0D, 0x02, 0xB4, 0x07}
	resp, err := ParseVersionResponse(params)
	if err != nil {
		t.Fatalf("ParseVersionResponse: %v", err)
	}
	if resp.ProtocolVersion != 13 {
		t.Errorf("ProtocolVersion = %d, want 13", resp.ProtocolVersion)
	}
	if resp.StackType != 2 {
		t.Errorf("StackType = %d, want 2", resp.StackType)
	}
	if resp.StackVersion != 0x07B4 {
		t.Errorf("StackVersion = 0x%04X, want 0x07B4", resp.StackVersion)
	}
}

// TestParseVersionResponseTooShort verifies that short input returns an error.
func TestParseVersionResponseTooShort(t *testing.T) {
	_, err := ParseVersionResponse([]byte{0x0D, 0x02, 0xB4})
	if err == nil {
		t.Error("expected error for version response too short, got nil")
	}
}

// TestSerializeEZSPFrameExtendedHeader verifies the 5-byte header format.
func TestSerializeEZSPFrameExtendedHeader(t *testing.T) {
	frame := &EZSPFrame{
		Sequence:   0x07,
		Control:    EZSP_FRAME_CONTROL_COMMAND,
		FrameID:    EZSP_PERMIT_JOINING,
		Parameters: []byte{0x3C}, // 60 seconds
	}
	data := SerializeEZSPFrameExtended(frame)

	// Minimum 6 bytes: 5-byte header + 1 parameter
	if len(data) < 6 {
		t.Fatalf("serialized frame too short: %d bytes", len(data))
	}
	if data[0] != 0x07 {
		t.Errorf("byte[0] (sequence) = 0x%02X, want 0x07", data[0])
	}
	if data[1] != 0x00 {
		t.Errorf("byte[1] (fcLo) = 0x%02X, want 0x00 (command direction)", data[1])
	}
	if data[2] != EZSP_EXTENDED_FRAME_FORMAT_VERSION {
		t.Errorf("byte[2] (fcHi) = 0x%02X, want 0x%02X (extended format version)", data[2], EZSP_EXTENDED_FRAME_FORMAT_VERSION)
	}
}
