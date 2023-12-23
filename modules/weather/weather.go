package weather

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/grafana/dskit/services"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

var module = "weather"

type Weather struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	owmClient *http.Client

	alertReceiverClient iotv1proto.AlertReceiverServiceClient
}

func New(cfg Config, logger *slog.Logger, conn *grpc.ClientConn) (*Weather, error) {
	w := &Weather{
		cfg:                 &cfg,
		logger:              logger.With("module", module),
		tracer:              otel.Tracer(module),
		alertReceiverClient: iotv1proto.NewAlertReceiverServiceClient(conn),

		owmClient: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}

	w.Service = services.NewIdleService(w.starting, w.stopping)
	/* w.Service = services.NewBasicService(w.starting, w.running, w.stopping) */
	return w, nil
}

func (w *Weather) starting(ctx context.Context) error {
	go w.run(ctx)
	return nil
}

/* func (w *Weather) running(ctx context.Context) error { */
/* 	<-ctx.Done() */
/* 	return nil */
/* } */

func (w *Weather) stopping(_ error) error {
	return nil
}

func (w *Weather) run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.Interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Collect(ctx)
		}
	}
}
