package util

import "github.com/grafana/e2e"

func NewK3dCluster(name string) *e2e.ConcreteService {
	s := e2e.NewConcreteService(
		name,
		"rancher/k3d:5.3.0-dind",
		e2e.NewCommand("k3d", "cluster", "create", name, "--api-port", "localhost:6443"),
		// e2e.NewCommand("kubectl", "wait", "--for=condition=Ready", "node", "--all", "--timeout=5m"),
		e2e.NewHTTPReadinessProbe(6443, "/", 200, 200),
		6443,
	)

	return s
}
