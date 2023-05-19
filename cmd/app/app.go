package app

import (
	"context"
	"fmt"
	"os"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/modules"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"github.com/weaveworks/common/server"
	"github.com/weaveworks/common/signals"
	"github.com/zachfi/iotcontroller/modules/controller"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
)

const (
	appName          = "iotcontroller"
	metricsNamespace = "iot"
)

type App struct {
	cfg Config

	// ConfigDir   string
	// Data        netconfig.Data
	// Environment map[string]string

	Server *server.Server

	logger log.Logger

	// Modules.
	controller *controller.Controller
	mqttclient *mqttclient.MQTTClient
	// timer     *timer.Timer
	//
	// inventory *inventory.Server
	// lights    *lights.Lights

	ModuleManager *modules.Manager
	serviceMap    map[string]services.Service
}

func New(cfg Config) (*App, error) {
	a := &App{
		cfg: cfg,
	}

	a.logger = log.With(log.NewLogfmtLogger(os.Stdout), "app", appName)

	if a.cfg.Target == "" {
		a.cfg.Target = All
	}

	if err := a.setupModuleManager(); err != nil {
		return nil, errors.Wrap(err, "failed to setup module manager")
	}

	return a, nil
}

func (a *App) Run() error {
	serviceMap, err := a.ModuleManager.InitModuleServices(a.cfg.Target)
	if err != nil {
		return fmt.Errorf("failed to init module services %w", err)
	}
	a.serviceMap = serviceMap

	servs := []services.Service(nil)
	for _, s := range serviceMap {
		servs = append(servs, s)
	}

	sm, err := services.NewManager(servs...)
	if err != nil {
		return fmt.Errorf("failed to start service manager %w", err)
	}

	// Listen for events from this manager, and log them.
	healthy := func() { _ = level.Info(a.logger).Log("msg", "started") }
	stopped := func() { _ = level.Info(a.logger).Log("msg", "stopped") }
	serviceFailed := func(service services.Service) {
		// if any service fails, stop everything
		sm.StopAsync()

		// let's find out which module failed
		for m, s := range serviceMap {
			if s == service {
				if service.FailureCase() == modules.ErrStopProcess {
					_ = level.Info(a.logger).Log("msg", "received stop signal via return error", "module", m, "err", service.FailureCase())
				} else {
					_ = level.Error(a.logger).Log("msg", "module failed", "module", m, "err", service.FailureCase())
				}
				return
			}
		}

		_ = level.Error(a.logger).Log("msg", "module failed", "module", "unknown", "err", service.FailureCase())
	}
	sm.AddListener(services.NewManagerListener(healthy, stopped, serviceFailed))

	// Setup signal handler. If signal arrives, we stop the manager, which stops all the services.
	handler := signals.NewHandler(a.Server.Log)
	go func() {
		handler.Loop()
		sm.StopAsync()
	}()

	// Start all services. This can really only fail if some service is already
	// in other state than New, which should not be the case.
	err = sm.StartAsync(context.Background())
	if err != nil {
		return fmt.Errorf("failed to start service manager %w", err)
	}

	return sm.AwaitStopped(context.Background())
}
