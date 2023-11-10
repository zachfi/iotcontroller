package iot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	sync "sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	tempEvening       = 500
	tempLateAfternoon = 400
	tempDay           = 300
	tempMorning       = 200
	tempFirstLight    = 100

	nightVisionColor = `#FF00FF`
)

var (
	defaultColorPool           = []string{"#006c7f", "#e32636", "#b0bf1a"}
	defaultColorTemperatureMap = map[iotv1proto.ColorTemperature]int32{
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_FIRSTLIGHT:    tempFirstLight,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING:       tempMorning,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_DAY:           tempDay,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_LATEAFTERNOON: tempDay,
		iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING:       tempEvening,
	}
)

var defaultBrightnessMap = map[iotv1proto.Brightness]uint8{
	iotv1proto.Brightness_BRIGHTNESS_FULL:    254,
	iotv1proto.Brightness_BRIGHTNESS_DIMPLUS: 120,
	iotv1proto.Brightness_BRIGHTNESS_DIM:     100,
	iotv1proto.Brightness_BRIGHTNESS_LOWPLUS: 95,
	iotv1proto.Brightness_BRIGHTNESS_LOW:     90,
	iotv1proto.Brightness_BRIGHTNESS_VERYLOW: 80,
}
var defaultScheduleDuration = time.Minute * 10

type Zone struct {
	mtx    sync.Mutex
	logger *slog.Logger
	tracer trace.Tracer

	name string

	brightness uint8
	colorPool  []string
	color      string
	colorTemp  int32
	state      iotv1proto.ZoneState

	devices map[*iotv1proto.Device]Handler

	colorTempMap  map[iotv1proto.ColorTemperature]int32
	brightnessMap map[iotv1proto.Brightness]uint8
}

func (z *Zone) State() iotv1proto.ZoneState {
	z.mtx.Lock()
	defer z.mtx.Unlock()
	return z.state
}

func NewZone(name string, logger *slog.Logger) (*Zone, error) {
	if name == "" {
		return nil, fmt.Errorf("zone name required")
	}

	z := &Zone{
		name:          name,
		logger:        logger.With("zone", name),
		tracer:        otel.Tracer(name),
		colorPool:     defaultColorPool,
		colorTemp:     tempDay,
		brightnessMap: defaultBrightnessMap,
		colorTempMap:  defaultColorTemperatureMap,
	}

	return z, nil
}

func (z *Zone) SetDevice(device *iotv1proto.Device, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("unable to set nil handler on device: %q", device.Name)
	}

	z.mtx.Lock()
	defer z.mtx.Unlock()

	if device == nil {
		return ErrInvalidDevice
	}

	if device.Name == "" {
		return ErrInvalidDevice
	}

	if device.Type == iotv1proto.DeviceType_DEVICE_TYPE_UNSPECIFIED {
		return ErrInvalidDevice
	}

	if z.devices == nil {
		z.devices = make(map[*iotv1proto.Device]Handler)
	}

	for k := range z.devices {
		if strings.EqualFold(k.Name, device.Name) {
			k.Type = device.Type
			return nil
		}
	}

	z.devices[device] = handler

	return nil
}

func (z *Zone) SetBrightnessMap(m map[iotv1proto.Brightness]uint8) {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.brightnessMap = m
}

func (z *Zone) SetColorTemperatureMap(m map[iotv1proto.ColorTemperature]int32) {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.colorTempMap = m
}

func (z *Zone) Name() string {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	return z.name
}

func (z *Zone) SetColorTemperature(ctx context.Context, colorTemp iotv1proto.ColorTemperature) {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.colorTemp = z.colorTempMap[colorTemp]
}

func (z *Zone) SetBrightness(ctx context.Context, brightness iotv1proto.Brightness) {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.brightness = z.brightnessMap[brightness]
}

func (z *Zone) IncrementBrightness(ctx context.Context) {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	var currentBri iotv1proto.Brightness
	for k, v := range z.brightnessMap {
		if v == z.brightness {
			currentBri = k
		}
	}

	if currentBri > 0 {
		z.brightness = z.brightnessMap[currentBri-1]
	}
}

func (z *Zone) DecrementBrightness(ctx context.Context) {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	var currentBri iotv1proto.Brightness
	for k, v := range z.brightnessMap {
		if v == z.brightness {
			currentBri = k
		}
	}

	if currentBri < 5 {
		z.brightness = z.brightnessMap[currentBri+1]
	}
}

func (z *Zone) Off(ctx context.Context) {
	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_OFF)
}

func (z *Zone) On(ctx context.Context) {
	onStates := []iotv1proto.ZoneState{
		iotv1proto.ZoneState_ZONE_STATE_COLOR,
		iotv1proto.ZoneState_ZONE_STATE_EVENINGVISION,
		iotv1proto.ZoneState_ZONE_STATE_MORNINGVISION,
		iotv1proto.ZoneState_ZONE_STATE_NIGHTVISION,
		iotv1proto.ZoneState_ZONE_STATE_ON,
		iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR,
	}

	state := z.State()

	for _, s := range onStates {
		if s == state {
			return
		}
	}

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)
}

func (z *Zone) SetColor(ctx context.Context, color string) {
	z.color = color
	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_COLOR)
}

func (z *Zone) RandomColor(ctx context.Context, colors []string) {
	z.colorPool = colors
	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR)
}

func (z *Zone) SetState(ctx context.Context, state iotv1proto.ZoneState) {
	span := trace.SpanFromContext(ctx)
	defer span.End()

	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.state = state
}

func (z *Zone) HasDevice(device string) bool {
	z.logger.Info("HasDevice", "devices", len(z.devices))

	for d := range z.devices {
		z.logger.Info("equalfold", "d.Name", d.Name, "device", device)
		if strings.EqualFold(d.Name, device) {
			return true
		}
	}

	return false
}

// Flush handles pushing the current state out to each of the hnadlers.
func (z *Zone) Flush(ctx context.Context) error {
	if z.name == "" {
		return fmt.Errorf("unable to handle unnamed zone: %+v", z)
	}

	ctx, span := z.tracer.Start(ctx, "Zone.Flush")
	defer span.End()

	span.SetAttributes(
		attribute.String("name", z.name),
		attribute.String("state", z.state.String()),
	)

	switch z.state {
	case iotv1proto.ZoneState_ZONE_STATE_ON:
		return z.handleOn(ctx)
	case iotv1proto.ZoneState_ZONE_STATE_OFF:
		return z.handleOff(ctx)
	case iotv1proto.ZoneState_ZONE_STATE_COLOR:
		return z.handleColor(ctx)
	case iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR:
		return z.handleRandomColor(ctx)
	case iotv1proto.ZoneState_ZONE_STATE_NIGHTVISION:
		z.color = nightVisionColor
		return z.handleColor(ctx)
	case iotv1proto.ZoneState_ZONE_STATE_EVENINGVISION:
		z.colorTemp = z.colorTempMap[iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING]
		return z.handleColorTemperature(ctx)
	case iotv1proto.ZoneState_ZONE_STATE_MORNINGVISION:
		z.colorTemp = z.colorTempMap[iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING]
		return z.handleColorTemperature(ctx)
	}

	return nil
}

// handleOn takes care of the behavior when the light is set to On.  This
// includes brightness and color temperature.  The color hue of the light is
// handled by ZoneState_COLOR.
func (z *Zone) handleOn(ctx context.Context) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleOn")
	defer span.End()

	var err error
	var errs []error
	for device, h := range z.devices {

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_RELAY:
		default:
			continue
		}

		err = h.On(ctx, device)
		if err != nil {
			errs = append(errs, err)
		}
	}

	err = z.handleBrightness(ctx)
	if err != nil {
		errs = append(errs, err)
	}

	err = z.handleColorTemperature(ctx)
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleOff(ctx context.Context) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleOff")
	defer span.End()

	var err error
	var errs []error
	for device, h := range z.devices {

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_RELAY:
		default:
			continue
		}

		err = h.Off(ctx, device)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleColorTemperature(ctx context.Context) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleColorTemperature")
	defer span.End()

	var err error
	var errs []error
	for device, h := range z.devices {

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		default:
			continue
		}

		err = h.SetColorTemp(ctx, device, z.colorTemp)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleBrightness(ctx context.Context) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleBrightness")
	defer span.End()

	var err error
	var errs []error
	for device, h := range z.devices {

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		default:
			continue
		}

		err = h.SetBrightness(ctx, device, z.brightness)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleColor(ctx context.Context) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleColor")
	defer span.End()

	var err error
	var errs []error
	for device, h := range z.devices {

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		default:
			continue
		}

		err = h.SetColor(ctx, device, z.color)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleRandomColor(ctx context.Context) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleRandomColor")
	defer span.End()

	var err error
	var errs []error
	for device, h := range z.devices {

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		default:
			continue
		}

		err = h.RandomColor(ctx, device, z.colorPool)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
