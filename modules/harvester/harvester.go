package harvester

import (
	"context"
	"log/slog"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/pkg/iot"
	telemetryv1 "github.com/zachfi/iotcontroller/proto/telemetry/v1"
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

	telemetryClient telemetryv1.TelemetryServiceClient
	stream          telemetryv1.TelemetryService_TelemetryReportIOTDeviceClient

	mqttClient *mqttclient.MQTTClient
}

func New(cfg Config, logger *slog.Logger, conn *grpc.ClientConn, mqttClient *mqttclient.MQTTClient) (*Harvester, error) {
	h := &Harvester{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		telemetryClient: telemetryv1.NewTelemetryServiceClient(conn),
		mqttClient:      mqttClient,
	}

	h.Service = services.NewBasicService(h.starting, h.running, h.stopping)

	return h, nil
}

func (h *Harvester) starting(ctx context.Context) error {
	stream, err := h.telemetryClient.TelemetryReportIOTDevice(ctx)
	if err != nil {
		return err
	}

	h.stream = stream

	return nil
}

func (h *Harvester) running(ctx context.Context) error {
	var onMessageReceived mqtt.MessageHandler = func(_ mqtt.Client, msg mqtt.Message) {
		var err error
		messageCtx, span := h.tracer.Start(
			ctx,
			"Harvester.messageReceived",
			trace.WithSpanKind(trace.SpanKindClient),
		)

		defer func() { handleErr(span, err) }()

		harvesterMessageTotal.WithLabelValues().Inc()

		var topicPath iot.TopicPath
		topicPath, err = iot.ParseTopicPath(msg.Topic())
		if err != nil {
			harvesterMessageErrors.WithLabelValues().Inc()
			h.logger.Error("err", errors.Wrap(err, "failed to parse topic path"))
			return
		}

		req := &telemetryv1.TelemetryReportIOTDeviceRequest{
			DeviceDiscovery: iot.ParseDiscoveryMessage(topicPath, msg),
		}

		_, streamSpan := h.tracer.Start(messageCtx, "stream.Send", trace.WithSpanKind(trace.SpanKindClient))
		err = h.stream.Send(req)
		if err != nil {
			harvesterMessageErrors.WithLabelValues().Inc()
			h.logger.Error("failed to send on stream", "err", err.Error())
		}
		handleErr(streamSpan, err)
		handleErr(span, err)
	}

	go func() {
		token := h.mqttClient.Client().Subscribe("#", 0, onMessageReceived)
		token.Wait()
		if token.Error() != nil {
			h.logger.Error("subscribe error", "err", token.Error())
		}
	}()

	<-ctx.Done()

	return nil
}

func (h *Harvester) stopping(_ error) error {
	_, err := h.stream.CloseAndRecv()
	return err
}

func handleErr(span trace.Span, err error) {
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "ok")
	}
	span.End()
}
