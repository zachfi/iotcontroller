package harvester

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/zachfi/zkit/pkg/boundedwaitgroup"
	"github.com/zachfi/zkit/pkg/tracing"

	"github.com/zachfi/iotcontroller/modules/mqttclient"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const module = "harvester"

type Harvester struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	routeClient iotv1proto.RouteServiceClient

	mqttClient *mqttclient.MQTTClient
	itemCh     chan item

	mu sync.Mutex
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

func (h *Harvester) starting(_ context.Context) error {
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
//
// Concurrency model: the receive loop reads items from itemCh as fast as it
// can and dispatches each to a goroutine bounded by cfg.Concurrency. This
// replaces an earlier single-goroutine consumer where any slow Send (gRPC
// redial during a controller-core restart, kube-apiserver TLS handshake
// storm, etc.) stalled every subsequent MQTT message — visible in traces
// as 1.7 s queue waits between Harvester.messageFunc and Harvester.receiver
// during cold start. With fan-out, a slow Send pins one worker while the
// other 15 keep draining.
//
// Ordering across topics is not preserved (workers race). Per-topic
// ordering was never strictly preserved either: messageFunc already
// dispatches each message to its own goroutine before the channel write,
// so two messages on the same topic could already arrive on itemCh out of
// order under load. In practice z2m emits per-device events serially with
// gaps measured in seconds, so the reorder window is well below
// observable.
func (h *Harvester) run(ctx context.Context) error {
	var (
		onMessageReceived = h.messageFunc()
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

	concurrency := h.cfg.Concurrency
	if concurrency == 0 {
		concurrency = 1
	}
	bg := boundedwaitgroup.New(concurrency)

	for {
		select {
		case <-ctx.Done():
			bg.Wait()
			return nil
		case i := <-h.itemCh:
			harvesterQueueDepth.Set(float64(len(h.itemCh)))

			bg.Add(1)
			harvesterActiveWorkers.Inc()
			go func(i item) {
				defer bg.Done()
				defer harvesterActiveWorkers.Dec()

				spanCtx, span := h.tracer.Start(i.ctx, "Harvester.receiver", trace.WithSpanKind(trace.SpanKindConsumer))

				req := &iotv1proto.SendRequest{
					Path:    i.Path,
					Message: i.Payload,
				}

				_, sendErr := h.routeClient.Send(spanCtx, req)
				if sendErr != nil {
					harvesterRouteSendErrorTotal.WithLabelValues().Inc()
					h.logger.Error("failed to send message to route server", "err", sendErr, "topic", i.Path)
				}
				tracing.ErrHandler(span, sendErr, "harvester receiver failed", h.logger)
				harvesterRouteSendTotal.WithLabelValues().Inc()
			}(i)
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

		harvesterMessageTotal.WithLabelValues(msg.Topic()).Inc()

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
