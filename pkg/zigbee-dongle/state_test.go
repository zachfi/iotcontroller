package zigbeedongle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// TestIsZeroNetworkKey verifies detection of an all-zero key.
func TestIsZeroNetworkKey(t *testing.T) {
	var zero [16]byte
	if !IsZeroNetworkKey(zero) {
		t.Error("all-zero key should return true")
	}

	nonZero := [16]byte{0x01}
	if IsZeroNetworkKey(nonZero) {
		t.Error("non-zero key should return false")
	}

	allFF := [16]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	if IsZeroNetworkKey(allFF) {
		t.Error("all-0xFF key should return false")
	}
}

// TestSaveLoadNetworkStateRoundTrip verifies that state is preserved through a file round-trip.
func TestSaveLoadNetworkStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")

	key := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	original := types.NetworkParameters{
		PanID:         0xABCD,
		ExtendedPanID: 0x0102030405060708,
		Channel:       15,
		NetworkKey:    key,
	}

	if err := SaveNetworkState(path, original); err != nil {
		t.Fatalf("SaveNetworkState: %v", err)
	}

	loaded, err := LoadNetworkState(path)
	if err != nil {
		t.Fatalf("LoadNetworkState: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadNetworkState returned nil")
	}

	if loaded.PanID != original.PanID {
		t.Errorf("PanID = 0x%04X, want 0x%04X", loaded.PanID, original.PanID)
	}
	if loaded.ExtendedPanID != original.ExtendedPanID {
		t.Errorf("ExtendedPanID = 0x%016X, want 0x%016X", loaded.ExtendedPanID, original.ExtendedPanID)
	}
	if loaded.Channel != original.Channel {
		t.Errorf("Channel = %d, want %d", loaded.Channel, original.Channel)
	}
	if loaded.NetworkKey != original.NetworkKey {
		t.Errorf("NetworkKey = %v, want %v", loaded.NetworkKey, original.NetworkKey)
	}
}

// TestLoadNetworkStateNotExist verifies that a missing file returns nil without error.
func TestLoadNetworkStateNotExist(t *testing.T) {
	loaded, err := LoadNetworkState("/nonexistent/path/state.yaml")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if loaded != nil {
		t.Errorf("expected nil for missing file, got: %v", loaded)
	}
}

// TestLoadNetworkStateInvalidChannel verifies that an out-of-range channel fails.
func TestLoadNetworkStateInvalidChannel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-state.yaml")

	// Write a state file with an invalid channel
	content := "panid: 1234\nextendedpanid: 0\nchannel: 27\nnetworkkey: 00000000000000000000000000000001\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadNetworkState(path)
	if err == nil {
		t.Error("expected error for invalid channel 27, got nil")
	}
}

// TestSaveNetworkStateCreatesDirectory verifies that SaveNetworkState creates missing parent dirs.
func TestSaveNetworkStateCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "nested", "state.yaml")

	params := types.NetworkParameters{
		PanID:   0x1234,
		Channel: 11,
	}

	if err := SaveNetworkState(path, params); err != nil {
		t.Fatalf("SaveNetworkState: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("state file was not created")
	}
}

// TestNetworkKeyYAMLHexRoundTrip verifies hex string marshal/unmarshal via state file.
func TestNetworkKeyYAMLHexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")

	key := [16]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}
	params := types.NetworkParameters{
		PanID:      0x1234,
		Channel:    15,
		NetworkKey: key,
	}

	if err := SaveNetworkState(path, params); err != nil {
		t.Fatalf("SaveNetworkState: %v", err)
	}

	loaded, err := LoadNetworkState(path)
	if err != nil {
		t.Fatalf("LoadNetworkState: %v", err)
	}
	if loaded.NetworkKey != key {
		t.Errorf("NetworkKey = %v, want %v", loaded.NetworkKey, key)
	}
}

// TestGenerateRandomNetworkParameters verifies the generated parameters are valid.
func TestGenerateRandomNetworkParameters(t *testing.T) {
	params, err := GenerateRandomNetworkParameters()
	if err != nil {
		t.Fatalf("GenerateRandomNetworkParameters: %v", err)
	}
	if params.PanID == 0 {
		t.Error("PanID should be non-zero")
	}
	if params.ExtendedPanID == 0 {
		t.Error("ExtendedPanID should be non-zero")
	}
	if params.Channel < 11 || params.Channel > 26 {
		t.Errorf("Channel = %d, want 11-26", params.Channel)
	}
	if IsZeroNetworkKey(params.NetworkKey) {
		t.Error("NetworkKey should not be all zeros")
	}
}
