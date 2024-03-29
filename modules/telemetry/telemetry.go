package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strconv"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zachfi/zkit/pkg/tracing"

	"github.com/zachfi/zkit/pkg/boundedwaitgroup"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	telemetryv1proto "github.com/zachfi/iotcontroller/proto/telemetry/v1"
)

const (
	defaultExpiry = 5 * time.Minute
	namespace     = "iot"
	module        = "telemetry"
)

type Telemetry struct {
	// UnimplementedTelemetryServer

	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	keeper thingKeeper

	zonekeeperClient iotv1proto.ZoneKeeperServiceClient

	seenThings map[string]time.Time

	reportQueue chan *telemetryv1proto.TelemetryReportIOTDeviceRequest

	kubeclient client.Client

	// cached cache.Source
}

type thingKeeper map[string]map[string]string

func New(cfg Config, logger *slog.Logger, kubeclient client.Client, conn *grpc.ClientConn) (*Telemetry, error) {
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
				case "ispindel":
					err = l.handleIspindelReport(ctx, req)
					_ = tracing.ErrHandler(span, err, "failed to handle ispindel report", l.logger)
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
		if err == io.EOF {
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
		workQueueLength.With(prometheus.Labels{}).Set(float64(len(l.reportQueue)))
	}
}

func (l *Telemetry) handleIspindelReport(ctx context.Context, req *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
	var err error

	if len(req.DeviceDiscovery.Endpoints) == 0 {
		l.logger.Debug("unhandled empty ispindel endpoints", "discovery", fmt.Sprintf("%+v", req.DeviceDiscovery))
		return nil
	}

	ctx, span := l.tracer.Start(ctx, "handleIspindelReport")
	defer span.End()

	name := strings.ToLower(req.DeviceDiscovery.ObjectId)
	device, err := l.getOrCreateAPIDevice(ctx, name)
	if err != nil {
		return tracing.ErrHandler(span, err, "failed to get or create API device", nil)
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		l.updateLastSeen(ctx, device)
	}()

	if device.Spec.Type != iotv1proto.DeviceType_DEVICE_TYPE_ISPINDEL.String() {
		device.Spec.Type = iotv1proto.DeviceType_DEVICE_TYPE_ISPINDEL.String()
		if err = l.kubeclient.Update(ctx, device); err != nil {
			return fmt.Errorf("failed to update device spec: %w", err)
		}
	}

	var z string
	if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		z = zone
	}
	if z == "" {
		return fmt.Errorf("unable to metric without a zone")
	}

	d := req.DeviceDiscovery.ObjectId
	m := req.DeviceDiscovery.Message

	span.SetAttributes(
		attribute.String("z", z),
		attribute.String("d", d),
		attribute.String("m", string(m)),
	)

	switch req.DeviceDiscovery.Endpoints[0] {
	case "tilt":
		f, err := strconv.ParseFloat(string(m), 64)
		if err != nil {
			return err
		}
		metricTiltAngle.WithLabelValues(d, z).Set(f)
	case "temperature":
		f, err := strconv.ParseFloat(string(m), 64)
		if err != nil {
			return err
		}
		metricTemperature.WithLabelValues(d, z).Set(f)
	case "temp_units":
	case "battery":
		f, err := strconv.ParseFloat(string(m), 64)
		if err != nil {
			return err
		}
		metricBattery.WithLabelValues(d, z).Set(f)
	case "gravity":
		f, err := strconv.ParseFloat(string(m), 64)
		if err != nil {
			return err
		}
		metricSpecificGravity.WithLabelValues(d, z).Set(f)
	case "interval":
	case "RSSI":
	}

	wg.Wait()

	return nil
}

func (l *Telemetry) handleZigbeeReport(ctx context.Context, req *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
	if req == nil {
		return fmt.Errorf("unable to read zigbee report from nil request")
	}

	ctx, span := l.tracer.Start(ctx, "handleZigbeeReport")
	defer span.End()

	name := strings.ToLower(req.DeviceDiscovery.ObjectId)

	device, err := l.getOrCreateAPIDevice(ctx, name)
	if err != nil {
		return tracing.ErrHandler(span, err, "failed to get or create API device", l.logger)
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		l.updateLastSeen(ctx, device)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		l.executeForReport(ctx, req, device)
	}()

	wg.Wait()

	return nil
}

func (l *Telemetry) executeForReport(ctx context.Context, req *telemetryv1proto.TelemetryReportIOTDeviceRequest, device *apiv1.Device) {
	var err error

	ctx, span := l.tracer.Start(ctx, "executeForReport")
	defer func() { _ = tracing.ErrHandler(span, err, "read report", l.logger) }()

	// Avoid recursively responding to /set calls that the action handler makes
	if len(req.DeviceDiscovery.Endpoints) > 0 && req.DeviceDiscovery.Endpoints[0] == "set" {
		span.AddEvent("skipped")
		return
	}

	msg, err := iot.ReadZigbeeMessage(ctx, l.tracer, req.DeviceDiscovery)
	if err != nil {
		return
	}

	if msg == nil {
		return
	}

	// TODO: Review the desire to avoid updating too quickly
	// lf := time.Unix(int64(device.Status.LastFlushed), 0)
	// if time.Since(lf) < 10*time.Second {
	// 	return
	// }

	switch reflect.TypeOf(msg).String() {
	case "iot.ZigbeeBridgeState":
		span.SetAttributes(attribute.String("message_type", "iot.ZigbeeBridgeState"))
		m := msg.(iot.ZigbeeBridgeState)
		switch m {
		case iot.Offline:
			telemetryIOTBridgeState.WithLabelValues().Set(float64(0))
		case iot.Online:
			telemetryIOTBridgeState.WithLabelValues().Set(float64(1))
		}

	case "iot.ZigbeeMessageBridgeDevices":
		span.SetAttributes(attribute.String("message_type", "iot.ZigbeeMessageBridgeDevices"))
		m := msg.(iot.ZigbeeMessageBridgeDevices)
		err = l.handleZigbeeDevices(ctx, m)
		_ = tracing.ErrHandler(span, err, "failed to handle zigbee devices", l.logger)
		return
	case "iot.ZigbeeBridgeInfo":
		span.SetAttributes(attribute.String("message_type", "iot.ZigbeeBridgeInfo"))
		// zigbee2mqtt/bridge/info
	case "iot.ZigbeeBridgeLog":
		span.SetAttributes(attribute.String("message_type", "iot.ZigbeeBridgeLog"))
		// zigbee2mqtt/bridge/log
	case "iot.ZigbeeMessage":
		span.SetAttributes(attribute.String("message_type", "iot.ZigbeeMessage"))
		m := msg.(iot.ZigbeeMessage)

		l.updateZigbeeMessageMetrics(ctx, m, req.DeviceDiscovery.Component, device)

		// If this device has been annotated by a zone, then we pass the action to
		// the zone handler.
		if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
			span.SetAttributes(
				attribute.String("zone", zone),
				attribute.String("device", device.Name),
			)

			wg := sync.WaitGroup{}

			wg.Add(1)
			go func() {
				defer wg.Done()
				l.selfAnnounce(ctx, device.Name, zone)
			}()

			if m.Action != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					l.action(ctx, *m.Action, device.Name, zone)
				}()
			}

			wg.Wait()
		}
	default:
		l.logger.Error("unhandled iot message type", "type", fmt.Sprintf("%T", msg))
	}
}

func (l *Telemetry) selfAnnounce(ctx context.Context, device, zone string) {
	var err error

	ctx, span := l.tracer.Start(ctx, "selfAnnounce", trace.WithAttributes(
		attribute.String("device", device),
		attribute.String("zone", zone),
	))
	defer func() { _ = tracing.ErrHandler(span, err, "self announce", l.logger) }()

	_, err = l.zonekeeperClient.SelfAnnounce(ctx,
		&iotv1proto.SelfAnnounceRequest{
			Device: device,
			Zone:   zone,
		},
	)
}

func (l *Telemetry) action(ctx context.Context, action, device, zone string) {
	var err error

	ctx, span := l.tracer.Start(ctx, "action", trace.WithAttributes(
		attribute.String("action", action),
		attribute.String("device", device),
		attribute.String("zone", zone),
	))
	defer func() { _ = tracing.ErrHandler(span, err, "action", l.logger) }()

	_, err = l.zonekeeperClient.ActionHandler(ctx,
		&iotv1proto.ActionHandlerRequest{
			Event:  action,
			Device: device,
			Zone:   zone,
		},
	)
}

func (l *Telemetry) handleZigbeeDevices(ctx context.Context, m iot.ZigbeeMessageBridgeDevices) error {
	spanCtx, span := l.tracer.Start(ctx, "handleZigbeeDevices")
	defer span.End()

	traceID := trace.SpanContextFromContext(spanCtx).TraceID().String()

	for _, d := range m {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := l.handleZigbeeBridgeDevice(spanCtx, d); err != nil {
			l.logger.Error("device report failed", "traceID", traceID, "err", err)
		}
	}

	return nil
}

func (l *Telemetry) handleZigbeeBridgeDevice(ctx context.Context, d iot.ZigbeeBridgeDevice) error {
	var err error

	ctx, span := l.tracer.Start(ctx, "Telemetry.handleZigbeeBridgeDevices")
	defer func() {
		_ = tracing.ErrHandler(span, err, "failed to handle zigbee bridge device", nil)
	}()

	span.SetAttributes(
		attribute.String("name", d.FriendlyName),
	)

	l.logger.Debug("device report", "traceID", trace.SpanContextFromContext(ctx).TraceID().String())

	var device *apiv1.Device
	device, err = l.getOrCreateAPIDevice(ctx, d.FriendlyName)
	if err != nil {
		return tracing.ErrHandler(span, err, "failed to get or create API device", nil)
	}

	span.SetAttributes(
		attribute.String("d", fmt.Sprintf("%+v", d)),
	)

	device.Spec.Type = iot.ZigbeeDeviceType(d).String()
	device.Spec.DateCode = d.DateCode
	device.Spec.Model = d.Definition.Model
	device.Spec.Vendor = d.Definition.Vendor
	device.Spec.Description = d.Definition.Description

	span.SetAttributes(
		attribute.String("d", fmt.Sprintf("%+v", d)),
		attribute.String("type", device.Spec.Type),
		attribute.String("model", device.Spec.Model),
		attribute.String("vendor", device.Spec.Vendor),
		attribute.String("description", device.Spec.Description),
	)

	if err = l.kubeclient.Update(ctx, device); err != nil {
		return fmt.Errorf("failed to update device spec: %w", err)
	}

	device.Status.SoftwareBuildID = d.SoftwareBuildID

	if err = l.kubeclient.Status().Update(ctx, device); err != nil {
		return fmt.Errorf("failed to update device status: %w", err)
	}

	return nil
}

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

func (l *Telemetry) handleWaterReport(ctx context.Context, request *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
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

func (l *Telemetry) handleAirReport(ctx context.Context, request *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
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

func (l *Telemetry) handleWifiReport(ctx context.Context, request *telemetryv1proto.TelemetryReportIOTDeviceRequest) error {
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

func (l *Telemetry) updateZigbeeMessageMetrics(ctx context.Context, m iot.ZigbeeMessage, component string, device *apiv1.Device) {
	var zone string

	if v, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		zone = v
	}

	if m.Battery != nil {
		telemetryIOTBatteryPercent.WithLabelValues(device.Name, component, zone).Set(*m.Battery)
	}

	if m.LinkQuality != nil {
		telemetryIOTLinkQuality.WithLabelValues(device.Name, component, zone).Set(float64(*m.LinkQuality))
	}

	if m.Temperature != nil {
		telemetryIOTTemperature.WithLabelValues(device.Name, component, zone).Set(*m.Temperature)
	}

	if m.Humidity != nil {
		telemetryIOTHumidity.WithLabelValues(device.Name, component, zone).Set(*m.Humidity)
	}

	if m.Co2 != nil {
		telemetryIOTCo2.WithLabelValues(device.Name, component, zone).Set(*m.Co2)
	}

	if m.Formaldehyde != nil {
		telemetryIOTFormaldehyde.WithLabelValues(device.Name, component, zone).Set(*m.Formaldehyde)
	}

	if m.VOC != nil {
		telemetryIOTVoc.WithLabelValues(device.Name, component, zone).Set(float64(*m.VOC))
	}

	if m.State != nil {
		switch *m.State {
		case "ON":
			telemetryIOTState.WithLabelValues(device.Name, component, zone).Set(float64(1))
		case "OFF":
			telemetryIOTState.WithLabelValues(device.Name, component, zone).Set(float64(0))
		}
	}

	if m.Illuminance != nil {
		telemetryIOTIlluminance.WithLabelValues(device.Name, component, zone).Set(float64(*m.Illuminance))
	}

	if m.Occupancy != nil {
		if *m.Occupancy {
			telemetryIOTOccupancy.WithLabelValues(device.Name, component, zone).Set(float64(1))
		} else {
			telemetryIOTOccupancy.WithLabelValues(device.Name, component, zone).Set(float64(0))
		}
	}

	if m.WaterLeak != nil {
		if *m.WaterLeak {
			telemetryIOTWaterLeak.WithLabelValues(device.Name, component, zone).Set(float64(1))
		} else {
			telemetryIOTWaterLeak.WithLabelValues(device.Name, component, zone).Set(float64(0))
		}
	}

	if m.Tamper != nil {
		if *m.Tamper {
			telemetryIOTTamper.WithLabelValues(device.Name, component, zone).Set(float64(1))
		} else {
			telemetryIOTTamper.WithLabelValues(device.Name, component, zone).Set(float64(0))
		}
	}
}

func (l *Telemetry) getOrCreateAPIDevice(ctx context.Context, name string) (*apiv1.Device, error) {
	var (
		err    error
		device apiv1.Device
	)

	ctx, span := l.tracer.Start(ctx, "getOrCreateAPIDevice", trace.WithAttributes(
		attribute.String("device", name),
	))
	defer func() { _ = tracing.ErrHandler(span, err, "create API device", nil) }()

	nsn := types.NamespacedName{
		Name:      strings.ToLower(name),
		Namespace: namespace,
	}

	err = l.kubeclient.Get(ctx, nsn, &device)
	if err != nil {
		if apierrors.IsNotFound(err) {
			span.AddEvent("zone not found")
			err = l.createAPIDevice(ctx, &device, nsn.Name)
			if err != nil {
				return nil, tracing.ErrHandler(span, err, "failed", nil)
			}
		}
	}
	return &device, tracing.ErrHandler(span, err, "", nil)
}

func (l *Telemetry) createAPIDevice(ctx context.Context, device *apiv1.Device, name string) error {
	var err error

	ctx, span := l.tracer.Start(ctx, "createAPIDevice", trace.WithAttributes(
		attribute.String("device", device.Name),
	))
	defer func() { _ = tracing.ErrHandler(span, err, "create API device", nil) }()

	device.Name = name
	device.Namespace = namespace

	err = l.kubeclient.Create(ctx, device)
	if err != nil {
		return tracing.ErrHandler(span, err, "failed", nil)
	}

	span.AddEvent("created")

	return nil
}

func (l *Telemetry) updateLastSeen(ctx context.Context, device *apiv1.Device) {
	var err error

	if time.Since(time.Unix(int64(device.Status.LastSeen), 0)) < time.Minute {
		return
	}

	_, span := l.tracer.Start(ctx, "updateLastSeen")
	defer func() { _ = tracing.ErrHandler(span, err, "update last seen", l.logger) }()

	device.Status.LastSeen = uint64(time.Now().Unix())

	err = l.kubeclient.Status().Update(ctx, device)
}
