package harvester

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/backoff"
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
		Help: "The the total number of messages seen by the harvester",
	}, []string{})
	harvesterMessageErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_message_error",
		Help: "The the total number of messages failed to process by the harvester",
	}, []string{})
	harvesterRouteSendErrorTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_route_send_error_total",
		Help: "The the total number of messages failed to route",
	}, []string{})
	harvesterRouteSendTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "iot_harvester_route_send_total",
		Help: "The the total number of messages routed",
	}, []string{})
)

type Harvester struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	routeClient iotv1proto.RouteServiceClient

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
	b := backoff.New(ctx, h.cfg.Backoff)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			stream, err := h.routeClient.Route(ctx)
			if err != nil {
				h.logger.Error("stream Route failed, will retry", "err", err)
				b.Wait()
				continue
			}
			b.Reset()

			err = h.run(ctx, stream)
			if err != nil {
				return fmt.Errorf("run failed: %w", err)
			}
		}
	}
}

// An error is returned only when we should exit.
func (h *Harvester) run(ctx context.Context, stream iotv1proto.RouteService_RouteClient) error {
	bgCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	msgCh := make(chan mqtt.Message, 1000)

	go h.receiver(ctx, cancel, stream, msgCh)

	var (
		onMessageReceived = h.messageFunc(bgCtx, msgCh)
		token             mqtt.Token
		timeout           = 30 * time.Second
		err               error
	)

	token = h.mqttClient.Unsubscribe()

	err = handleToken(token, "unsubscribe", timeout)
	if err != nil {
		return err
	}

	token = h.mqttClient.Subscribe(onMessageReceived)
	err = handleToken(token, "subscribe", timeout)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-stream.Context().Done():
			return nil
		case <-bgCtx.Done():
			return nil
		}
	}
}

func (h *Harvester) stopping(_ error) error {
	return nil
}

func (h *Harvester) receiver(ctx context.Context, cancel context.CancelFunc, stream iotv1proto.RouteService_RouteClient, msgChan chan mqtt.Message) {
	var routeErr error

	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stream.Context().Done():
			return
		case msg := <-msgChan:
			routeReq := &iotv1proto.RouteRequest{
				Path:    msg.Topic(),
				Message: msg.Payload(),
			}

			if routeErr = stream.Send(routeReq); routeErr != nil {
				if errors.Is(routeErr, io.EOF) {
					return
				}
				harvesterRouteSendErrorTotal.WithLabelValues().Inc()
				continue
			}
			harvesterRouteSendTotal.WithLabelValues().Inc()
		}
	}
}

func (h *Harvester) messageFunc(ctx context.Context, msgCh chan mqtt.Message) mqtt.MessageHandler {
	return func(_ mqtt.Client, msg mqtt.Message) {
		_, span := h.tracer.Start(
			ctx,
			"Harvester.messageFunc",
			trace.WithSpanKind(trace.SpanKindConsumer),
		)
		defer func() { _ = tracing.ErrHandler(span, nil, "harvester mqtt message failed", h.logger) }()

		harvesterMessageTotal.WithLabelValues().Inc()

		go func() {
			msgCh <- msg
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
