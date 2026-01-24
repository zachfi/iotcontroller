// Package zigbeedongle provides network state persistence functionality.
package zigbeedongle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// StateFile is the default filename for persisting network state.
const StateFile = "zigbee-network-state.json"

// SaveNetworkState saves the network state to a JSON file.
// This allows the same network to be restored when swapping devices.
func SaveNetworkState(statePath string, params types.NetworkParameters) error {
	state := types.NetworkState{
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

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling network state: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0600); err != nil {
		return fmt.Errorf("writing network state file: %w", err)
	}

	return nil
}

// LoadNetworkState loads the network state from a JSON file.
// Returns nil if the file doesn't exist (network not yet formed).
func LoadNetworkState(statePath string) (*types.NetworkParameters, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No saved state - this is OK
		}
		return nil, fmt.Errorf("reading network state file: %w", err)
	}

	var state types.NetworkState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshaling network state: %w", err)
	}

	return &types.NetworkParameters{
		PanID:         state.PanID,
		ExtendedPanID: state.ExtendedPanID,
		Channel:       state.Channel,
		NetworkKey:    state.NetworkKey,
	}, nil
}
