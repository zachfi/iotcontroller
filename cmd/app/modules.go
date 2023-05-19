package app

import (
	"context"
	"fmt"

	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/modules"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"github.com/weaveworks/common/server"
	"github.com/zachfi/iotcontroller/modules/controller"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
)

const (
	Server string = "server"

	Controller string = "controller"
	MQTTClient string = "mqttclient"

	All string = "all"
)

func (a *App) setupModuleManager() error {
	mm := modules.NewManager(a.logger)
	mm.RegisterModule(Server, a.initServer, modules.UserInvisibleModule)
	mm.RegisterModule(MQTTClient, a.initMqttClient)
	mm.RegisterModule(Controller, a.initController)
	mm.RegisterModule(All, nil)

	deps := map[string][]string{
		// Server:       nil,

		// Inventory: {Server},
		// Lights:    {Server},
		// Telemetry: {Server, Inventory, Lights},
		//
		// Harvester: {Server, Telemetry},
		Controller: {Server},
		MQTTClient: {Server},

		All: {Controller, MQTTClient},
	}

	for mod, targets := range deps {
		if err := mm.AddDependency(mod, targets...); err != nil {
			return err
		}
	}

	a.ModuleManager = mm

	return nil
}

func (a *App) initMqttClient() (services.Service, error) {
	c, err := mqttclient.New(a.cfg.MQTT, a.logger)
	if err != nil {
		return nil, err
	}
	a.mqttclient = c

	return c, nil
}

func (a *App) initController() (services.Service, error) {
	c, err := controller.New(a.cfg.Controller, a.logger)
	if err != nil {
		return nil, err
	}
	a.controller = c

	return c, nil
}

func (a *App) initServer() (services.Service, error) {
	a.cfg.Server.MetricsNamespace = metricsNamespace
	a.cfg.Server.ExcludeRequestInLog = true
	a.cfg.Server.RegisterInstrumentation = true

	server, err := server.New(a.cfg.Server)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create server")
	}

	servicesToWaitFor := func() []services.Service {
		svs := []services.Service(nil)
		for m, s := range a.serviceMap {
			// Server should not wait for itself.
			if m != Server {
				svs = append(svs, s)
			}
		}
		return svs
	}

	a.Server = server

	serverDone := make(chan error, 1)

	runFn := func(ctx context.Context) error {
		go func() {
			defer close(serverDone)
			serverDone <- server.Run()
		}()

		select {
		case <-ctx.Done():
			return nil
		case err := <-serverDone:
			if err != nil {
				return err
			}
			return fmt.Errorf("server stopped unexpectedly")
		}
	}

	stoppingFn := func(_ error) error {
		// wait until all modules are done, and then shutdown server.
		for _, s := range servicesToWaitFor() {
			_ = s.AwaitTerminated(context.Background())
		}

		// shutdown HTTP and gRPC servers (this also unblocks Run)
		server.Shutdown()

		// if not closed yet, wait until server stops.
		<-serverDone
		_ = level.Info(a.logger).Log("msg", "server stopped")
		return nil
	}

	return services.NewBasicService(nil, runFn, stoppingFn), nil
}
