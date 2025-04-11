package util

import "github.com/grafana/e2e"

// Package util provides utility functions for creating services in a Kubernetes cluster using the e2e framework.
func NewKind(name string) *e2e.ConcreteService {
	port := 6443

	s := e2e.NewConcreteService(
		name,
		mqttImage,
		e2e.NewCommand("kind", "cluster", "create"),
		e2e.NewTCPReadinessProbe(port),
		port,
	)

	return s
}
