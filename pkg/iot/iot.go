// IOT is used to model and interface with various devices.
package iot

import "errors"

var (
	ErrHandlerFailed = errors.New("handler failed")
	ErrInvalidDevice = errors.New("invalid device")
)
