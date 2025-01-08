package util

import "github.com/grafana/e2e"

const (
	mqttImage = "eclipse-mosquitto:2"
)

func NewMQTTServer(name string) *e2e.ConcreteService {
	port := 1883

	s := e2e.NewConcreteService(
		name,
		mqttImage,
		// e2e.NewCommand("mosquitto", "-c", "/mosquitto/config/mosquitto.conf"),
		e2e.NewCommand("mosquitto", "-c", "/shared/mosquitto.conf"),
		e2e.NewTCPReadinessProbe(port),
		port,
	)

	return s
}
