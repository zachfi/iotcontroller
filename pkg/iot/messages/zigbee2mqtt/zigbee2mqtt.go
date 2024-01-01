package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/zachfi/zkit/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	iotutil "github.com/zachfi/iotcontroller/pkg/iot/util"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

type BridgeState string

const (
	RouteName             = "zigbee2mqtt"
	Offline   BridgeState = "offline"
	Online    BridgeState = "online"
)

type Zigbee2Mqtt struct {
	logger           *slog.Logger
	tracer           trace.Tracer
	kubeclient       kubeclient.Client
	zonekeeperClient iotv1proto.ZoneKeeperServiceClient
}

func New(logger *slog.Logger, tracer trace.Tracer, kubeclient kubeclient.Client, conn *grpc.ClientConn) (*Zigbee2Mqtt, error) {
	z := &Zigbee2Mqtt{
		logger:           logger.With("router", RouteName),
		kubeclient:       kubeclient,
		zonekeeperClient: iotv1proto.NewZoneKeeperServiceClient(conn),
		tracer:           tracer,
	}

	return z, nil
}

func (z *Zigbee2Mqtt) DeviceRoute(ctx context.Context, b []byte, vars ...interface{}) error {
	/* m := iot.ZigbeeMessage{} */
	/* err := json.Unmarshal(b, &m) */
	/* if err != nil { */
	/* 	return err */
	/* } */

	// FIXME: do something with a device.  Check telemetry for existing functionality

	return nil
}

func (z *Zigbee2Mqtt) DevicesRoute(ctx context.Context, b []byte, _ ...interface{}) error {
	// TODO: use the new data to populate knowledge of the device in k8s.  This
	// might mean that we adjust the proto to include multiple kinds, or supports
	// multiple features to match the new spec.  It makes me wonder if this new
	// structure is being pushed to more closely match what comes off the wire.
	// If so, this might be alos useful in the future if we get away from
	// zigbee2mqtt and directly implement a character device reading and ZCL
	// parsing..

	m := Devices{}
	err := json.Unmarshal(b, &m)
	if err != nil {
		return err
	}

	for _, d := range m {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := z.handleZigbeeBridgeDevice(ctx, z.logger, d); err != nil {
			z.logger.Error("device report failed", "err", err)
		}
	}

	return nil
}

func (z *Zigbee2Mqtt) handleZigbeeBridgeDevice(ctx context.Context, logger *slog.Logger, d Device) error {
	var err error

	ctx, span := z.tracer.Start(ctx, "Zigbee2Mqtt.handleZigbeeBridgeDevices")
	defer func() {
		_ = tracing.ErrHandler(span, err, "failed to handle zigbee bridge device", nil)
	}()

	span.SetAttributes(
		attribute.String("name", d.FriendlyName),
	)

	/* logger.Debug("device report", "traceID", trace.SpanContextFromContext(ctx).TraceID().String()) */

	var device *apiv1.Device
	device, err = iotutil.GetOrCreateAPIDevice(ctx, z.kubeclient, d.FriendlyName)
	if err != nil {
		return tracing.ErrHandler(span, err, "failed to get or create API device", nil)
	}

	device.Spec.Type = DeviceType(d).String()
	device.Spec.DateCode = d.DateCode
	device.Spec.Model = d.Definition.Model
	device.Spec.Vendor = d.Definition.Vendor
	device.Spec.Description = d.Definition.Description

	span.SetAttributes(
		attribute.String("d", fmt.Sprintf("%+v", d)),
		attribute.String("type", device.Spec.Type),
		attribute.String("model", device.Spec.Model),
		attribute.String("vendor", device.Spec.Vendor),
		attribute.String("description", device.Spec.Description),
	)

	if err = z.kubeclient.Update(ctx, device); err != nil {
		return fmt.Errorf("failed to update device spec: %w", err)
	}

	// Refresh the device after the update
	device, err = iotutil.GetOrCreateAPIDevice(ctx, z.kubeclient, d.FriendlyName)
	if err != nil {
		return tracing.ErrHandler(span, err, "failed to get or create API device", nil)
	}

	device.Status.SoftwareBuildID = d.SoftwareBuildID

	if err = z.kubeclient.Status().Update(ctx, device); err != nil {
		return fmt.Errorf("failed to update device status: %w", err)
	}

	return nil
}
