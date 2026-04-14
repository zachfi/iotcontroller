package znp

import (
	"bufio"
	"bytes"
	"testing"
)

// TestCalculateFCS verifies the XOR-based frame check sequence.
func TestCalculateFCS(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want byte
	}{
		{"single byte", []byte{0x01}, 0x01},
		{"two bytes", []byte{0x01, 0x02}, 0x03},
		{"xor cancels", []byte{0xAB, 0xAB}, 0x00},
		{"multi byte", []byte{0x01, 0x02, 0x03}, 0x00},
		{"empty", []byte{}, 0x00},
		// Real ZNP frame: [len=0x00, cmd0=0x21, cmd1=0x02] (SYS_PING SREQ)
		// FCS = 0x00^0x21^0x02 = 0x23
		{"sys_ping_sreq", []byte{0x00, 0x21, 0x02}, 0x23},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateFCS(tt.data)
			if got != tt.want {
				t.Errorf("calculateFCS(%v) = 0x%02X, want 0x%02X", tt.data, got, tt.want)
			}
		})
	}
}

// TestWriteReadFrameRoundTrip verifies that writeFrame/readFrame are inverses.
func TestWriteReadFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		frame Frame
	}{
		{
			name: "empty data frame",
			frame: Frame{
				FrameHeader: FrameHeader{
					Type:      FRAME_TYPE_SREQ,
					Subsystem: FRAME_SUBSYSTEM_SYS,
					ID:        0x02,
				},
				Data: []byte{},
			},
		},
		{
			name: "frame with data",
			frame: Frame{
				FrameHeader: FrameHeader{
					Type:      FRAME_TYPE_AREQ,
					Subsystem: FRAME_SUBSYSTEM_ZDO,
					ID:        0x42,
				},
				Data: []byte{0x01, 0x02, 0x03, 0xFF},
			},
		},
		{
			name: "max subsystem bits",
			frame: Frame{
				FrameHeader: FrameHeader{
					Type:      FRAME_TYPE_SRSP,
					Subsystem: FRAME_SUBSYSTEM_APP,
					ID:        0xAB,
				},
				Data: []byte{0xDE, 0xAD},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeFrame(&buf, tt.frame); err != nil {
				t.Fatalf("writeFrame: %v", err)
			}

			reader := bufio.NewReader(&buf)
			got, err := readFrame(reader)
			if err != nil {
				t.Fatalf("readFrame: %v", err)
			}

			if got.Type != tt.frame.Type {
				t.Errorf("Type = %v, want %v", got.Type, tt.frame.Type)
			}
			if got.Subsystem != tt.frame.Subsystem {
				t.Errorf("Subsystem = %v, want %v", got.Subsystem, tt.frame.Subsystem)
			}
			if got.ID != tt.frame.ID {
				t.Errorf("ID = %v, want %v", got.ID, tt.frame.ID)
			}
			if !bytes.Equal(got.Data, tt.frame.Data) {
				t.Errorf("Data = %v, want %v", got.Data, tt.frame.Data)
			}
		})
	}
}

// TestReadFrameSkipsGarbage verifies that readFrame skips bytes before SOF.
func TestReadFrameSkipsGarbage(t *testing.T) {
	var buf bytes.Buffer

	// Write a valid frame first to get the bytes
	frame := Frame{
		FrameHeader: FrameHeader{
			Type:      FRAME_TYPE_SREQ,
			Subsystem: FRAME_SUBSYSTEM_SYS,
			ID:        0x01,
		},
		Data: []byte{0xAA},
	}
	if err := writeFrame(&buf, frame); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}

	// Prepend garbage bytes before the valid frame
	garbage := []byte{0x00, 0x11, 0x22}
	frameBytes := buf.Bytes()
	input := append(garbage, frameBytes...)

	reader := bufio.NewReader(bytes.NewReader(input))

	// First read should return ErrGarbage
	_, err := readFrame(reader)
	if err == nil {
		t.Fatal("expected ErrGarbage, got nil")
	}

	// Second read should successfully parse the frame
	got, err := readFrame(reader)
	if err != nil {
		t.Fatalf("readFrame after garbage: %v", err)
	}
	if got.ID != frame.ID {
		t.Errorf("ID = %v, want %v", got.ID, frame.ID)
	}
}

// TestReadFrameInvalidFCS verifies that a bad FCS returns ErrInvalidFrame.
func TestReadFrameInvalidFCS(t *testing.T) {
	var buf bytes.Buffer
	frame := Frame{
		FrameHeader: FrameHeader{
			Type:      FRAME_TYPE_SREQ,
			Subsystem: FRAME_SUBSYSTEM_SYS,
			ID:        0x01,
		},
		Data: []byte{0x00},
	}
	if err := writeFrame(&buf, frame); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}

	// Corrupt the last byte (FCS)
	b := buf.Bytes()
	b[len(b)-1] ^= 0xFF

	reader := bufio.NewReader(bytes.NewReader(b))
	_, err := readFrame(reader)
	if err != ErrInvalidFrame {
		t.Errorf("expected ErrInvalidFrame, got %v", err)
	}
}

// TestFrameTypeString verifies the String() method for known frame types.
func TestFrameTypeString(t *testing.T) {
	tests := []struct {
		ft   FrameType
		want string
	}{
		{FRAME_TYPE_POLL, "POLL"},
		{FRAME_TYPE_SREQ, "SREQ"},
		{FRAME_TYPE_AREQ, "AREQ"},
		{FRAME_TYPE_SRSP, "SRSP"},
	}
	for _, tt := range tests {
		if got := tt.ft.String(); got != tt.want {
			t.Errorf("FrameType(%d).String() = %q, want %q", tt.ft, got, tt.want)
		}
	}
}
