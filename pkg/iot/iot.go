// IOT is used to model and interface with various devices.
package iot

import "errors"

const (
	DeviceZoneLabel = "iot/zone"
)

var (
	ErrHandlerFailed = errors.New("handler failed")
	ErrInvalidDevice = errors.New("invalid device")
)
