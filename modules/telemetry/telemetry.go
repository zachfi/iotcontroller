package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zachfi/zkit/pkg/boundedwaitgroup"
	"github.com/zachfi/zkit/pkg/tracing"

	"github.com/zachfi/iotcontroller/pkg/iot"
	iotutil "github.com/zachfi/iotcontroller/pkg/iot/util"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	telemetryv1proto "github.com/zachfi/iotcontroller/proto/telemetry/v1"
)

const (
	namespace = "iot"
	module    = "telemetry"
)

type Telemetry struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	keeper thingKeeper

	zonekeeperClient iotv1proto.ZoneKeeperServiceClient

	seenThings map[string]time.Time

	reportQueue chan *telemetryv1proto.TelemetryReportIOTDeviceRequest

	kubeclient kubeclient.Client
}

type thingKeeper map[string]map[string]string

func New(cfg Config, logger *slog.Logger, kubeclient kubeclient.Client, conn *grpc.ClientConn) (*Telemetry, error) {
	s := &Telemetry{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		kubeclient:       kubeclient,
		zonekeeperClient: iotv1proto.NewZoneKeeperServiceClient(conn),

		reportQueue: make(chan *telemetryv1proto.TelemetryReportIOTDeviceRequest, 10000),
		keeper:      make(thingKeeper),
		seenThings:  make(map[string]time.Time),
	}

	// go func(s *Telemetry) {
	// 	for {
	// 		// Make a copy
	// 		tMap := make(map[string]time.Time)
	// 		for k, v := range s.seenThings {
	// 			tMap[k] = v
	// 		}
	//
	// 		// Expire the old entries
	// 		for k, v := range tMap {
	// 			if time.Since(v) > defaultExpiry {
	// 				_ = level.Info(s.logger).Log("expiring",
	// 					"device", k,
	// 				)
	//
	// 				airHeatindex.Delete(prometheus.Labels{"device": k})
	// 				airHumidity.Delete(prometheus.Labels{"device": k})
	// 				airTemperature.Delete(prometheus.Labels{"device": k})
	// 				thingWireless.Delete(prometheus.Labels{"device": k})
	// 				waterTemperature.Delete(prometheus.Labels{"device": k})
	//
	// 				delete(s.seenThings, k)
	// 				delete(s.keeper, k)
	// 			}
	// 		}
	//
	// 		time.Sleep(30 * time.Second)
	// 	}
	// }(s)

	s.Service = services.NewBasicService(s.starting, s.running, s.stopping)

	return s, nil
}

func (l *Telemetry) starting(ctx context.Context) error {
	go l.reportReceiver(ctx)
	return nil
}

func (l *Telemetry) running(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (l *Telemetry) stopping(_ error) error {
	return nil
}

// storeThingLabel records the received key/value pair for the given node ID.
func (l *Telemetry) storeThingLabel(nodeID string, key, value string) {
	if len(l.keeper) == 0 {
		l.keeper = make(thingKeeper)
	}

	if _, ok := l.keeper[nodeID]; !ok {
		l.keeper[nodeID] = make(map[string]string)
	}

	if key != "" && value != "" {
		l.keeper[nodeID][key] = value
	}
}

func (l *Telemetry) nodeLabels(nodeID string) map[string]string {
	if nodeLabelMap, ok := l.keeper[nodeID]; ok {
		return nodeLabelMap
	}

	return map[string]string{}
}

// hasLabels checks to see if the keeper has all of the received labels for the given node ID.
func (l *Telemetry) hasLabels(nodeID string, labels []string) bool {
	nodeLabels := l.nodeLabels(nodeID)

	nodeHasLabel := func(nodeLabels map[string]string, label string) bool {
		for key := range nodeLabels {
			if key == label {
				return true
			}
		}

		return false
	}

	for _, label := range labels {
		if !nodeHasLabel(nodeLabels, label) {
			return false
		}
	}

	return true
}

func (l *Telemetry) reportReceiver(controlCtx context.Context) {
	var (
		req *telemetryv1proto.TelemetryReportIOTDeviceRequest
		bg  = boundedwaitgroup.New(l.cfg.ReportConcurrency)
	)

	for {
		select {
		case <-controlCtx.Done():
			return
		case req = <-l.reportQueue:

			bg.Add(1)
			go func() {
				bg.Done()

				var err error
				ctx, span := l.tracer.Start(
					context.Background(),
					"Telemetry.ReportIOTDevice",
					trace.WithSpanKind(trace.SpanKindServer),
				)
				defer tracing.ErrHandler(span, err, "report failed", l.logger)

				if req.DeviceDiscovery.ObjectId != "" {
					telemetryIOTReport.WithLabelValues(req.DeviceDiscovery.ObjectId, req.DeviceDiscovery.Component).Inc()
				}

				span.SetAttributes(
					attribute.String("component", req.DeviceDiscovery.Component),
					attribute.String("node_id", req.DeviceDiscovery.NodeId),
					attribute.String("object_id", req.DeviceDiscovery.ObjectId),
					attribute.StringSlice("endpoints", req.DeviceDiscovery.Endpoints),
				)

				switch req.DeviceDiscovery.Component {
				case "zigbee2mqtt":
					err = l.handleZigbeeReport(ctx, req)
					_ = tracing.ErrHandler(span, err, "failed to handle zigbee report", l.logger)
					return
				}

				switch req.DeviceDiscovery.ObjectId {
				case "wifi":
					err = l.handleWifiReport(ctx, req)
					_ = tracing.ErrHandler(span, err, "failed to handle wifi report", l.logger)

				case "air":
					err = l.handleAirReport(ctx, req)
					_ = tracing.ErrHandler(span, err, "failed to handle air report", l.logger)

				case "water":
					err = l.handleWaterReport(ctx, req)
					_ = tracing.ErrHandler(span, err, "failed to handle water report", l.logger)

				case "led1", "led2":
					err = l.handleLEDReport(ctx, req)
					_ = tracing.ErrHandler(span, err, "failed to handle led report", l.logger)

				default:
					telemetryIOTUnhandledReport.WithLabelValues(req.DeviceDiscovery.ObjectId, req.DeviceDiscovery.Component).Inc()
				}
			}()
		}
	}
}

func (l *Telemetry) TelemetryReportIOTDevice(stream telemetryv1proto.TelemetryService_TelemetryReportIOTDeviceServer) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// Close the connection and return the response to the client
			return stream.SendAndClose(&telemetryv1proto.TelemetryReportIOTDeviceResponse{})
		}

		if errors.Is(err, context.Canceled) {
			return nil
		}

		if err != nil {
			l.logger.Error("stream error", "err", err)
			return err
		}

		l.reportQueue <- req
		queueLength.With(prometheus.Labels{}).Set(float64(len(l.reportQueue)))
	}
}

func (l *Telemetry) handleZigbeeReport(ctx context.Context, req *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
	if req == nil {
		return fmt.Errorf("unable to read zigbee report from nil request")
	}

	ctx, span := l.tracer.Start(ctx, "handleZigbeeReport")
	defer span.End()

	name := strings.ToLower(req.DeviceDiscovery.ObjectId)

	device, err := iotutil.GetOrCreateAPIDevice(ctx, l.kubeclient, name)
	if err != nil {
		return tracing.ErrHandler(span, err, "failed to get or create API device", l.logger)
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		iotutil.UpdateLastSeen(ctx, l.kubeclient, device)
	}()

	wg.Wait()

	return nil
}

// TODO: move to router
func (l *Telemetry) handleLEDReport(ctx context.Context, request *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
	if request == nil {
		return fmt.Errorf("unable to read led report from nil request")
	}

	_, span := l.tracer.Start(ctx, "Telemetry.handleLEDReport")
	defer span.End()

	discovery := request.DeviceDiscovery

	msg, err := iot.ReadMessage("led", discovery.Message, discovery.Endpoints...)
	if err != nil {
		return fmt.Errorf("failed to read message: %w", err)
	}

	if msg != nil {
		m := msg.(iot.LEDConfig)

		for i, deviceConnection := range m.Device.Connections {
			if len(deviceConnection) == 2 {
				l.storeThingLabel(discovery.NodeId, "mac", m.Device.Connections[i][1])
			}
		}
	}

	return nil
}

// TODO: move to router
func (l *Telemetry) handleWaterReport(_ context.Context, request *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
	if request == nil {
		return fmt.Errorf("unable to read water report from nil request")
	}

	discovery := request.DeviceDiscovery

	msg, err := iot.ReadMessage("water", discovery.Message, discovery.Endpoints...)
	if err != nil {
		return err
	}

	if msg != nil {
		m := msg.(iot.WaterMessage)

		if m.Temperature != nil {
			waterTemperature.WithLabelValues(discovery.NodeId).Set(float64(*m.Temperature))
		}
	}

	return nil
}

// TODO: move to router
func (l *Telemetry) handleAirReport(_ context.Context, request *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
	if request == nil {
		return fmt.Errorf("unable to read air report from nil request")
	}

	discovery := request.DeviceDiscovery

	msg, err := iot.ReadMessage("air", discovery.Message, discovery.Endpoints...)
	if err != nil {
		return err
	}

	if msg != nil {
		m := msg.(iot.AirMessage)

		// l.storeThingLabel(discovery.NodeId, "tempcoef", m.TempCoef)

		if m.Temperature != nil {
			airTemperature.WithLabelValues(discovery.NodeId).Set(float64(*m.Temperature))
		}

		if m.Humidity != nil {
			airHumidity.WithLabelValues(discovery.NodeId).Set(float64(*m.Humidity))
		}

		if m.HeatIndex != nil {
			airHeatindex.WithLabelValues(discovery.NodeId).Set(float64(*m.HeatIndex))
		}
	}

	return nil
}

// TODO: move to router
func (l *Telemetry) handleWifiReport(_ context.Context, request *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
	if request == nil {
		return fmt.Errorf("unable to read wifi report from nil request")
	}

	discovery := request.DeviceDiscovery

	msg, err := iot.ReadMessage("wifi", discovery.Message, discovery.Endpoints...)
	if err != nil {
		return err
	}

	if msg != nil {
		m := msg.(iot.WifiMessage)

		l.storeThingLabel(discovery.NodeId, "ssid", m.SSID)
		l.storeThingLabel(discovery.NodeId, "bssid", m.BSSID)
		l.storeThingLabel(discovery.NodeId, "ip", m.IP)

		labels := l.nodeLabels(discovery.NodeId)

		if l.hasLabels(discovery.NodeId, []string{"ssid", "bssid", "ip"}) {
			if m.RSSI != 0 {
				thingWireless.With(prometheus.Labels{
					"device": discovery.NodeId,
					"ssid":   labels["ssid"],
					"bssid":  labels["ssid"],
					"ip":     labels["ip"],
				}).Set(float64(m.RSSI))
			}
		}
	}

	return nil
}
