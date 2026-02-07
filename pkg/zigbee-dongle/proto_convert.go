// Package zigbeedongle provides conversion functions between proto types and internal types.
package zigbeedongle

import (
	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
	zigbeev1 "github.com/zachfi/iotcontroller/proto/zigbee/v1"
)

// ToProtoNetworkState converts an internal NetworkState to proto NetworkState.
// Maps our simplified states to the proto enum values (which are based on EmberNetworkStatus).
func ToProtoNetworkState(state types.NetworkState) zigbeev1.NetworkState {
	switch state {
	case types.NetworkStateUp:
		return zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK
	case types.NetworkStateDown:
		return zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK // Or could be NO_PARENT
	case types.NetworkStateNotJoined:
		return zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK
	default:
		return zigbeev1.NetworkState_NETWORK_STATE_UNSPECIFIED
	}
}

// FromProtoNetworkState converts a proto NetworkState to internal NetworkState.
// Proto NetworkState is based on EmberNetworkStatus enum (0-6).
func FromProtoNetworkState(state zigbeev1.NetworkState) types.NetworkState {
	switch state {
	case zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK:
		return types.NetworkStateUp
	case zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK:
		return types.NetworkStateNotJoined
	case zigbeev1.NetworkState_NETWORK_STATE_JOINING_NETWORK:
		return types.NetworkStateNotJoined // Treat joining as not fully joined
	case zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK_NO_PARENT:
		return types.NetworkStateDown
	case zigbeev1.NetworkState_NETWORK_STATE_LEAVING_NETWORK:
		return types.NetworkStateDown
	default:
		return types.NetworkStateUnknown
	}
}

// EmberNetworkStatusToProto converts a raw EmberNetworkStatus byte value to proto NetworkState.
// EmberNetworkStatus values: 0=NO_NETWORK, 1=JOINING, 2=JOINED, 3=JOINED_NO_PARENT, 6=LEAVING
// Also handles SLStatus codes: 0x90=NETWORK_UP, 0x91=NETWORK_DOWN, 0x93=NOT_JOINED
func EmberNetworkStatusToProto(status byte) zigbeev1.NetworkState {
	switch status {
	case 0: // EmberNetworkStatus.NO_NETWORK
		return zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK
	case 1: // EmberNetworkStatus.JOINING_NETWORK
		return zigbeev1.NetworkState_NETWORK_STATE_JOINING_NETWORK
	case 2: // EmberNetworkStatus.JOINED_NETWORK
		return zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK
	case 3: // EmberNetworkStatus.JOINED_NETWORK_NO_PARENT
		return zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK_NO_PARENT
	case 6: // EmberNetworkStatus.LEAVING_NETWORK
		return zigbeev1.NetworkState_NETWORK_STATE_LEAVING_NETWORK
	case 0x90: // SLStatus.NETWORK_UP (legacy/firmware variant)
		return zigbeev1.NetworkState_NETWORK_STATE_JOINED_NETWORK
	case 0x91: // SLStatus.NETWORK_DOWN (legacy/firmware variant)
		return zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK
	case 0x93: // SLStatus.NOT_JOINED (legacy/firmware variant)
		return zigbeev1.NetworkState_NETWORK_STATE_NO_NETWORK
	default:
		return zigbeev1.NetworkState_NETWORK_STATE_UNSPECIFIED
	}
}

// ToProtoNetworkInfo converts internal NetworkInfo to proto NetworkInfo.
func ToProtoNetworkInfo(info *types.NetworkInfo) *zigbeev1.NetworkInfo {
	if info == nil {
		return nil
	}
	return &zigbeev1.NetworkInfo{
		ShortAddress:          uint32(info.ShortAddress),
		PanId:                 uint32(info.PanID),
		ParentAddress:         uint32(info.ParentAddress),
		ExtendedPanId:         info.ExtendedPanID,
		ExtendedParentAddress: info.ExtendedParentAddress,
		Channel:               uint32(info.Channel),
		State:                 ToProtoNetworkState(info.State),
	}
}

// FromProtoNetworkInfo converts proto NetworkInfo to internal NetworkInfo.
func FromProtoNetworkInfo(info *zigbeev1.NetworkInfo) *types.NetworkInfo {
	if info == nil {
		return nil
	}
	return &types.NetworkInfo{
		ShortAddress:          uint16(info.ShortAddress),
		PanID:                 uint16(info.PanId),
		ParentAddress:         uint16(info.ParentAddress),
		ExtendedPanID:         info.ExtendedPanId,
		ExtendedParentAddress: info.ExtendedParentAddress,
		Channel:               uint16(info.Channel),
		State:                 FromProtoNetworkState(info.State),
	}
}

// ToProtoNetworkParameters converts internal NetworkParameters to proto NetworkParameters.
func ToProtoNetworkParameters(params types.NetworkParameters) *zigbeev1.NetworkParameters {
	return &zigbeev1.NetworkParameters{
		PanId:         uint32(params.PanID),
		ExtendedPanId: params.ExtendedPanID,
		Channel:       uint32(params.Channel),
		NetworkKey:    params.NetworkKey[:],
	}
}

// FromProtoNetworkParameters converts proto NetworkParameters to internal NetworkParameters.
func FromProtoNetworkParameters(params *zigbeev1.NetworkParameters) *types.NetworkParameters {
	if params == nil {
		return nil
	}
	var networkKey [16]byte
	copy(networkKey[:], params.NetworkKey)
	return &types.NetworkParameters{
		PanID:         uint16(params.PanId),
		ExtendedPanID: params.ExtendedPanId,
		Channel:       uint8(params.Channel),
		NetworkKey:    networkKey,
	}
}

// ToProtoPersistedNetworkState converts internal PersistedNetworkState to proto PersistedNetworkState.
func ToProtoPersistedNetworkState(state types.PersistedNetworkState) *zigbeev1.PersistedNetworkState {
	return &zigbeev1.PersistedNetworkState{
		PanId:         uint32(state.PanID),
		ExtendedPanId: state.ExtendedPanID,
		Channel:       uint32(state.Channel),
		NetworkKey:    state.NetworkKey[:],
	}
}

// FromProtoPersistedNetworkState converts proto PersistedNetworkState to internal PersistedNetworkState.
func FromProtoPersistedNetworkState(state *zigbeev1.PersistedNetworkState) *types.PersistedNetworkState {
	if state == nil {
		return nil
	}
	var networkKey [16]byte
	copy(networkKey[:], state.NetworkKey)
	return &types.PersistedNetworkState{
		PanID:         uint16(state.PanId),
		ExtendedPanID: state.ExtendedPanId,
		Channel:       uint8(state.Channel),
		NetworkKey:    networkKey,
	}
}
