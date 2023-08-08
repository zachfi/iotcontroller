package harvester

import (
	"context"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/pkg/iot"
	telemetryv1 "github.com/zachfi/iotcontroller/proto/telemetry/v1"
)

type Harvester struct {
	services.Service
	cfg *Config

	logger log.Logger
	tracer trace.Tracer

	telemetryClient telemetryv1.TelemetryServiceClient
	stream          telemetryv1.TelemetryService_TelemetryReportIOTDeviceClient

	mqttClient *mqttclient.MQTTClient
}

func New(cfg Config, logger log.Logger, conn *grpc.ClientConn, mqttClient *mqttclient.MQTTClient) (*Harvester, error) {
	h := &Harvester{
		cfg:             &cfg,
		logger:          log.With(logger, "module", "harvester"),
		tracer:          otel.Tracer("harvester"),
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
		_, span := h.tracer.Start(
			context.Background(),
			"Harvester.messageReceived",
			trace.WithSpanKind(trace.SpanKindClient),
		)
		defer span.End()

		topicPath, err := iot.ParseTopicPath(msg.Topic())
		if err != nil {
			_ = level.Error(h.logger).Log("err", errors.Wrap(err, "failed to parse topic path"))
			return
		}

		req := &telemetryv1.TelemetryReportIOTDeviceRequest{
			DeviceDiscovery: iot.ParseDiscoveryMessage(topicPath, msg),
		}

		if err := h.stream.Send(req); err != nil {
			_ = level.Error(h.logger).Log("err", err.Error())
		}
	}

	go func() {
		token := h.mqttClient.Client().Subscribe("#", 0, onMessageReceived)
		token.Wait()
		if token.Error() != nil {
			_ = level.Error(h.logger).Log("err", token.Error())
		}
	}()

	<-ctx.Done()

	return nil
}

func (h *Harvester) stopping(_ error) error {
	_, err := h.stream.CloseAndRecv()
	if err != nil {
		return err
	}
	return nil
}
