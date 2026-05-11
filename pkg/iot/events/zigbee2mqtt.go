package events

import (
	apiv1 "github.com/zachfi/iotcontroller/api/v1"
)

// Z2MMessage is the subset of zigbee2mqtt's per-device message JSON that we
// decode into events. Defined here (rather than imported from the z2m router
// package) to keep the dependency direction routers→events, not the reverse.
//
// Fields use pointers because z2m only includes a key when the device
// reported it; nil = not asserted in this message.
type Z2MMessage struct {
	Action    *string
	Occupancy *bool
	WaterLeak *bool
	Contact   *bool
	Tamper    *bool
	Vibration *bool
	State     *string // "ON" / "OFF" / "TOGGLE" — see normalizeState
}

// FromZ2M converts a single zigbee2mqtt message into a slice of normalized
// events for the given device. A message commonly carries multiple asserted
// properties (e.g. action + state); each becomes its own DeviceEvent so the
// matcher can pair each with its own Binding.
func FromZ2M(m Z2MMessage, d *apiv1.Device) []DeviceEvent {
	var out []DeviceEvent
	if m.Action != nil {
		out = append(out, DeviceEvent{Property: PropertyAction, Value: *m.Action, Device: d})
	}
	if m.Occupancy != nil {
		out = append(out, DeviceEvent{Property: PropertyOccupancy, Value: BoolString(*m.Occupancy), Device: d})
	}
	if m.WaterLeak != nil {
		out = append(out, DeviceEvent{Property: PropertyWaterLeak, Value: BoolString(*m.WaterLeak), Device: d})
	}
	if m.Contact != nil {
		out = append(out, DeviceEvent{Property: PropertyContact, Value: BoolString(*m.Contact), Device: d})
	}
	if m.Tamper != nil {
		out = append(out, DeviceEvent{Property: PropertyTamper, Value: BoolString(*m.Tamper), Device: d})
	}
	if m.Vibration != nil {
		out = append(out, DeviceEvent{Property: PropertyVibration, Value: BoolString(*m.Vibration), Device: d})
	}
	if m.State != nil {
		out = append(out, DeviceEvent{Property: PropertyState, Value: normalizeState(*m.State), Device: d})
	}
	return out
}

// normalizeState lowercases z2m's "ON"/"OFF"/"TOGGLE" so binding values can
// be written in either case. Empty input is preserved (some non-relay
// devices send empty state).
func normalizeState(s string) string {
	switch s {
	case "ON":
		return "on"
	case "OFF":
		return "off"
	case "TOGGLE":
		return "toggle"
	}
	return s
}
