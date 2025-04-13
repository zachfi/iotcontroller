package zigbee

import (
	"fmt"
	"math/rand"
	"sync"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

type message struct {
	sync.Mutex
	msg   map[string]interface{}
	topic string
}

func newMessage(device *iotv1proto.Device) *message {
	m := &message{
		topic: fmt.Sprintf("zigbee2mqtt/%s/set", device.Name),
		msg:   make(map[string]any, 1),
	}

	switch device.Type {
	case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT,
		iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		m.withTransition(defaultTransitionTime)
	}

	return m
}

func (m *message) withBrightness(brightness uint8) *message {
	m.Lock()
	defer m.Unlock()
	m.msg["brightness"] = brightness

	return m
}

func (m *message) withTransition(t float64) *message {
	m.Lock()
	defer m.Unlock()

	m.msg["transition"] = t

	return m
}

func (m *message) withColorTemp(temp int32) *message {
	m.Lock()
	defer m.Unlock()
	m.msg["color_temp"] = temp

	return m
}

func (m *message) withColor(hex string) *message {
	m.Lock()
	defer m.Unlock()

	return m.withInternalColor(hex)
}

// Must be called under lock
func (m *message) withInternalColor(hex string) *message {
	m.msg["color"] = map[string]string{"hex": hex}
	return m
}

func (m *message) withRandomColor(hex []string) *message {
	m.Lock()
	defer m.Unlock()

	return m.withInternalColor(hex[rand.Intn(len(hex))])
}

func (m *message) withOn() *message {
	m.Lock()
	defer m.Unlock()

	m.msg["state"] = "ON"
	return m
}

func (m *message) withOff() *message {
	m.Lock()
	defer m.Unlock()

	m.msg["state"] = "OFF"
	return m
}

func (m *message) withBlink() {
	m.Lock()
	defer m.Unlock()

	m.msg["effect"] = "blink"
	m.withTransition(0.1)
}

// func (m *message) MarshalJSON() ([]byte, error) {
// 	m.Lock()
// 	defer m.Unlock()
// 	return json.Marshal(m.msg)
// }
