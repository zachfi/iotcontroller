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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/iotcontroller/modules/conditioner"
	"github.com/zachfi/iotcontroller/modules/controller"
	"github.com/zachfi/iotcontroller/modules/harvester"
	"github.com/zachfi/iotcontroller/modules/hookreceiver"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/modules/router"
	"github.com/zachfi/iotcontroller/modules/weather"
	"github.com/zachfi/iotcontroller/modules/zonekeeper"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	Server string = "server"

	EventClient  string = "eventclient"
	Conditioner  string = "conditioner"
	Controller   string = "controller"
	Harvester    string = "harvester"
	HookReceiver string = "hook-receiver"
	MQTTClient   string = "mqttclient"
	Router       string = "router"
	Weather      string = "weather"
	ZoneKeeper   string = "zone-keeper"

	All      string = "all"
	Core     string = "core"
	Receiver string = "receiver"
)

func (a *App) setupModuleManager() error {
	mm := modules.NewManager(kitlog.NewLogfmtLogger(os.Stderr))
	mm.RegisterModule(Server, a.initServer, modules.UserInvisibleModule)

	mm.RegisterModule(MQTTClient, a.initMqttClient)

	mm.RegisterModule(Conditioner, a.initConditioner)
	mm.RegisterModule(Controller, a.initController)
	mm.RegisterModule(Harvester, a.initHarvester)
	mm.RegisterModule(HookReceiver, a.initHookReceiver)
	mm.RegisterModule(Router, a.initRouter)
	mm.RegisterModule(Weather, a.initWeather)
	mm.RegisterModule(ZoneKeeper, a.initZoneKeeper)

	mm.RegisterModule(All, nil)
	mm.RegisterModule(Core, nil)
	mm.RegisterModule(Receiver, nil)

	deps := map[string][]string{
		// Server:       nil,

		MQTTClient: {Server}, // MQTT client
		Controller: {Server}, // K8s client

		Conditioner:  {Server, Controller},
		Harvester:    {Server, MQTTClient},
		HookReceiver: {Server},
		Router:       {Server, Controller},
		Weather:      {Server, Conditioner},
		ZoneKeeper:   {Server, MQTTClient, Controller},

		// Timer:      {Server},

		All: {
			Conditioner,
			Controller,
			Harvester,
			HookReceiver,
			Router,
			Weather,
			ZoneKeeper,
		},
		Core: {
			Conditioner,
			Router,
			ZoneKeeper,
		},
		Receiver: {
			Controller,
			Harvester,
			HookReceiver,
			Weather,
		},
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
	c, err := common.NewClientConn(a.cfg.HookReceiver.EventReceiverClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create new client connection; %w", err)
	}

	m, err := hookreceiver.New(a.cfg.HookReceiver, a.logger, iotv1proto.NewEventReceiverServiceClient(c))
	if err != nil {
		return nil, err
	}

	a.Server.HTTP.Handle("/alerts", http.HandlerFunc(m.Handler)).Methods(http.MethodPost)

	a.hookreceiver = m
	return m, nil
}

func (a *App) initWeather() (services.Service, error) {
	c, err := common.NewClientConn(a.cfg.HookReceiver.EventReceiverClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create new client connection; %w", err)
	}

	m, err := weather.New(a.cfg.Weather, a.logger, iotv1proto.NewEventReceiverServiceClient(c))
	if err != nil {
		return nil, err
	}

	a.weather = m
	return m, nil
}

/* func (a *App) initTimer() (services.Service, error) { */
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
/* 	return nil, nil */
/* } */

func (a *App) initMqttClient() (services.Service, error) {
	c, err := mqttclient.New(a.cfg.MQTTClient, a.logger)
	if err != nil {
		return nil, err
	}

	a.mqttclient = c
	return c, nil
}

func (a *App) initController() (services.Service, error) {
	c, err := controller.New(a.cfg.Controller, a.logger, a.logHandler)
	if err != nil {
		return nil, err
	}

	a.controller = c
	return c, nil
}

func (a *App) initRouter() (services.Service, error) {
	c, err := common.NewClientConn(a.cfg.Router.ZoneKeeperClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create new client connection; %w", err)
	}

	r, err := router.New(a.cfg.Router, a.logger, a.controller.Client(), iotv1proto.NewZoneKeeperServiceClient(c))
	if err != nil {
		return nil, err
	}

	iotv1proto.RegisterRouteServiceServer(a.Server.GRPC, r)

	a.router = r
	return r, nil
}

func (a *App) initZoneKeeper() (services.Service, error) {
	z, err := zonekeeper.New(a.cfg.ZoneKeeper, a.logger, a.mqttclient, a.controller.Client())
	if err != nil {
		return nil, err
	}

	iotv1proto.RegisterZoneKeeperServiceServer(a.Server.GRPC, z)

	a.zonekeeper = z
	return z, nil
}

func (a *App) initHarvester() (services.Service, error) {
	c, err := common.NewClientConn(a.cfg.Harvester.RouterClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create new client connection; %w", err)
	}

	m, err := harvester.New(a.cfg.Harvester, a.logger, iotv1proto.NewRouteServiceClient(c), a.mqttclient)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create harvester")
	}

	a.harvester = m
	return m, nil
}

func (a *App) initConditioner() (services.Service, error) {
	c, err := common.NewClientConn(a.cfg.Conditioner.ZoneKeeperClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create new client connection; %w", err)
	}

	m, err := conditioner.New(a.cfg.Conditioner, a.logger, iotv1proto.NewZoneKeeperServiceClient(c), a.controller.Client())
	if err != nil {
		return nil, fmt.Errorf("failed to create conditioner: %w", err)
	}

	iotv1proto.RegisterEventReceiverServiceServer(a.Server.GRPC, m)

	a.conditioner = m
	return m, nil
}

func (a *App) initServer() (services.Service, error) {
	a.cfg.Server.MetricsNamespace = metricsNamespace
	a.cfg.Server.ExcludeRequestInLog = false
	a.cfg.Server.RegisterInstrumentation = true
	a.cfg.Server.DisableRequestSuccessLog = false
	// a.cfg.Server.Log = a.logger

	a.cfg.Server.GRPCStreamMiddleware = []grpc.StreamServerInterceptor{
		otelgrpc.StreamServerInterceptor(),
	}

	a.cfg.Server.GRPCMiddleware = []grpc.UnaryServerInterceptor{
		otelgrpc.UnaryServerInterceptor(),
	}

	/* prometheus.MustRegister(&a.cfg) */

	if a.cfg.EnableGoRuntimeMetrics {
		// unregister default Go collector
		prometheus.Unregister(collectors.NewGoCollector())
		// register Go collector with all available runtime metrics
		prometheus.MustRegister(collectors.NewGoCollector(
			collectors.WithGoCollectorRuntimeMetrics(collectors.MetricsAll),
		))
	}

	/* DisableSignalHandling(&t.cfg.Server) */

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

			if errors.Is(err, context.Canceled) {
				return nil
			}

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
