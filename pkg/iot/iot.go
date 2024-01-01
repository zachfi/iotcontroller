// IOT is used to model and interface with various devices.
package iot

import "errors"

const (
	// CRD
	DeviceZoneLabel = "iot/zone"

	// Condition matching
	AlertNameLabel = "alertname"
	EventNameLabel = "eventname"
	StatusLabel    = "status"
	ZoneLabel      = "zone"
)

var (
	ErrHandlerFailed = errors.New("handler failed")
	ErrInvalidDevice = errors.New("invalid device")
)
