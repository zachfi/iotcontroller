package telemetry

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/kubeclient"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotv1 "github.com/zachfi/iotcontroller/proto/iot/v1"
	telemetryv1 "github.com/zachfi/iotcontroller/proto/telemetry/v1"
)

const (
	defaultExpiry = 5 * time.Minute
	namespace     = "iot"
)

type Telemetry struct {
	// UnimplementedTelemetryServer

	services.Service
	cfg *Config

	logger log.Logger
	tracer trace.Tracer

	keeper thingKeeper
	// lights     *lights.Lights
	iotServer  *iot.Server
	seenThings map[string]time.Time

	reportQueue chan *telemetryv1.TelemetryReportIOTDeviceRequest

	klient *kubeclient.KubeClient

	// cached cache.Source
}

type thingKeeper map[string]map[string]string

func New(cfg Config, logger log.Logger, klient *kubeclient.KubeClient) (*Telemetry, error) {
	// client := klient.Client()
	// cached, _ := cache.NewInformer(client, &apiv1.Device{}, 10*time.Second, ResourceEventHandlerDetailedFuncs{
	// 	AddFunc: func(obj interface{}, isInInitialList bool) {
	// 		klient.Delete(obj.(runtime.Object))
	// 	},
	// 	DeleteFunc: func(obj interface{}) {
	// 		key, err := DeletionHandlingMetaNamespaceKeyFunc(obj)
	// 		if err != nil {
	// 			key = "oops something went wrong with the key"
	// 		}
	//
	// 		// Report this deletion.
	// 		deletionCounter <- key
	// 	},
	// })

	s := &Telemetry{
		cfg:    &cfg,
		logger: log.With(logger, "module", "telemetry"),
		tracer: otel.Tracer("telemetry"),

		// lights:     lig,
		klient: klient,

		// cached: cached,
		reportQueue: make(chan *telemetryv1.TelemetryReportIOTDeviceRequest, 1000),

		keeper:     make(thingKeeper),
		seenThings: make(map[string]time.Time),
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
	// 				_ = level.Info(s.logger).Log("msg", "expiring",
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

func (l *Telemetry) reportReceiver(ctx context.Context) {
	var err error

	for {
		select {
		case <-ctx.Done():
			return
		case req := <-l.reportQueue:
			rCtx, span := l.tracer.Start(
				context.Background(),
				"Telemetry.TelemetryReportIOTDevice",
				trace.WithSpanKind(trace.SpanKindServer),
			)

			discovery := req.GetDeviceDiscovery()

			if discovery.ObjectId != "" {
				telemetryIOTReport.WithLabelValues(discovery.ObjectId, discovery.Component).Inc()

				span.SetAttributes(
					attribute.String("component", discovery.Component),
					attribute.String("object_id", discovery.ObjectId),
				)
			}

			switch discovery.Component {
			case "zigbee2mqtt":
				err = l.handleZigbeeReport(rCtx, req)
				if err != nil {
					_ = level.Error(l.logger).Log("failed to handle zigbee report", err.Error())
				}
				continue
			}

			switch discovery.ObjectId {
			case "wifi":
				err = l.handleWifiReport(req)
				if err != nil {
					_ = level.Error(l.logger).Log("failed to handle wifi report", err.Error())
				}
			case "air":
				err = l.handleAirReport(req)
				if err != nil {
					_ = level.Error(l.logger).Log("failed to handle air report", err.Error())
				}
			case "water":
				err = l.handleWaterReport(req)
				if err != nil {
					_ = level.Error(l.logger).Log("failed to handle water report", err.Error())
				}
			case "led1", "led2":
				err = l.handleLEDReport(req)
				if err != nil {
					_ = level.Error(l.logger).Log("failed to handle led report", err.Error())
				}
			default:
				telemetryIOTUnhandledReport.WithLabelValues(discovery.ObjectId, discovery.Component).Inc()
			}

		}
	}
}

func (l *Telemetry) TelemetryReportIOTDevice(stream telemetryv1.TelemetryService_TelemetryReportIOTDeviceServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// Close the connection and return the response to the client
			return stream.SendAndClose(&telemetryv1.TelemetryReportIOTDeviceResponse{})
		}

		if err != nil {
			_ = level.Error(l.logger).Log("err", err.Error())
			return err
		}

		_, span := l.tracer.Start(
			context.Background(),
			"Telemetry.TelemetryReportIOTDevice",
			trace.WithSpanKind(trace.SpanKindServer),
		)

		l.reportQueue <- req
		workQueueLength.With(prometheus.Labels{}).Set(float64(len(l.reportQueue)))

		span.End()
	}
}

func (l *Telemetry) SetIOTServer(iotServer *iot.Server) error {
	if l.iotServer != nil {
		_ = level.Debug(l.logger).Log("replacing iotServer on telemetryServer")
	}

	l.iotServer = iotServer

	return nil
}

func (l *Telemetry) handleZigbeeReport(ctx context.Context, request *telemetryv1.TelemetryReportIOTDeviceRequest) error {
	if request == nil {
		return fmt.Errorf("unable to read zigbee report from nil request")
	}

	ctx, span := l.tracer.Start(ctx, "handleZigbeeReport")
	defer span.End()
	traceID := trace.SpanContextFromContext(ctx).TraceID().String()
	_ = level.Debug(l.logger).Log("msg", "zigbee report", "traceID", traceID)

	discovery := request.DeviceDiscovery

	span.SetAttributes(
		attribute.String("component", discovery.Component),
	)

	msg, err := iot.ReadZigbeeMessage(discovery.ObjectId, discovery.Message, discovery.Endpoints...)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return errors.Wrap(err, "failed to read zigbee message")
	}

	if msg == nil {
		return nil
	}

	switch reflect.TypeOf(msg).String() {
	case "iot.ZigbeeBridgeState":
		m := msg.(iot.ZigbeeBridgeState)
		switch m {
		case iot.Offline:
			telemetryIOTBridgeState.WithLabelValues().Set(float64(0))
		case iot.Online:
			telemetryIOTBridgeState.WithLabelValues().Set(float64(1))
		}

	case "iot.ZigbeeMessageBridgeDevices":
		m := msg.(iot.ZigbeeMessageBridgeDevices)
		return l.handleZigbeeDevices(ctx, m)

	case "iot.ZigbeeBridgeInfo":
		// zigbee2mqtt/bridge/info
	case "iot.ZigbeeBridgeLog":
		// zigbee2mqtt/bridge/log
	case "iot.ZigbeeMessage":
		m := msg.(iot.ZigbeeMessage)

		var d apiv1.Device
		nsn := types.NamespacedName{
			Name:      request.DeviceDiscovery.ObjectId,
			Namespace: namespace,
		}

		_, getSpan := l.tracer.Start(ctx, "iot.ZigbeeMessage/Get")
		err := l.klient.Client().Get(ctx, nsn, &d)
		if err != nil {
			if apierrors.IsNotFound(err) {
				d.SetName(request.DeviceDiscovery.ObjectId)
				d.SetNamespace(namespace)

				_, createSpan := l.tracer.Start(ctx, "iot.ZigbeeMessage/Create")
				err = l.klient.Client().Create(ctx, &d)
				if err != nil {
					return errHandler(createSpan, err)
				}
				createSpan.End()
			}

			return errHandler(getSpan, err)
		}
		getSpan.End()

		d.Status.LastSeen = uint64(time.Now().Unix())

		_, statusUpdateSpan := l.tracer.Start(ctx, "iot.ZigbeeMessage/Status/Update")
		if err = l.klient.Client().Status().Update(ctx, &d); err != nil {
			statusUpdateSpan.SetStatus(codes.Error, err.Error())
			statusUpdateSpan.End()
			return err
		}
		statusUpdateSpan.End()

		l.updateZigbeeMessageMetrics(m, request.DeviceDiscovery.Component, d)

		// TODO: implement all actions needed to condition the environment according to spec.

		// TODO: maybe implement this as an alert receiver.  I think this ends up being simpler to reason about, and avoids trying to rebuild promql in the Condition spec.
		// l.conditioner(ctx, m, d)

		// TODO: need to handle events for button presses, etc.
		// 	if m.Action != nil {
		// 		action := &iotv1.Action{
		// 			Event:  *m.Action,
		// 			Device: x.Name,
		// 			Zone:   result.IotZone,
		// 		}
		//
		// 		err = l.lights.ActionHandler(ctx, action)
		// 		if err != nil {
		// 			_ = level.Error(l.logger).Log("err", err.Error())
		// 		}
		// 	}

	default:
		_ = level.Error(l.logger).Log("unhandled iot message type", fmt.Sprintf("%T", msg))
	}

	return nil
}

func (l *Telemetry) handleZigbeeDeviceUpdate(ctx context.Context, m iot.ZigbeeBridgeLog) error {
	span := trace.SpanFromContext(ctx)
	defer span.End()

	// zigbee2mqtt/bridge/request/device/ota_update/update
	_ = level.Debug(l.logger).Log("msg", "upgrade report",
		"device", m.Meta["device"],
		"status", m.Meta["status"],
	)

	req := &iotv1.UpdateDeviceRequest{
		Device: m.Meta["device"].(string),
	}

	go func() {
		_, err := l.iotServer.UpdateDevice(ctx, req)
		if err != nil {
			_ = level.Error(l.logger).Log("err", err.Error())
		}
	}()

	return nil
}

func (l *Telemetry) handleZigbeeDevices(ctx context.Context, m iot.ZigbeeMessageBridgeDevices) error {
	spanCtx, span := l.tracer.Start(ctx, "handleZigbeeDevices")
	defer span.End()

	traceID := trace.SpanContextFromContext(spanCtx).TraceID().String()
	_ = level.Debug(l.logger).Log("msg", "devices report", "traceID", traceID)

	c := l.klient.Client()
	for _, d := range m {
		select {
		case <-ctx.Done():
		default:
			if err := l.handleZigbeeBridgeDevice(spanCtx, c, d); err != nil {
				span.SetStatus(codes.Error, err.Error())
				_ = level.Error(l.logger).Log("msg", "device report failed", "traceID", traceID, "err", err)
			}
		}
	}

	return nil
}

func (l *Telemetry) handleZigbeeBridgeDevice(ctx context.Context, c client.Client, d iot.ZigbeeBridgeDevice) error {
	ctx, span := l.tracer.Start(ctx, "handleZigbeeDevice")
	defer span.End()

	span.SetAttributes(
		attribute.String("name", d.FriendlyName),
	)

	_ = level.Debug(l.logger).Log("msg", "device report", "traceID", trace.SpanContextFromContext(ctx).TraceID().String())

	deviceName := types.NamespacedName{
		Namespace: namespace,
		Name:      d.FriendlyName,
	}

	var device apiv1.Device
	if err := c.Get(ctx, deviceName, &device); err != nil {
		level.Error(l.logger).Log("msg", "failed to get device", "err", err)
		device.SetName(d.FriendlyName)
		device.SetNamespace(namespace)

		if err := c.Create(ctx, &device); err != nil {
			level.Error(l.logger).Log("msg", "failed to create new device", "err", err)
		}
	}

	device.Spec.Type = iot.ZigbeeDeviceType(d).String()
	device.Spec.DateCode = d.DateCode
	device.Spec.Model = d.Definition.Model
	device.Spec.Vendor = d.Definition.Vendor
	device.Spec.Description = d.Definition.Description

	if err := c.Update(ctx, &device); err != nil {
		level.Error(l.logger).Log("msg", "failed to update device spec", "err", err)
	}

	device.Status.SoftwareBuildID = d.SoftwareBuildID

	if err := c.Status().Update(ctx, &device); err != nil {
		level.Error(l.logger).Log("msg", "failed to update device status", "err", err)
	}

	return nil
}

func (l *Telemetry) handleLEDReport(request *telemetryv1.TelemetryReportIOTDeviceRequest) error {
	if request == nil {
		return fmt.Errorf("unable to read led report from nil request")
	}

	discovery := request.DeviceDiscovery

	msg, err := iot.ReadMessage("led", discovery.Message, discovery.Endpoints...)
	if err != nil {
		return err
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

func (l *Telemetry) handleWaterReport(request *telemetryv1.TelemetryReportIOTDeviceRequest) error {
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

func (l *Telemetry) handleAirReport(request *telemetryv1.TelemetryReportIOTDeviceRequest) error {
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

func (l *Telemetry) handleWifiReport(request *telemetryv1.TelemetryReportIOTDeviceRequest) error {
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

func (l *Telemetry) updateZigbeeMessageMetrics(
	m iot.ZigbeeMessage,
	component string,
	device apiv1.Device,
) {
	var zone string

	if val := device.Spec.Zone; val != "" {
		zone = val
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

func errHandler(span trace.Span, err error) error {
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
}
