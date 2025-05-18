package ispindel

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/trace"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotutil "github.com/zachfi/iotcontroller/pkg/iot/util"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	RouteName = "zigbee2mqtt"
)

// A wifi report to mqtt from the ESP32 on the ispindel board.
type Ispindel struct {
	logger *slog.Logger
	tracer trace.Tracer

	kubeclient kubeclient.Client

	deviceTracker *iotutil.DeviceTracker
}

func New(logger *slog.Logger, tracer trace.Tracer, kubeclient kubeclient.Client, _ iotv1proto.ZoneKeeperServiceClient) (*Ispindel, error) {
	i := &Ispindel{
		logger:     logger.With("router", RouteName),
		tracer:     tracer,
		kubeclient: kubeclient,
		deviceTracker: iotutil.NewDeviceTracker(
			[]iotutil.Metric{
				metricTiltAngle,
				metricSpecificGravity,
				metricBattery,
				metricRSSI,
				metricTemperature,
			},
			time.Minute*35, // 900 seconds is the default report interval for the ispindel
		),
	}

	go i.deviceTracker.Run(time.Minute * 5)

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

	i.deviceTracker.Track(deviceID)

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

	i.deviceTracker.Track(deviceID)

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

	i.deviceTracker.Track(deviceID)

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

	i.deviceTracker.Track(deviceID)

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

	i.deviceTracker.Track(deviceID)

	if zone, ok := device.Labels[iot.DeviceZoneLabel]; ok {
		metricRSSI.WithLabelValues(deviceID, zone).Set(float64(f))
	}

	return nil
}
