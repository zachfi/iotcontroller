// Package zigbeedongle provides network state persistence functionality.
package zigbeedongle

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// networkKeyYAML is a 16-byte key that marshals as a hex string (32 chars) and
// unmarshals from either a hex string or a YAML list of 16 integers (0-255).
// This lets users set the key via e.g. openssl rand -hex 16.
type networkKeyYAML [16]byte

func (n *networkKeyYAML) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var v interface{}
	if err := unmarshal(&v); err != nil {
		return err
	}
	switch val := v.(type) {
	case string:
		decoded, err := hex.DecodeString(val)
		if err != nil {
			return fmt.Errorf("network key hex decode: %w", err)
		}
		if len(decoded) != 16 {
			return fmt.Errorf("network key must be 32 hex chars (16 bytes), got %d bytes", len(decoded))
		}
		copy((*n)[:], decoded)
		return nil
	case []interface{}:
		if len(val) != 16 {
			return fmt.Errorf("network key must be 16 bytes, got %d elements", len(val))
		}
		for i := 0; i < 16; i++ {
			var b int
			switch num := val[i].(type) {
			case int:
				b = num
			case float64:
				b = int(num)
			default:
				return fmt.Errorf("network key byte[%d]: expected number, got %T", i, val[i])
			}
			if b < 0 || b > 255 {
				return fmt.Errorf("network key byte[%d]: must be 0-255, got %d", i, b)
			}
			(*n)[i] = byte(b)
		}
		return nil
	default:
		return fmt.Errorf("network key must be hex string (32 chars) or list of 16 bytes, got %T", v)
	}
}

func (n networkKeyYAML) MarshalYAML() (interface{}, error) {
	return hex.EncodeToString(n[:]), nil
}

// StateFile is the default filename for persisting network state.
const StateFile = "zigbee-network-state.yaml"

// IsZeroNetworkKey returns true if the 16-byte key is all zeros.
// An all-zero key is insecure and can appear when state was saved after
// adopting an already-joined device (the key cannot be read from the dongle).
func IsZeroNetworkKey(key [16]byte) bool {
	for _, b := range key {
		if b != 0 {
			return false
		}
	}
	return true
}

// persistedFileFormat is the YAML file layout. Network key is stored as hex for readability.
type persistedFileFormat struct {
	PanID         uint16         `yaml:"panid"`
	ExtendedPanID uint64         `yaml:"extendedpanid"`
	Channel       uint8          `yaml:"channel"`
	NetworkKey    networkKeyYAML `yaml:"networkkey"`
}

// SaveNetworkState saves the network state to a YAML file.
// The network key is written as a 32-character hex string so users can edit it
// (e.g. generate with: openssl rand -hex 16). This allows the same network to
// be restored when swapping devices.
func SaveNetworkState(statePath string, params types.NetworkParameters) error {
	state := persistedFileFormat{
		PanID:         params.PanID,
		ExtendedPanID: params.ExtendedPanID,
		Channel:       params.Channel,
		NetworkKey:    networkKeyYAML(params.NetworkKey),
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(statePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating state directory: %w", err)
		}
	}

	data, err := yaml.Marshal(&state)
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
// The file can be edited by users; network key may be hex (32 chars) or a list of 16 bytes.
// If the loaded NetworkKey is all zeros (e.g. placeholder from adopting an
// already-joined device), the caller must not use it for forming a new
// network; use IsZeroNetworkKey to check and replace with a random key.
func LoadNetworkState(statePath string) (*types.NetworkParameters, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No saved state - this is OK
		}
		return nil, fmt.Errorf("reading network state file: %w", err)
	}

	var state persistedFileFormat
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
		NetworkKey:    [16]byte(state.NetworkKey),
	}, nil
}

// NWKMapPath derives the NWK address map file path from the network state file path.
// For example "network.yaml" → "network-nwkmap.yaml".
func NWKMapPath(statePath string) string {
	ext := filepath.Ext(statePath)
	base := strings.TrimSuffix(statePath, ext)
	return base + "-nwkmap" + ext
}

// nwkMapFileFormat is the YAML layout for the NWK→IEEE address map.
type nwkMapFileFormat struct {
	Entries []nwkMapEntry `yaml:"entries"`
}

type nwkMapEntry struct {
	NWK  uint16 `yaml:"nwk"`
	IEEE uint64 `yaml:"ieee"`
}

// SaveNWKMap persists the NWK→IEEE address map to disk atomically.
// The map file sits next to the network state file (see NWKMapPath).
func SaveNWKMap(statePath string, nwkToIEEE map[uint16]uint64) error {
	mapPath := NWKMapPath(statePath)

	dir := filepath.Dir(mapPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating nwk map directory: %w", err)
		}
	}

	f := nwkMapFileFormat{}
	for nwk, ieee := range nwkToIEEE {
		f.Entries = append(f.Entries, nwkMapEntry{NWK: nwk, IEEE: ieee})
	}

	data, err := yaml.Marshal(&f)
	if err != nil {
		return fmt.Errorf("marshaling nwk map: %w", err)
	}

	tmp := mapPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("writing nwk map tmp: %w", err)
	}
	if err := os.Rename(tmp, mapPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming nwk map: %w", err)
	}
	return nil
}

// LoadNWKMap loads the NWK→IEEE address map from disk.
// Returns an empty map (not an error) when the file does not exist.
func LoadNWKMap(statePath string) (map[uint16]uint64, error) {
	mapPath := NWKMapPath(statePath)
	data, err := os.ReadFile(mapPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[uint16]uint64), nil
		}
		return nil, fmt.Errorf("reading nwk map: %w", err)
	}

	var f nwkMapFileFormat
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("unmarshaling nwk map: %w", err)
	}

	m := make(map[uint16]uint64, len(f.Entries))
	for _, e := range f.Entries {
		m[e.NWK] = e.IEEE
	}
	return m, nil
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
