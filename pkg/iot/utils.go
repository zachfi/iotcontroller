package iot

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

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

		switch nodeIndex {
		case 1:
			tp.Component = parts[0]
			tp.NodeID = parts[nodeIndex]
		case 2:
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
func ReadZigbeeMessage(ctx context.Context, tracer trace.Tracer, dis *iotv1proto.DeviceDiscovery) (interface{}, error) {
	_, span := tracer.Start(ctx, "iot.ReadZigbeeMessage")
	defer span.End()

	e := strings.Join(dis.Endpoints, "/")

	span.SetAttributes(
		attribute.String("endpoint", e),
		attribute.String("object_id", dis.ObjectId),
	)

	switch dis.ObjectId {
	case "bridge":
		// topic: zigbee2mqtt/bridge/log
		switch e {
		case "info", "groups", "extensions", "devices", "config", "logging", "log", "state":
			// devices moved to router
			// state moved to router
			return nil, nil
		case "config/devices": // the publish channel to ask for devices
			return nil, nil
		}
		return nil, fmt.Errorf("unhandled bridge endpoint: %s", e)
	default:
		/* if len(dis.Endpoints) == 0 { */
		/* 	m := zigbee2mqtt.ZigbeeMessage{} */
		/* 	err := json.Unmarshal(dis.Message, &m) */
		/* 	if err != nil { */
		/* 		return nil, err */
		/* 	} */
		/* 	return m, nil */
		/* } */
	}

	return nil, nil
}

func ReadMessage(objectID string, payload []byte, endpoint ...string) (interface{}, error) {
	switch objectID {
	case "wifi":
		m := wifiMessage{}
		err := json.Unmarshal(payload, &m)
		if err != nil {
			return nil, err
		}
		return m, nil
	case "air":
		m := airMessage{}
		err := json.Unmarshal(payload, &m)
		if err != nil {
			return nil, err
		}
		return m, nil
	case "water":
		m := waterMessage{}
		err := json.Unmarshal(payload, &m)
		if err != nil {
			return nil, err
		}
		return m, nil
	case "led":
		if len(endpoint) > 0 {
			switch endpoint[0] {
			case "config":
				m := lEDConfig{}
				err := json.Unmarshal(payload, &m)
				if err != nil {
					return nil, err
				}
				return m, nil
			case "color":
				m := lEDColor{}
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

func ZoneStateToProto(status string) iotv1proto.ZoneState {
	if v, ok := iotv1proto.ZoneState_value[status]; ok {
		return iotv1proto.ZoneState(v)
	}

	return iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED
}
