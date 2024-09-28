package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/zachfi/zkit/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotutil "github.com/zachfi/iotcontroller/pkg/iot/util"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

type BridgeState string

const (
	routeName = "zigbee2mqtt"

	Offline BridgeState = "offline"
	Online  BridgeState = "online"

	StateON  = "ON"
	StateOFF = "OFF"
)

type Zigbee2Mqtt struct {
	logger *slog.Logger
	tracer trace.Tracer

	kubeclient kubeclient.Client

	zonekeeperClient iotv1proto.ZoneKeeperServiceClient
	/* reportStream     telemetryv1proto.TelemetryService_TelemetryReportIOTDeviceClient */
}

func New(logger *slog.Logger, tracer trace.Tracer, kubeclient kubeclient.Client, zonekeeperClient iotv1proto.ZoneKeeperServiceClient) (*Zigbee2Mqtt, error) {
	z := &Zigbee2Mqtt{
		logger:           logger.With("router", routeName),
		tracer:           tracer,
		kubeclient:       kubeclient,
		zonekeeperClient: zonekeeperClient,
	}

	return z, nil
}

func (z *Zigbee2Mqtt) BridgeStateRoute(_ context.Context, b []byte) error {
	switch BridgeState(string(b)) {
	case Offline:
		metricIOTBridgeState.WithLabelValues().Set(float64(0))
	case Online:
		metricIOTBridgeState.WithLabelValues().Set(float64(1))
	}

	return nil
}

func (z *Zigbee2Mqtt) DeviceRoute(ctx context.Context, b []byte, vars ...interface{}) error {
	var (
		device   *apiv1.Device
		deviceID string
		err      error
		m        = ZigbeeMessage{}
		name     string
		wg       = sync.WaitGroup{}
		zone     string
	)
	_, span := z.tracer.Start(ctx, "Zigbee2Mqtt.DeviceRoute")
	defer tracing.ErrHandler(span, err, "device route failed", z.logger)

	err = json.Unmarshal(b, &m)
	if err != nil {
		return err
	}

	if len(vars) == 0 {
		err = fmt.Errorf("empty device not allowed")
		return err
	}

	deviceID = vars[0].(string)
	name = strings.ToLower(deviceID)
	device, err = iotutil.GetOrCreateAPIDevice(ctx, z.kubeclient, name)
	if err != nil {
		return err
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		z.updateZigbeeMessageMetrics(ctx, m, device)
	}()

	// If this device has been annotated by a zone, then we pass the action to
	// the zone handler.
	if zo, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		zone = zo
		if m.Action != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				z.action(ctx, *m.Action, device.Name, zone)
			}()
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				z.selfAnnounce(ctx, device.Name, zone)
			}()
		}
	}

	/* deviceReq := &telemetryv1proto.TelemetryReportIOTDeviceRequest{ */
	/* 	DeviceDiscovery: iot.ParseDiscoveryMessage(topicPath, msg), */
	/* } */
	/* err = z.reportStream.Send(deviceReq) */

	wg.Wait()

	return nil
}

func (z *Zigbee2Mqtt) DevicesRoute(ctx context.Context, b []byte, _ ...interface{}) error {
	var err error
	_, span := z.tracer.Start(ctx, "Zigbee2Mqtt.DevicesRoute")
	defer tracing.ErrHandler(span, err, "devices route failed", z.logger)

	m := Devices{}
	err = json.Unmarshal(b, &m)
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
		_ = tracing.ErrHandler(span, err, "failed to handle zigbee bridge device", logger)
	}()

	span.SetAttributes(
		attribute.String("name", d.FriendlyName),
	)

	/* logger.Debug("device report", "traceID", trace.SpanContextFromContext(ctx).TraceID().String()) */

	var device *apiv1.Device
	device, err = iotutil.GetOrCreateAPIDevice(ctx, z.kubeclient, d.FriendlyName)
	if err != nil {
		return tracing.ErrHandler(span, err, "failed to get or create API device", logger)
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
		return tracing.ErrHandler(span, err, "failed to get or create API device", logger)
	}

	device.Status.SoftwareBuildID = d.SoftwareBuildID

	if err = z.kubeclient.Status().Update(ctx, device); err != nil {
		return fmt.Errorf("failed to update device status: %w", err)
	}

	if err = iotutil.UpdateLastSeen(ctx, z.kubeclient, device); err != nil {
		return fmt.Errorf("failed to update device last seen: %w", err)
	}

	return nil
}

func (z *Zigbee2Mqtt) selfAnnounce(ctx context.Context, device, zone string) {
	var err error

	ctx, span := z.tracer.Start(ctx, "selfAnnounce", trace.WithAttributes(
		attribute.String("device", device),
		attribute.String("zone", zone),
	))
	defer func() { _ = tracing.ErrHandler(span, err, "self announce", z.logger) }()

	_, err = z.zonekeeperClient.SelfAnnounce(ctx,
		&iotv1proto.SelfAnnounceRequest{
			Device: device,
			Zone:   zone,
		},
	)
}

func (z *Zigbee2Mqtt) action(ctx context.Context, action, device, zone string) {
	var err error

	ctx, span := z.tracer.Start(ctx, "action", trace.WithAttributes(
		attribute.String("action", action),
		attribute.String("device", device),
		attribute.String("zone", zone),
	))
	defer func() { _ = tracing.ErrHandler(span, err, "action", z.logger) }()

	_, err = z.zonekeeperClient.ActionHandler(ctx,
		&iotv1proto.ActionHandlerRequest{
			Event:  action,
			Device: device,
			Zone:   zone,
		},
	)
}

func (z *Zigbee2Mqtt) updateZigbeeMessageMetrics(_ context.Context, m ZigbeeMessage, device *apiv1.Device) {
	var zone string

	if v, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		zone = v
	}

	if m.Battery != nil {
		metricIOTBatteryPercent.WithLabelValues(device.Name, routeName, zone).Set(*m.Battery)
	}

	if m.LinkQuality != nil {
		metricIOTLinkQuality.WithLabelValues(device.Name, routeName, zone).Set(float64(*m.LinkQuality))
	}

	if m.Temperature != nil {
		metricIOTTemperature.WithLabelValues(device.Name, routeName, zone).Set(*m.Temperature)
	}

	if m.Humidity != nil {
		metricIOTHumidity.WithLabelValues(device.Name, routeName, zone).Set(*m.Humidity)
	}

	if m.SoilMoisture != nil {
		metricIOTSoilMoisture.WithLabelValues(device.Name, routeName, zone).Set(*m.SoilMoisture)
	}

	if m.Co2 != nil {
		metricIOTCo2.WithLabelValues(device.Name, routeName, zone).Set(*m.Co2)
	}

	if m.Formaldehyde != nil {
		metricIOTFormaldehyde.WithLabelValues(device.Name, routeName, zone).Set(*m.Formaldehyde)
	}

	if m.VOC != nil {
		metricIOTVoc.WithLabelValues(device.Name, routeName, zone).Set(float64(*m.VOC))
	}

	if m.State != nil {
		switch *m.State {
		case StateON:
			metricIOTState.WithLabelValues(device.Name, routeName, zone).Set(float64(1))
		case StateOFF:
			metricIOTState.WithLabelValues(device.Name, routeName, zone).Set(float64(0))
		}
	}

	if m.Illuminance != nil {
		metricIOTIlluminance.WithLabelValues(device.Name, routeName, zone).Set(float64(*m.Illuminance))
	}

	if m.Occupancy != nil {
		if *m.Occupancy {
			metricIOTOccupancy.WithLabelValues(device.Name, routeName, zone).Set(float64(1))
		} else {
			metricIOTOccupancy.WithLabelValues(device.Name, routeName, zone).Set(float64(0))
		}
	}

	if m.WaterLeak != nil {
		if *m.WaterLeak {
			metricIOTWaterLeak.WithLabelValues(device.Name, routeName, zone).Set(float64(1))
		} else {
			metricIOTWaterLeak.WithLabelValues(device.Name, routeName, zone).Set(float64(0))
		}
	}

	if m.Tamper != nil {
		if *m.Tamper {
			metricIOTTamper.WithLabelValues(device.Name, routeName, zone).Set(float64(1))
		} else {
			metricIOTTamper.WithLabelValues(device.Name, routeName, zone).Set(float64(0))
		}
	}
}
