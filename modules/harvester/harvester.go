package harvester

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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

	mu sync.RWMutex
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

	// TODO: wait for the mqtt client to be haelthy before proceeding
	// h.mqttClient.CheckHealth()

	return nil
}

func (h *Harvester) running(ctx context.Context) error {
	var (
		err    error
		stream iotv1proto.RouteService_RouteClient
	)

	for {
		if err = ctx.Err(); err != nil {
			return nil
		}

		stream, err = h.routeClient.Route(ctx)
		if err != nil {
			h.logger.Error("stream Route failed, will retry", "err", err)
			continue
		}

		h.mu.Lock()
		h.routeStream = stream
		h.mu.Unlock()

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
	)

	token = h.mqttClient.Client().Unsubscribe("#")

	err = handleToken(token, "unsubscribe on #", timeout)
	if err != nil {
		return err
	}

	token = h.mqttClient.Client().Subscribe("#", 0, onMessageReceived)
	err = handleToken(token, "subscribe on #", timeout)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-h.routeStream.Context().Done():
			err = h.routeStream.CloseSend()
			if err != nil {
				return fmt.Errorf("failed to close route stream: %w", err)
			}
		}
	}
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

			h.mu.RLock()
			defer h.mu.RUnlock()
			if routeErr := h.routeStream.Send(routeReq); routeErr != nil {
				harvesterMessageErrors.WithLabelValues().Inc()

				h.logger.Error("failed to send on routeStream", "err", routeErr)
			}
		}()
	}
}

func handleToken(token mqtt.Token, msg string, timeout time.Duration) error {
	if !token.WaitTimeout(timeout) {
		return fmt.Errorf("%s failed", msg)
	}

	<-token.Done()
	if err := token.Error(); err != nil {
		return fmt.Errorf("%s failed: %w", msg, err)
	}

	return nil
}
