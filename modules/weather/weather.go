package weather

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/grafana/dskit/services"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/zkit/pkg/boundedwaitgroup"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

var module = "weather"

const (
	EventSunset  = "sunset"
	EventSunrise = "sunrise"
)

type Weather struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	owmClient *http.Client

	eventReceiverClient iotv1proto.EventReceiverServiceClient
}

func New(cfg Config, logger *slog.Logger, eventReceiverClient iotv1proto.EventReceiverServiceClient) (*Weather, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("unable to create new %q with empty cfg.apikey", module)
	}

	if len(cfg.Locations) == 0 {
		return nil, fmt.Errorf("unable to create new %q with empty cfg.locations", module)
	}

	w := &Weather{
		cfg:                 &cfg,
		logger:              logger.With("module", module),
		tracer:              otel.Tracer(module),
		eventReceiverClient: eventReceiverClient,

		owmClient: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}

	w.Service = services.NewBasicService(w.starting, w.running, w.stopping)
	return w, nil
}

func (w *Weather) starting(_ context.Context) error {
	return nil
}

func (w *Weather) stopping(_ error) error {
	return nil
}

func (w *Weather) running(ctx context.Context) error {
	collectTicker := time.NewTicker(w.cfg.Interval)

	bg := boundedwaitgroup.New(3)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-collectTicker.C:
			bg.Add(1)
			go func() {
				defer bg.Done()
				w.Collect(ctx)
			}()

			bg.Wait()
		}
	}
}
