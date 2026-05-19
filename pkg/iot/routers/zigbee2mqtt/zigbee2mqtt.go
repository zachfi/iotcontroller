package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/zachfi/zkit/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	"github.com/zachfi/iotcontroller/pkg/iot/bindings"
	"github.com/zachfi/iotcontroller/pkg/iot/events"
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

	kubeclient          kubeclient.Client
	zonekeeperClient    iotv1proto.ZoneKeeperServiceClient
	eventReceiverClient iotv1proto.EventReceiverServiceClient
	matcher             *bindings.Matcher

	deviceTracker *iotutil.DeviceTracker
}

func New(logger *slog.Logger, tracer trace.Tracer, kubeclient kubeclient.Client, zonekeeperClient iotv1proto.ZoneKeeperServiceClient, eventReceiverClient iotv1proto.EventReceiverServiceClient) (*Zigbee2Mqtt, error) {
	z := &Zigbee2Mqtt{
		logger:              logger.With("router", routeName),
		tracer:              tracer,
		kubeclient:          kubeclient,
		zonekeeperClient:    zonekeeperClient,
		eventReceiverClient: eventReceiverClient,
		matcher: bindings.New(kubeclient, "iot", logger).
			WithActivateFunc(func(ctx context.Context, condition string) error {
				_, err := eventReceiverClient.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{
					Condition: condition,
				})
				return err
			}),

		deviceTracker: iotutil.NewDeviceTracker(
			[]iotutil.Metric{
				metricIOTReport,
				metricIOTBatteryPercent,
				metricIOTLinkQuality,
				metricIOTTransmitPower,
				metricIOTBridgeState,
				metricIOTOccupancy,
				metricIOTWaterLeak,
				metricIOTTemperature,
				metricIOTSoilMoisture,
				metricIOTHumidity,
				metricIOTFormaldehyde,
				metricIOTCo2,
				metricIOTVoc,
				metricIOTIlluminance,
				metricIOTState,
				metricIOTTamper,
				waterTemperature,
				airTemperature,
				airHumidity,
				airHeatindex,
				thingWirelessSignalStrength,
			},
			time.Minute*5,
		),
	}

	go z.deviceTracker.Run(context.TODO(), time.Minute)

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

func (z *Zigbee2Mqtt) DeviceRoute(ctx context.Context, b []byte, vars ...any) error {
	var (
		device   *apiv1.Device
		deviceID string
		err      error
		name     string

		m  = ZigbeeMessage{}
		wg = sync.WaitGroup{}
	)
	ctx, span := z.tracer.Start(ctx, "Zigbee2Mqtt.DeviceRoute")
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

	wg.Go(func() {
		z.updateZigbeeMessageMetrics(ctx, m, device)
	})

	// Build normalized events and try to match them against Bindings before
	// touching any legacy handler. A single inbound message can carry
	// multiple asserted properties (action + state, occupancy + tamper);
	// each becomes its own DeviceEvent so a Binding can target each
	// independently.
	evs := events.FromZ2M(events.Z2MMessage{
		Action:    m.Action,
		Occupancy: m.Occupancy,
		WaterLeak: m.WaterLeak,
		Tamper:    m.Tamper,
		State:     m.State,
	}, device)
	dispatched := make(map[string]bool, len(evs))
	for _, ev := range evs {
		if z.dispatchEvent(ctx, ev) {
			dispatched[ev.Property] = true
		}
	}

	// If this device has been annotated by a zone, then we pass the action to
	// the zone handler. Bindings that already dispatched an event are
	// skipped here so we don't double-fire.
	if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		span.SetAttributes(
			attribute.String("zone", zone),
			attribute.String("device", device.Name),
		)

		switch {
		case m.Action != nil && !dispatched[events.PropertyAction]:
			span.SetAttributes(attribute.String("action", *m.Action))
			wg.Go(func() {
				z.action(ctx, *m.Action, device.Name, zone)
			})
		case m.Occupancy != nil && *m.Occupancy && !dispatched[events.PropertyOccupancy]:
			span.SetAttributes(attribute.Bool(events.PropertyOccupancy, *m.Occupancy))
			wg.Go(func() {
				z.occupied(ctx, device.Name, zone)
			})
		case len(dispatched) == 0:
			wg.Go(func() {
				z.selfAnnounce(ctx, device.Name, zone)
			})
		}
	}

	/* deviceReq := &telemetryv1proto.TelemetryReportIOTDeviceRequest{ */
	/* 	DeviceDiscovery: iot.ParseDiscoveryMessage(topicPath, msg), */
	/* } */
	/* err = z.reportStream.Send(deviceReq) */

	wg.Wait()

	return nil
}

func (z *Zigbee2Mqtt) DevicesRoute(ctx context.Context, b []byte, _ ...any) error {
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

	device.Spec.Type = deviceType(d).String()
	device.Spec.DateCode = d.DateCode
	device.Spec.Model = d.Definition.Model
	device.Spec.Vendor = d.Definition.Vendor
	device.Spec.Description = d.Definition.Description
	// IEEEAddress unifies device identity across transports. Without it,
	// Bindings with `selector.ieee` silently never match z2m-discovered
	// devices — they have to use `selector.device` (the CR name), which
	// then drifts from the equivalent native-Zigbee Binding which uses
	// the stripped form as its name. Set it from the z2m bridge JSON so
	// the same Binding works on both transports.
	if d.IeeeAddress != "" {
		device.Spec.IEEEAddress = d.IeeeAddress
	}

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

func (z *Zigbee2Mqtt) occupied(ctx context.Context, device, zone string) {
	var err error

	ctx, span := z.tracer.Start(ctx, "occupied", trace.WithAttributes(
		attribute.String("device", device),
		attribute.String("zone", zone),
	))
	defer func() { _ = tracing.ErrHandler(span, err, "zone occupied", z.logger) }()

	_, err = z.zonekeeperClient.OccupancyHandler(ctx,
		&iotv1proto.OccupancyHandlerRequest{
			Device: device,
			Zone:   zone,
		},
	)
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

// dispatchEvent runs ev through the Binding matcher; on hit, it activates
// the named Condition. Returns true when a Binding handled the event so
// the caller can skip its legacy fallback path.
func (z *Zigbee2Mqtt) dispatchEvent(ctx context.Context, ev events.DeviceEvent) bool {
	if z.eventReceiverClient == nil {
		return false
	}
	cond := z.matcher.FindCondition(ctx, ev)
	if cond == "" {
		return false
	}
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("binding.property", ev.Property),
		attribute.String("binding.value", ev.Value),
		attribute.String("binding.condition", cond),
	)
	if _, err := z.eventReceiverClient.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{
		Condition: cond,
	}); err != nil {
		z.logger.Debug("activate condition failed",
			slog.String("condition", cond),
			slog.String("error", err.Error()),
		)
		// Activation error doesn't fall back to the unhandled-action path —
		// the binding matched and the Condition exists; surfacing the
		// failure is more useful than masking it with a no-op fallback.
		return true
	}
	return true
}

// action is the post-matcher fallback path: a zoned device emitted an
// action property whose value didn't match any Binding. Records the event
// for visibility — both via the per-(device, action, zone) counter that
// drove the Stage 3 migration thermometer, and via a span event so a
// trace through Zigbee2Mqtt.DeviceRoute makes the unhandled action
// obvious — and otherwise does nothing. Steady state, this is dominated
// by Hue dimmer `*_press_release` events that pair with already-bound
// `*_press` events.
func (z *Zigbee2Mqtt) action(ctx context.Context, action, device, zone string) {
	_, span := z.tracer.Start(ctx, "action", trace.WithAttributes(
		attribute.String("action", action),
		attribute.String("device", device),
		attribute.String("zone", zone),
	))
	defer span.End()
	span.AddEvent("unhandled action (no Binding matched)")

	metricFallbackTotal.WithLabelValues(device, action, zone).Inc()

	z.logger.Debug("unhandled action (no Binding matched)",
		"device", device,
		"action", action,
		"zone", zone,
	)
}

func (z *Zigbee2Mqtt) updateZigbeeMessageMetrics(_ context.Context, m ZigbeeMessage, device *apiv1.Device) {
	var zone string

	z.deviceTracker.Track(device.Name)

	if v, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		zone = v
	}

	if m.Battery != nil {
		metricIOTBatteryPercent.WithLabelValues(device.Name, routeName, zone, device.Spec.Type).Set(*m.Battery)
	}

	if m.LinkQuality != nil {
		metricIOTLinkQuality.WithLabelValues(device.Name, routeName, zone, device.Spec.Type).Set(float64(*m.LinkQuality))
	}

	if m.TransmitPower != nil {
		metricIOTTransmitPower.WithLabelValues(device.Name, routeName, zone, device.Spec.Type).Set(float64(*m.TransmitPower))
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

	setBinaryMetric(metricIOTOccupancy, m.Occupancy, device.Name, routeName, zone)
	setBinaryMetric(metricIOTWaterLeak, m.WaterLeak, device.Name, routeName, zone)
	setBinaryMetric(metricIOTTamper, m.Tamper, device.Name, routeName, zone)

	// Power-metering plug fields — only emit when the device actually
	// reported them, so non-metering devices don't get phantom 0 series.
	if m.Voltage != nil {
		metricIOTVoltage.WithLabelValues(device.Name, routeName, zone).Set(*m.Voltage)
	}
	if m.Current != nil {
		metricIOTCurrent.WithLabelValues(device.Name, routeName, zone).Set(*m.Current)
	}
	if m.Power != nil {
		metricIOTPower.WithLabelValues(device.Name, routeName, zone).Set(*m.Power)
	}
	if m.Energy != nil {
		metricIOTEnergy.WithLabelValues(device.Name, routeName, zone).Set(*m.Energy)
	}
	if m.PowerFactor != nil {
		metricIOTPowerFactor.WithLabelValues(device.Name, routeName, zone).Set(*m.PowerFactor)
	}
	if m.AcFrequency != nil {
		metricIOTAcFrequency.WithLabelValues(device.Name, routeName, zone).Set(*m.AcFrequency)
	}
}
