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
		msg:   make(map[string]interface{}, 1),
	}

	switch device.Type {
	case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT,
		iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT:
		m.withTransition(defaultTransitionTime)
	}

	return m
}

func (m *message) withBrightness(brightness uint8) {
	m.Lock()
	defer m.Unlock()
	m.msg["brightness"] = brightness
}

func (m *message) withTransition(t float64) {
	m.Lock()
	defer m.Unlock()

	m.msg["transition"] = t
}

func (m *message) withColorTemp(temp int32) {
	m.Lock()
	defer m.Unlock()
	m.msg["color_temp"] = temp
}

func (m *message) withColor(hex string) {
	m.Lock()
	defer m.Unlock()
	m.msg["color"] = map[string]string{
		"hex": hex,
	}
}

func (m *message) withRandomColor(hex []string) {
	m.Lock()
	defer m.Unlock()

	m.withColor(hex[rand.Intn(len(hex))])
}

func (m *message) withOn() {
	m.Lock()
	defer m.Unlock()

	m.msg["state"] = "ON"
}

func (m *message) withOff() {
	m.Lock()
	defer m.Unlock()

	m.msg["state"] = "OFF"
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
