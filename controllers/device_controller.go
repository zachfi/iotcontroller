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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	iotv1 "github.com/zachfi/iotcontroller/api/v1"
)

// DeviceReconciler reconciles a Device object
type DeviceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	tracer     trace.Tracer
	mqttclient mqtt.Client
}

//+kubebuilder:rbac:groups=iot.iot,resources=devices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=iot.iot,resources=devices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=iot.iot,resources=devices/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Device object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *DeviceReconciler) Reconcile(rctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error
	// logger := log.FromContext(rctx)

	attributes := []attribute.KeyValue{
		attribute.String("req", req.String()),
		attribute.String("namespace", req.Namespace),
	}
	_, span := r.tracer.Start(rctx, "Reconcile", trace.WithAttributes(attributes...))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iotv1.Device{}).
		Complete(r)
}

func (r *DeviceReconciler) SetMQTTClient(client mqtt.Client) {
	if client != nil {
		r.mqttclient = client
	}
}

func (r *DeviceReconciler) SetTracer(tracer trace.Tracer) {
	if tracer != nil {
		r.tracer = tracer
	}
}
