package onoff

import "github.com/shimmeringbee/zcl"

const (
	OnOff              = zcl.AttributeID(0x0000)
	GlobalSceneControl = zcl.AttributeID(0x4000)
	OnTime             = zcl.AttributeID(0x4001)
	OffWaitTime        = zcl.AttributeID(0x4002)
)

const (
	OffId                     = zcl.CommandIdentifier(0x00)
	OnId                      = zcl.CommandIdentifier(0x01)
	ToggleId                  = zcl.CommandIdentifier(0x02)
	OffWithEffectId           = zcl.CommandIdentifier(0x40)
	OnWithRecallGlobalSceneId = zcl.CommandIdentifier(0x41)
	OnWithTimedOffId          = zcl.CommandIdentifier(0x42)
)

type Off struct{}

type On struct{}

type Toggle struct{}

type OffWithEffect struct {
	EffectIdentifier uint8
	EffectVariant    uint8
}

type OnWithRecallGlobalScene struct{}

type OnOffControl struct {
	Reserved         uint8 `bcfieldwidth:"7"`
	AcceptOnlyWhenOn bool  `bcfieldwidth:"1"`
}

type OnWithTimedOff struct {
	OnOffControl OnOffControl
	OnTime       uint16
	OffWaitTime  uint16
}
