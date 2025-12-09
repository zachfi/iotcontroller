package util

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	v1 "github.com/zachfi/iotcontroller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewZone creates a ready‑to‑apply Zone object.
func NewZone(name string, devices, colors []string) *v1.Zone {
	return &v1.Zone{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Zone",
			APIVersion: "iot.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default", // change if you need a different namespace
		},
		Spec: v1.ZoneSpec{
			Devices: devices,
			Colors:  colors,
		},
	}
}

// ApplyZone creates or updates a Zone using the controller‑runtime client.
func ApplyZone(t *testing.T, cli client.Client, z *v1.Zone) {
	ctx := context.Background()
	// Try to fetch first – if it doesn't exist, create it
	err := cli.Get(ctx, client.ObjectKey{Name: z.Name, Namespace: z.Namespace}, z)
	if err != nil {
		// not found
		require.NoError(t, cli.Create(ctx, z), "creating zone")
	} else {
		// exists, update the spec
		require.NoError(t, cli.Update(ctx, z), "updating zone")
	}
}

// DeleteZone deletes the zone (used in cleanup or test isolation).
func DeleteZone(t *testing.T, cli client.Client, name, ns string) {
	ctx := context.Background()
	z := &v1.Zone{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	require.NoError(t, cli.Delete(ctx, z), "deleting zone")
}

// GetZone fetches a zone and optionally blocks until its status reaches the expected value.
func GetZone(t *testing.T, cli client.Client, name, ns string) *v1.Zone {
	ctx := context.Background()
	z := &v1.Zone{}
	require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, z), "getting zone")
	return z
}

// WaitForZoneState waits until the Zone's status.State equal expState or times out.
func WaitForZoneState(t *testing.T, cli client.Client, name, ns, expState string, timeout time.Duration) {
	ctx := context.Background()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	dead := time.Now().Add(timeout)

	for {
		if time.Now().After(dead) {
			t.Fatalf("timeout waiting for zone %s state=%s", name, expState)
		}
		z := &v1.Zone{}
		require.NoError(t, cli.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, z))
		t.Logf("state: %v", z.Status.State)
		if z.Status.State == expState {
			return
		}
		<-ticker.C
	}
}
