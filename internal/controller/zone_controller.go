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

	"github.com/zachfi/zkit/pkg/boundedwaitgroup"
	"github.com/zachfi/zkit/pkg/tracing"

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
	var (
		err error
		/* logger  = log.FromContext(ctx) */
		zone *iotv1.Zone
	)

	attributes := []attribute.KeyValue{
		attribute.String("name", req.Name),
		attribute.String("namespace", req.Namespace),
	}

	ctx, span := r.tracer.Start(ctx, "Zone.Reconcile", trace.WithAttributes(attributes...))
	defer func() { _ = tracing.ErrHandler(span, err, "reconcile failed", r.logger) }()

	if req.Name == "" {
		err = fmt.Errorf("unable to retrieve zone with empty name")
		return ctrl.Result{}, err
	}

	zone, err = r.getZone(ctx, req)
	if err != nil {
		err = client.IgnoreNotFound(err)
		return ctrl.Result{}, err
	}

	span.SetAttributes(
		attribute.Int("devices", len(zone.Spec.Devices)),
	)

	err = r.syncLabels(ctx, zone)
	if err != nil {
		return ctrl.Result{}, err
	}

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

func (r *ZoneReconciler) getZone(ctx context.Context, req ctrl.Request) (*iotv1.Zone, error) {
	var (
		err  error
		zone = new(iotv1.Zone)
	)

	attributes := []attribute.KeyValue{
		attribute.String("name", req.Name),
		attribute.String("namespace", req.Namespace),
	}

	ctx, span := r.tracer.Start(ctx, "ZoneReconciler.getZone", trace.WithAttributes(attributes...))
	defer func() { _ = tracing.ErrHandler(span, err, "get zone failed", r.logger) }()

	if err = r.Get(ctx, req.NamespacedName, zone); err != nil {
		return nil, err
	}

	return zone, nil
}

// syncLabels updates the zone label on Device objects to match the Zone's
// Spec.Devices list. It first removes the label from any Device that currently
// has it but is not listed in the spec, then adds (or updates) the label on
// each Device named in the spec. The updates are performed concurrently with a
// bounded wait‑group; all errors are collected and returned as a single joined
// error.
func (r *ZoneReconciler) syncLabels(ctx context.Context, zone *iotv1.Zone) error {
	var (
		err     error
		errs    []error
		errChan = make(chan error, len(zone.Spec.Devices))
		bg      = boundedwaitgroup.New(3)
	)

	ctx, span := r.tracer.Start(ctx, "ZoneReconciler.syncLabels")
	defer func() { _ = tracing.ErrHandler(span, err, "sync labels failed", r.logger) }()

	span.SetAttributes(
		attribute.Int("device_count", len(zone.Spec.Devices)),
		attribute.StringSlice("devices", zone.Spec.Devices),
	)

	// Get all the devices with this label to remove the label from those that
	// are not listed in the zone spec.
	deviceList := iotv1.DeviceList{}
	selector := labels.SelectorFromSet(labels.Set{iot.DeviceZoneLabel: zone.Name})
	listOptions := &client.ListOptions{
		LabelSelector: selector,
	}
	err = r.List(ctx, &deviceList, listOptions)
	if err != nil {
		errs = append(errs, err)
	}

	// Remove the non-included
	for _, d := range deviceList.Items {
		if hasName(d.Name, zone.Spec.Devices) {
			continue
		}

		delete(d.Labels, iot.DeviceZoneLabel)

		if err = r.Update(ctx, &d); err != nil {
			return fmt.Errorf("failed to delete device %q from zone %q: %w", d.Name, zone.Name, err)
		}
	}

	// Add the included
	for _, d := range zone.Spec.Devices {
		bg.Add(1)
		go func(name string) {
			defer bg.Done()

			device := &iotv1.Device{}
			nsn := types.NamespacedName{
				Namespace: zone.Namespace,
				Name:      strings.ToLower(name),
			}

			if err = r.Get(ctx, nsn, device); err != nil {
				errChan <- err
				return
			}

			if device.Labels == nil {
				device.Labels = make(map[string]string)
			}

			device.Labels[iot.DeviceZoneLabel] = zone.Name

			if err = r.Update(ctx, device); err != nil {
				errChan <- err
				return
			}
		}(d)
	}

	bg.Add(1)
	go func() {
		defer bg.Done()
	}()

	bg.Wait()
	close(errChan)

	for e := range errChan {
		errs = append(errs, e)
	}

	if len(errs) > 0 {
		err = errors.Join(errs...)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}

	return nil
}

func hasName(s string, ss []string) bool {
	for _, x := range ss {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
