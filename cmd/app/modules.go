package app

import (
	"context"
	"fmt"
	"net/http"
	"os"

	kitlog "github.com/go-kit/log"
	"github.com/grafana/dskit/modules"
	"github.com/grafana/dskit/server"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"

	"github.com/zachfi/iotcontroller/modules/client"
	"github.com/zachfi/iotcontroller/modules/conditioner"
	"github.com/zachfi/iotcontroller/modules/controller"
	"github.com/zachfi/iotcontroller/modules/harvester"
	"github.com/zachfi/iotcontroller/modules/hookreceiver"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/modules/telemetry"
	"github.com/zachfi/iotcontroller/modules/zonekeeper"
	iotv1 "github.com/zachfi/iotcontroller/proto/iot/v1"
	telemetryv1 "github.com/zachfi/iotcontroller/proto/telemetry/v1"
)

const (
	Server string = "server"

	Client       string = "client"
	Conditioner  string = "conditioner"
	Controller   string = "controller"
	Harvester    string = "harvester"
	HookReceiver string = "hook-receiver"
	MQTTClient   string = "mqttclient"
	Telemetry    string = "telemetry"
	ZoneKeeper   string = "zone-keeper"

	// Weather string = "weather"

	All string = "all"
)

func (a *App) setupModuleManager() error {
	mm := modules.NewManager(kitlog.NewLogfmtLogger(os.Stderr))
	mm.RegisterModule(Server, a.initServer, modules.UserInvisibleModule)

	mm.RegisterModule(Client, a.initClient)
	mm.RegisterModule(Conditioner, a.initConditioner)
	mm.RegisterModule(Conditioner, a.initConditioner)
	mm.RegisterModule(Controller, a.initController)
	mm.RegisterModule(Harvester, a.initHarvester)
	mm.RegisterModule(HookReceiver, a.initHookReceiver)
	mm.RegisterModule(MQTTClient, a.initMqttClient)
	mm.RegisterModule(Telemetry, a.initTelemetry)
	mm.RegisterModule(ZoneKeeper, a.initZoneKeeper)

	mm.RegisterModule(All, nil)

	// mm.RegisterModule(Lights, a.initLights)
	// mm.RegisterModule(Timer, a.initTimer)
	// mm.RegisterModule(Inventory, a.initInventory)
	// mm.RegisterModule(InventoryClient, a.initInventoryClient)

	deps := map[string][]string{
		// Server:       nil,

		Client:       {Server},
		Conditioner:  {Server, MQTTClient, Controller},
		Controller:   {Server},
		Harvester:    {Server, MQTTClient, Client, Telemetry},
		HookReceiver: {Server, Client, Conditioner},
		MQTTClient:   {Server},
		Telemetry:    {Server, Controller},
		ZoneKeeper:   {Server, MQTTClient},

		// Inventory: {Server},
		// Lights:          {Server},
		// InventoryClient: {Server},
		// Timer:      {Server},

		All: {Controller, Harvester, HookReceiver, ZoneKeeper},
	}

	for mod, targets := range deps {
		if err := mm.AddDependency(mod, targets...); err != nil {
			return err
		}
	}

	a.ModuleManager = mm

	return nil
}

func (a *App) initHookReceiver() (services.Service, error) {
	h, err := hookreceiver.New(a.cfg.HookReceiver, a.logger, a.client.Conn())
	if err != nil {
		return nil, err
	}

	a.Server.HTTP.Handle("/alerts", http.HandlerFunc(h.Handler)).Methods(http.MethodPost)

	a.hookreceiver = h
	return h, nil
}

func (a *App) initClient() (services.Service, error) {
	c, err := client.New(a.cfg.Client, a.logger)
	if err != nil {
		return nil, err
	}

	a.client = c
	return c, nil
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

	fmt.Printf("%+v", a.controller)

	t, err := telemetry.New(a.cfg.Telemetry, a.logger, a.controller.Client())
	if err != nil {
		return nil, err
	}

	telemetryv1.RegisterTelemetryServiceServer(a.Server.GRPC, t)

	a.telemetry = t
	return t, nil
}

func (a *App) initZoneKeeper() (services.Service, error) {
	z, err := zonekeeper.New(a.cfg.ZoneKeeper, a.logger, a.mqttclient)
	if err != nil {
		return nil, err
	}

	iotv1.RegisterZoneServiceServer(a.Server.GRPC, z)

	a.zonekeeper = z
	return z, nil
}

func (a *App) initHarvester() (services.Service, error) {
	h, err := harvester.New(a.cfg.Harvester, a.logger, a.client.Conn(), a.mqttclient)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create harvester")
	}

	a.harvester = h
	return h, nil
}

func (a *App) initConditioner() (services.Service, error) {
	c, err := conditioner.New(a.cfg.Conditioner, a.logger, a.client.Conn(), a.mqttclient, a.controller.Client())
	if err != nil {
		return nil, errors.Wrap(err, "failed to create harvester")
	}

	iotv1.RegisterAlertReceiverServiceServer(a.Server.GRPC, c)

	a.conditioner = c
	return c, nil
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
	a.cfg.Server.ExcludeRequestInLog = false
	a.cfg.Server.RegisterInstrumentation = true
	a.cfg.Server.DisableRequestSuccessLog = false
	// a.cfg.Server.Log = a.logger

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
		a.logger.Info("server stopped")
		return nil
	}

	return services.NewBasicService(nil, runFn, stoppingFn), nil
}
