package util

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, apiv1.AddToScheme(s))
	return s
}

// TestGetOrCreateAPIDeviceCreatesWhenAbsent: first observation of a device
// — Get returns NotFound, fall through to Create, return the new object.
func TestGetOrCreateAPIDeviceCreatesWhenAbsent(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	d, err := GetOrCreateAPIDevice(context.Background(), c, "0xabc")
	require.NoError(t, err)
	require.Equal(t, "0xabc", d.Name)

	// Confirm it's actually persisted.
	var got apiv1.Device
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "0xabc", Namespace: namespace}, &got))
	require.Equal(t, "0xabc", got.Name)
}

// TestGetOrCreateAPIDeviceReturnsExisting: the CR already exists. Must
// return it without erroring (the bridge/devices handler then updates it).
func TestGetOrCreateAPIDeviceReturnsExisting(t *testing.T) {
	existing := &apiv1.Device{}
	existing.Name = "0xabc"
	existing.Namespace = namespace
	existing.Spec.Vendor = "PreviousVendor"

	c := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(existing).
		Build()

	d, err := GetOrCreateAPIDevice(context.Background(), c, "0xabc")
	require.NoError(t, err)
	require.Equal(t, "PreviousVendor", d.Spec.Vendor,
		"existing device must be returned with its existing fields, not overwritten")
}

// TestGetOrCreateAPIDeviceHandlesAlreadyExistsRace simulates two
// controller-core replicas processing the same bridge/devices payload:
// our Get sees NotFound, then between Get and Create a peer creates the
// object, so our Create gets AlreadyExists. The function must recover by
// re-fetching and returning the now-existing object — not propagate the
// error to the caller (which previously logged "device report failed").
func TestGetOrCreateAPIDeviceHandlesAlreadyExistsRace(t *testing.T) {
	scheme := newScheme(t)
	gvr := schema.GroupVersionResource{Group: apiv1.GroupVersion.Group, Version: apiv1.GroupVersion.Version, Resource: "devices"}

	// Pre-create the object so the fake client returns AlreadyExists on
	// our Create. But we want the FIRST Get to see NotFound (simulating
	// the cache lag where our pod's informer hasn't caught up yet).
	// Use an interceptor to make the first Get return NotFound; subsequent
	// Gets fall through to the underlying store and find it.
	pre := &apiv1.Device{}
	pre.Name = "0xrace"
	pre.Namespace = namespace
	pre.Spec.Vendor = "RaceWinner"

	getCount := 0
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pre).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cli client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				getCount++
				if getCount == 1 {
					return apierrors.NewNotFound(gvr.GroupResource(), key.Name)
				}
				return cli.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	d, err := GetOrCreateAPIDevice(context.Background(), c, "0xrace")
	require.NoError(t, err, "AlreadyExists from a racing peer must be recovered, not surfaced")
	require.Equal(t, "RaceWinner", d.Spec.Vendor,
		"recovered device must carry the peer's already-created spec")
	require.GreaterOrEqual(t, getCount, 2,
		"must re-Get after the racing Create returned AlreadyExists")
}

// TestGetOrCreateAPIDeviceSurfacesNonNotFoundGet: previously, any Get
// error other than NotFound was silently swallowed and an empty Device
// returned. That is the worst kind of failure mode — the caller gets
// no signal and proceeds to Update an unset object. The fix returns the
// raw error to the caller.
func TestGetOrCreateAPIDeviceSurfacesNonNotFoundGet(t *testing.T) {
	wantErr := apierrors.NewServiceUnavailable("apiserver down")
	c := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cli client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return wantErr
			},
		}).
		Build()

	_, err := GetOrCreateAPIDevice(context.Background(), c, "0xabc")
	require.Error(t, err)
	require.Equal(t, wantErr.Error(), err.Error())
}
