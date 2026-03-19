package colorcontrol

import (
	"github.com/shimmeringbee/zcl"
	"github.com/shimmeringbee/zigbee"
)

const (
	ColorControlId = zcl.ColorControlId // 0x0300

	MoveToHueId                      = zcl.CommandIdentifier(0x00)
	MoveHueId                        = zcl.CommandIdentifier(0x01)
	StepHueId                        = zcl.CommandIdentifier(0x02)
	MoveToSaturationId               = zcl.CommandIdentifier(0x03)
	MoveSaturationId                 = zcl.CommandIdentifier(0x04)
	StepSaturationId                 = zcl.CommandIdentifier(0x05)
	MoveToHueAndSaturationId         = zcl.CommandIdentifier(0x06)
	MoveToColorId                    = zcl.CommandIdentifier(0x07)
	MoveColorId                      = zcl.CommandIdentifier(0x08)
	StepColorId                      = zcl.CommandIdentifier(0x09)
	MoveToColorTempId                = zcl.CommandIdentifier(0x0A)
	EnhancedMoveToHueId              = zcl.CommandIdentifier(0x40)
	EnhancedMoveHueId                = zcl.CommandIdentifier(0x41)
	EnhancedStepHueId                = zcl.CommandIdentifier(0x42)
	EnhancedMoveToHueAndSaturationId = zcl.CommandIdentifier(0x43)
	ColorLoopSetId                   = zcl.CommandIdentifier(0x44)
	StopMoveStepId                   = zcl.CommandIdentifier(0x47)
	MoveColorTempId                  = zcl.CommandIdentifier(0x4B)
	StepColorTempId                  = zcl.CommandIdentifier(0x4C)
)

type MoveToHue struct {
	Hue            uint8
	Direction      uint8
	TransitionTime uint16
}

type MoveHue struct {
	MoveMode uint8
	Rate     uint8
}

type StepHue struct {
	StepMode       uint8
	StepSize       uint8
	TransitionTime uint8
}

type MoveToSaturation struct {
	Saturation     uint8
	TransitionTime uint16
}

type MoveSaturation struct {
	MoveMode uint8
	Rate     uint8
}

type StepSaturation struct {
	StepMode       uint8
	StepSize       uint8
	TransitionTime uint8
}

type MoveToHueAndSaturation struct {
	Hue            uint8
	Saturation     uint8
	TransitionTime uint16
}

type MoveToColor struct {
	ColorX         uint16
	ColorY         uint16
	TransitionTime uint16
}

type MoveColor struct {
	RateX int16
	RateY int16
}

type StepColor struct {
	StepX          int16
	StepY          int16
	TransitionTime uint16
}

type MoveToColorTemp struct {
	ColorTemperature uint16
	TransitionTime   uint16
}

type StopMoveStep struct{}

type MoveColorTemp struct {
	MoveMode          uint8
	Rate              uint16
	ColorTempMinMired uint16
	ColorTempMaxMired uint16
}

type StepColorTemp struct {
	StepMode          uint8
	StepSize          uint16
	TransitionTime    uint16
	ColorTempMinMired uint16
	ColorTempMaxMired uint16
}

func Register(cr *zcl.CommandRegistry) {
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveToHueId, &MoveToHue{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveHueId, &MoveHue{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, StepHueId, &StepHue{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveToSaturationId, &MoveToSaturation{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveSaturationId, &MoveSaturation{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, StepSaturationId, &StepSaturation{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveToHueAndSaturationId, &MoveToHueAndSaturation{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveToColorId, &MoveToColor{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveColorId, &MoveColor{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, StepColorId, &StepColor{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveToColorTempId, &MoveToColorTemp{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, StopMoveStepId, &StopMoveStep{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveColorTempId, &MoveColorTemp{})
	cr.RegisterLocal(ColorControlId, zigbee.NoManufacturer, zcl.ClientToServer, StepColorTempId, &StepColorTemp{})
}
