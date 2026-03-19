package levelcontrol

import (
	"github.com/shimmeringbee/zcl"
	"github.com/shimmeringbee/zigbee"
)

const (
	LevelControlId = zcl.LevelControlId // 0x0008

	MoveToLevelId          = zcl.CommandIdentifier(0x00)
	MoveId                 = zcl.CommandIdentifier(0x01)
	StepId                 = zcl.CommandIdentifier(0x02)
	StopId                 = zcl.CommandIdentifier(0x03)
	MoveToLevelWithOnOffId = zcl.CommandIdentifier(0x04)
	MoveWithOnOffId        = zcl.CommandIdentifier(0x05)
	StepWithOnOffId        = zcl.CommandIdentifier(0x06)
	StopWithOnOffId        = zcl.CommandIdentifier(0x07)
)

type MoveToLevel struct {
	Level          uint8
	TransitionTime uint16
}

type Move struct {
	MoveMode uint8
	Rate     uint8
}

type Step struct {
	StepMode       uint8
	StepSize       uint8
	TransitionTime uint16
}

type Stop struct{}

type MoveToLevelWithOnOff struct {
	Level          uint8
	TransitionTime uint16
}

type MoveWithOnOff struct {
	MoveMode uint8
	Rate     uint8
}

type StepWithOnOff struct {
	StepMode       uint8
	StepSize       uint8
	TransitionTime uint16
}

type StopWithOnOff struct{}

func Register(cr *zcl.CommandRegistry) {
	cr.RegisterLocal(LevelControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveToLevelId, &MoveToLevel{})
	cr.RegisterLocal(LevelControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveId, &Move{})
	cr.RegisterLocal(LevelControlId, zigbee.NoManufacturer, zcl.ClientToServer, StepId, &Step{})
	cr.RegisterLocal(LevelControlId, zigbee.NoManufacturer, zcl.ClientToServer, StopId, &Stop{})
	cr.RegisterLocal(LevelControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveToLevelWithOnOffId, &MoveToLevelWithOnOff{})
	cr.RegisterLocal(LevelControlId, zigbee.NoManufacturer, zcl.ClientToServer, MoveWithOnOffId, &MoveWithOnOff{})
	cr.RegisterLocal(LevelControlId, zigbee.NoManufacturer, zcl.ClientToServer, StepWithOnOffId, &StepWithOnOff{})
	cr.RegisterLocal(LevelControlId, zigbee.NoManufacturer, zcl.ClientToServer, StopWithOnOffId, &StopWithOnOff{})
}
