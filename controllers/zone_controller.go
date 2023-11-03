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
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	iotv1 "github.com/zachfi/iotcontroller/api/v1"
	iot "github.com/zachfi/iotcontroller/pkg/iot"
)

// ZoneReconciler reconciles a Zone object
type ZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	sync.Mutex

	tracer trace.Tracer
	logger *slog.Logger
}

//+kubebuilder:rbac:groups=iot.iot,resources=devices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=iot.iot,resources=devices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=iot.iot,resources=devices/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *ZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error
	log := log.FromContext(ctx)

	attributes := []attribute.KeyValue{
		attribute.String("req", req.Name),
		attribute.String("namespace", req.Namespace),
	}
	ctx, span := r.tracer.Start(ctx, "Zone.Reconcile", trace.WithAttributes(attributes...))
	defer func() { handleErr(span, err) }()

	zone := iotv1.Zone{}
	_, getZoneSpan := r.tracer.Start(ctx, "GetZone")
	if err = r.Get(ctx, req.NamespacedName, &zone); err != nil {
		log.Error(err, "failed to get resource")
		handleErr(getZoneSpan, err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	handleErr(getZoneSpan, err)
	getZoneSpan.End()

	deviceList := iotv1.DeviceList{}

	// wg := sync.WaitGroup{}
	// go func() {
	// 	wg.Add(1)
	// 	defer wg.Done()
	//
	// 	ctx, syncLabelSpan := r.tracer.Start(ctx, "Reconcile", trace.WithAttributes(attributes...))
	// 	defer syncLabelSpan.End()
	//
	// 	device := iotv1.Device{}
	// 	for _, id := range zone.Spec.Devices {
	// 		nsn := types.NamespacedName{
	// 			Namespace: zone.Namespace,
	// 			Name:      id,
	// 		}
	//
	// 		if err = r.Get(ctx, nsn, &device); err != nil {
	// 		}
	// 	}
	// }()

	span.SetAttributes(
		attribute.Int("devices", len(zone.Spec.Devices)),
	)

	err = r.syncLabels(ctx, &zone)

	selector := labels.SelectorFromSet(labels.Set{iot.DeviceZoneLabel: req.Name})
	listOptions := &client.ListOptions{
		LabelSelector: selector,
	}
	_, listSpan := r.tracer.Start(ctx, "ListDevices")
	err = r.List(ctx, &deviceList, listOptions)
	if err != nil {
		listSpan.SetStatus(codes.Error, err.Error())
		listSpan.End()
		handleErr(listSpan, err)
		return ctrl.Result{}, fmt.Errorf("failed to list zones: %w", err)
	}
	handleErr(listSpan, nil)

	// wg.Wait()

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iotv1.Zone{}).
		Complete(r)
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

func (r *ZoneReconciler) syncLabels(ctx context.Context, zone *iotv1.Zone) error {
	var err error
	var errs []error

	ctx, span := r.tracer.Start(ctx, "syncLabels")
	defer func() { handleErr(span, err) }()

	span.SetAttributes(
		attribute.Int("devices", len(zone.Spec.Devices)),
	)

	for _, d := range zone.Spec.Devices {
		device := &iotv1.Device{}
		nsn := types.NamespacedName{
			Namespace: zone.Namespace,
			Name:      strings.ToLower(d),
		}

		r.logger.Debug("get for zone", "zone", zone.Name, "device", d)

		if err = r.Get(ctx, nsn, device); err != nil {
			errs = append(errs, err)
			continue
		}

		if device.Labels == nil {
			device.Labels = make(map[string]string)
		}

		device.Labels[iot.DeviceZoneLabel] = zone.Name

		if err = r.Update(ctx, device); err != nil {
			errs = append(errs, err)
			continue
		}
	}

	if len(errs) > 0 {
		span.SetStatus(codes.Error, fmt.Sprintf("%s", errors.Join(errs...)))
		return errors.Join(errs...)
	}

	return nil
}

func handleErr(span trace.Span, err error) {
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "ok")
	}
	span.End()
}
