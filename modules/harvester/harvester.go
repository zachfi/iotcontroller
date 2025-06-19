package harvester

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/services"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
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
	itemCh     chan item

	mu sync.RWMutex
}

type item struct {
	ctx     context.Context
	Path    string
	Payload []byte
}

func New(cfg Config, logger *slog.Logger, routeClient iotv1proto.RouteServiceClient, mqttClient *mqttclient.MQTTClient) (*Harvester, error) {
	h := &Harvester{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		routeClient: routeClient,
		mqttClient:  mqttClient,
		itemCh:      make(chan item, 1000),
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
	var err error
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			err = h.run(ctx)
			if err != nil {
				return fmt.Errorf("run failed: %w", err)
			}
		}
	}
}

// An error is returned only when we should exit.
func (h *Harvester) run(ctx context.Context) error {
	var (
		onMessageReceived = h.messageFunc()
		token             mqtt.Token
		timeout           = 30 * time.Second
		err               error

		spanCtx context.Context
		span    trace.Span
		req     *iotv1proto.SendRequest
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
		case i := <-h.itemCh:
			spanCtx, span = h.tracer.Start(i.ctx, "Harvester.receiver", trace.WithSpanKind(trace.SpanKindConsumer))

			req = &iotv1proto.SendRequest{
				Path:    i.Path,
				Message: i.Payload,
			}

			if _, err = h.routeClient.Send(spanCtx, req); err != nil {
				harvesterRouteSendErrorTotal.WithLabelValues().Inc()
				h.logger.Error("failed to send message to route server", "err", err, "topic", i.Path)
			}
			tracing.ErrHandler(span, err, "harvester receiver failed", h.logger)
			harvesterRouteSendTotal.WithLabelValues().Inc()
		}
	}
}

func (h *Harvester) stopping(_ error) error {
	close(h.itemCh)
	return nil
}

func (h *Harvester) messageFunc() mqtt.MessageHandler {
	return func(_ mqtt.Client, msg mqtt.Message) {
		ctx, span := h.tracer.Start(
			context.Background(),
			"Harvester.messageFunc",
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(attribute.String(string(semconv.PeerServiceKey), "mqtt")),
		)
		defer func() { _ = tracing.ErrHandler(span, nil, "harvester mqtt message failed", h.logger) }()

		harvesterMessageTotal.WithLabelValues().Inc()

		go func() {
			h.itemCh <- item{
				ctx:     ctx,
				Path:    msg.Topic(),
				Payload: msg.Payload(),
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
