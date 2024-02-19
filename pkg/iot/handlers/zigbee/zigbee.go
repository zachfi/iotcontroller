package zigbee

import (
	"context"
	"encoding/json"
	"fmt"
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
	_ iot.Handler = ZigbeeHandler{}

	defaultConfirmTimout = time.Second * 3
)

type ZigbeeHandler struct {
	mqttClient mqtt.Client

	logger *slog.Logger
	tracer trace.Tracer
}

func New(mqttClient mqtt.Client, logger *slog.Logger, tracer trace.Tracer) (*ZigbeeHandler, error) {
	if mqttClient == nil {
		return nil, fmt.Errorf("mqttClient cannot be nil")
	}

	return &ZigbeeHandler{
		mqttClient: mqttClient,
		logger:     logger.With("handler", "zigbee"),
		tracer:     tracer,
	}, nil
}

func (h ZigbeeHandler) On(ctx context.Context, device *iotv1proto.Device) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/On", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))

	msg := newMessage(device)
	msg.withOn()

	return h.send(ctx, span, msg)
}

func (h ZigbeeHandler) Off(ctx context.Context, device *iotv1proto.Device) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/Off", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	msg := newMessage(device)
	msg.withOff()

	return h.send(ctx, span, msg)
}

func (h ZigbeeHandler) Alert(ctx context.Context, device *iotv1proto.Device) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/Alert", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	msg := newMessage(device)
	msg.withBlink()

	return h.send(ctx, span, msg)
}

func (h ZigbeeHandler) SetBrightness(ctx context.Context, device *iotv1proto.Device, brightness uint8) error {
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

func (h ZigbeeHandler) SetColor(ctx context.Context, device *iotv1proto.Device, hex string) error {
	_, span := h.tracer.Start(ctx, "ZigbeeHandler/SetColor", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
		attribute.String("hex", hex),
	))
	defer span.End()

	return h.send(ctx, span, newMessage(device).withColor(hex))
}

func (h ZigbeeHandler) RandomColor(ctx context.Context, device *iotv1proto.Device, hex []string) error {
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

func (h ZigbeeHandler) SetColorTemp(ctx context.Context, device *iotv1proto.Device, temp int32) error {
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

func (h ZigbeeHandler) send(ctx context.Context, span trace.Span, msg *message) error {
	m, err := json.Marshal(msg.msg)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	t := h.mqttClient.Publish(msg.topic, byte(0), false, string(m))
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
