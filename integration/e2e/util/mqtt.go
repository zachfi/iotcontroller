package util

import (
	"github.com/grafana/e2e"
)

const (
	mqttImage = "eclipse-mosquitto:2"
	image     = "zachfi/iotcontroller:latest"
)

func NewMQTTServer(name string) *e2e.ConcreteService {
	port := 1883

	s := e2e.NewConcreteService(
		name,
		mqttImage,
		e2e.NewCommand("mosquitto", "-c", "/shared/mosquitto.conf"),
		e2e.NewTCPReadinessProbe(port),
		port,
	)

	return s
}
