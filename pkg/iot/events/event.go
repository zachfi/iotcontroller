// Package events normalizes inbound device messages from any transport
// (zigbee2mqtt MQTT JSON, native Zigbee ZCL) into a single DeviceEvent
// shape. Bindings match against this normalized shape so a single
// EventTrigger Spec works regardless of how the device reaches us.
package events

import (
	"strconv"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
)

// Property names. These mirror zigbee2mqtt's expose property names so a
// Binding written for a z2m device can also match the same logical event
// arriving via the native Zigbee path. Add new constants here when adding
// support for a new expose.
const (
	PropertyAction        = "action"
	PropertyOccupancy     = "occupancy"
	PropertyWaterLeak     = "water_leak"
	PropertyContact       = "contact"
	PropertyTamper        = "tamper"
	PropertyVibration     = "vibration"
	PropertyState         = "state"
	PropertyBattery       = "battery"
	PropertyBrightness    = "brightness"
	PropertyColor         = "color"
	PropertyColorTemp     = "color_temp"
	PropertyHumidity      = "humidity"
	PropertyTemperature   = "temperature"
	PropertyIlluminance   = "illuminance"
	PropertySoilMoisture  = "soil_moisture"
	PropertyVOC           = "voc"
	PropertyFormaldehyde  = "formaldehyd"
	PropertyPressure      = "pressure"
	PropertyDeviceCo2     = "device_co2"
	PropertyLinkQuality   = "linkquality"
	PropertyTransmitPower = "transmit_power"
	PropertyVoltage       = "voltage"
)

// FeatureType values (z2m expose `type` field).
const (
	FeatureTypeSwitch = "switch"
)

// DeviceEvent is a single normalized event emitted by a device. The router
// builds a slice of these per inbound message and runs each through the
// Binding matcher.
type DeviceEvent struct {
	// Property is one of the Property* constants.
	Property string

	// Value is the property value rendered as a string. For booleans use
	// "true" / "false". For action enums use the action name. Empty when
	// the source message did not carry a value (rare).
	Value string

	// Device is the resolved Device CR, used for selector matching
	// (IEEE, name, type, labels).
	Device *apiv1.Device
}

// BoolString renders a bool as "true"/"false" for use in DeviceEvent.Value.
// Helper kept here so transports stay terse.
func BoolString(b bool) string { return strconv.FormatBool(b) }
