package zonekeeper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/pkg/iot"
	"github.com/zachfi/iotcontroller/pkg/iot/handlers/mock"
	"github.com/zachfi/iotcontroller/pkg/iot/handlers/zigbee"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	module    = "zonekeeper"
	namespace = "iot"
)

type ZoneKeeper struct {
	services.Service
	mtx sync.Mutex

	cfg    *Config
	logger *slog.Logger
	tracer trace.Tracer

	mqttclient *mqttclient.MQTTClient
	kubeclient client.Client

	handlers map[controllerHandler]iot.Handler

	// TODO: a color temperature scheduler might adjsut the temperature of the lights depending on a schedule.  Could this be a kubernetes object that we consume?  Could be done over RPC for the udpate from a schedule_controller or something.
	// colorTempScheduler ColorTempSchedulerFunc

	zones map[string]*iot.Zone
}

type controllerHandler int

const (
	controllerHandlerZigbee controllerHandler = iota
	controllerHandlerNoop
)

func New(cfg Config, logger *slog.Logger, mqttclient *mqttclient.MQTTClient, kubeclient client.Client) (*ZoneKeeper, error) {
	z := &ZoneKeeper{
		cfg:        &cfg,
		logger:     logger.With("module", module),
		tracer:     otel.Tracer(module),
		mqttclient: mqttclient,
		kubeclient: kubeclient,
		zones:      make(map[string]*iot.Zone),
	}

	z.Service = services.NewBasicService(z.starting, z.running, z.stopping)

	return z, nil
}

func (z *ZoneKeeper) SetState(ctx context.Context, req *iotv1proto.SetStateRequest) (*iotv1proto.SetStateResponse, error) {
	var err error

	_, span := z.tracer.Start(ctx, "ZoneKeeper.SetState", trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	span.SetAttributes(
		attribute.String("name", req.Name),
		attribute.String("state", req.State.String()),
	)

	zone, err := z.GetZone(req.Name)
	if err != nil {
		return nil, errHandler(span, fmt.Errorf("failed to get zone %q: %w", req.Name, err))
	}

	zone.SetState(ctx, req.State)

	err = z.Flush(ctx, zone)
	if err != nil {
		return nil, errHandler(span, err)
	}

	return &iotv1proto.SetStateResponse{}, errHandler(span, err)
}

// ActionHandler is called when an action is requested against a light group.
// The action speciefies the a button press and a room to give enough context
// for how to change the behavior of the lights in response to the action.
func (z *ZoneKeeper) ActionHandler(ctx context.Context, action *iotv1proto.ActionHandlerRequest) (*iotv1proto.ActionHandlerResponse, error) {
	var err error

	resp := &iotv1proto.ActionHandlerResponse{}

	_, span := z.tracer.Start(
		ctx,
		"ZoneKeeper.ActionHandler",
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	span.SetAttributes(
		attribute.String("event", action.Event),
		attribute.String("device", action.Device),
		attribute.String("zone", action.Zone),
	)

	zone, err := z.GetZone(action.Zone)
	if err != nil {
		return resp, fmt.Errorf("failed to get zone %q for action %q: %w", action.Zone, action.Event, err)
	}

	// setStateReq := &iotv1proto.SetStateRequest{
	// }

	switch action.Event {
	case "single", "button_1_press":
		// Toggle from current state
		currentState := zone.State()
		switch currentState {
		case iotv1proto.ZoneState_ZONE_STATE_ON:
			zone.Off(ctx)
		case iotv1proto.ZoneState_ZONE_STATE_OFF:
			zone.On(ctx)
		}
	case "on", "double", "tap", "slide", "on_press":
		zone.SetBrightness(ctx, iotv1proto.Brightness_BRIGHTNESS_FULL)
		zone.On(ctx)
	case "off", "triple", "off_press":
		zone.Off(ctx)
		// case "quadruple", "flip90", "flip180", "fall", "button_3_press":
		// 	return zone.RandomColor(ctx, z.cfg.PartyColors)
	case "hold", "button_2_press":
		zone.SetBrightness(ctx, iotv1proto.Brightness_BRIGHTNESS_DIM)
		zone.On(ctx)
	case "up_press", "rotate_right", "dial_rotate_right_slow", "dial_rotate_right_fast", "dial_rotate_right_step", "brightness_step_up":
		zone.IncrementBrightness(ctx)
		zone.On(ctx)
	case "down_press", "rotate_left", "dial_rotate_left_slow", "dial_rotate_left_fast", "dial_rotate_left_step", "brightness_step_down":
		zone.DecrementBrightness(ctx)
		zone.On(ctx)
	case "wakeup", "press", "release", "off_hold", "off_hold_release", "on_press_release": // do nothing
		return resp, nil
	default:
		return resp, errHandler(span, fmt.Errorf("unknown action %q for device %q in zone %q", action.Event, action.Device, action.Zone))
	}

	return resp, errHandler(span, z.Flush(ctx, zone))
}

// Flush handles pushing the current state out to each of the hnadlers.
func (z *ZoneKeeper) Flush(ctx context.Context, iotZone *iot.Zone) error {
	attributes := []attribute.KeyValue{
		attribute.String("zone", iotZone.Name()),
	}

	ctx, span := z.tracer.Start(ctx, "ZoneKeeper.Flush", trace.WithAttributes(attributes...))
	defer span.End()

	name, state, brightness := iotZone.Name(), iotZone.State(), iotZone.Brightness()

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()
		var err error

		_, statusUpdateSpan := z.tracer.Start(ctx, "iot.Zone/Status/Update", trace.WithAttributes(attributes...))
		defer func() { _ = errHandler(statusUpdateSpan, err) }()

		// Update the API status for this zone
		var zone *apiv1.Zone
		zone, err = z.getOrCreateAPIZone(ctx, name)
		if err != nil {
			return
		}
		zone.Status.State = state.String()
		zone.Status.Brightness = iotv1proto.Brightness_name[int32(brightness)]

		if err = z.kubeclient.Status().Update(ctx, zone); err != nil {
			return
		}
	}(ctx)

	iotZone.SetState(ctx, state)
	err := iotZone.Flush(ctx)
	if err != nil {
		return err
	}

	wg.Wait()

	return nil
}

func (z *ZoneKeeper) starting(ctx context.Context) error {
	// Run once before looping
	err := z.zoneUpdate(ctx)
	if err != nil {
		return err
	}
	go z.zoneUpdaterLoop(ctx)

	zigbeeHandler, err := zigbee.New(z.mqttclient.Client(), z.logger, z.tracer)
	if err != nil {
		return err
	}

	hhh := make(map[controllerHandler]iot.Handler, 0)
	hhh[controllerHandlerZigbee] = zigbeeHandler
	hhh[controllerHandlerNoop] = &mock.MockHandler{}
	z.handlers = hhh

	return nil
}

func (z *ZoneKeeper) running(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (z *ZoneKeeper) stopping(_ error) error {
	return nil
}

func (z *ZoneKeeper) GetZone(name string) (*iot.Zone, error) {
	z.mtx.Lock()
	defer z.mtx.Unlock()
	if v, ok := z.zones[name]; ok {
		return v, nil
	}

	zone, err := iot.NewZone(name, z.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create new zone: %w", err)
	}

	z.zones[name] = zone

	return zone, nil
}

func (z *ZoneKeeper) zoneUpdaterLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	var err error

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err = z.zoneUpdate(ctx)
			if err != nil {
				z.logger.Error("zoneUpdate failed", "err", err)
			}
		}
	}
}

func (z *ZoneKeeper) zoneUpdate(ctx context.Context) error {
	var (
		err        error
		errs       []error
		zoneName   string
		deviceList apiv1.DeviceList
		handler    iot.Handler
	)

	ctx, span := z.tracer.Start(ctx, "flushZone")
	defer func() { _ = errHandler(span, err) }()

	opts := &client.ListOptions{
		Namespace: namespace,
	}

	err = z.kubeclient.List(ctx, &deviceList, opts)
	if err != nil {
		return fmt.Errorf("failed to list apiv1.Devices: %w", err)
	}

	for _, d := range deviceList.Items {

		if zz, ok := d.Labels[iot.DeviceZoneLabel]; ok {
			zoneName = zz
		} else {
			// Skip when no zone label is set by the zone_controller.
			z.logger.Warn("no zone label set for device", "device", d.Name)
			continue

		}

		device := &iotv1proto.Device{
			Name: d.Name,
			Type: iotv1proto.DeviceType(iotv1proto.DeviceType_value[d.Spec.Type]),
		}

		switch d.Spec.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_COORDINATOR.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_RELAY.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_LEAK.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_BUTTON.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_MOISTURE.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_MOTION.String():
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_ISPINDEL.String():
			handler = z.handlers[controllerHandlerNoop]
		default:
			z.logger.Warn("using default handler", "device", d.Name, "type", d.Spec.Type)
			handler = z.handlers[controllerHandlerNoop]
		}

		if zone, ok := z.zones[zoneName]; ok {
			err = zone.SetDevice(device, handler)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// GetZoneName returns a a pointer to a string containing the name of the zone for a given device name, or nil if one was not found.
func (z *ZoneKeeper) GetDeviceZone(ctx context.Context, req *iotv1proto.GetDeviceZoneRequest) (*iotv1proto.GetDeviceZoneResponse, error) {
	resp := &iotv1proto.GetDeviceZoneResponse{}

	for name, zone := range z.zones {
		if zone.HasDevice(req.Device) {
			resp.Zone = name
			return resp, nil
		}
	}

	return nil, fmt.Errorf("device not matched in any zone")
}

func (z *ZoneKeeper) getOrCreateAPIZone(ctx context.Context, name string) (*apiv1.Zone, error) {
	_, span := z.tracer.Start(ctx, "ZoneKeeper/getOrCreateAPIZone", trace.WithAttributes(
		attribute.String("zone", name),
	))

	var zone apiv1.Zone
	var err error

	nsn := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	err = z.kubeclient.Get(ctx, nsn, &zone)
	if err != nil {
		if apierrors.IsNotFound(err) {
			span.AddEvent("zone not found")
			err = z.createAPIZone(ctx, &zone)
			if err != nil {
				return nil, errHandler(span, err)
			}
		}
	}
	return &zone, errHandler(span, err)
}

func (z *ZoneKeeper) createAPIZone(ctx context.Context, zone *apiv1.Zone) error {
	_, span := z.tracer.Start(ctx, "ZoneKeeper/createAPIZone", trace.WithAttributes(
		attribute.String("zone", zone.Name),
	))

	err := z.kubeclient.Create(ctx, zone)
	if err != nil {
		return errHandler(span, err)
	}

	span.AddEvent("created")

	return errHandler(span, err)
}

func errHandler(span trace.Span, err error) error {
	defer span.End()

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "ok")
	}
	return err
}
