package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"

	"github.com/gorilla/mux"
	"github.com/grafana/dskit/grpcutil"
	"github.com/grafana/dskit/modules"
	"github.com/grafana/dskit/server"
	"github.com/grafana/dskit/services"
	"github.com/grafana/dskit/signals"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/prometheus/common/version"
	"google.golang.org/grpc/health/grpc_health_v1"

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
	appName          = "iotcontroller"
	metricsNamespace = "iot"
)

type App struct {
	cfg Config

	// ConfigDir   string
	// Data        netconfig.Data
	// Environment map[string]string

	Server *server.Server

	logger     *slog.Logger
	logHandler slog.Handler // Used to translate from slog to logr

	// Modules.
	controller  *controller.Controller
	harvester   *harvester.Harvester
	conditioner *conditioner.Conditioner
	// lights     *lights.Lights
	mqttclient   *mqttclient.MQTTClient
	kubeclient   client.Client
	weather      *weather.Weather
	zonekeeper   *zonekeeper.ZoneKeeper
	hookreceiver *hookreceiver.HookReceiver
	router       *router.Router

	// Service clients
	eventReceiverClient iotv1proto.EventReceiverServiceClient

	ModuleManager *modules.Manager
	serviceMap    map[string]services.Service
}

func New(cfg Config, logger *slog.Logger, logHandler slog.Handler) (*App, error) {
	a := &App{
		cfg:        cfg,
		logger:     logger,
		logHandler: logHandler,
	}

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

	a.Server.HTTP.Path("/ready").Handler(a.readyHandler(sm)).Methods(http.MethodGet)
	a.Server.HTTP.Path("/status").Handler(a.statusHandler()).Methods(http.MethodGet)
	a.Server.HTTP.Path("/status/{endpoint}").Handler(a.statusHandler()).Methods(http.MethodGet)

	grpc_health_v1.RegisterHealthServer(a.Server.GRPC,
		grpcutil.NewHealthCheck(sm),
	)

	// Listen for events from this manager, and log them.
	healthy := func() { a.logger.Info("started", "app", appName) }
	stopped := func() { a.logger.Info("stopped", "app", appName) }
	serviceFailed := func(service services.Service) {
		// if any service fails, stop everything
		sm.StopAsync()

		// let's find out which module failed
		for m, s := range serviceMap {
			if s == service {
				if errors.Is(service.FailureCase(), modules.ErrStopProcess) {
					a.logger.Info("received stop signal via return error", "module", m, "err", service.FailureCase())
				} else {
					a.logger.Error("module failed", "module", m, "err", service.FailureCase())
				}
				return
			}
		}

		a.logger.Error("module failed", "module", "unknown", "err", service.FailureCase())
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

func (a *App) readyHandler(sm *services.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if !sm.IsHealthy() {
			msg := bytes.Buffer{}
			msg.WriteString("Some services are not Running:\n")
			byState := sm.ServicesByState()
			for st, ls := range byState {
				msg.WriteString(fmt.Sprintf("%v: %d\n", st, len(ls)))
			}

			http.Error(w, msg.String(), http.StatusServiceUnavailable)
			return
		}

		// if t.ingester != nil {
		// 	if err := t.ingester.CheckReady(r.Context()); err != nil {
		// 		http.Error(w, "Ingester no ready: "+err.Error(), http.StatusServiceUnavailable)
		// 	}
		// }

		if a.mqttclient != nil {
			if err := a.mqttclient.CheckHealth(); err != nil {
				http.Error(w, "MQTTClient not ready: "+err.Error(), http.StatusServiceUnavailable)
			}
		}

		http.Error(w, "ready", http.StatusOK)
	}
}

func (a *App) statusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var errs []error
		msg := bytes.Buffer{}

		simpleEndpoints := map[string]func(io.Writer) error{
			"version":     a.writeStatusVersion,
			"endpoints":   a.writeStatusEndpoints,
			"services":    a.writeStatusServices,
			"mqttclient":  a.writeStatusMqttClient,
			"conditioner": a.writeStatusConditioner,
		}

		wrapStatus := func(endpoint string) {
			msg.WriteString("GET /status/" + endpoint + "\n")
			switch endpoint {
			case "config":
				err := a.writeStatusConfig(&msg, r)
				if err != nil {
					errs = append(errs, err)
				}
			default:
				err := simpleEndpoints[endpoint](&msg)
				if err != nil {
					errs = append(errs, err)
				}
			}
		}
		vars := mux.Vars(r)

		if endpoint, ok := vars["endpoint"]; ok {
			wrapStatus(endpoint)
		} else {
			sortedEndpoints := []string{"version", "endpoints", "services", "config"}

			if a.mqttclient != nil {
				sortedEndpoints = append(sortedEndpoints, "mqttclient")
			}

			if a.conditioner != nil {
				sortedEndpoints = append(sortedEndpoints, "conditioner")
			}

			for e := range sortedEndpoints {
				wrapStatus(sortedEndpoints[e])
			}
		}

		w.Header().Set("Content-Type", "text/plain")
		joinErrors := func(errs []error) error {
			if len(errs) == 0 {
				return nil
			}

			var err error
			for _, e := range errs {
				if e != nil {
					if err == nil {
						err = e
					} else {
						err = errors.Wrap(err, e.Error())
					}
				}
			}
			return err
		}

		err := joinErrors(errs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		if _, err := w.Write(msg.Bytes()); err != nil {
			a.logger.Error("error writing response", "err", err)
		}
	}
}

func (a *App) writeStatusEndpoints(w io.Writer) error {
	type endpoint struct {
		name    string
		regex   string
		methods []string
	}

	endpoints := []endpoint{}

	err := a.Server.HTTP.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		e := endpoint{}

		pathTemplate, err := route.GetPathTemplate()
		if err == nil {
			e.name = pathTemplate
		}

		pathRegexp, err := route.GetPathRegexp()
		if err == nil {
			e.regex = pathRegexp
		}

		methods, err := route.GetMethods()
		if err == nil {
			e.methods = methods
		}

		endpoints = append(endpoints, e)
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "err walking routes")
	}

	sort.Slice(endpoints[:], func(i, j int) bool {
		return endpoints[i].name < endpoints[j].name
	})

	x := table.NewWriter()
	x.SetOutputMirror(w)
	x.AppendHeader(table.Row{"name", "regex", "methods"})

	for _, e := range endpoints {
		x.AppendRows([]table.Row{
			{e.name, e.regex, e.methods},
		})
	}

	x.AppendSeparator()
	x.Render()

	return nil
}

func (a *App) writeStatusServices(w io.Writer) error {
	svcNames := make([]string, 0, len(a.serviceMap))
	for name := range a.serviceMap {
		svcNames = append(svcNames, name)
	}

	sort.Strings(svcNames)

	x := table.NewWriter()
	x.SetOutputMirror(w)
	x.AppendHeader(table.Row{"service name", "status", "failure case"})

	for _, name := range svcNames {
		service := a.serviceMap[name]

		var e string

		if err := service.FailureCase(); err != nil {
			e = err.Error()
		}

		x.AppendRows([]table.Row{
			{name, service.State(), e},
		})
	}

	x.AppendSeparator()
	x.Render()

	return nil
}

func (a *App) writeStatusMqttClient(w io.Writer) error {
	x := table.NewWriter()
	x.SetOutputMirror(w)
	x.AppendHeader(table.Row{"name", "status"})

	x.AppendRow(
		table.Row{
			"mqtt", a.mqttclient.CheckHealth(),
		},
	)

	x.AppendSeparator()
	x.Render()

	return nil
}

func (a *App) writeStatusConfig(w io.Writer, r *http.Request) error {
	var output any

	mode := r.URL.Query().Get("mode")
	switch mode {
	case "diff":
		defaultCfg := NewDefaultConfig()

		defaultCfgYaml, err := yamlMarshalUnmarshal(defaultCfg)
		if err != nil {
			return err
		}

		cfgYaml, err := yamlMarshalUnmarshal(a.cfg)
		if err != nil {
			return err
		}

		output, err = diffConfig(defaultCfgYaml, cfgYaml)
		if err != nil {
			return err
		}
	case "defaults":
		output = NewDefaultConfig()
	case "":
		output = a.cfg
	default:
		return fmt.Errorf("unknown value for mode query parameter: %v", mode)
	}

	out, err := yaml.Marshal(output)
	if err != nil {
		return err
	}

	_, err = w.Write([]byte("---\n"))
	if err != nil {
		return err
	}

	_, err = w.Write(out)
	if err != nil {
		return err
	}

	return nil
}

func (a *App) writeStatusConditioner(w io.Writer) error {
	x := table.NewWriter()
	x.SetOutputMirror(w)
	x.AppendHeader(table.Row{"name", "next", "scene", "state"})

	for _, s := range a.conditioner.Status() {
		x.AppendRow(
			table.Row{
				s.Name, s.Next, s.Scene, s.State,
			},
		)
	}

	x.AppendSeparator()
	x.Render()

	return nil
}

func (t *App) writeStatusVersion(w io.Writer) error {
	_, err := w.Write([]byte(version.Print(appName) + "\n"))
	if err != nil {
		return err
	}

	return nil
}
