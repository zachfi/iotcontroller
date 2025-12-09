// Package weather provides functionality to collect weather data and send sunrise/sunset events.
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

	"github.com/nathan-osman/go-sunrise"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	"github.com/zachfi/zkit/pkg/tracing"
)

var module = "weather"

type Epoch int

const (
	EventSunset Epoch = iota
	EventSunrise
)

var stateName = map[Epoch]string{
	EventSunset:  "sunset",
	EventSunrise: "sunrise",
}

func (e Epoch) String() string {
	if name, ok := stateName[e]; ok {
		return name
	}
	return "unknown"
}

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
	var (
		collectTicker = time.NewTicker(w.cfg.Interval)
		epochTicker   = time.NewTicker(time.Hour)
	)

	defer collectTicker.Stop()
	defer epochTicker.Stop()

	w.sendEpochEvents(ctx)
	w.Collect(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-collectTicker.C:
			w.Collect(ctx)
		case <-epochTicker.C:
			w.sendEpochEvents(context.Background())
		}
	}
}

func (w *Weather) sendEpochEvents(ctx context.Context) {
	var err error
	ctx, span := w.tracer.Start(ctx, "sendEpochEvents")
	defer tracing.ErrHandler(span, err, "sendEpochEvents", w.logger)

	if len(w.cfg.Locations) == 0 {
		w.logger.Warn("no locations configured, skipping sending sunrise/sunset events")
		span.AddEvent("no locations configured")
		return
	}

	for _, location := range w.cfg.Locations {
		rise, set := sunrise.SunriseSunset(
			location.Latitude,
			location.Longitude,
			time.Now().Year(),
			time.Now().Month(),
			time.Now().Day(),
		)

		for _, epoch := range []Epoch{EventSunset, EventSunrise} {
			var eventTime time.Time

			switch epoch {
			case EventSunset:
				eventTime = set
			case EventSunrise:
				eventTime = rise
			default:
				w.logger.Error("unknown epoch", "epoch", epoch)
				err = fmt.Errorf("unknown epoch: %v", epoch)
				span.RecordError(err)
				continue
			}

			e := &iotv1proto.EventRequest{
				Name: epoch.String(),
				Labels: map[string]string{
					iot.EpochLabel:    epoch.String(),
					iot.LocationLabel: location.Name,
					iot.WhenLabel:     eventTime.Format(time.RFC3339),
					iot.ValueLabel:    fmt.Sprintf("%d", eventTime.Unix()),
				},
			}

			_, err := w.eventReceiverClient.Event(ctx, e)
			if err != nil {
				w.logger.Error("failed to send event", "err", err)
			}
		}
	}
}
