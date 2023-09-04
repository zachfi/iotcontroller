package conditioner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/fields"

	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	iotv1 "github.com/zachfi/iotcontroller/proto/iot/v1"
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

	iotClient  iotv1.ZoneServiceClient
	kubeclient client.Client
	mqttClient *mqttclient.MQTTClient
}

func New(cfg Config, logger *slog.Logger, conn *grpc.ClientConn, mqttClient *mqttclient.MQTTClient, kubeclient client.Client) (*Conditioner, error) {
	c := &Conditioner{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		iotClient:  iotv1.NewZoneServiceClient(conn),
		kubeclient: kubeclient,
		mqttClient: mqttClient,
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	return c, nil
}

func (c *Conditioner) Alert(ctx context.Context, req *iotv1.AlertRequest) (*iotv1.AlertResponse, error) {
	c.logger.With(
		"group", req.Group,
		"status", req.Status,
		"labels", fmt.Sprintf("%+v", req.GroupLabels),
	).Info("alert received")

	_, span := c.tracer.Start(
		ctx,
		"Conditioner.Alert",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	// TODO: we should query for the condition objects, and find any that match this alert name.  Then we should switch the zone state based on the condition, and flush the zone.  Perhaps we want to keep track of the previous state, so that when the alert clears.  Or perhaps we want to have an ENABLED state and a DISABLED, or ACTIVE/INACTIVE.  This way, there is a definitive on, and off condition to match the alert firing.
	var list *apiv1.ConditionList

	set := fields.SelectorFromSet(fields.Set{"alert_name": req.Group})
	listOptions := &client.ListOptions{FieldSelector: set}
	c.kubeclient.List(ctx, list, listOptions)

	for _, cond := range list.Items {
		for _, rem := range cond.Spec.Remediations {
			var state string

			switch req.Status {
			case "firing":
				state = rem.ActiveState
			case "resolved":
				state = rem.InactiveState
			}

			var zoneState iotv1.ZoneState

			switch state {
			case "on":
				zoneState = iotv1.ZoneState_ZONE_STATE_ON
			case "off":
				zoneState = iotv1.ZoneState_ZONE_STATE_OFF
			case "offtimer":
				zoneState = iotv1.ZoneState_ZONE_STATE_OFFTIMER
			case "color":
				zoneState = iotv1.ZoneState_ZONE_STATE_COLOR
			case "randomcolor":
				zoneState = iotv1.ZoneState_ZONE_STATE_RANDOMCOLOR
			case "nightvision":
				zoneState = iotv1.ZoneState_ZONE_STATE_NIGHTVISION
			case "eveningvision":
				zoneState = iotv1.ZoneState_ZONE_STATE_EVENINGVISION
			case "morningvision":
				zoneState = iotv1.ZoneState_ZONE_STATE_MORNINGVISION
			}

			c.iotClient.SetState(ctx, &iotv1.ZoneServiceSetStateRequest{
				Name:  rem.Zone,
				State: zoneState,
			})

		}
	}

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
