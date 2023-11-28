package mock

import (
	"context"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

type MockHandler struct{}

func (h MockHandler) Off(context.Context, *iotv1proto.Device) error {
	return nil
}

func (h MockHandler) On(context.Context, *iotv1proto.Device) error {
	return nil
}

func (h MockHandler) Alert(context.Context, *iotv1proto.Device) error {
	return nil
}

func (h MockHandler) SetBrightness(context.Context, *iotv1proto.Device, uint8) error {
	return nil
}

func (h MockHandler) RandomColor(context.Context, *iotv1proto.Device, []string) error {
	return nil
}

func (h MockHandler) SetColor(context.Context, *iotv1proto.Device, string) error {
	return nil
}

func (h MockHandler) SetColorTemp(context.Context, *iotv1proto.Device, int32) error {
	return nil
}
