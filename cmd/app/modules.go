package app

import (
	"context"
	"fmt"

	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/modules"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"github.com/weaveworks/common/server"
	"github.com/zachfi/iotcontroller/modules/client"
	"github.com/zachfi/iotcontroller/modules/controller"
	"github.com/zachfi/iotcontroller/modules/harvester"
	"github.com/zachfi/iotcontroller/modules/kubeclient"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/modules/telemetry"
	telemetryv1 "github.com/zachfi/iotcontroller/proto/telemetry/v1"
)

const (
	Server string = "server"

	Controller string = "controller"
	Harvester  string = "harvester"
	// Lights          string = "lights"
	MQTTClient string = "mqttclient"
	// Inventory       string = "inventory"
	// InventoryClient string = "inventory_client"
	Telemetry string = "telemetry"
	// Timer      string = "timer"
	Client     string = "client"
	KubeClient string = "kube-client"

	// Weather string = "weather"

	All string = "all"
)

func (a *App) setupModuleManager() error {
	mm := modules.NewManager(a.logger)
	mm.RegisterModule(Server, a.initServer, modules.UserInvisibleModule)
	mm.RegisterModule(MQTTClient, a.initMqttClient)
	mm.RegisterModule(Harvester, a.initHarvester)
	mm.RegisterModule(Telemetry, a.initTelemetry)
	mm.RegisterModule(Controller, a.initController)
	// mm.RegisterModule(Lights, a.initLights)
	// mm.RegisterModule(Timer, a.initTimer)
	// mm.RegisterModule(Inventory, a.initInventory)
	// mm.RegisterModule(InventoryClient, a.initInventoryClient)
	mm.RegisterModule(KubeClient, a.initKubeClient)
	mm.RegisterModule(Client, a.initClient)
	mm.RegisterModule(All, nil)

	deps := map[string][]string{
		// Server:       nil,

		// Inventory: {Server},
		// Lights:          {Server},
		// InventoryClient: {Server},

		Harvester:  {Server, MQTTClient, Telemetry, Client},
		MQTTClient: {Server},
		Controller: {Server, MQTTClient},
		Telemetry:  {Server, KubeClient},
		KubeClient: {Server},
		Client:     {Server},
		// Timer:      {Server},

		All: {Controller, Harvester},
	}

	for mod, targets := range deps {
		if err := mm.AddDependency(mod, targets...); err != nil {
			return err
		}
	}

	a.ModuleManager = mm

	return nil
}

func (a *App) initClient() (services.Service, error) {
	c, err := client.New(a.cfg.Client, a.logger)
	if err != nil {
		return nil, err
	}

	a.client = c
	return c, nil
}

func (a *App) initKubeClient() (services.Service, error) {
	k, err := kubeclient.New(a.cfg.KubeClient, a.logger)
	if err != nil {
		return nil, err
	}

	a.kubeclient = k
	return k, nil
}

func (a *App) initTimer() (services.Service, error) {
	// conn := comms.SlimRPCClient(z.cfg.RPC.ServerAddress, z.logger)
	//
	// t, err := timer.New(z.cfg.Timer, z.logger, conn)
	// if err != nil {
	// 	return nil, errors.Wrap(err, "unable to init timer")
	// }
	//
	// // astro.RegisterAstroServer(z.Server.GRPC, t.Astro)
	// // named.RegisterNamedServer(z.Server.GRPC, t.Named)
	//
	// z.timer = t
	// return t, nil
	return nil, nil
}

func (a *App) initMqttClient() (services.Service, error) {
	c, err := mqttclient.New(a.cfg.MQTTClient, a.logger)
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

// func (a *All) initInventory() (services.Service, error) {
// 	i, err := inventory.NewLDAPServer(z.cfg.Inventory, z.logger)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	inventory.RegisterInventoryServer(z.Server.GRPC, i)
//
// 	z.inventory = i
// 	return i, nil
// }

func (a *App) initTelemetry() (services.Service, error) {
	// invClient, err := inventory.NewLDAPInventory(a.cfg.Inventory, a.logger)
	// if err != nil {
	// 	return nil, err
	// }

	t, err := telemetry.New(a.cfg.Telemetry, a.logger, a.kubeclient)
	if err != nil {
		return nil, err
	}

	telemetryv1.RegisterTelemetryServiceServer(a.Server.GRPC, t)

	a.telemetry = t
	return t, nil
}

func (a *App) initHarvester() (services.Service, error) {
	h, err := harvester.New(a.cfg.Harvester, a.logger, a.client.Conn(), a.mqttclient)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create harvester")
	}

	a.harvester = h
	return h, nil
}

// func (a *App) initLights() (services.Service, error) {
// 	mqttClient, err := iot.NewMQTTClient(a.cfg.IOT.MQTT, a.logger)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	s, err := lights.New(z.cfg.Lights, z.logger)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	scheduler, err := lights.StaticColorTempSchedule(z.cfg.Lights.TimeZone)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	zigbee, err := lights.NewZigbeeLight(a.cfg.Lights, mqttClient, a.inventoryClient, a.logger)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	s.AddHandler(zigbee)
// 	s.SetColorTempScheduler(scheduler)
//
// 	lights.RegisterLightsServer(a.Server.GRPC, s)
// 	a.lights = s
//
// 	return s, nil
// }

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
