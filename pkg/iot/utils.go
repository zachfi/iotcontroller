package iot

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

type TopicPath struct {
	DiscoveryPrefix string
	Component       string
	NodeID          string
	ObjectID        string
	Endpoints       []string
}

func ParseDiscoveryMessage(topicPath TopicPath, msg mqtt.Message) *iotv1proto.DeviceDiscovery {
	return &iotv1proto.DeviceDiscovery{
		Component: topicPath.Component,
		NodeId:    topicPath.NodeID,
		ObjectId:  topicPath.ObjectID,
		Endpoints: topicPath.Endpoints,
		Message:   msg.Payload(),
	}
}

func ParseTopicPath(topic string) (TopicPath, error) {
	// <discovery_prefix>/<component>/[<node_id>]/<object_id>/config

	var tp TopicPath
	tp.Endpoints = make([]string, 0)

	nodeIDRegex := regexp.MustCompile(`^.*/([0-9a-z]{32})/.*$`)
	parts := strings.Split(topic, "/")

	match := nodeIDRegex.FindAllStringSubmatch(topic, -1)
	if len(match) == 1 {
		// A node ID has been found in the topic path.

		// Figure out which part matches the node ID.
		nodeIndex := func(parts []string) int {
			for i, p := range parts {
				if p == match[0][1] {
					return i
				}
			}

			return 0
		}(parts)

		// Determine if we have a discovery_prefix to set.
		if nodeIndex == 1 {
			tp.Component = parts[0]
			tp.NodeID = parts[nodeIndex]
		} else if nodeIndex == 2 {
			tp.DiscoveryPrefix = parts[0]
			tp.Component = parts[1]
			tp.NodeID = parts[nodeIndex]
		}

		if nodeIndex > 0 {
			next := nodeIndex + 1
			tp.ObjectID = parts[next]
			next++
			tp.Endpoints = parts[next:]
		}

	} else {
		// else a node ID is not matched in the topic path.
		tp.Component = parts[0]
		tp.ObjectID = parts[1]
		tp.Endpoints = parts[2:]
	}

	return tp, nil
}

// ReadZigbeeMessage implements the payload unmarshaling for zigbee2mqtt
// https://www.zigbee2mqtt.io/information/mqtt_topics_and_message_structure.html
func ReadZigbeeMessage(objectID string, payload []byte, endpoint ...string) (interface{}, error) {
	e := strings.Join(endpoint, "/")

	switch objectID {
	case "bridge":
		// topic: zigbee2mqtt/bridge/log
		switch e {
		case "log":
			m := ZigbeeBridgeLog{}
			err := json.Unmarshal(payload, &m)
			if err != nil {
				return nil, err
			}
			return m, nil
		case "state":
			m := ZigbeeBridgeState(string(payload))
			if m != "" {
				return m, nil
			}
		case "config", "logging":
			// do nothing for a config message
			return nil, nil
		case "devices":
			m := ZigbeeMessageBridgeDevices{}
			err := json.Unmarshal(payload, &m)
			if err != nil {
				return nil, err
			}
			return m, nil
		case "info", "groups", "extensions":
			return nil, nil
		case "config/devices": // the publish channel to ask for devices
			return nil, nil
		}
		return nil, fmt.Errorf("unhandled bridge endpoint: %s", e)
	default:
		if len(endpoint) == 0 {
			m := ZigbeeMessage{}
			err := json.Unmarshal(payload, &m)
			if err != nil {
				return nil, err
			}
			return m, nil
		}
	}

	return nil, nil
}

func ReadMessage(objectID string, payload []byte, endpoint ...string) (interface{}, error) {
	log.WithFields(log.Fields{
		"objectID": objectID,
		"endpoint": endpoint,
		"payload":  string(payload),
	}).Trace("ReadMessage()")

	switch objectID {
	case "wifi":
		m := WifiMessage{}
		err := json.Unmarshal(payload, &m)
		if err != nil {
			log.Error(err)
		}
		return m, nil
	case "air":
		m := AirMessage{}
		err := json.Unmarshal(payload, &m)
		if err != nil {
			log.Error(err)
		}
		return m, nil
	case "water":
		m := WaterMessage{}
		err := json.Unmarshal(payload, &m)
		if err != nil {
			return nil, err
		}
		return m, nil
	case "led":
		if len(endpoint) > 0 {
			if endpoint[0] == "config" {
				m := LEDConfig{}
				err := json.Unmarshal(payload, &m)
				if err != nil {
					return nil, err
				}
				return m, nil
			} else if endpoint[0] == "color" {
				m := LEDColor{}
				err := json.Unmarshal(payload, &m)
				if err != nil {
					return nil, err
				}
				return m, nil
			}
			return nil, fmt.Errorf("unhandled led endpoint: %s : %+v", endpoint, string(payload))
		}
	}

	return nil, nil
}

func ZigbeeDeviceType(z ZigbeeBridgeDevice) iotv1proto.DeviceType {
	switch z.Type {
	case "Coordinator":
		return iotv1proto.DeviceType_DEVICE_TYPE_COORDINATOR
	}

	switch z.Definition.Vendor {
	case "Philips":
		switch z.ModelID {
		case "LWB014":
			return iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT
		case "ROM001":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		default:
			if strings.HasPrefix(z.ModelID, "LC") {
				return iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT
			}
		}

	case "Xiaomi":
		switch z.Definition.Model {
		case "WXKG11LM":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		}

		switch z.ModelID {
		case "lumi.sensor_switch":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		case "lumi.sensor_motion.aq2":
			return iotv1proto.DeviceType_DEVICE_TYPE_MOTION
		case "lumi.weather":
			return iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE
		case "lumi.sensor_cube.aqgl01":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		case "lumi.remote.b1acn01":
			return iotv1proto.DeviceType_DEVICE_TYPE_BUTTON
		case "lumi.sensor_wleak.aq1":
			return iotv1proto.DeviceType_DEVICE_TYPE_LEAK
		}

	case "SONOFF":
		relayRegex := regexp.MustCompile(`^S[0-9]{2}ZB.*$`)
		match := relayRegex.FindAllString(z.Definition.Model, -1)
		if len(match) == 1 {
			return iotv1proto.DeviceType_DEVICE_TYPE_RELAY
		}
	}

	return iotv1proto.DeviceType_DEVICE_TYPE_UNSPECIFIED
}

func ZoneStateToProto(status string) iotv1proto.ZoneState {
	if v, ok := iotv1proto.ZoneState_value[status]; ok {
		return iotv1proto.ZoneState(v)
	}

	return iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED
}
