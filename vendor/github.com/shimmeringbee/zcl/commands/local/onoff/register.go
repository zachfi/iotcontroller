package onoff

import (
	"github.com/shimmeringbee/zcl"
	"github.com/shimmeringbee/zigbee"
)

func Register(cr *zcl.CommandRegistry) {
	cr.RegisterLocal(zcl.OnOffId, zigbee.NoManufacturer, zcl.ClientToServer, OffId, &Off{})
	cr.RegisterLocal(zcl.OnOffId, zigbee.NoManufacturer, zcl.ClientToServer, OnId, &On{})
	cr.RegisterLocal(zcl.OnOffId, zigbee.NoManufacturer, zcl.ClientToServer, ToggleId, &Toggle{})
	cr.RegisterLocal(zcl.OnOffId, zigbee.NoManufacturer, zcl.ClientToServer, OffWithEffectId, &OffWithEffect{})
	cr.RegisterLocal(zcl.OnOffId, zigbee.NoManufacturer, zcl.ClientToServer, OnWithRecallGlobalSceneId, &OnWithRecallGlobalScene{})
	cr.RegisterLocal(zcl.OnOffId, zigbee.NoManufacturer, zcl.ClientToServer, OnWithTimedOffId, &OnWithTimedOff{})
}
