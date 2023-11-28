package conditioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"sigs.k8s.io/controller-runtime/pkg/client"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	module    = "conditioner"
	namespace = "iot"
)

type Conditioner struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	zonekeeperClient iotv1proto.ZoneKeeperServiceClient
	kubeClient       kubeclient.Client
	mqttClient       *mqttclient.MQTTClient
}

func New(cfg Config, logger *slog.Logger, conn *grpc.ClientConn, mqttClient *mqttclient.MQTTClient, kubeclient client.Client) (*Conditioner, error) {
	c := &Conditioner{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		zonekeeperClient: iotv1proto.NewZoneKeeperServiceClient(conn),
		kubeClient:       kubeclient,
		mqttClient:       mqttClient,
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	return c, nil
}

func (c *Conditioner) Alert(ctx context.Context, req *iotv1proto.AlertRequest) (*iotv1proto.AlertResponse, error) {
	ctx, span := c.tracer.Start(ctx, "Conditioner.Alert",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("group", req.Group),
			attribute.String("status", req.Status),
			attribute.String("name", req.Name),
			attribute.String("zone", req.Zone),
			attribute.String("labels", fmt.Sprintf("%+v", req.Labels)),
		),
	)
	defer span.End()

	list := &apiv1.ConditionList{}
	var errs []error

	err := c.kubeClient.List(ctx, list, &client.ListOptions{})
	if err != nil {
		return &iotv1proto.AlertResponse{}, fmt.Errorf("failed to list conditions: %w", err)
	}

	for _, cond := range list.Items {
		span.SetAttributes(attribute.String("condition", cond.Name))
		if !cond.Spec.Enabled {
			span.SetAttributes(attribute.Bool("enabled", false))
			continue
		}
		span.SetAttributes(attribute.Bool("enabled", true))

		if cond.Spec.Alertname != req.Name {
			span.SetAttributes(attribute.Bool("matched", false))
			continue
		}
		span.SetAttributes(attribute.Bool("matched", true))

		for _, rem := range cond.Spec.Remediations {
			var state string

			switch req.Status {
			case "firing":
				state = rem.ActiveState
			case "resolved":
				state = rem.InactiveState
			}

			var zoneState iotv1proto.ZoneState

			switch state {
			case "on":
				zoneState = iotv1proto.ZoneState_ZONE_STATE_ON
			case "off":
				zoneState = iotv1proto.ZoneState_ZONE_STATE_OFF
			case "offtimer":
				zoneState = iotv1proto.ZoneState_ZONE_STATE_OFFTIMER
			case "color":
				zoneState = iotv1proto.ZoneState_ZONE_STATE_COLOR
			case "randomcolor":
				zoneState = iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR
			case "nightvision":
				zoneState = iotv1proto.ZoneState_ZONE_STATE_NIGHTVISION
			case "eveningvision":
				zoneState = iotv1proto.ZoneState_ZONE_STATE_EVENINGVISION
			case "morningvision":
				zoneState = iotv1proto.ZoneState_ZONE_STATE_MORNINGVISION
			}

			_, err := c.zonekeeperClient.SetState(ctx, &iotv1proto.SetStateRequest{
				Name:  rem.Zone,
				State: zoneState,
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to set zone %q to state %q: %w", rem.Zone, zoneState, err))
			}
		}
	}

	if len(errs) > 0 {
		return &iotv1proto.AlertResponse{}, errors.Join(errs...)
	}

	return &iotv1proto.AlertResponse{}, nil
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
