// Package zigbeedongle provides network state persistence functionality.
package zigbeedongle

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// StateFile is the default filename for persisting network state.
const StateFile = "zigbee-network-state.yaml"

// SaveNetworkState saves the network state to a YAML file.
// This allows the same network to be restored when swapping devices.
// The file is human-readable and can be edited by users.
func SaveNetworkState(statePath string, params types.NetworkParameters) error {
	state := types.PersistedNetworkState{
		PanID:         params.PanID,
		ExtendedPanID: params.ExtendedPanID,
		Channel:       params.Channel,
		NetworkKey:    params.NetworkKey,
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(statePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating state directory: %w", err)
		}
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling network state: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0600); err != nil {
		return fmt.Errorf("writing network state file: %w", err)
	}

	return nil
}

// LoadNetworkState loads the network state from a YAML file.
// Returns nil if the file doesn't exist (network not yet formed).
// The file can be edited by users, so we validate the loaded data.
func LoadNetworkState(statePath string) (*types.NetworkParameters, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No saved state - this is OK
		}
		return nil, fmt.Errorf("reading network state file: %w", err)
	}

	var state types.PersistedNetworkState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshaling network state: %w", err)
	}

	// Validate loaded state
	if state.Channel < 11 || state.Channel > 26 {
		return nil, fmt.Errorf("invalid channel in state file: %d (must be 11-26)", state.Channel)
	}

	return &types.NetworkParameters{
		PanID:         state.PanID,
		ExtendedPanID: state.ExtendedPanID,
		Channel:       state.Channel,
		NetworkKey:    state.NetworkKey,
	}, nil
}

// GenerateRandomNetworkParameters creates random network parameters for a new network.
// This should be used when no saved state exists and a network needs to be formed.
func GenerateRandomNetworkParameters() (*types.NetworkParameters, error) {
	// Generate random PAN ID (16-bit, non-zero)
	panIDBytes := make([]byte, 2)
	if _, err := rand.Read(panIDBytes); err != nil {
		return nil, fmt.Errorf("generating random PAN ID: %w", err)
	}
	panID := uint16(panIDBytes[0])<<8 | uint16(panIDBytes[1])
	// Ensure non-zero
	if panID == 0 {
		panID = 0x0001
	}

	// Generate random Extended PAN ID (64-bit, non-zero)
	extPanIDBytes := make([]byte, 8)
	if _, err := rand.Read(extPanIDBytes); err != nil {
		return nil, fmt.Errorf("generating random Extended PAN ID: %w", err)
	}
	extPanID := uint64(extPanIDBytes[0]) |
		uint64(extPanIDBytes[1])<<8 |
		uint64(extPanIDBytes[2])<<16 |
		uint64(extPanIDBytes[3])<<24 |
		uint64(extPanIDBytes[4])<<32 |
		uint64(extPanIDBytes[5])<<40 |
		uint64(extPanIDBytes[6])<<48 |
		uint64(extPanIDBytes[7])<<56
	// Ensure non-zero
	if extPanID == 0 {
		extPanID = 0x0000000000000001
	}

	// Generate random channel (11-26 for 2.4GHz Zigbee)
	// Use channel 11 as default, but could randomize if desired
	channel := uint8(11) // Default to channel 11, or could randomize: 11 + uint8(rand.Intn(16))

	// Generate random 128-bit Network Key
	var networkKey [16]byte
	if _, err := rand.Read(networkKey[:]); err != nil {
		return nil, fmt.Errorf("generating random Network Key: %w", err)
	}

	return &types.NetworkParameters{
		PanID:         panID,
		ExtendedPanID: extPanID,
		Channel:       channel,
		NetworkKey:    networkKey,
	}, nil
}
