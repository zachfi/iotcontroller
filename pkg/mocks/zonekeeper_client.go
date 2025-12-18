package mocks

import (
	"context"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	"google.golang.org/grpc"
)

var _ iotv1proto.ZoneKeeperServiceClient = (*ZoneKeeperClientMock)(nil)

type ZoneKeeperClientMock struct {
	zones map[string]struct {
		scene string
		state iotv1proto.ZoneState
	}
}

func (z *ZoneKeeperClientMock) ActionHandler(ctx context.Context, in *iotv1proto.ActionHandlerRequest, opts ...grpc.CallOption) (*iotv1proto.ActionHandlerResponse, error) {
	return &iotv1proto.ActionHandlerResponse{}, nil
}

func (z *ZoneKeeperClientMock) SetState(ctx context.Context, in *iotv1proto.SetStateRequest, opts ...grpc.CallOption) (*iotv1proto.SetStateResponse, error) {
	return &iotv1proto.SetStateResponse{}, nil
}

func (z *ZoneKeeperClientMock) SetScene(ctx context.Context, in *iotv1proto.SetSceneRequest, opts ...grpc.CallOption) (*iotv1proto.SetSceneResponse, error) {
	return &iotv1proto.SetSceneResponse{}, nil
}

func (z *ZoneKeeperClientMock) GetDeviceZone(ctx context.Context, in *iotv1proto.GetDeviceZoneRequest, opts ...grpc.CallOption) (*iotv1proto.GetDeviceZoneResponse, error) {
	return &iotv1proto.GetDeviceZoneResponse{}, nil
}

func (z *ZoneKeeperClientMock) SelfAnnounce(ctx context.Context, in *iotv1proto.SelfAnnounceRequest, opts ...grpc.CallOption) (*iotv1proto.SelfAnnounceResponse, error) {
	return &iotv1proto.SelfAnnounceResponse{}, nil
}
