package harvester

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/grafana/dskit/services"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"github.com/zachfi/zkit/pkg/tracing"

	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	telemetryv1proto "github.com/zachfi/iotcontroller/proto/telemetry/v1"
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

	telemetryClient telemetryv1proto.TelemetryServiceClient
	routeClient     iotv1proto.RouteServiceClient
	reportStream    telemetryv1proto.TelemetryService_TelemetryReportIOTDeviceClient
	routeStream     iotv1proto.RouteService_RouteClient

	mqttClient *mqttclient.MQTTClient
}

func New(cfg Config, logger *slog.Logger, conn *grpc.ClientConn, mqttClient *mqttclient.MQTTClient) (*Harvester, error) {
	h := &Harvester{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		telemetryClient: telemetryv1proto.NewTelemetryServiceClient(conn),
		routeClient:     iotv1proto.NewRouteServiceClient(conn),
		mqttClient:      mqttClient,
	}

	h.Service = services.NewBasicService(h.starting, h.running, h.stopping)

	return h, nil
}

func (h *Harvester) starting(ctx context.Context) error {
	// TODO: when we are starting with All target, we don't want to connect to
	// the k8s service because the service is not yet available.  Perhaps split
	// this out into various targets, OR, in All mode connect to only the local
	// bind address and not the k8s service.

	reportStream, err := h.telemetryClient.TelemetryReportIOTDevice(ctx)
	if err != nil {
		return err
	}

	h.reportStream = reportStream

	routeStream, err := h.routeClient.Route(ctx)
	if err != nil {
		return err
	}

	h.routeStream = routeStream

	return nil
}

func (h *Harvester) messageFunc(_ context.Context) mqtt.MessageHandler {
	return func(_ mqtt.Client, msg mqtt.Message) {
		var err error
		_, span := h.tracer.Start(
			context.Background(),
			"Harvester.messageFunc",
			trace.WithSpanKind(trace.SpanKindClient),
		)
		defer func() { _ = tracing.ErrHandler(span, err, "harvester mqtt message failed", h.logger) }()

		harvesterMessageTotal.WithLabelValues().Inc()

		var topicPath iot.TopicPath
		topicPath, err = iot.ParseTopicPath(msg.Topic())
		if err != nil {
			harvesterMessageErrors.WithLabelValues().Inc()
			h.logger.Error("msg", "failed to parse topic path", "err", err)
			return
		}

		wg := sync.WaitGroup{}

		wg.Add(1)
		go func() {
			defer wg.Done()

			deviceReq := &telemetryv1proto.TelemetryReportIOTDeviceRequest{
				DeviceDiscovery: iot.ParseDiscoveryMessage(topicPath, msg),
			}

			deviceErr := h.reportStream.Send(deviceReq)
			if deviceErr != nil {
				harvesterMessageErrors.WithLabelValues().Inc()
				h.logger.Error("failed to send on reportStream", "err", deviceErr)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()

			routeReq := &iotv1proto.RouteRequest{
				Path:    msg.Topic(),
				Message: msg.Payload(),
			}

			routeErr := h.routeStream.Send(routeReq)
			if routeErr != nil {
				harvesterMessageErrors.WithLabelValues().Inc()
				h.logger.Error("failed to send on routeStream", "err", routeErr)
			}
		}()

		wg.Wait()
	}
}

func (h *Harvester) running(ctx context.Context) error {
	onMessageReceived := h.messageFunc(ctx)

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
	var (
		err  error
		errs []error
	)
	if h.reportStream != nil {
		_, err = h.reportStream.CloseAndRecv()
		if err != nil {
			errs = append(errs, err)
		}
	}

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
