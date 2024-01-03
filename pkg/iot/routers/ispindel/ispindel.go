package ispindel

import (
	"context"
	"log/slog"
	"strconv"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotutil "github.com/zachfi/iotcontroller/pkg/iot/util"
)

const (
	RouteName = "zigbee2mqtt"
)

// A wifi report to mqtt from the ESP32 on the ispindel board.
type Ispindel struct {
	logger *slog.Logger
	tracer trace.Tracer

	kubeclient kubeclient.Client
}

func New(logger *slog.Logger, tracer trace.Tracer, kubeclient kubeclient.Client, _ *grpc.ClientConn) (*Ispindel, error) {
	i := &Ispindel{
		logger:     logger.With("router", RouteName),
		tracer:     tracer,
		kubeclient: kubeclient,
	}

	/* reportStream, err := z.telemetryClient.TelemetryReportIOTDevice(ctx) */
	/* if err != nil { */
	/* 	return err */
	/* } */
	/**/
	/* z.reportStream = reportStream */

	return i, nil
}

func (i *Ispindel) TiltRoute(ctx context.Context, b []byte, deviceID string) error {
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return err
	}

	var device *apiv1.Device

	device, err = iotutil.GetOrCreateAPIDevice(ctx, i.kubeclient, deviceID)
	if err != nil {
		return err
	}

	if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		metricTiltAngle.WithLabelValues(deviceID, zone).Set(f)
	}

	return nil
}

func (i *Ispindel) TemperatureRoute(ctx context.Context, b []byte, deviceID string) error {
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return err
	}

	var device *apiv1.Device

	device, err = iotutil.GetOrCreateAPIDevice(ctx, i.kubeclient, deviceID)
	if err != nil {
		return err
	}

	if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		metricTemperature.WithLabelValues(deviceID, zone).Set(f)
	}

	return nil
}

func (i *Ispindel) BatteryRoute(ctx context.Context, b []byte, deviceID string) error {
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return err
	}

	var device *apiv1.Device

	device, err = iotutil.GetOrCreateAPIDevice(ctx, i.kubeclient, deviceID)
	if err != nil {
		return err
	}

	if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		metricBattery.WithLabelValues(deviceID, zone).Set(f)
	}

	return nil
}

func (i *Ispindel) SpecificGravityRoute(ctx context.Context, b []byte, deviceID string) error {
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return err
	}

	var device *apiv1.Device

	device, err = iotutil.GetOrCreateAPIDevice(ctx, i.kubeclient, deviceID)
	if err != nil {
		return err
	}

	if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		metricSpecificGravity.WithLabelValues(deviceID, zone).Set(f)
	}

	return nil
}

func (i *Ispindel) RSSI(ctx context.Context, b []byte, deviceID string) error {
	f, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return err
	}

	var device *apiv1.Device

	device, err = iotutil.GetOrCreateAPIDevice(ctx, i.kubeclient, deviceID)
	if err != nil {
		return err
	}

	if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		metricRSSI.WithLabelValues(deviceID, zone).Set(float64(f))
	}

	return nil
}
