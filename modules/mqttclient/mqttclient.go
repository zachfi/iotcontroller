package mqttclient

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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
	sync.Mutex

	cfg *Config

	client    mqtt.Client
	clientCtx context.Context

	logger *slog.Logger
	tracer trace.Tracer
}

func New(cfg Config, logger *slog.Logger) (*MQTTClient, error) {
	m := &MQTTClient{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),
	}

	err := m.replaceClient()
	if err != nil {
		return nil, err
	}

	m.Service = services.NewBasicService(m.starting, m.running, m.stopping)

	return m, nil
}

func (m *MQTTClient) Unsubscribe() mqtt.Token {
	return m.client.Unsubscribe(m.cfg.MQTT.Topic)
}

func (m *MQTTClient) Subscribe(f mqtt.MessageHandler) mqtt.Token {
	return m.client.Subscribe(m.cfg.MQTT.Topic, 0, f)
}

func (m *MQTTClient) Publish(topic string, qos byte, retained bool, payload any) mqtt.Token {
	return m.client.Publish(topic, qos, retained, payload)
}

func (m *MQTTClient) CheckHealth() error {
	if m.client == nil || !m.client.IsConnected() {
		return fmt.Errorf("mqtt client is not connected")
	}

	return nil
}

func (m *MQTTClient) starting(_ context.Context) error {
	m.logger.Info("connecting to MQTT broker", "broker", m.cfg.MQTT.URL)
	token := m.client.Connect()
	m.logger.Debug("waiting for MQTT connection")
	if ok := token.WaitTimeout(time.Minute); ok {
		return nil
	}

	err := token.Error()
	if err != nil {
		m.logger.Error("failed to connect to MQTT broker", "error", err)
		return err
	}

	return nil
}

func (m *MQTTClient) running(ctx context.Context) error {
	var (
		t           = time.NewTicker(10 * time.Second)
		failures    = 0
		maxFailures = 3
	)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.clientCtx.Done():
			err := m.replaceClient()
			if err != nil {
				metricMQTTClientReplacementError.Inc()
				return err
			}

			metricMQTTClientReplaced.Inc()

			continue
		case <-t.C:
			if m.client != nil && m.client.IsConnected() {
				failures = 0
				continue
			}

			failures++

			if failures > maxFailures {
				return fmt.Errorf("failed to reconnect to MQTT broker after %d attempts", maxFailures)
			}

			m.logger.Info("MQTT client is not connected")
		}
	}
}

func (m *MQTTClient) stopping(_ error) error {
	if m.client != nil {
		m.logger.Info("disconnecting from MQTT broker")
		m.client.Disconnect(10000)
	}
	return nil
}

// replaceClient is used to replace an MQTT client in the case of a failure.
func (m *MQTTClient) replaceClient() error {
	m.Lock()
	defer m.Unlock()
	if m.client != nil {
		m.logger.Info("replacing MQTT client")
		m.client.Disconnect(1000)
	}

	client, clientCtx, err := iot.NewMQTTClient(m.cfg.MQTT, m.logger)
	if err != nil {
		return err
	}
	m.client = client
	m.clientCtx = clientCtx

	return nil
}
