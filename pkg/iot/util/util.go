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
	var (
		err    error
		device apiv1.Device
	)

	nsn := types.NamespacedName{
		Name:      strings.ToLower(name),
		Namespace: namespace,
	}

	err = kubeclient.Get(ctx, nsn, &device)
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = createAPIDevice(ctx, kubeclient, &device, nsn.Name)
			if err != nil {
				return nil, err
			}
		}
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
