package iot

import (
	"context"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// Handler is a basic handler.
type Handler interface {
	Off(context.Context, *iotv1proto.Device) error
	On(context.Context, *iotv1proto.Device) error
	Alert(context.Context, *iotv1proto.Device) error
	SetBrightness(context.Context, *iotv1proto.Device, uint8) error
	RandomColor(context.Context, *iotv1proto.Device, []string) error
	SetColor(context.Context, *iotv1proto.Device, string) error
	SetColorTemp(context.Context, *iotv1proto.Device, int32) error
}
