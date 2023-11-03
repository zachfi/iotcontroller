package zigbee

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	iot "github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

var (
	_ iot.Handler = ZigbeeHandler{}

	defaultConfirmTimout = time.Second * 3
)

type ZigbeeHandler struct {
	mqttClient mqtt.Client

	logger *slog.Logger
	tracer trace.Tracer
}

const defaultTransitionTime = 0.5

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

func (l ZigbeeHandler) On(ctx context.Context, device *iotv1proto.Device) error {
	_, span := l.tracer.Start(ctx, "ZigbeeHandler/On", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))

	topic := fmt.Sprintf("zigbee2mqtt/%s/set", device.Name)
	message := map[string]interface{}{
		"state":      "ON",
		"transition": defaultTransitionTime,
	}

	m, err := json.Marshal(message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	l.mqttClient.Publish(topic, byte(0), false, string(m))
	t := l.mqttClient.Publish(topic, byte(0), false, string(m))
	t.WaitTimeout(defaultConfirmTimout)
	return t.Error()
}

func (l ZigbeeHandler) Off(ctx context.Context, device *iotv1proto.Device) error {
	_, span := l.tracer.Start(ctx, "ZigbeeHandler/Off", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	topic := fmt.Sprintf("zigbee2mqtt/%s/set", device.Name)
	message := map[string]interface{}{
		"state":      "OFF",
		"transition": defaultTransitionTime,
	}

	m, err := json.Marshal(message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	l.mqttClient.Publish(topic, byte(0), false, string(m))
	t := l.mqttClient.Publish(topic, byte(0), false, string(m))
	t.WaitTimeout(defaultConfirmTimout)
	return t.Error()
}

func (l ZigbeeHandler) Alert(ctx context.Context, device *iotv1proto.Device) error {
	_, span := l.tracer.Start(ctx, "ZigbeeHandler/Alert", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	topic := fmt.Sprintf("zigbee2mqtt/%s/set", device.Name)
	message := map[string]interface{}{
		"effect":     "blink",
		"transition": 0.1,
	}

	m, err := json.Marshal(message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	l.mqttClient.Publish(topic, byte(0), false, string(m))
	t := l.mqttClient.Publish(topic, byte(0), false, string(m))
	t.WaitTimeout(defaultConfirmTimout)
	return t.Error()
}

func (l ZigbeeHandler) SetBrightness(ctx context.Context, device *iotv1proto.Device, brightness uint8) error {
	_, span := l.tracer.Start(ctx, "ZigbeeHandler/SetBrightness", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	topic := fmt.Sprintf("zigbee2mqtt/%s/set", device.Name)
	message := map[string]interface{}{
		"brightness": brightness,
		"transition": defaultTransitionTime,
	}
	m, err := json.Marshal(message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	l.mqttClient.Publish(topic, byte(0), false, string(m))
	t := l.mqttClient.Publish(topic, byte(0), false, string(m))
	t.WaitTimeout(defaultConfirmTimout)
	return t.Error()
}

func (l ZigbeeHandler) SetColor(ctx context.Context, device *iotv1proto.Device, hex string) error {
	_, span := l.tracer.Start(ctx, "ZigbeeHandler/SetColor", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	topic := fmt.Sprintf("zigbee2mqtt/%s/set", device.Name)
	message := map[string]interface{}{
		"transition": defaultTransitionTime,
		"color": map[string]string{
			"hex": hex,
		},
	}

	m, err := json.Marshal(message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	l.mqttClient.Publish(topic, byte(0), false, string(m))
	t := l.mqttClient.Publish(topic, byte(0), false, string(m))
	t.WaitTimeout(defaultConfirmTimout)
	return t.Error()
}

func (l ZigbeeHandler) RandomColor(ctx context.Context, device *iotv1proto.Device, hex []string) error {
	_, span := l.tracer.Start(ctx, "ZigbeeHandler/RandomColor", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	topic := fmt.Sprintf("zigbee2mqtt/%s/set", device.Name)
	message := map[string]interface{}{
		"transition": defaultTransitionTime,
		"color": map[string]string{
			"hex": hex[rand.Intn(len(hex))],
		},
	}

	m, err := json.Marshal(message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	l.mqttClient.Publish(topic, byte(0), false, string(m))
	t := l.mqttClient.Publish(topic, byte(0), false, string(m))
	t.WaitTimeout(defaultConfirmTimout)
	return t.Error()
}

func (l ZigbeeHandler) SetColorTemp(ctx context.Context, device *iotv1proto.Device, temp int32) error {
	_, span := l.tracer.Start(ctx, "ZigbeeHandler/SetColorTemp", trace.WithAttributes(
		attribute.String("device_name", device.Name),
		attribute.String("device_type", device.Type.String()),
	))
	defer span.End()

	topic := fmt.Sprintf("zigbee2mqtt/%s/set", device.Name)
	message := map[string]interface{}{
		"transition": defaultTransitionTime,
		"color_temp": temp,
	}

	m, err := json.Marshal(message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	l.mqttClient.Publish(topic, byte(0), false, string(m))
	t := l.mqttClient.Publish(topic, byte(0), false, string(m))
	t.WaitTimeout(defaultConfirmTimout)
	return t.Error()
}
