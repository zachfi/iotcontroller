package harvester

import (
	"context"
	"errors"
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/services"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/zkit/pkg/tracing"

	"github.com/zachfi/iotcontroller/modules/mqttclient"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const module = "harvester"

var (
	harvesterMessageTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_message_total",
		Help: "The the total number of messages processed by the harvester",
	}, []string{})
	harvesterMessageErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_message_error",
		Help: "The the total number of messages failed to process by the harvester",
	}, []string{})
)

type Harvester struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	routeClient iotv1proto.RouteServiceClient
	routeStream iotv1proto.RouteService_RouteClient

	mqttClient *mqttclient.MQTTClient
}

func New(cfg Config, logger *slog.Logger, routeClient iotv1proto.RouteServiceClient, mqttClient *mqttclient.MQTTClient) (*Harvester, error) {
	h := &Harvester{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		routeClient: routeClient,
		mqttClient:  mqttClient,
	}

	h.Service = services.NewBasicService(h.starting, h.running, h.stopping)

	return h, nil
}

func (h *Harvester) starting(ctx context.Context) error {
	// TODO: when we are starting with All target, we don't want to connect to
	// the k8s service because the service is not yet available.  Perhaps split
	// this out into various targets, OR, in All mode connect to only the local
	// bind address and not the k8s service.
	/* if h.cfg.Target == All { } */

	routeStream, err := h.routeClient.Route(ctx)
	if err != nil {
		return err
	}

	h.routeStream = routeStream

	return nil
}

func (h *Harvester) running(ctx context.Context) error {
	var err error

	for {
		err = h.run(ctx)
		if err != nil {
			return err
		}
	}
}

// An error is returned only when we should exit.
func (h *Harvester) run(ctx context.Context) error {
	var (
		onMessageReceived = h.messageFunc(ctx)
		token             mqtt.Token
		timeout           = 30 * time.Second
		err               error
		routeStream       iotv1proto.RouteService_RouteClient
	)
	token = h.mqttClient.Client().Subscribe("#", 0, onMessageReceived)

	if !token.WaitTimeout(timeout) {
		h.logger.Error("failed to subscribe to topic #")
		return nil
	}

	<-token.Done()
	if token.Error() != nil {
		h.logger.Error("token failed on topic #", "err", err)
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.routeStream.Context().Done():

		err = h.routeStream.CloseSend()
		if err != nil {
			h.logger.Error("failed to close route stream", "err", err)
			return nil
		}

		h.logger.Info("route stream closed, reconnecting")
		routeStream, err = h.routeClient.Route(ctx)
		if err != nil {
			h.logger.Error("route stream failed", "err", err)
			return nil
		}

		h.routeStream = routeStream
	}

	return nil
}

func (h *Harvester) stopping(_ error) error {
	var (
		err  error
		errs []error
	)

	if h.routeStream != nil {
		_, err = h.routeStream.CloseAndRecv()
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (h *Harvester) messageFunc(_ context.Context) mqtt.MessageHandler {
	return func(_ mqtt.Client, msg mqtt.Message) {
		var err error
		_, span := h.tracer.Start(
			context.Background(),
			"Harvester.messageFunc",
			trace.WithSpanKind(trace.SpanKindConsumer),
		)
		defer func() { _ = tracing.ErrHandler(span, err, "harvester mqtt message failed", h.logger) }()

		harvesterMessageTotal.WithLabelValues().Inc()

		go func() {
			routeReq := &iotv1proto.RouteRequest{
				Path:    msg.Topic(),
				Message: msg.Payload(),
			}

			if routeErr := h.routeStream.Send(routeReq); routeErr != nil {
				harvesterMessageErrors.WithLabelValues().Inc()

				h.logger.Error("failed to send on routeStream", "err", routeErr)
			}
		}()
	}
}
