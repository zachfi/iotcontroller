package mqttclient

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/iotcontroller/pkg/iot"
)

var module = "mqttclient"

type MQTTClient struct {
	services.Service

	cfg *Config

	client mqtt.Client

	logger *slog.Logger
	tracer trace.Tracer
}

func New(cfg Config, logger *slog.Logger) (*MQTTClient, error) {
	m := &MQTTClient{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),
	}

	client, err := iot.NewMQTTClient(m.cfg.MQTT, m.logger)
	if err != nil {
		return nil, err
	}

	m.client = client
	m.Service = services.NewBasicService(m.starting, m.running, m.stopping)

	return m, nil
}

func (m *MQTTClient) Unsubscribe() mqtt.Token {
	return m.client.Unsubscribe(m.cfg.MQTT.Topic)
}

func (m *MQTTClient) Subscribe(f mqtt.MessageHandler) mqtt.Token {
	return m.client.Subscribe(m.cfg.MQTT.Topic, 0, f)
}

func (m *MQTTClient) Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token {
	return m.client.Publish(topic, qos, retained, payload)
}

func (m *MQTTClient) CheckHealth() error {
	if m.client == nil || !m.client.IsConnected() {
		return fmt.Errorf("mqtt client is not connected")
	}

	return nil
}

func (m *MQTTClient) starting(ctx context.Context) error {
	m.logger.Info("connecting to MQTT broker", "broker", m.cfg.MQTT.URL)
	token := m.client.Connect()
	m.logger.Debug("waiting for MQTT connection")
	token.Wait()

	err := token.Error()
	if err != nil {
		return err
	}

	return nil
}

func (m *MQTTClient) running(ctx context.Context) error {
	t := time.NewTicker(10 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if m.client != nil && m.client.IsConnected() {
				continue
			}

			m.logger.Info("MQTT client is not connected")
		}
	}
}

func (m *MQTTClient) stopping(_ error) error {
	m.logger.Info("disconnecting from MQTT broker")
	if m.client != nil {
		m.client.Disconnect(100)
	}
	return nil
}
