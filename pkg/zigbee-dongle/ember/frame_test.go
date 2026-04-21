package ember

import (
	"bytes"
	"testing"
)

// TestCrcCCITT verifies against the standard CRC-CCITT test vector.
// CRC-CCITT (poly 0x1021, init 0xFFFF) of "123456789" = 0x29B1.
func TestCrcCCITT(t *testing.T) {
	got := crcCCITT([]byte("123456789"))
	if got != 0x29B1 {
		t.Errorf("crcCCITT(\"123456789\") = 0x%04X, want 0x29B1", got)
	}
}

// TestCrcCCITTEmpty verifies that an empty input returns the initial value.
func TestCrcCCITTEmpty(t *testing.T) {
	got := crcCCITT(nil)
	if got != 0xFFFF {
		t.Errorf("crcCCITT(nil) = 0x%04X, want 0xFFFF", got)
	}
}

// TestGeneratePseudoRandomSequence verifies the first 8 bytes of the LFSR sequence.
// Starting from rand0=0x42:
//
//	0x42 (bit0=0 → >>1 = 0x21)
//	0x21 (bit0=1 → >>1 ^ 0xB8 = 0x10^0xB8 = 0xA8)
//	0xA8 (bit0=0 → >>1 = 0x54)
//	0x54 (bit0=0 → >>1 = 0x2A)
//	0x2A (bit0=0 → >>1 = 0x15)
//	0x15 (bit0=1 → >>1 ^ 0xB8 = 0x0A^0xB8 = 0xB2)
//	0xB2 (bit0=0 → >>1 = 0x59)
//	0x59 (bit0=1 → >>1 ^ 0xB8 = 0x2C^0xB8 = 0x94)
func TestGeneratePseudoRandomSequence(t *testing.T) {
	want := []byte{0x42, 0x21, 0xA8, 0x54, 0x2A, 0x15, 0xB2, 0x59}
	got := generatePseudoRandomSequence(8)
	if !bytes.Equal(got, want) {
		t.Errorf("generatePseudoRandomSequence(8) = %#v, want %#v", got, want)
	}
}

func TestGeneratePseudoRandomSequenceZero(t *testing.T) {
	got := generatePseudoRandomSequence(0)
	if got != nil {
		t.Errorf("generatePseudoRandomSequence(0) = %v, want nil", got)
	}
}

// TestRandomizeDataRoundTrip verifies that XORing twice restores original data.
func TestRandomizeDataRoundTrip(t *testing.T) {
	original := []byte{0x01, 0x02, 0x03, 0xAA, 0xFF}
	randomized := randomizeData(original)
	restored := derandomizeData(randomized)
	if !bytes.Equal(original, restored) {
		t.Errorf("randomize/derandomize round-trip failed: got %v, want %v", restored, original)
	}
}

func TestRandomizeDataEmpty(t *testing.T) {
	got := randomizeData(nil)
	if got != nil {
		t.Errorf("randomizeData(nil) = %v, want nil", got)
	}
}

// TestEscapeUnescapeRoundTrip verifies that escaped bytes are correctly unescaped.
func TestEscapeUnescapeRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"no special bytes", []byte{0x01, 0x02, 0x03}},
		{"flag byte", []byte{0x7E}},
		{"escape byte", []byte{0x7D}},
		{"XON byte", []byte{0x11}},
		{"XOFF byte", []byte{0x13}},
		{"all special bytes", []byte{0x7E, 0x7D, 0x11, 0x13}},
		{"mixed", []byte{0x00, 0x7E, 0x01, 0x7D, 0x02, 0x11, 0x03, 0x13, 0xFF}},
		{"empty", []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escaped := escapeASHFrame(tt.data)
			got := unescapeASHFrame(escaped)
			if !bytes.Equal(tt.data, got) {
				t.Errorf("escape/unescape round-trip: got %v, want %v", got, tt.data)
			}
		})
	}
}

// TestParseASHFrameRSTACK builds an RSTACK frame and verifies it parses correctly.
func TestParseASHFrameRSTACK(t *testing.T) {
	raw := buildRSTACKFrame()
	parsed, err := ParseASHFrame(raw)
	if err != nil {
		t.Fatalf("ParseASHFrame(RSTACK): %v", err)
	}
	if parsed.Type != ASH_FRAME_RSTACK {
		t.Errorf("Type = %v, want ASH_FRAME_RSTACK", parsed.Type)
	}
}

// TestParseASHFrameACK builds ACK frames for all valid ackNum values.
func TestParseASHFrameACK(t *testing.T) {
	for ackNum := uint8(0); ackNum < 8; ackNum++ {
		raw := buildASHACKFrame(ackNum)
		parsed, err := ParseASHFrame(raw)
		if err != nil {
			t.Fatalf("ackNum=%d: ParseASHFrame: %v", ackNum, err)
		}
		if parsed.Type != ASH_FRAME_ACK {
			t.Errorf("ackNum=%d: Type = %v, want ASH_FRAME_ACK", ackNum, parsed.Type)
		}
		if parsed.AckNum != ackNum {
			t.Errorf("ackNum=%d: AckNum = %d, want %d", ackNum, parsed.AckNum, ackNum)
		}
	}
}

// TestParseASHFrameNAK verifies that a NAK frame is correctly identified.
// NAK control byte: 0xA0 | ackNum (bits[7:5] = 101).
func TestParseASHFrameNAK(t *testing.T) {
	ackNum := uint8(3)
	control := byte(0xA0) | ackNum
	crc := crcCCITT([]byte{control})
	frameData := []byte{control, byte(crc >> 8), byte(crc & 0xFF)}
	escaped := escapeASHFrame(frameData)
	raw := append([]byte{ASH_FLAG}, append(escaped, ASH_FLAG)...)

	parsed, err := ParseASHFrame(raw)
	if err != nil {
		t.Fatalf("ParseASHFrame(NAK): %v", err)
	}
	if parsed.Type != ASH_FRAME_NAK {
		t.Errorf("Type = %v, want ASH_FRAME_NAK", parsed.Type)
	}
	if parsed.AckNum != ackNum {
		t.Errorf("AckNum = %d, want %d", parsed.AckNum, ackNum)
	}
}

// TestParseASHFrameDataRoundTrip builds a DATA frame and verifies the header fields.
func TestParseASHFrameDataRoundTrip(t *testing.T) {
	// control byte for DATA: frameNum=2, ReTx=0, ackNum=1 => (2<<4)|(0<<3)|1 = 0x21
	control := byte((2 << 4) | (0 << 3) | 1)
	payload := []byte{0xAA, 0xBB, 0xCC}
	raw := buildASHDataFrame(control, payload)

	parsed, err := ParseASHFrame(raw)
	if err != nil {
		t.Fatalf("ParseASHFrame(DATA): %v", err)
	}
	if parsed.Type != ASH_FRAME_DATA {
		t.Errorf("Type = %v, want ASH_FRAME_DATA", parsed.Type)
	}
	if parsed.FrameNum != 2 {
		t.Errorf("FrameNum = %d, want 2", parsed.FrameNum)
	}
	if parsed.AckNum != 1 {
		t.Errorf("AckNum = %d, want 1", parsed.AckNum)
	}
	if parsed.ReTx {
		t.Errorf("ReTx = true, want false")
	}
}

// TestParseASHFrameTooShort verifies that short frames return an error.
func TestParseASHFrameTooShort(t *testing.T) {
	// 2 bytes total after stripping flags: only 1 byte (not enough for control+CRC)
	raw := []byte{ASH_FLAG, 0x00, ASH_FLAG}
	_, err := ParseASHFrame(raw)
	if err == nil {
		t.Error("expected error for frame too short, got nil")
	}
}

// TestBuildRSTFrameStructure verifies the RST frame has cancel bytes and flag delimiters.
func TestBuildASHNAKFrame(t *testing.T) {
	for ackNum := uint8(0); ackNum < 8; ackNum++ {
		raw := buildASHNAKFrame(ackNum)
		parsed, err := ParseASHFrame(raw)
		if err != nil {
			t.Fatalf("ackNum=%d: ParseASHFrame: %v", ackNum, err)
		}
		if parsed.Type != ASH_FRAME_NAK {
			t.Errorf("ackNum=%d: Type = %v, want ASH_FRAME_NAK", ackNum, parsed.Type)
		}
		if parsed.AckNum != ackNum {
			t.Errorf("ackNum=%d: AckNum = %d, want %d", ackNum, parsed.AckNum, ackNum)
		}
	}
}

func TestBuildASHNAKFrameControlByte(t *testing.T) {
	// NAK control byte must be 0xA0 | ackNum; verify the raw byte before FLAG/escape.
	for ackNum := uint8(0); ackNum < 8; ackNum++ {
		raw := buildASHNAKFrame(ackNum)
		// Frame: FLAG + (escaped control + CRC) + FLAG
		// After unescaping, first byte is the control byte.
		inner := raw[1 : len(raw)-1]
		unescaped := unescapeASHFrame(inner)
		if len(unescaped) < 1 {
			t.Fatalf("ackNum=%d: unescaped frame too short", ackNum)
		}
		want := byte(0xA0) | (ackNum & 0x07)
		if unescaped[0] != want {
			t.Errorf("ackNum=%d: control = 0x%02X, want 0x%02X", ackNum, unescaped[0], want)
		}
	}
}

func TestBuildASHNAKFrameDistinctFromACK(t *testing.T) {
	// NAK and ACK frames for the same ackNum must be different bytes.
	for ackNum := uint8(0); ackNum < 8; ackNum++ {
		nak := buildASHNAKFrame(ackNum)
		ack := buildASHACKFrame(ackNum)
		if bytes.Equal(nak, ack) {
			t.Errorf("ackNum=%d: NAK and ACK frames are identical", ackNum)
		}
	}
}

func TestBuildRSTFrameStructure(t *testing.T) {
	raw := buildRSTFrame()

	// Should start with 32 cancel bytes
	if len(raw) < 32 {
		t.Fatalf("RST frame too short: %d bytes", len(raw))
	}
	for i := 0; i < 32; i++ {
		if raw[i] != ASH_CANCEL {
			t.Errorf("raw[%d] = 0x%02X, want 0x%02X (ASH_CANCEL)", i, raw[i], ASH_CANCEL)
		}
	}
	// After cancel bytes, should start with FLAG
	if raw[32] != ASH_FLAG {
		t.Errorf("raw[32] = 0x%02X, want 0x%02X (ASH_FLAG)", raw[32], ASH_FLAG)
	}
	// Should end with FLAG
	if raw[len(raw)-1] != ASH_FLAG {
		t.Errorf("last byte = 0x%02X, want 0x%02X (ASH_FLAG)", raw[len(raw)-1], ASH_FLAG)
	}

	// The frame portion (after cancel bytes) should parse as RST
	parsed, err := ParseASHFrame(raw[32:])
	if err != nil {
		t.Fatalf("ParseASHFrame(RST portion): %v", err)
	}
	if parsed.Type != ASH_FRAME_RST {
		t.Errorf("Type = %v, want ASH_FRAME_RST", parsed.Type)
	}
}
