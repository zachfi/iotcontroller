package conditioner

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

func TestOOBInvalidation_StatusDriftDropsCache(t *testing.T) {
	// Activate seeds the cache with state ON. Then something else
	// (button press, second-source alert) moves the zone to OFF via
	// Status. The next activate must see the drift and re-apply, not
	// suppress as a redundant call.
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{zones: map[string]apiv1.Zone{
		"office": {Status: apiv1.ZoneStatus{
			State: iotv1proto.ZoneState_ZONE_STATE_ON.String(),
		}},
	}}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "on", InactiveState: "off"}

	// First activate seeds cache with state=ON. Zone CR Status agrees.
	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount(), "first activate writes")

	// Simulate an out-of-band actor moving the zone to OFF without
	// telling the conditioner.
	z := kube.zones["office"]
	z.Status.State = iotv1proto.ZoneState_ZONE_STATE_OFF.String()
	kube.zones["office"] = z

	// Second activate — same desired state ON, but Status now says OFF.
	// Without OOB invalidation this would be suppressed as a redundant
	// "we already applied ON" call. With OOB it re-applies.
	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 2, rec.setStateCount(), "OOB drift forces re-apply")
}

func TestOOBInvalidation_AgreeingStatusKeepsCache(t *testing.T) {
	// Activate seeds the cache. The Zone CR Status agrees with the
	// cache's stored state. Subsequent activates suppress normally
	// — no spurious invalidations.
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{zones: map[string]apiv1.Zone{
		"office": {Status: apiv1.ZoneStatus{
			State: iotv1proto.ZoneState_ZONE_STATE_ON.String(),
		}},
	}}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "on", InactiveState: "off"}

	for i := 0; i < 5; i++ {
		require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	}
	require.Equal(t, 1, rec.setStateCount(), "agreeing Status → cache holds, suppressions count")
}

func TestOOBInvalidation_EmptyStatusKeepsCache(t *testing.T) {
	// First activate writes to ZoneKeeper, but the Status field on the
	// Zone CR hasn't been populated yet (the apiserver write hadn't
	// propagated back to the informer cache in time for the next
	// activate). Conservative: don't drop the cache on empty Status,
	// otherwise we'd ping-pong every activate while Status catches up.
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{zones: map[string]apiv1.Zone{
		"office": {Status: apiv1.ZoneStatus{State: ""}},
	}}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "on", InactiveState: "off"}

	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount(), "empty Status keeps cache (conservative)")
}

func TestOOBInvalidation_ReadFailureKeepsCache(t *testing.T) {
	// Zone CR doesn't exist in the cache (kube.zones map empty);
	// fakeKubeClient returns errExpectError. The OOB check must
	// gracefully degrade — return false (no drift detected) so a
	// transient kube blip doesn't blow every cache entry.
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "on", InactiveState: "off"}

	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount(), "kube read failure keeps cache (conservative)")
}

func TestOOBInvalidation_UnknownStatusValueKeepsCache(t *testing.T) {
	// Status contains a string that doesn't map to any known ZoneState
	// enum (e.g. a future state we don't recognize, or a corruption).
	// Conservative: treat as no-drift rather than blow the cache.
	cfg := Config{ApplyDesiredRefreshAge: time.Hour}
	rec := &recordingZoneKeeper{}
	kube := &fakeKubeClient{zones: map[string]apiv1.Zone{
		"office": {Status: apiv1.ZoneStatus{State: "ZONE_STATE_MARTIAN"}},
	}}
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	ctx := context.Background()

	c, err := New(cfg, testLogger, rec, kube)
	require.NoError(t, err)

	rem := apiv1.Remediation{Zone: "office", ActiveState: "on", InactiveState: "off"}

	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.NoError(t, c.activateRemediation(ctx, "cond", rem))
	require.Equal(t, 1, rec.setStateCount(), "unknown Status value keeps cache (conservative)")
}
