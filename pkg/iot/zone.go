package iot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	sync "sync"

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
	defaultBrightnessMap = map[iotv1proto.Brightness]uint8{
		iotv1proto.Brightness_BRIGHTNESS_FULL:    254,
		iotv1proto.Brightness_BRIGHTNESS_DIMPLUS: 125,
		iotv1proto.Brightness_BRIGHTNESS_DIM:     110,
		iotv1proto.Brightness_BRIGHTNESS_LOWPLUS: 95,
		iotv1proto.Brightness_BRIGHTNESS_LOW:     90,
		iotv1proto.Brightness_BRIGHTNESS_VERYLOW: 70,
	}
	/* defaultScheduleDuration = time.Minute * 10 */
	defaultOnStates = []iotv1proto.ZoneState{
		iotv1proto.ZoneState_ZONE_STATE_COLOR,
		iotv1proto.ZoneState_ZONE_STATE_EVENINGVISION,
		iotv1proto.ZoneState_ZONE_STATE_MORNINGVISION,
		iotv1proto.ZoneState_ZONE_STATE_NIGHTVISION,
		iotv1proto.ZoneState_ZONE_STATE_ON,
		iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR,
	}
)

type Zone struct {
	mtx    sync.Mutex
	logger *slog.Logger
	tracer trace.Tracer

	name string

	brightness iotv1proto.Brightness
	colorPool  []string
	color      string
	colorTemp  iotv1proto.ColorTemperature
	state      iotv1proto.ZoneState

	devices map[*iotv1proto.Device]Handler

	colorTempMap map[iotv1proto.ColorTemperature]int32
	/* brightnessMap map[iotv1proto.Brightness]uint8 */
}

func NewZone(name string, logger *slog.Logger) (*Zone, error) {
	if name == "" {
		return nil, fmt.Errorf("zone name required")
	}

	z := &Zone{
		name:      name,
		logger:    logger.With("zone", name),
		tracer:    otel.Tracer(name),
		colorPool: defaultColorPool,
		/* colorTemp: tempDay, */
		/* brightnessMap: defaultBrightnessMap, */
		colorTempMap: defaultColorTemperatureMap,
	}

	return z, nil
}

func (z *Zone) State() iotv1proto.ZoneState {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	return z.state
}

func (z *Zone) Brightness() iotv1proto.Brightness {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	return z.brightness
}

func (z *Zone) ColorTemperature() iotv1proto.ColorTemperature {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	return z.colorTemp
}

func (z *Zone) SetDevice(ctx context.Context, device *iotv1proto.Device, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("unable to set nil handler on device: %q", device.Name)
	}

	if device == nil || device.Name == "" || device.Type == iotv1proto.DeviceType_DEVICE_TYPE_UNSPECIFIED {
		return ErrInvalidDevice
	}

	_, span := z.tracer.Start(ctx, "Zone.SetDevice", trace.WithAttributes(
		attribute.String("device", device.Name),
	))
	defer span.End()

	z.mtx.Lock()
	defer z.mtx.Unlock()

	if z.devices == nil {
		z.devices = make(map[*iotv1proto.Device]Handler)
	}

	for d := range z.devices {
		if strings.EqualFold(d.Name, device.Name) {
			d.Type = device.Type
			z.devices[d] = handler
			return nil
		}
	}

	z.devices[device] = handler

	return nil
}

/* func (z *Zone) SetBrightnessMap(m map[iotv1proto.Brightness]uint8) { */
/* 	z.mtx.Lock() */
/* 	defer z.mtx.Unlock() */
/**/
/* 	z.brightnessMap = m */
/* } */

func (z *Zone) SetColorTemperatureMap(m map[iotv1proto.ColorTemperature]int32) {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.colorTempMap = m
}

func (z *Zone) SetColorPool(ctx context.Context, c []string) {
	_, span := z.tracer.Start(ctx, "Zone.SetColorPool", trace.WithAttributes(
		attribute.StringSlice("colorPool", c),
	))
	defer span.End()

	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.colorPool = c
}

func (z *Zone) Name() string {
	z.mtx.Lock()
	defer z.mtx.Unlock()

	return z.name
}

func (z *Zone) SetColorTemperature(ctx context.Context, colorTemp iotv1proto.ColorTemperature) {
	_, span := z.tracer.Start(ctx, "Zone.SetColorTemperature")
	defer span.End()

	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.colorTemp = colorTemp
}

func (z *Zone) SetBrightness(ctx context.Context, brightness iotv1proto.Brightness) {
	_, span := z.tracer.Start(ctx, "Zone.SetBrightness")
	defer span.End()

	z.mtx.Lock()
	defer z.mtx.Unlock()

	z.brightness = brightness
}

func (z *Zone) IncrementBrightness(ctx context.Context) {
	_, span := z.tracer.Start(ctx, "Zone.IncrementBrightness")
	defer span.End()

	z.mtx.Lock()
	defer z.mtx.Unlock()

	span.SetAttributes(attribute.String("before", z.brightness.String()))

	switch z.brightness {
	case iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_FULL

	case iotv1proto.Brightness_BRIGHTNESS_VERYLOW:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_LOW
	case iotv1proto.Brightness_BRIGHTNESS_LOW:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_LOWPLUS
	case iotv1proto.Brightness_BRIGHTNESS_LOWPLUS:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_DIM
	case iotv1proto.Brightness_BRIGHTNESS_DIM:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_DIMPLUS
	case iotv1proto.Brightness_BRIGHTNESS_DIMPLUS:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_FULL
	}

	span.SetAttributes(attribute.String("after", z.brightness.String()))
}

func (z *Zone) DecrementBrightness(ctx context.Context) {
	_, span := z.tracer.Start(ctx, "Zone.DecrementBrightness")
	defer span.End()

	z.mtx.Lock()
	defer z.mtx.Unlock()

	span.SetAttributes(attribute.String("before", z.brightness.String()))

	switch z.brightness {
	case iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_FULL

	case iotv1proto.Brightness_BRIGHTNESS_FULL:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_DIMPLUS
	case iotv1proto.Brightness_BRIGHTNESS_DIMPLUS:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_DIM
	case iotv1proto.Brightness_BRIGHTNESS_DIM:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_LOWPLUS
	case iotv1proto.Brightness_BRIGHTNESS_LOWPLUS:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_LOW
	case iotv1proto.Brightness_BRIGHTNESS_LOW:
		z.brightness = iotv1proto.Brightness_BRIGHTNESS_VERYLOW
	}

	span.SetAttributes(attribute.String("after", z.brightness.String()))
}

func (z *Zone) Off(ctx context.Context) {
	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_OFF)
}

func (z *Zone) On(ctx context.Context) {
	state := z.State()

	for _, s := range defaultOnStates {
		if s == state {
			return
		}
	}

	z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)
}

func (z *Zone) SetState(ctx context.Context, state iotv1proto.ZoneState) {
	_, span := z.tracer.Start(ctx, "Zone.SetState")
	defer span.End()

	span.SetAttributes(attribute.String("state", state.String()))

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

// FlushLimiter is used to determine if a device should be included in the flush.  A true value indicates inclusivity.
type FlushLimiter func(name string) bool

// Flush handles pushing the current state out to each of the handlers.
func (z *Zone) Flush(ctx context.Context, limiter FlushLimiter) error {
	if z.name == "" {
		return fmt.Errorf("unable to handle unnamed zone: %+v", z)
	}

	ctx, span := z.tracer.Start(ctx, "Zone.Flush")
	defer span.End()

	span.SetAttributes(
		attribute.String("name", z.name),
		attribute.String("state", z.state.String()),
		attribute.String("brightness", z.brightness.String()),
		attribute.String("color_temp", z.colorTemp.String()),
		attribute.Int("device_count", len(z.devices)),
	)

	var (
		err  error
		errs []error
	)

	switch z.state {
	case iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED:
		// Raise the state off the ground
		z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_OFF)
		return nil
	case iotv1proto.ZoneState_ZONE_STATE_ON:
		err = z.handleOn(ctx, limiter)
		if err != nil {
			errs = append(errs, err)
		}
	case iotv1proto.ZoneState_ZONE_STATE_OFF:
		err = z.handleOff(ctx, limiter)
		if err != nil {
			errs = append(errs, err)
		}
	case iotv1proto.ZoneState_ZONE_STATE_COLOR:
		err = z.handleColor(ctx, limiter)
		if err != nil {
			errs = append(errs, err)
		}
	case iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR:
		err = z.handleRandomColor(ctx, limiter)
		if err != nil {
			errs = append(errs, err)
		}
	case iotv1proto.ZoneState_ZONE_STATE_NIGHTVISION:
		z.color = nightVisionColor
		err = z.handleColor(ctx, limiter)
		if err != nil {
			errs = append(errs, err)
		}
	case iotv1proto.ZoneState_ZONE_STATE_EVENINGVISION:
		z.colorTemp = iotv1proto.ColorTemperature_COLOR_TEMPERATURE_EVENING
	case iotv1proto.ZoneState_ZONE_STATE_MORNINGVISION:
		z.colorTemp = iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING
	}

	switch z.state {
	case iotv1proto.ZoneState_ZONE_STATE_OFF:
	default:
		err = z.handleBrightness(ctx, limiter)
		if err != nil {
			errs = append(errs, err)
		}
	}

	// Skip setting the white color-temp when in a color state.
	switch z.state {
	case iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR:
	case iotv1proto.ZoneState_ZONE_STATE_COLOR:
	case iotv1proto.ZoneState_ZONE_STATE_OFF:
	default:
		err = z.handleColorTemperature(ctx, limiter)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// handleOn takes care of the behavior when the light is set to On.  This
// includes brightness and color temperature.  The color hue of the light is
// handled by ZoneState_COLOR.
func (z *Zone) handleOn(ctx context.Context, limiter FlushLimiter) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleOn")
	defer span.End()

	var (
		err         error
		errs        []error
		deviceNames []string
	)

	for device, h := range z.devices {
		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_RELAY:
		default:
			continue
		}

		if limiter != nil && !limiter(device.Name) {
			continue
		}
		deviceNames = append(deviceNames, device.Name)

		err = h.On(ctx, device)
		if err != nil {
			errs = append(errs, err)
		}
	}

	span.SetAttributes(
		attribute.StringSlice("deviceNames", deviceNames),
		attribute.Int("totalDevices", len(z.devices)),
	)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleOff(ctx context.Context, limiter FlushLimiter) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleOff")
	defer span.End()

	var (
		err         error
		errs        []error
		deviceNames []string
	)

	for device, h := range z.devices {
		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_RELAY:
		default:
			continue
		}

		if limiter != nil && !limiter(device.Name) {
			continue
		}
		deviceNames = append(deviceNames, device.Name)

		err = h.Off(ctx, device)
		if err != nil {
			errs = append(errs, err)
		}
	}

	span.SetAttributes(
		attribute.StringSlice("deviceNames", deviceNames),
		attribute.Int("totalDevices", len(z.devices)),
	)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleColorTemperature(ctx context.Context, limiter FlushLimiter) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleColorTemperature")
	defer span.End()

	var (
		err         error
		errs        []error
		deviceNames []string
	)

	for device, h := range z.devices {
		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		default:
			continue
		}

		if limiter != nil && !limiter(device.Name) {
			continue
		}
		deviceNames = append(deviceNames, device.Name)

		err = h.SetColorTemp(ctx, device, defaultColorTemperatureMap[z.colorTemp])
		if err != nil {
			errs = append(errs, err)
		}
	}

	span.SetAttributes(
		attribute.StringSlice("deviceNames", deviceNames),
		attribute.Int("totalDevices", len(z.devices)),
	)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleBrightness(ctx context.Context, limiter FlushLimiter) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleBrightness")
	defer span.End()

	var (
		err         error
		errs        []error
		deviceNames []string
	)

	for device, h := range z.devices {

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT:
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		default:
			continue
		}

		if limiter != nil && !limiter(device.Name) {
			continue
		}
		deviceNames = append(deviceNames, device.Name)

		err = h.SetBrightness(ctx, device, defaultBrightnessMap[z.brightness])
		if err != nil {
			errs = append(errs, err)
		}
	}

	span.SetAttributes(
		attribute.StringSlice("deviceNames", deviceNames),
		attribute.Int("totalDevices", len(z.devices)),
	)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleColor(ctx context.Context, limiter FlushLimiter) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleColor")
	defer span.End()

	var (
		err         error
		errs        []error
		deviceNames []string
	)

	for device, h := range z.devices {

		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		default:
			continue
		}

		if limiter != nil && !limiter(device.Name) {
			continue
		}

		deviceNames = append(deviceNames, device.Name)

		err = h.SetColor(ctx, device, z.color)
		if err != nil {
			errs = append(errs, err)
		}
	}

	span.SetAttributes(
		attribute.StringSlice("deviceNames", deviceNames),
		attribute.Int("totalDevices", len(z.devices)),
	)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (z *Zone) handleRandomColor(ctx context.Context, limiter FlushLimiter) error {
	ctx, span := z.tracer.Start(ctx, "Zone.handleRandomColor")
	defer span.End()

	var (
		err         error
		errs        []error
		deviceNames []string
	)

	for device, h := range z.devices {
		switch device.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		default:
			continue
		}

		if limiter != nil && !limiter(device.Name) {
			continue
		}

		deviceNames = append(deviceNames, device.Name)

		err = h.RandomColor(ctx, device, z.colorPool)
		if err != nil {
			errs = append(errs, err)
		}
	}

	span.SetAttributes(
		attribute.StringSlice("deviceNames", deviceNames),
		attribute.Int("totalDevices", len(z.devices)),
	)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
