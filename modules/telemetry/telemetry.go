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

	klient *kubeclient.KubeClient
}

type thingKeeper map[string]map[string]string

func New(cfg Config, logger log.Logger, klient *kubeclient.KubeClient) (*Telemetry, error) {
	s := &Telemetry{
		cfg:    &cfg,
		logger: log.With(logger, "module", "telemetry"),
		tracer: otel.Tracer("telemetry"),

		// lights:     lig,
		klient:     klient,
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

		spanCtx, span := l.tracer.Start(
			stream.Context(),
			"TelemetryReportIODDevice",
			trace.WithSpanKind(trace.SpanKindServer),
		)

		discovery := req.GetDeviceDiscovery()

		span.SetAttributes(
			attribute.String("component", discovery.Component),
			attribute.String("object_id", discovery.ObjectId),
		)

		if discovery.ObjectId != "" {
			telemetryIOTReport.WithLabelValues(discovery.ObjectId, discovery.Component).Inc()
		}

		switch discovery.Component {
		case "zigbee2mqtt":
			err = l.handleZigbeeReport(spanCtx, req)
			if err != nil {
				_ = level.Error(l.logger).Log("failed to handle zigbee report", err.Error())
			}
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

	span.SetAttributes(attribute.String("name", request.Name))

	_ = level.Debug(l.logger).Log("msg", "device report", "traceID", trace.SpanContextFromContext(ctx).TraceID().String())

	discovery := request.DeviceDiscovery

	msg, err := iot.ReadZigbeeMessage(discovery.ObjectId, discovery.Message, discovery.Endpoints...)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return errors.Wrap(err, "failed to read zigbee message")
	}

	if msg == nil {
		return nil
	}

	// now := time.Now()

	switch reflect.TypeOf(msg).String() {
	// case "iot.ZigbeeBridgeState":
	// 	m := msg.(iot.ZigbeeBridgeState)
	// 	switch m {
	// 	case iot.Offline:
	// 		telemetryIOTBridgeState.WithLabelValues().Set(float64(0))
	// 	case iot.Online:
	// 		telemetryIOTBridgeState.WithLabelValues().Set(float64(1))
	// 	}
	//
	// case "iot.ZigbeeBridgeLog":
	// 	m := msg.(iot.ZigbeeBridgeLog)
	//
	// 	if m.Message == nil {
	// 		span.SetStatus(codes.Error, err.Error())
	// 		return fmt.Errorf("unhandled iot.ZigbeeBridgeLog type: %s", m.Type)
	// 	}
	//
	// 	messageTypeName := reflect.TypeOf(m.Message).String()
	//
	// 	switch messageTypeName {
	// 	case "string":
	// 		if strings.HasPrefix(m.Message.(string), "Update available") {
	// 			return l.handleZigbeeDeviceUpdate(ctx, m)
	// 		}
	// 	case "iot.ZigbeeMessageBridgeDevices":
	// 		// TODO: When we receive a devices collection, we might want to do something with it.
	// 		return nil
	// 		// return l.handleZigbeeDevices(ctx, m.Message.(iot.ZigbeeMessageBridgeDevices))
	// 	default:
	// 		return fmt.Errorf("unhandled iot.ZigbeeBridgeLog: %s", messageTypeName)
	// 	}
	//
	// case "iot.ZigbeeMessageBridgeDevices":
	// 	// m := msg.(iot.ZigbeeMessageBridgeDevices)
	// 	//
	// 	// return l.handleZigbeeDevices(ctx, m)
	//
	// 	// TODO: Same as above, do we want to handle this somehow?
	// 	return nil
	case "iot.ZigbeeMessage":
		// m := msg.(iot.ZigbeeMessage)

		var d apiv1.Device
		nsn := types.NamespacedName{
			Name:      request.DeviceDiscovery.ObjectId,
			Namespace: namespace,
		}

		err := l.klient.Client().Get(ctx, nsn, &d)
		if err != nil {
			if apierrors.IsNotFound(err) {
				d.SetName(request.DeviceDiscovery.ObjectId)
				d.SetNamespace(namespace)
				err := l.klient.Client().Create(ctx, &d)
				if err != nil {
					return err
				}

			} else {
				_ = level.Error(l.logger).Log("error getting device from api", err, "request", request)
			}
		}

	// 	x := &inventory.ZigbeeDevice{
	// 		Name:     request.DeviceDiscovery.ObjectId,
	// 		LastSeen: timestamppb.New(now),
	// 	}
	//
	// 	result, err := l.fetchZigbeeDevice(ctx, x)
	// 	if err != nil {
	// 		return err
	// 	}
	//
	// 	// l.updateZigbeeMessageMetrics(m, request, result)
	//
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

// func (l *Telemetry) updateZigbeeMessageMetrics(
// 	m iot.ZigbeeMessage,
// 	request *inventory.IOTDevice,
// 	device *inventory.ZigbeeDevice,
// ) {
// 	var zone string
//
// 	if val := device.GetIotZone(); val != "" {
// 		zone = val
// 	}
//
// 	deviceName := request.DeviceDiscovery.ObjectId
// 	component := request.DeviceDiscovery.Component
//
// 	if m.Battery != nil {
// 		telemetryIOTBatteryPercent.WithLabelValues(deviceName, component, zone).Set(*m.Battery)
// 	}
//
// 	if m.LinkQuality != nil {
// 		telemetryIOTLinkQuality.WithLabelValues(deviceName, component, zone).Set(float64(*m.LinkQuality))
// 	}
//
// 	if m.Temperature != nil {
// 		telemetryIOTTemperature.WithLabelValues(deviceName, component, zone).Set(*m.Temperature)
// 	}
//
// 	if m.Humidity != nil {
// 		telemetryIOTHumidity.WithLabelValues(deviceName, component, zone).Set(*m.Humidity)
// 	}
//
// 	if m.Co2 != nil {
// 		telemetryIOTCo2.WithLabelValues(deviceName, component, zone).Set(*m.Co2)
// 	}
//
// 	if m.Formaldehyde != nil {
// 		telemetryIOTFormaldehyde.WithLabelValues(deviceName, component, zone).Set(*m.Formaldehyde)
// 	}
//
// 	if m.VOC != nil {
// 		telemetryIOTVoc.WithLabelValues(deviceName, component, zone).Set(float64(*m.VOC))
// 	}
//
// 	if m.Illuminance != nil {
// 		telemetryIOTIlluminance.WithLabelValues(deviceName, component, zone).Set(float64(*m.Illuminance))
// 	}
//
// 	if m.Occupancy != nil {
// 		if *m.Occupancy {
// 			telemetryIOTOccupancy.WithLabelValues(deviceName, component, zone).Set(float64(1))
// 		} else {
// 			telemetryIOTOccupancy.WithLabelValues(deviceName, component, zone).Set(float64(0))
// 		}
// 	}
//
// 	if m.WaterLeak != nil {
// 		if *m.WaterLeak {
// 			telemetryIOTWaterLeak.WithLabelValues(deviceName, component, zone).Set(float64(1))
// 		} else {
// 			telemetryIOTWaterLeak.WithLabelValues(deviceName, component, zone).Set(float64(0))
// 		}
// 	}
//
// 	if m.Tamper != nil {
// 		if *m.Tamper {
// 			telemetryIOTTamper.WithLabelValues(deviceName, component, zone).Set(float64(1))
// 		} else {
// 			telemetryIOTTamper.WithLabelValues(deviceName, component, zone).Set(float64(0))
// 		}
// 	}
// }

// func (l *Telemetry) fetchZigbeeDevice(ctx context.Context, x *inventory.ZigbeeDevice) (*inventory.ZigbeeDevice, error) {
// }

// func (l *Telemetry) fetchZigbeeDevice(ctx context.Context, x *inventory.ZigbeeDevice) (*inventory.ZigbeeDevice, error) {
// 	result, err := l.inventory.FetchZigbeeDevice(ctx, x.Name)
// 	if err != nil {
// 		result, err = l.inventory.CreateZigbeeDevice(ctx, x)
// 		if err != nil {
// 			return nil, err
// 		}
// 	}
//
// 	return result, nil
// }
