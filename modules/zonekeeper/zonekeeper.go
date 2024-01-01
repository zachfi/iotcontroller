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
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zachfi/zkit/pkg/tracing"

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

var unlimited = func(_ string) bool {
	return true
}

type ZoneKeeper struct {
	services.Service
	mtx sync.Mutex

	cfg    *Config
	logger *slog.Logger
	tracer trace.Tracer

	mqttclient *mqttclient.MQTTClient
	kubeclient kubeclient.Client

	handlers   map[controllerHandler]iot.Handler
	announceer map[string]time.Time

	// TODO: a color temperature scheduler might adjsut the temperature of the
	// lights depending on a schedule.  Could this be a kubernetes object that we
	// consume?  Could be done over RPC for the udpate from a
	// schedule_controller. colorTempScheduler ColorTempSchedulerFunc

	zones map[string]*iot.Zone
}

type controllerHandler int

const (
	controllerHandlerZigbee controllerHandler = iota
	controllerHandlerNoop
)

func New(cfg Config, logger *slog.Logger, mqttclient *mqttclient.MQTTClient, kubeclient kubeclient.Client) (*ZoneKeeper, error) {
	z := &ZoneKeeper{
		cfg:        &cfg,
		logger:     logger.With("module", module),
		tracer:     otel.Tracer(module),
		mqttclient: mqttclient,
		kubeclient: kubeclient,
		zones:      make(map[string]*iot.Zone),
		announceer: make(map[string]time.Time),
	}

	z.Service = services.NewBasicService(z.starting, z.running, z.stopping)

	return z, nil
}

func (z *ZoneKeeper) SetState(ctx context.Context, req *iotv1proto.SetStateRequest) (*iotv1proto.SetStateResponse, error) {
	var (
		err  error
		zone *iot.Zone
		resp = &iotv1proto.SetStateResponse{}
	)

	ctx, span := z.tracer.Start(ctx, "ZoneKeeper.SetState",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("name", req.Name),
			attribute.String("state", req.State.String()),
		),
	)
	defer func() { _ = tracing.ErrHandler(span, err, "self announce", z.logger) }()

	zone, err = z.GetZone(ctx, req.Name)
	if err != nil {
		return resp, err
	}

	zone.SetState(ctx, req.State)

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()
		z.apiStatusUpdate(ctx, zone)
	}(ctx)

	err = zone.Flush(ctx, unlimited)
	wg.Wait()
	return resp, err
}

func (z *ZoneKeeper) SelfAnnounce(ctx context.Context, req *iotv1proto.SelfAnnounceRequest) (*iotv1proto.SelfAnnounceResponse, error) {
	var (
		err     error
		zone    *iot.Zone
		resp    = &iotv1proto.SelfAnnounceResponse{}
		limiter iot.FlushLimiter
	)

	_, span := z.tracer.Start(ctx, "ZoneKeeper.SelfAnnounce",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("device", req.Device),
			attribute.String("zone", req.Zone),
		),
	)
	defer func() { _ = tracing.ErrHandler(span, err, "self announce", z.logger) }()

	z.mtx.Lock()
	if v, ok := z.announceer[req.String()]; ok {
		if time.Since(v) < 1*time.Second {
			span.SetAttributes(
				attribute.String("skipped", "too recent"),
			)
			defer z.mtx.Unlock()
			return resp, nil
		}
	}

	z.announceer[req.String()] = time.Now()
	z.mtx.Unlock()

	limiter = func(d string) bool {
		return d == req.Device
	}

	zone, err = z.GetZone(ctx, req.Zone)
	return resp, zone.Flush(ctx, limiter)
}

// ActionHandler is called when an action is requested against a zone. The
// action is the event, like a button press.
func (z *ZoneKeeper) ActionHandler(ctx context.Context, req *iotv1proto.ActionHandlerRequest) (*iotv1proto.ActionHandlerResponse, error) {
	var (
		err  error
		resp = &iotv1proto.ActionHandlerResponse{}
	)

	_, span := z.tracer.Start(ctx, "ZoneKeeper.ActionHandler", trace.WithSpanKind(trace.SpanKindServer))
	defer func() { _ = errHandler(span, err) }()

	span.SetAttributes(
		attribute.String("event", req.Event),
		attribute.String("device", req.Device),
		attribute.String("zone", req.Zone),
	)

	if req.Event == "" {
		return resp, nil
	}

	zone, err := z.GetZone(ctx, req.Zone)
	if err != nil {
		return resp, fmt.Errorf("failed to get zone %q for action %q: %w", req.Zone, req.Event, err)
	}

	// TODO: move the strings here to constants

	switch req.Event {
	case "single", "button_1_press":
		// Toggle from current state
		currentState := zone.State()
		switch currentState {
		case iotv1proto.ZoneState_ZONE_STATE_ON:
			zone.Off(ctx)
		// case iotv1proto.ZoneState_ZONE_STATE_OFF:
		default:
			zone.SetColorTemperature(ctx, iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY)
			zone.SetBrightness(ctx, iotv1proto.Brightness_BRIGHTNESS_FULL)
			zone.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)
			zone.On(ctx)
		}
	case "on", "double", "tap", "slide", "on_press":
		zone.SetColorTemperature(ctx, iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY)
		zone.SetBrightness(ctx, iotv1proto.Brightness_BRIGHTNESS_FULL)
		zone.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)
		zone.On(ctx)
	case "off", "triple", "off_press":
		zone.Off(ctx)
	case "quadruple", "flip90", "flip180", "fall", "button_3_press":
		zone.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR)
	case "hold", "button_2_press":
		zone.SetBrightness(ctx, iotv1proto.Brightness_BRIGHTNESS_DIM)
		zone.On(ctx)
	case UpPress, RotateRight, DialRotateRightSlow, DialRotateRightFast, DialRotateRightStep, BrightnessStepUp:
		zone.IncrementBrightness(ctx)
		zone.On(ctx)
	case DownPress, RotateLeft, DialRotateLeftSlow, DialRotateLeftFast, DialRotateLeftStep, BrightnessStepDown:
		zone.DecrementBrightness(ctx)
		zone.On(ctx)
	case "wakeup", "press", "release", "off_hold", "off_hold_release", "on_press_release", "up_press_release", "down_press_release": // do nothing
		return resp, nil
	default:
		return resp, errHandler(span, fmt.Errorf("unknown action %q for device %q in zone %q", req.Event, req.Device, req.Zone))
	}

	return resp, errHandler(span, zone.Flush(ctx, unlimited))
}

const (
	// Incprement Brightness
	UpPress             = "up_press"
	RotateRight         = "rotate_right"
	DialRotateRightSlow = "dial_rotate_right_slow"
	DialRotateRightFast = "dial_rotate_right_fast"
	DialRotateRightStep = "dial_rotate_right_step"
	BrightnessStepUp    = "brightness_step_up"

	// Decrement Brightness
	DownPress          = "down_press"
	RotateLeft         = "rotate_left"
	DialRotateLeftSlow = "dial_rotate_left_slow"
	DialRotateLeftFast = "dial_rotate_left_fast"
	DialRotateLeftStep = "dial_rotate_left_step"
	BrightnessStepDown = "brightness_step_down"
)

func (z *ZoneKeeper) apiStatusUpdate(ctx context.Context, iotZone *iot.Zone) {
	var (
		err  error
		zone *apiv1.Zone
	)

	ctx, span := z.tracer.Start(ctx, "ZoneKeeper.apiStatusUpdate")
	defer func() { _ = errHandler(span, err) }()

	name, state, brightness, colorTemp := iotZone.Name(), iotZone.State(), iotZone.Brightness(), iotZone.ColorTemperature()

	span.SetAttributes(
		attribute.String("zone", name),
		attribute.String("state", state.String()),
		attribute.String("brightness", brightness.String()),
		attribute.String("colorTemp", colorTemp.String()),
	)

	zone, err = z.getOrCreateAPIZone(ctx, name)
	if err != nil {
		return
	}

	zone.Status.State = iotZone.State().String()
	zone.Status.Brightness = iotv1proto.Brightness_name[int32(iotZone.Brightness())]
	zone.Status.ColorTemperature = iotv1proto.ColorTemperature_name[int32(iotZone.ColorTemperature())]

	if err = z.kubeclient.Status().Update(ctx, zone); err != nil {
		return
	}

	// Update the API status for this zone
	if len(zone.Spec.Colors) > 0 {
		iotZone.SetColorPool(ctx, zone.Spec.Colors)
	}
}

func (z *ZoneKeeper) starting(ctx context.Context) error {
	zigbeeHandler, err := zigbee.New(z.mqttclient.Client(), z.logger, z.tracer)
	if err != nil {
		return err
	}

	hhh := make(map[controllerHandler]iot.Handler, 0)
	hhh[controllerHandlerZigbee] = zigbeeHandler
	hhh[controllerHandlerNoop] = &mock.MockHandler{}
	z.handlers = hhh

	// Run once before looping
	err = z.zoneUpdate(ctx)
	if err != nil {
		return err
	}
	go z.zoneUpdaterLoop(ctx)

	return nil
}

func (z *ZoneKeeper) running(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (z *ZoneKeeper) stopping(_ error) error {
	return nil
}

func (z *ZoneKeeper) GetZone(ctx context.Context, name string) (*iot.Zone, error) {
	var err error
	_, span := z.tracer.Start(ctx, "ZoneKeeper.GetZone", trace.WithSpanKind(trace.SpanKindInternal))
	defer func() { _ = errHandler(span, err) }()

	z.mtx.Lock()
	defer z.mtx.Unlock()

	if v, ok := z.zones[name]; ok {
		span.SetAttributes(
			attribute.String("name", v.Name()),
			attribute.String("state", v.State().String()),
			attribute.String("brightness", v.Brightness().String()),
			attribute.String("color_temp", v.ColorTemperature().String()),
		)

		return v, nil
	}

	span.AddEvent("new zone")

	zone, err := iot.NewZone(name, z.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create new zone: %w", err)
	}

	apiZone, err := z.getOrCreateAPIZone(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get api zone: %w", err)
	}

	zone.SetState(ctx, iotv1proto.ZoneState(iotv1proto.ZoneState_value[apiZone.Status.State]))
	zone.SetBrightness(ctx, iotv1proto.Brightness(iotv1proto.Brightness_value[apiZone.Status.Brightness]))
	zone.SetColorTemperature(ctx, iotv1proto.ColorTemperature(iotv1proto.ColorTemperature_value[apiZone.Status.ColorTemperature]))

	z.zones[name] = zone

	span.SetAttributes(
		attribute.String("name", zone.Name()),
		attribute.String("state", zone.State().String()),
		attribute.String("brightness", zone.Brightness().String()),
		attribute.String("color_temp", zone.ColorTemperature().String()),
	)

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

// zoneUpdate
func (z *ZoneKeeper) zoneUpdate(ctx context.Context) error {
	var (
		err            error
		errs           []error
		zoneName       string
		deviceList     apiv1.DeviceList
		handler        iot.Handler
		devicesSkipped []string
	)

	ctx, span := z.tracer.Start(ctx, "ZoneKeeper.zoneUpdate")
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
			devicesSkipped = append(devicesSkipped, d.Name)
			continue
		}

		device := &iotv1proto.Device{
			Name: d.Name,
			Type: iotv1proto.DeviceType(iotv1proto.DeviceType_value[d.Spec.Type]),
		}

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_COORDINATOR:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_RELAY:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_LEAK:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_BUTTON:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_MOISTURE:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_MOTION:
			handler = z.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_ISPINDEL:
			handler = z.handlers[controllerHandlerNoop]
		default:
			z.logger.Warn("using default handler", "device", d.Name, "type", d.Spec.Type)
			handler = z.handlers[controllerHandlerNoop]
		}

		if zone, ok := z.zones[zoneName]; ok {
			err = zone.SetDevice(ctx, device, handler)
			if err != nil {
				errs = append(errs, err)
			}
		}

		// TODO: remove/update a device when its zone changes

	}

	if len(errs) > 0 {
		err = errors.Join(errs...)
	}

	if len(devicesSkipped) > 0 {
		span.SetAttributes(
			attribute.StringSlice("devicesSkipped", devicesSkipped),
		)
	}

	return err
}

// GetZoneName returns a a pointer to a string containing the name of the zone for a given device name, or nil if one was not found.
func (z *ZoneKeeper) GetDeviceZone(ctx context.Context, req *iotv1proto.GetDeviceZoneRequest) (*iotv1proto.GetDeviceZoneResponse, error) {
	_, span := z.tracer.Start(ctx, "ZoneKeeper/GetDeviceZone", trace.WithAttributes(
		attribute.String("device", req.Device),
	))

	resp := &iotv1proto.GetDeviceZoneResponse{}

	for name, zone := range z.zones {
		if zone.HasDevice(req.Device) {
			resp.Zone = name
			span.SetAttributes(attribute.String("found zone", name))
			return resp, errHandler(span, nil)
		}
	}

	return resp, errHandler(span, fmt.Errorf("device not matched in any zone"))
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
