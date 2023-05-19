package controller

import (
	"context"

	"github.com/go-kit/log"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type Controller struct {
	services.Service

	cfg *Config

	logger log.Logger
	tracer trace.Tracer

	mgr manager.Manager
}

var scheme = runtime.NewScheme()

func New(cfg Config, logger log.Logger) (*Controller, error) {
	c := &Controller{
		cfg:    &cfg,
		logger: log.With(logger, "module", "controller"),
		tracer: otel.Tracer("controller"),
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	return c, nil
}

func (c *Controller) starting(ctx context.Context) error {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     c.cfg.MetricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: c.cfg.ProbeAddr,
		LeaderElection:         c.cfg.EnableLeaderElection,
		LeaderElectionID:       "cefdf353.iot",
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
		return errors.Wrap(err, "unable to start manager")
	}

	c.mgr = mgr

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
	return nil
}
