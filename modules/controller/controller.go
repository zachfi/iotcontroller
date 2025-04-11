package controller

import (
	"context"
	"log/slog"

	"github.com/go-logr/logr"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	iotv1 "github.com/zachfi/iotcontroller/api/v1"
	controller "github.com/zachfi/iotcontroller/internal/controller"
)

type Controller struct {
	services.Service

	cfg *Config

	logger     *slog.Logger
	logHandler slog.Handler
	tracer     trace.Tracer

	mgr manager.Manager
}

var scheme = runtime.NewScheme() // setupLog = ctrl.Log.WithName("setup")

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(iotv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func New(cfg Config, logger *slog.Logger, logHandler slog.Handler) (*Controller, error) {
	c := &Controller{
		cfg:        &cfg,
		logger:     logger.With("module", "controller"),
		logHandler: logHandler,
		tracer:     otel.Tracer("controller"),
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	kfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, err
	}

	mgr, err := ctrl.NewManager(kfg, ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: c.cfg.ProbeAddr,
		LeaderElection:         c.cfg.EnableLeaderElection,
		LeaderElectionID:       "cefdf353.iot",
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				c.cfg.Namespace: {},
			},
		},
		Metrics: server.Options{
			BindAddress: c.cfg.MetricsAddr,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port: 9443,
		}),
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return nil, errors.Wrap(err, "unable to start manager")
	}

	c.mgr = mgr

	return c, nil
}

func (c *Controller) starting(_ context.Context) error {
	var err error

	if c.logHandler != nil {
		ctrlruntimelog.SetLogger(logr.FromSlogHandler(c.logHandler))
	}

	deviceController := &controller.DeviceReconciler{
		Client: c.mgr.GetClient(),
		Scheme: c.mgr.GetScheme(),
	}
	deviceController.SetLogger(slog.With(c.logger, "reconciler", "device"))
	deviceController.SetTracer(c.tracer)

	if err = deviceController.SetupWithManager(c.mgr); err != nil {
		return errors.Wrap(err, "unable to create Device controller")
	}

	zoneController := &controller.ZoneReconciler{
		Client: c.mgr.GetClient(),
		Scheme: c.mgr.GetScheme(),
	}
	zoneController.SetLogger(slog.With(c.logger, "reconciler", "zone"))
	zoneController.SetTracer(c.tracer)

	if err = zoneController.SetupWithManager(c.mgr); err != nil {
		return errors.Wrap(err, "unable to create Zone controller")
	}

	return nil
}

func (c *Controller) running(ctx context.Context) error {
	if err := c.mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return errors.Wrap(err, "unable to set up health check")
	}

	if err := c.mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return errors.Wrap(err, "unable to set up ready check")
	}

	err := c.mgr.Start(ctx)
	if err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

func (c *Controller) stopping(_ error) error {
	c.mgr.GetCache().WaitForCacheSync(context.Background())
	return nil
}
