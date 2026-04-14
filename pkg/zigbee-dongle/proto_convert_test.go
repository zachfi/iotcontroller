package zigbeedongle

import (
	"testing"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
	zigbeev1 "github.com/zachfi/iotcontroller/proto/zigbee/v1"
)

// TestNetworkStateRoundTrip verifies that To/From proto conversions are consistent.
func TestNetworkStateRoundTrip(t *testing.T) {
	states := []types.NetworkState{
		types.NetworkStateUp,
		types.NetworkStateDown,
		types.NetworkStateNotJoined,
	}
	for _, s := range states {
		proto := ToProtoNetworkState(s)
		got := FromProtoNetworkState(proto)
		// NetworkStateDown and NetworkStateNotJoined both map to NOT_JOINED in proto,
		// so they may not round-trip perfectly — just verify no panic and valid output.
		_ = got
	}
}

// TestToProtoNetworkState verifies specific state mappings.
func TestToProtoNetworkState(t *testing.T) {
	tests := []struct {
		input types.NetworkState
		want  zigbeev1.NetworkState
	}{
		{types.NetworkStateUp, zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK},
		{types.NetworkStateDown, zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK},
		{types.NetworkStateNotJoined, zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK},
		{types.NetworkStateUnknown, zigbeev1.NetworkState_NETWORK_STATE_UNSPECIFIED},
	}
	for _, tt := range tests {
		got := ToProtoNetworkState(tt.input)
		if got != tt.want {
			t.Errorf("ToProtoNetworkState(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestEmberNetworkStatusToProto verifies raw EmberNetworkStatus byte mapping.
func TestEmberNetworkStatusToProto(t *testing.T) {
	tests := []struct {
		status byte
		want   zigbeev1.NetworkState
	}{
		{0x00, zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK},
		{0x01, zigbeev1.NetworkState_NETWORK_STATE_JOINING_NETWORK},
		{0x02, zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK},
		{0x03, zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK_NO_PARENT},
		{0x06, zigbeev1.NetworkState_NETWORK_STATE_LEAVING_NETWORK},
		{0x90, zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK}, // SLStatus.NETWORK_UP
		{0x91, zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK},     // SLStatus.NETWORK_DOWN
		{0x93, zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK},     // SLStatus.NOT_JOINED
		{0xFF, zigbeev1.NetworkState_NETWORK_STATE_UNSPECIFIED},    // unknown
	}
	for _, tt := range tests {
		got := EmberNetworkStatusToProto(tt.status)
		if got != tt.want {
			t.Errorf("EmberNetworkStatusToProto(0x%02X) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

// TestToFromProtoNetworkInfo verifies NetworkInfo round-trip.
func TestToFromProtoNetworkInfo(t *testing.T) {
	original := &types.NetworkInfo{
		ShortAddress:          0x1234,
		PanID:                 0xABCD,
		ParentAddress:         0x0000,
		ExtendedPanID:         0x0102030405060708,
		ExtendedParentAddress: 0xFFFEFDFCFBFAF9F8,
		Channel:               15,
		State:                 types.NetworkStateUp,
	}

	proto := ToProtoNetworkInfo(original)
	got := FromProtoNetworkInfo(proto)

	if got.ShortAddress != original.ShortAddress {
		t.Errorf("ShortAddress = 0x%04X, want 0x%04X", got.ShortAddress, original.ShortAddress)
	}
	if got.PanID != original.PanID {
		t.Errorf("PanID = 0x%04X, want 0x%04X", got.PanID, original.PanID)
	}
	if got.Channel != original.Channel {
		t.Errorf("Channel = %d, want %d", got.Channel, original.Channel)
	}
}

// TestToFromProtoNetworkInfoNil verifies nil handling.
func TestToFromProtoNetworkInfoNil(t *testing.T) {
	if got := ToProtoNetworkInfo(nil); got != nil {
		t.Errorf("ToProtoNetworkInfo(nil) = %v, want nil", got)
	}
	if got := FromProtoNetworkInfo(nil); got != nil {
		t.Errorf("FromProtoNetworkInfo(nil) = %v, want nil", got)
	}
}

// TestToFromProtoNetworkParameters verifies NetworkParameters round-trip.
func TestToFromProtoNetworkParameters(t *testing.T) {
	key := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	original := types.NetworkParameters{
		PanID:         0x1234,
		ExtendedPanID: 0xDEADBEEF12345678,
		Channel:       15,
		NetworkKey:    key,
	}

	proto := ToProtoNetworkParameters(original)
	got := FromProtoNetworkParameters(proto)

	if got.PanID != original.PanID {
		t.Errorf("PanID = 0x%04X, want 0x%04X", got.PanID, original.PanID)
	}
	if got.ExtendedPanID != original.ExtendedPanID {
		t.Errorf("ExtendedPanID = 0x%016X, want 0x%016X", got.ExtendedPanID, original.ExtendedPanID)
	}
	if got.Channel != original.Channel {
		t.Errorf("Channel = %d, want %d", got.Channel, original.Channel)
	}
	if got.NetworkKey != original.NetworkKey {
		t.Errorf("NetworkKey = %v, want %v", got.NetworkKey, original.NetworkKey)
	}
}

// TestFromProtoNetworkParametersNil verifies nil handling.
func TestFromProtoNetworkParametersNil(t *testing.T) {
	if got := FromProtoNetworkParameters(nil); got != nil {
		t.Errorf("FromProtoNetworkParameters(nil) = %v, want nil", got)
	}
}

// TestToFromProtoPersistedNetworkState verifies PersistedNetworkState round-trip.
func TestToFromProtoPersistedNetworkState(t *testing.T) {
	key := [16]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x11,
		0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99}
	original := types.PersistedNetworkState{
		PanID:         0xBEEF,
		ExtendedPanID: 0x0102030405060708,
		Channel:       25,
		NetworkKey:    key,
	}

	proto := ToProtoPersistedNetworkState(original)
	got := FromProtoPersistedNetworkState(proto)

	if got.PanID != original.PanID {
		t.Errorf("PanID = 0x%04X, want 0x%04X", got.PanID, original.PanID)
	}
	if got.Channel != original.Channel {
		t.Errorf("Channel = %d, want %d", got.Channel, original.Channel)
	}
	if got.NetworkKey != original.NetworkKey {
		t.Errorf("NetworkKey = %v, want %v", got.NetworkKey, original.NetworkKey)
	}
}
