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

	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	module = "conditioner"

	// Alertmanager webhook payload
	alertStatusFiring   = "firing"
	alertStatusResolved = "resolved"
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

func New(cfg Config, logger *slog.Logger, conn *grpc.ClientConn, mqttClient *mqttclient.MQTTClient, k kubeclient.Client) (*Conditioner, error) {
	c := &Conditioner{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		zonekeeperClient: iotv1proto.NewZoneKeeperServiceClient(conn),
		kubeClient:       k,
		mqttClient:       mqttClient,
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	return c, nil
}

func (c *Conditioner) Event(ctx context.Context, req *iotv1proto.EventRequest) (*iotv1proto.EventResponse, error) {
	attributes := []attribute.KeyValue{
		attribute.String("name", req.Name),
	}

	for k, v := range req.Labels {
		attributes = append(attributes, attribute.String(k, v))
	}

	ctx, span := c.tracer.Start(ctx, "Conditioner.Event",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attributes...),
	)
	defer span.End()

	var (
		list  = &apiv1.ConditionList{}
		errs  []error
		match bool
	)

	err := c.kubeClient.List(ctx, list, &kubeclient.ListOptions{})
	if err != nil {
		return &iotv1proto.EventResponse{}, fmt.Errorf("failed to list conditions: %w", err)
	}

	for _, cond := range list.Items {
		match = c.matchCondition(ctx, req.Labels, cond)
		if !match {
			continue
		}

		err = c.runConditionEvent(ctx, req, cond)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return &iotv1proto.EventResponse{}, errors.Join(errs...)
	}

	return &iotv1proto.EventResponse{}, nil
}

func (c *Conditioner) matchCondition(_ context.Context, labels map[string]string, cond apiv1.Condition) bool {
	if !cond.Spec.Enabled {
		return false
	}

	if len(cond.Spec.Matches) == 0 {
		return false
	}

	// Check that the labels are matched for each of the condition matchers.
	for _, match := range cond.Spec.Matches {
		for k, v := range match.Labels {
			if vv, ok := labels[k]; ok {
				if vv != v {
					return false
				}
			} else {
				return false
			}
		}
	}

	return true
}

func (c *Conditioner) runConditionEvent(ctx context.Context, req *iotv1proto.EventRequest, cond apiv1.Condition) error {
	attributes := []attribute.KeyValue{
		attribute.String("condition", cond.Name),
		attribute.Bool("enabled", cond.Spec.Enabled),
	}

	ctx, span := c.tracer.Start(ctx, "Conditioner.runConditionEvent",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attributes...),
	)
	defer span.End()

	for _, rem := range cond.Spec.Remediations {
		var (
			state     string
			zoneState = iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED
		)

		// A status exists on alerts, so lets check for it so that we can handle it below.
		if status, ok := req.Labels[iot.StatusLabel]; ok {
			switch status {
			case alertStatusFiring:
				if rem.ActiveState != "" {
					state = rem.ActiveState
				}
			case alertStatusResolved:
				if rem.InactiveState != "" {
					state = rem.InactiveState
				}
			}
		}

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
		default:
			if s, ok := iotv1proto.ZoneState_value[state]; ok {
				zoneState = iotv1proto.ZoneState(s)
			}
		}

		if zoneState > iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
			_, err := c.zonekeeperClient.SetState(ctx, &iotv1proto.SetStateRequest{
				Name:  rem.Zone,
				State: zoneState,
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Conditioner) starting(_ context.Context) error {
	return nil
}

func (c *Conditioner) running(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (c *Conditioner) stopping(_ error) error {
	return nil
}
