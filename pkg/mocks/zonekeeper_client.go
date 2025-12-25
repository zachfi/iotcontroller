package mocks

import (
	"context"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	"google.golang.org/grpc"
)

var _ iotv1proto.ZoneKeeperServiceClient = (*ZoneKeeperClientMock)(nil)

type ZoneKeeperClientMock struct {
	// zones map[string]struct {
	// 	scene string
	// 	state iotv1proto.ZoneState
	// }
}

func (z *ZoneKeeperClientMock) ActionHandler(_ context.Context, _ *iotv1proto.ActionHandlerRequest, _ ...grpc.CallOption) (*iotv1proto.ActionHandlerResponse, error) {
	return &iotv1proto.ActionHandlerResponse{}, nil
}

func (z *ZoneKeeperClientMock) SetState(_ context.Context, _ *iotv1proto.SetStateRequest, _ ...grpc.CallOption) (*iotv1proto.SetStateResponse, error) {
	return &iotv1proto.SetStateResponse{}, nil
}

func (z *ZoneKeeperClientMock) SetScene(_ context.Context, _ *iotv1proto.SetSceneRequest, _ ...grpc.CallOption) (*iotv1proto.SetSceneResponse, error) {
	return &iotv1proto.SetSceneResponse{}, nil
}

func (z *ZoneKeeperClientMock) GetDeviceZone(_ context.Context, _ *iotv1proto.GetDeviceZoneRequest, _ ...grpc.CallOption) (*iotv1proto.GetDeviceZoneResponse, error) {
	return &iotv1proto.GetDeviceZoneResponse{}, nil
}

func (z *ZoneKeeperClientMock) SelfAnnounce(_ context.Context, _ *iotv1proto.SelfAnnounceRequest, _ ...grpc.CallOption) (*iotv1proto.SelfAnnounceResponse, error) {
	return &iotv1proto.SelfAnnounceResponse{}, nil
}

func (z *ZoneKeeperClientMock) OccupancyHandler(_ context.Context, _ *iotv1proto.OccupancyHandlerRequest, _ ...grpc.CallOption) (*iotv1proto.OccupancyHandlerResponse, error) {
	return &iotv1proto.OccupancyHandlerResponse{}, nil
}
