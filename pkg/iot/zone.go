package iot

import (
	"context"
	"errors"
	"fmt"
	sync "sync"
	"time"

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

func NewZone(name string) *Zone {
	z := &Zone{
		colorTemp:     tempDay,
		brightnessMap: defaultBrightnessMap,
		colorTempMap:  defaultColorTemperatureMap,
	}

	z.SetName(name)

	return z
}

type Zone struct {
	sync.Mutex

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
	z.Lock()
	defer z.Unlock()
	return z.state
}

func (z *Zone) SetDevice(device *iotv1proto.Device, handler Handler) error {
	z.Lock()
	defer z.Unlock()

	if device == nil {
		return ErrInvalidDevice
	}

	if device.Name == "" {
		return ErrInvalidDevice
	}

	for k := range z.devices {
		if k.Name == device.Name {
			return nil
		}
	}

	z.devices[device] = handler

	return nil
}

func (z *Zone) SetName(name string) {
	z.Lock()
	defer z.Unlock()
	z.name = name
}

func (z *Zone) SetBrightnessMap(m map[iotv1proto.Brightness]uint8) {
	z.Lock()
	defer z.Unlock()
	z.brightnessMap = m
}

func (z *Zone) SetColorTemperatureMap(m map[iotv1proto.ColorTemperature]int32) {
	z.Lock()
	defer z.Unlock()
	z.colorTempMap = m
}

func (z *Zone) Name() string {
	return z.name
}

func (z *Zone) SetColorTemperature(ctx context.Context, colorTemp iotv1proto.ColorTemperature) error {
	z.colorTemp = z.colorTempMap[colorTemp]
	return nil
}

func (z *Zone) SetBrightness(ctx context.Context, brightness iotv1proto.Brightness) error {
	z.brightness = z.brightnessMap[brightness]
	return nil
}

func (z *Zone) IncrementBrightness(ctx context.Context) error {
	var currentBri iotv1proto.Brightness
	for k, v := range z.brightnessMap {
		if v == z.brightness {
			currentBri = k
		}
	}

	if currentBri > 0 {
		z.brightness = z.brightnessMap[currentBri-1]
	}
	return nil
}

func (z *Zone) DecrementBrightness(ctx context.Context) error {
	var currentBri iotv1proto.Brightness
	for k, v := range z.brightnessMap {
		if v == z.brightness {
			currentBri = k
		}
	}

	if currentBri < 5 {
		z.brightness = z.brightnessMap[currentBri+1]
	}
	return nil
}

func (z *Zone) Off(ctx context.Context) error {
	return z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_OFF)
}

func (z *Zone) On(ctx context.Context) error {
	return z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_ON)
}

// func (z *Zone) Alert(ctx context.Context) error {
// 	for _, h := range z.handlers {
// 		err := h.Alert(ctx, z.Name())
// 		if err != nil {
// 			return fmt.Errorf("%s alert: %w", z.name, ErrHandlerFailed)
// 		}
// 	}
//
// 	return nil
// }

func (z *Zone) SetColor(ctx context.Context, color string) error {
	z.color = color
	return z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_COLOR)
}

func (z *Zone) RandomColor(ctx context.Context, colors []string) error {
	z.colorPool = colors
	return z.SetState(ctx, iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR)
}

func (z *Zone) SetState(ctx context.Context, state iotv1proto.ZoneState) error {
	span := trace.SpanFromContext(ctx)
	defer span.End()

	z.Lock()
	defer z.Unlock()

	z.state = state

	return z.Flush(ctx)
}

// Flush handles pushing the current state out to each of the hnadlers.
func (z *Zone) Flush(ctx context.Context) error {
	if z.name == "" {
		return fmt.Errorf("unable to handle unnamed zone")
	}

	span := trace.SpanFromContext(ctx)
	defer span.End()

	switch z.state {
	case iotv1proto.ZoneState_ZONE_STATE_ON:
		return z.handleOn(ctx)
	case iotv1proto.ZoneState_ZONE_STATE_OFF:
		return z.handleOff(ctx)
	case iotv1proto.ZoneState_ZONE_STATE_COLOR:
		for device, h := range z.devices {
			err := h.SetColor(ctx, device, z.color)
			if err != nil {
				return fmt.Errorf("%s color: %w", z.name, ErrHandlerFailed)
			}
		}
	case iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR:
		for device, h := range z.devices {
			err := h.RandomColor(ctx, device, z.colorPool)
			if err != nil {
				return fmt.Errorf("%s random color: %w", z.name, ErrHandlerFailed)
			}
		}
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
