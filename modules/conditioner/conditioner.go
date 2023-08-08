package conditioner

import (
	"context"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zachfi/iotcontroller/modules/mqttclient"
	iotv1 "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	namespace = "iot"
)

type Conditioner struct {
	services.Service
	cfg *Config

	logger log.Logger
	tracer trace.Tracer

	kubeclient client.Client
	mqttClient *mqttclient.MQTTClient
}

func New(cfg Config, logger log.Logger, mqttClient *mqttclient.MQTTClient, kubeclient client.Client) (*Conditioner, error) {
	c := &Conditioner{
		cfg:        &cfg,
		logger:     log.With(logger, "module", "conditioner"),
		tracer:     otel.Tracer("conditioner"),
		kubeclient: kubeclient,
		mqttClient: mqttClient,
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	return c, nil
}

func (c *Conditioner) Alert(ctx context.Context, req *iotv1.AlertRequest) (*iotv1.AlertResponse, error) {
	_ = level.Info(c.logger).Log("msg", "alert received")
	return &iotv1.AlertResponse{}, nil
}

func (c *Conditioner) starting(ctx context.Context) error {
	return nil
}

func (c *Conditioner) running(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (c *Conditioner) stopping(_ error) error {
	return nil
}
