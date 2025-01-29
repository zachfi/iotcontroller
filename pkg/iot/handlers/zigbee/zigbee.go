package zigbee

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	iot "github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const defaultTransitionTime = 0.8

var (
	_ iot.Handler = Handler{}

	defaultConfirmTimout = time.Second * 3
)

type Handler struct {
	publish publishFunc

	logger *slog.Logger
	tracer trace.Tracer
}

type publishFunc func(topic string, qos byte, retained bool, payload interface{}) mqtt.Token

func New(f publishFunc, logger *slog.Logger, tracer trace.Tracer) (*Handler, error) {
	return &Handler{
		publish: f,
		logger:  logger.With("handler", "zigbee"),
		tracer:  tracer,
	}, nil
}

func (h Handler) On(ctx context.Context, device *iotv1proto.Device) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/On", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))

	msg := newMessage(device)
	msg.withOn()

	return h.send(ctx, span, msg)
}

func (h Handler) Off(ctx context.Context, device *iotv1proto.Device) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/Off", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	msg := newMessage(device)
	msg.withOff()

	return h.send(ctx, span, msg)
}

func (h Handler) Alert(ctx context.Context, device *iotv1proto.Device) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/Alert", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	msg := newMessage(device)
	msg.withBlink()

	return h.send(ctx, span, msg)
}

func (h Handler) SetBrightness(ctx context.Context, device *iotv1proto.Device, brightness uint8) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/SetBrightness", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
		attribute.Int("brightness", int(brightness)),
	))
	defer span.End()

	msg := newMessage(device)
	msg.withBrightness(brightness)

	return h.send(ctx, span, msg)
}

func (h Handler) SetColor(ctx context.Context, device *iotv1proto.Device, hex string) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/SetColor", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
		attribute.String("hex", hex),
	))
	defer span.End()

	return h.send(ctx, span, newMessage(device).withColor(hex))
}

func (h Handler) RandomColor(ctx context.Context, device *iotv1proto.Device, hex []string) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/RandomColor", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
		attribute.StringSlice("hex", hex),
	))
	defer span.End()

	msg := newMessage(device)
	msg.withRandomColor(hex)

	return h.send(ctx, span, msg)
}

func (h Handler) SetColorTemp(ctx context.Context, device *iotv1proto.Device, temp int32) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/SetColorTemp", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
		attribute.Int("temp", int(temp)),
	))
	defer span.End()

	msg := newMessage(device)
	msg.withColorTemp(temp)

	return h.send(ctx, span, msg)
}

// func (mqtt.Client) Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token

func (h Handler) send(_ context.Context, span trace.Span, msg *message) error {
	m, err := json.Marshal(msg.msg)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	t := h.publish(msg.topic, byte(0), false, string(m))
	t.WaitTimeout(defaultConfirmTimout)
	return errHandler(span, t.Error())
}

func errHandler(span trace.Span, err error) error {
	defer span.End()

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "ok")
	}
	return err
}
