package nativezigbee

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	iot "github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const defaultTransitionTime = uint32(8) // 0.8s in 1/10s units

var _ iot.Handler = Handler{}

// Handler implements iot.Handler by sending ZCL commands via ZigbeeCommandService gRPC.
type Handler struct {
	client iotv1proto.ZigbeeCommandServiceClient
	logger *slog.Logger
	tracer trace.Tracer
}

func New(client iotv1proto.ZigbeeCommandServiceClient, logger *slog.Logger, tracer trace.Tracer) (*Handler, error) {
	return &Handler{
		client: client,
		logger: logger.With("handler", "nativezigbee"),
		tracer: tracer,
	}, nil
}

func (h Handler) On(ctx context.Context, device *iotv1proto.Device) error {
	_, span := h.tracer.Start(ctx, "NativeZigbeeHandler/On", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("ieee", device.IeeeAddress),
	))
	defer span.End()

	if device.IeeeAddress == "" {
		span.SetStatus(codes.Error, "no ieee_address")
		return fmt.Errorf("device %q has no ieee_address", device.Name)
	}

	resp, err := h.client.SendCommand(ctx, &iotv1proto.SendCommandRequest{
		IeeeAddress: device.IeeeAddress,
		Command:     &iotv1proto.SendCommandRequest_On{On: &iotv1proto.ZigbeeCommandOn{}},
	})
	return checkResp(span, resp, err)
}

func (h Handler) Off(ctx context.Context, device *iotv1proto.Device) error {
	_, span := h.tracer.Start(ctx, "NativeZigbeeHandler/Off", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("ieee", device.IeeeAddress),
	))
	defer span.End()

	if device.IeeeAddress == "" {
		span.SetStatus(codes.Error, "no ieee_address")
		return fmt.Errorf("device %q has no ieee_address", device.Name)
	}

	resp, err := h.client.SendCommand(ctx, &iotv1proto.SendCommandRequest{
		IeeeAddress: device.IeeeAddress,
		Command:     &iotv1proto.SendCommandRequest_Off{Off: &iotv1proto.ZigbeeCommandOff{}},
	})
	return checkResp(span, resp, err)
}

func (h Handler) Alert(ctx context.Context, device *iotv1proto.Device) error {
	// Alert maps to a toggle for native Zigbee (no blink command in ZCL basic profile).
	_, span := h.tracer.Start(ctx, "NativeZigbeeHandler/Alert", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("ieee", device.IeeeAddress),
	))
	defer span.End()

	if device.IeeeAddress == "" {
		span.SetStatus(codes.Error, "no ieee_address")
		return fmt.Errorf("device %q has no ieee_address", device.Name)
	}

	resp, err := h.client.SendCommand(ctx, &iotv1proto.SendCommandRequest{
		IeeeAddress: device.IeeeAddress,
		Command:     &iotv1proto.SendCommandRequest_Toggle{Toggle: &iotv1proto.ZigbeeCommandToggle{}},
	})
	return checkResp(span, resp, err)
}

func (h Handler) SetBrightness(ctx context.Context, device *iotv1proto.Device, brightness uint8) error {
	_, span := h.tracer.Start(ctx, "NativeZigbeeHandler/SetBrightness", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("ieee", device.IeeeAddress),
		attribute.Int("brightness", int(brightness)),
	))
	defer span.End()

	if device.IeeeAddress == "" {
		span.SetStatus(codes.Error, "no ieee_address")
		return fmt.Errorf("device %q has no ieee_address", device.Name)
	}

	resp, err := h.client.SendCommand(ctx, &iotv1proto.SendCommandRequest{
		IeeeAddress: device.IeeeAddress,
		Command: &iotv1proto.SendCommandRequest_SetBrightness{
			SetBrightness: &iotv1proto.ZigbeeCommandSetBrightness{
				Level:          uint32(brightness),
				TransitionTime: defaultTransitionTime,
			},
		},
	})
	return checkResp(span, resp, err)
}

func (h Handler) RandomColor(ctx context.Context, device *iotv1proto.Device, _ []string) error {
	// RandomColor is not directly supported by ZCL; fall back to On.
	return h.On(ctx, device)
}

func (h Handler) SetColor(ctx context.Context, device *iotv1proto.Device, _ string) error {
	// SetColor (RGB hex) is not supported by ZCL color temperature commands; fall back to On.
	return h.On(ctx, device)
}

func (h Handler) SetColorTemp(ctx context.Context, device *iotv1proto.Device, mired int32) error {
	_, span := h.tracer.Start(ctx, "NativeZigbeeHandler/SetColorTemp", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("ieee", device.IeeeAddress),
		attribute.Int("mired", int(mired)),
	))
	defer span.End()

	if device.IeeeAddress == "" {
		span.SetStatus(codes.Error, "no ieee_address")
		return fmt.Errorf("device %q has no ieee_address", device.Name)
	}

	resp, err := h.client.SendCommand(ctx, &iotv1proto.SendCommandRequest{
		IeeeAddress: device.IeeeAddress,
		Command: &iotv1proto.SendCommandRequest_SetColorTemp{
			SetColorTemp: &iotv1proto.ZigbeeCommandSetColorTemp{
				ColorTemperatureMired: uint32(mired),
				TransitionTime:        defaultTransitionTime,
			},
		},
	})
	return checkResp(span, resp, err)
}

func checkResp(span trace.Span, resp *iotv1proto.SendCommandResponse, err error) error {
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if resp != nil && resp.Error != "" {
		span.SetStatus(codes.Error, resp.Error)
		return fmt.Errorf("zigbee command failed: %s", resp.Error)
	}
	span.SetStatus(codes.Ok, "ok")
	return nil
}
