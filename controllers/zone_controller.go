/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	iotv1 "github.com/zachfi/iotcontroller/api/v1"
	iot "github.com/zachfi/iotcontroller/pkg/iot"
	"github.com/zachfi/iotcontroller/pkg/iot/handlers/zigbee"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// ZoneReconciler reconciles a Zone object
type ZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	sync.Mutex

	tracer     trace.Tracer
	logger     *slog.Logger
	mqttclient mqtt.Client

	handlers map[controllerHandler]iot.Handler
	zones    map[string]*iot.Zone
}

type controllerHandler int

const (
	controllerHandlerZigbee controllerHandler = iota
)

//+kubebuilder:rbac:groups=iot.iot,resources=devices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=iot.iot,resources=devices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=iot.iot,resources=devices/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *ZoneReconciler) Reconcile(rctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error
	log := log.FromContext(rctx)

	// TODO: zleslie -- think about moving the iot.Zone object into the API so
	// that the controller can act on it.  Flushing could be done here, since
	// each time the status updated then we get a reconcile here, and so should
	// be able to flush out the mqtt what the status should be.  Handlers could
	// also be checked here, though perhaps static and receiving an API device.
	// Knowing the type of device ahead of time will let us know which handler to
	// call.  This might mean that the zonekeeper meerly updates the status in
	// kubernetes and the logic of taking action on that zone is here.

	attributes := []attribute.KeyValue{
		attribute.String("req", req.String()),
		attribute.String("namespace", req.Namespace),
	}
	ctx, span := r.tracer.Start(rctx, "Reconcile", trace.WithAttributes(attributes...))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	var zone *iotv1.Zone

	if err = r.Get(ctx, req.NamespacedName, zone); err != nil {
		log.Error(err, "failed to get resource")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// INFO: Think about moving the zone deices to the Status, then set the zone
	// on the device, which we will then query for here.

	// TODO: Get all of the devices that we have for this zone and update the
	// existing instance with the devics.

	deviceList := &iotv1.DeviceList{}

	selector := fields.SelectorFromSet(fields.Set{"zone": req.Name})
	listOptions := &client.ListOptions{FieldSelector: selector}
	err = r.Client.List(ctx, deviceList, listOptions)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err = r.flushZone(rctx, zone, deviceList); err != nil {
		log.Error(err, "failed to flush zone state")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iotv1.Zone{}).
		Complete(r)
}

func (r *ZoneReconciler) SetMQTTClient(client mqtt.Client) {
	if client != nil {
		r.mqttclient = client
	}
}

func (r *ZoneReconciler) SetTracer(tracer trace.Tracer) {
	if tracer != nil {
		r.tracer = tracer
	}
}

func (r *ZoneReconciler) SetLogger(logger *slog.Logger) {
	if logger != nil {
		r.logger = logger
	}
}

// Once we create a new reconciler, lets set the hanlders from outside.
func (r *ZoneReconciler) SetHandlers() error {
	zigbeeHandler, err := zigbee.New(r.mqttclient, r.logger)
	if err != nil {
		return err
	}

	hhh := make(map[controllerHandler]iot.Handler, 0)
	hhh[controllerHandlerZigbee] = zigbeeHandler
	r.handlers = hhh

	return nil
}

func (r *ZoneReconciler) flushZone(ctx context.Context, zone *iotv1.Zone, deviceList *iotv1.DeviceList) error {
	var z *iot.Zone
	if _, ok := r.zones[zone.Name]; !ok {
		z = iot.NewZone(zone.Spec.Name)
		r.zones[zone.Name] = z
	}

	if z == nil {
		z = r.zones[zone.Name]
	}

	var err error
	var errs []error

	for _, d := range deviceList.Items {
		device := &iotv1proto.Device{
			Name: d.Spec.Name,
		}
		var handler iot.Handler

		switch d.Spec.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_COORDINATOR.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_COLOR_LIGHT.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_RELAY.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_LEAK.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_BUTTON.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_MOISTURE.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_TEMPERATURE.String():
			handler = r.handlers[controllerHandlerZigbee]
		case iotv1proto.DeviceType_DEVICE_TYPE_MOTION.String():
			handler = r.handlers[controllerHandlerZigbee]
		}

		err = z.SetDevice(device, handler)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	err = z.SetState(ctx, iot.ZoneStateToProto(zone.Status.State))
	if err != nil {
		return err
	}

	err = z.SetBrightness(ctx, iotv1proto.Brightness(zone.Status.Brightness))
	if err != nil {
		return err
	}

	err = z.SetColor(ctx, zone.Status.Color)
	if err != nil {
		return err
	}

	err = z.SetColorTemperature(ctx, iotv1proto.ColorTemperature(zone.Status.ColorTemperature))
	if err != nil {
		return err
	}

	for _, d := range zone.Spec.Devices {
		var device iotv1.Device
		nsn := types.NamespacedName{
			Namespace: zone.Namespace,
			Name:      d,
		}
		if err = r.Get(ctx, nsn, &device); err != nil {
			return err
		}

		switch device.Spec.Type {
		case iotv1proto.DeviceType_DEVICE_TYPE_BASIC_LIGHT.String():
		}
	}

	err = z.Flush(ctx)
	if err != nil {
		return err
	}

	return nil
}
