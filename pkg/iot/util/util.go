package util

import (
	"context"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
)

const (
	namespace = "iot"
)

func GetOrCreateAPIDevice(ctx context.Context, kubeclient kubeclient.Client, name string) (*apiv1.Device, error) {
	nsn := types.NamespacedName{
		Name:      strings.ToLower(name),
		Namespace: namespace,
	}

	var device apiv1.Device
	err := kubeclient.Get(ctx, nsn, &device)
	if err == nil {
		return &device, nil
	}
	if !apierrors.IsNotFound(err) {
		// Surface real errors instead of silently returning an empty device,
		// which the previous implementation did when the Get failed with
		// anything other than NotFound (e.g. transient API errors, RBAC).
		return nil, err
	}

	// Not found at Get time — try Create. Two controller-core replicas
	// subscribe to the same MQTT topic, so a Create can race with a parallel
	// Create from the peer pod and lose with AlreadyExists. Re-fetch in that
	// case and return the now-existing object so callers can Update it.
	if err = createAPIDevice(ctx, kubeclient, &device, nsn.Name); err == nil {
		return &device, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return nil, err
	}

	device = apiv1.Device{}
	if err := kubeclient.Get(ctx, nsn, &device); err != nil {
		return nil, err
	}
	return &device, nil
}

func UpdateLastSeen(ctx context.Context, kubeclient kubeclient.Client, device *apiv1.Device) error {
	var err error

	if time.Since(time.Unix(int64(device.Status.LastSeen), 0)) < time.Minute {
		return nil
	}

	nsn := types.NamespacedName{
		Name:      strings.ToLower(device.Name),
		Namespace: device.Namespace,
	}

	err = kubeclient.Get(ctx, nsn, device)
	if err != nil {
		return err
	}

	if time.Since(time.Unix(int64(device.Status.LastSeen), 0)) > 1*time.Minute {
		device.Status.LastSeen = uint64(time.Now().Unix())
		return kubeclient.Status().Update(ctx, device)
	}
	return nil
}

func createAPIDevice(ctx context.Context, kubeclient kubeclient.Client, device *apiv1.Device, name string) error {
	var err error

	device.Name = name
	device.Namespace = namespace

	err = kubeclient.Create(ctx, device)
	if err != nil {
		return err
	}

	return nil
}
