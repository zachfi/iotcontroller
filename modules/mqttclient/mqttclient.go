package mqttclient

import (
	"context"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-kit/log"
	"github.com/grafana/dskit/services"
	"github.com/zachfi/iotcontroller/pkg/iot"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var module = "mqttclient"

type MQTTClient struct {
	services.Service

	cfg *Config

	client mqtt.Client

	logger log.Logger
	tracer trace.Tracer
}

func New(cfg Config, logger log.Logger) (*MQTTClient, error) {
	m := &MQTTClient{
		cfg:    &cfg,
		logger: log.With(logger, "module", module),
		tracer: otel.Tracer(module),
	}

	m.Service = services.NewBasicService(m.starting, m.running, m.stopping)

	return m, nil
}

func (m *MQTTClient) starting(ctx context.Context) error {
	return nil
}

func (m *MQTTClient) running(ctx context.Context) error {
	t := time.NewTicker(10 * time.Second)

	var client mqtt.Client
	var err error

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if client != nil && client.IsConnected() {
				continue
			}

			client, err = iot.NewMQTTClient(m.cfg.MQTT, m.logger)
			if err != nil {
				return err
			}
			m.client = client
		}
	}
}

func (m *MQTTClient) stopping(_ error) error {
	m.client.Disconnect(1000)
	return nil
}
