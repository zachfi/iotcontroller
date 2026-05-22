package conditioner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
	"github.com/zachfi/iotcontroller/pkg/mocks"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// recordingZoneKeeperForReconciler embeds the existing
// mocks.ZoneKeeperClientMock so it inherits all client-interface
// methods, then overrides ApplyValues to record calls and return an
// optional pre-set error. The other methods (SetState, SetScene,
// etc.) panic so any reconciler contract violation surfaces loudly —
// the reconciler must only call ApplyValues.
type recordingZoneKeeperForReconciler struct {
	mocks.ZoneKeeperClientMock

	mu              sync.Mutex
	applyValuesReqs []*iotv1proto.ApplyValuesRequest
	applyValuesErr  error
}

func (r *recordingZoneKeeperForReconciler) ApplyValues(_ context.Context, req *iotv1proto.ApplyValuesRequest, _ ...grpc.CallOption) (*iotv1proto.ApplyValuesResponse, error) {
	r.mu.Lock()
	r.applyValuesReqs = append(r.applyValuesReqs, req)
	err := r.applyValuesErr
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &iotv1proto.ApplyValuesResponse{}, nil
}

// SetState / SetScene / AdjustBrightness panic — the reconciler must
// only call ApplyValues. Contract violation = test failure.
func (r *recordingZoneKeeperForReconciler) SetState(_ context.Context, _ *iotv1proto.SetStateRequest, _ ...grpc.CallOption) (*iotv1proto.SetStateResponse, error) {
	panic("reconciler must only call ApplyValues, not SetState")
}

func (r *recordingZoneKeeperForReconciler) SetScene(_ context.Context, _ *iotv1proto.SetSceneRequest, _ ...grpc.CallOption) (*iotv1proto.SetSceneResponse, error) {
	panic("reconciler must only call ApplyValues, not SetScene")
}

func (r *recordingZoneKeeperForReconciler) AdjustBrightness(_ context.Context, _ *iotv1proto.AdjustBrightnessRequest, _ ...grpc.CallOption) (*iotv1proto.AdjustBrightnessResponse, error) {
	panic("reconciler must only call ApplyValues, not AdjustBrightness")
}

func (r *recordingZoneKeeperForReconciler) applyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.applyValuesReqs)
}

func (r *recordingZoneKeeperForReconciler) lastApply() *iotv1proto.ApplyValuesRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.applyValuesReqs) == 0 {
		return nil
	}
	return r.applyValuesReqs[len(r.applyValuesReqs)-1]
}

// fixedClock pins the reconciler's `now` to a known time so test
// Activations with synthetic PushedAt + TTL don't appear expired by
// the time the reconciler runs. Default base is Unix(1000, 0);
// tests that exercise TTL expiration mutate this between calls.
type fixedClock struct {
	t time.Time
}

func (c *fixedClock) now() time.Time { return c.t }

// newTestReconciler builds a Reconciler with the test ZoneKeeper, a
// fixed clock (pinned to Unix(1000, 0)), and a no-op afterFunc.
// Tests that need to advance time mutate the returned clock.
func newTestReconciler(t *testing.T) (*Reconciler, *recordingZoneKeeperForReconciler, *fixedClock) {
	t.Helper()
	zk := &recordingZoneKeeperForReconciler{}
	r := NewReconciler(zk, computer.Location{}, nil, nil)
	clock := &fixedClock{t: time.Unix(1000, 0)}
	r.now = clock.now
	// No-op afterFunc — real timers would race with the test goroutine.
	r.afterFunc = func(_ time.Duration, _ func()) reconcileTimer { return noopTimer{} }
	return r, zk, clock
}

type noopTimer struct{}

// Stop returns true to match time.Timer's contract: the timer was
// active when Stop was called. Tests don't currently inspect this
// return value, but matching the documented semantic avoids surprising
// any future caller that checks it for cleanup decisions.
func (noopTimer) Stop() bool { return true }

// TestReconciler_PushAndReconcile_AppliesTarget — the happy path.
// Push one Activation onto the state axis; ReconcileZone composes
// the target and calls ApplyValues once with the expected state.
func TestReconciler_PushAndReconcile_AppliesTarget(t *testing.T) {
	computer.Register("stub-reconciler-on", stubComputer{vals: computer.ApplyValues{
		State: iotv1proto.ZoneState_ZONE_STATE_ON,
	}})

	r, zk, _ := newTestReconciler(t)
	ctx := context.Background()

	act := &iotv1proto.Activation{
		ComputerName: "stub-reconciler-on",
		SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
		SourceName:   "test-push",
		PushedAt:     timestamppb.New(time.Unix(1000, 0)),
		Ttl:          durationpb.New(time.Hour),
		Priority:     50,
	}
	require.NoError(t, r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_STATE, act))

	require.Equal(t, 1, zk.applyCount(), "single push should produce one ApplyValues call")
	got := zk.lastApply()
	require.Equal(t, "test-zone", got.Name)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, got.State)
}

// TestReconciler_RepeatReconcile_NoApply — calling ReconcileZone
// twice with the same composed target results in exactly one
// ApplyValues call. The per-zone lastApplied cache absorbs repeats,
// matching the imperative path's applyDesired semantic at zone
// granularity.
func TestReconciler_RepeatReconcile_NoApply(t *testing.T) {
	computer.Register("stub-reconciler-repeat", stubComputer{vals: computer.ApplyValues{
		State: iotv1proto.ZoneState_ZONE_STATE_ON,
	}})

	r, zk, _ := newTestReconciler(t)
	ctx := context.Background()

	require.NoError(t, r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_STATE,
		&iotv1proto.Activation{
			ComputerName: "stub-reconciler-repeat",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "src",
			PushedAt:     timestamppb.New(time.Unix(1000, 0)),
			Ttl:          durationpb.New(time.Hour),
			Priority:     50,
		}))
	require.NoError(t, r.ReconcileZone(ctx, "test-zone", time.Unix(2000, 0)))
	require.NoError(t, r.ReconcileZone(ctx, "test-zone", time.Unix(3000, 0)))

	require.Equal(t, 1, zk.applyCount(), "repeated reconciles with same target produce one ApplyValues")
}

// TestReconciler_TargetChange_AppliesAgain — pushing a different
// Activation that changes the composed target produces a new
// ApplyValues call. Demonstrates the delta path: change in target =
// new flush.
func TestReconciler_TargetChange_AppliesAgain(t *testing.T) {
	computer.Register("stub-reconciler-low", stubComputer{vals: computer.ApplyValues{
		Brightness: iotv1proto.Brightness_BRIGHTNESS_DIM,
	}})
	computer.Register("stub-reconciler-high", stubComputer{vals: computer.ApplyValues{
		Brightness: iotv1proto.Brightness_BRIGHTNESS_FULL,
	}})

	r, zk, _ := newTestReconciler(t)
	ctx := context.Background()

	// First push: brightness=DIM
	require.NoError(t, r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		&iotv1proto.Activation{
			ComputerName: "stub-reconciler-low",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "low",
			PushedAt:     timestamppb.New(time.Unix(1000, 0)),
			Ttl:          durationpb.New(time.Hour),
			Priority:     50,
		}))
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_DIM, zk.lastApply().Brightness)

	// Second push (higher priority): brightness=FULL
	require.NoError(t, r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		&iotv1proto.Activation{
			ComputerName: "stub-reconciler-high",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "high",
			PushedAt:     timestamppb.New(time.Unix(2000, 0)),
			Ttl:          durationpb.New(time.Hour),
			Priority:     100,
		}))

	require.Equal(t, 2, zk.applyCount(), "target change should produce a second ApplyValues")
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_FULL, zk.lastApply().Brightness)
}

// TestReconciler_UnknownZone_NoOp — ReconcileZone on a zone that has
// no policy returns nil with zero ApplyValues calls. Unmanaged zones
// stay unmanaged.
func TestReconciler_UnknownZone_NoOp(t *testing.T) {
	r, zk, _ := newTestReconciler(t)
	require.NoError(t, r.ReconcileZone(context.Background(), "never-pushed-to", time.Now()))
	require.Equal(t, 0, zk.applyCount(), "unmanaged zone produces no ApplyValues")
}

// TestReconciler_EmptyTarget_Suppressed — a zone known to the
// reconciler but with no contributing Activations (e.g. all overrides
// expired and no background defined) does NOT flush an
// all-UNSPECIFIED target. Operators get the same behavior as today's
// imperative path: no Condition active = no apply.
func TestReconciler_EmptyTarget_Suppressed(t *testing.T) {
	computer.Register("stub-empty", stubComputer{vals: computer.ApplyValues{
		State: iotv1proto.ZoneState_ZONE_STATE_ON,
	}})

	r, zk, _ := newTestReconciler(t)
	ctx := context.Background()

	// Push with a short TTL.
	require.NoError(t, r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_STATE,
		&iotv1proto.Activation{
			ComputerName: "stub-empty",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "src",
			PushedAt:     timestamppb.New(time.Unix(1000, 0)),
			Ttl:          durationpb.New(time.Minute),
			Priority:     50,
		}))
	require.Equal(t, 1, zk.applyCount())

	// Reconcile well past TTL — all entries expired, stack is empty,
	// target is empty.
	require.NoError(t, r.ReconcileZone(ctx, "test-zone", time.Unix(1000, 0).Add(time.Hour)))
	require.Equal(t, 1, zk.applyCount(), "empty target should not produce a new ApplyValues")
}

// TestReconciler_ApplyError_Surfaces — when ZoneKeeper.ApplyValues
// returns an error, the reconciler surfaces it AND does not update
// lastApplied. The next reconcile re-attempts the flush.
func TestReconciler_ApplyError_Surfaces(t *testing.T) {
	computer.Register("stub-apply-error", stubComputer{vals: computer.ApplyValues{
		State: iotv1proto.ZoneState_ZONE_STATE_ON,
	}})

	r, zk, _ := newTestReconciler(t)
	zk.applyValuesErr = errors.New("simulated downstream gRPC failure")
	ctx := context.Background()

	err := r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_STATE,
		&iotv1proto.Activation{
			ComputerName: "stub-apply-error",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "src",
			PushedAt:     timestamppb.New(time.Unix(1000, 0)),
			Ttl:          durationpb.New(time.Hour),
			Priority:     50,
		})
	require.Error(t, err, "PushActivation should surface the downstream apply error")

	// Recover: clear the error, reconcile again, should succeed.
	zk.mu.Lock()
	zk.applyValuesErr = nil
	zk.mu.Unlock()

	require.NoError(t, r.ReconcileZone(ctx, "test-zone", time.Unix(1500, 0)))
	require.Equal(t, 2, zk.applyCount(), "retry after error should produce a fresh ApplyValues attempt")
}

// TestReconciler_MultiAxisCompose — push different Computers to
// different axes; the composed ApplyValues carries one value per
// axis from the corresponding stack's top. Demonstrates the "one
// Computer per axis" enforcement at the apply boundary.
func TestReconciler_MultiAxisCompose(t *testing.T) {
	computer.Register("stub-multi-state", stubComputer{vals: computer.ApplyValues{
		State: iotv1proto.ZoneState_ZONE_STATE_ON,
	}})
	computer.Register("stub-multi-bright", stubComputer{vals: computer.ApplyValues{
		Brightness: iotv1proto.Brightness_BRIGHTNESS_FULL,
	}})
	computer.Register("stub-multi-ct", stubComputer{vals: computer.ApplyValues{
		ColorTemperatureKelvin: 4000,
		ColorTemperature:       iotv1proto.ColorTemperature_COLOR_TEMPERATURE_MORNING,
	}})

	r, zk, _ := newTestReconciler(t)
	ctx := context.Background()
	pushedAt := time.Unix(1000, 0)

	// State axis
	require.NoError(t, r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_STATE,
		&iotv1proto.Activation{
			ComputerName: "stub-multi-state",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "s",
			PushedAt:     timestamppb.New(pushedAt),
			Ttl:          durationpb.New(time.Hour),
			Priority:     50,
		}))
	// Brightness axis
	require.NoError(t, r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		&iotv1proto.Activation{
			ComputerName: "stub-multi-bright",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "b",
			PushedAt:     timestamppb.New(pushedAt),
			Ttl:          durationpb.New(time.Hour),
			Priority:     50,
		}))
	// CT axis
	require.NoError(t, r.PushActivation(ctx, "test-zone", iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE,
		&iotv1proto.Activation{
			ComputerName: "stub-multi-ct",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "ct",
			PushedAt:     timestamppb.New(pushedAt),
			Ttl:          durationpb.New(time.Hour),
			Priority:     50,
		}))

	// Three PushActivation calls = three ReconcileZone invocations.
	// First push applies state=ON, second adds brightness=FULL (delta,
	// applies), third adds CT (delta, applies). So 3 applies.
	require.Equal(t, 3, zk.applyCount())
	got := zk.lastApply()
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, got.State)
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_FULL, got.Brightness)
	require.Equal(t, int32(4000), got.ColorTemperatureKelvin)
}

// TestReconciler_HasPolicy — `hasPolicy` is the reconciler-internal
// "do I have any state for this zone?" check. Distinct from
// Conditioner.isReconcileManaged, which is the config-driven routing
// decision. A zone in cfg.ReconcileZones that has never received a
// push is "managed" by the controller (routed to the reconciler) but
// has no policy in the reconciler (empty stack → no-op tick).
func TestReconciler_HasPolicy(t *testing.T) {
	computer.Register("stub-managed", stubComputer{vals: computer.ApplyValues{
		State: iotv1proto.ZoneState_ZONE_STATE_ON,
	}})

	r, _, _ := newTestReconciler(t)
	require.False(t, r.hasPolicy("never-pushed"), "unknown zone is not managed")

	require.NoError(t, r.PushActivation(context.Background(), "test-zone", iotv1proto.AxisKind_AXIS_KIND_STATE,
		&iotv1proto.Activation{
			ComputerName: "stub-managed",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "src",
			PushedAt:     timestamppb.New(time.Unix(1000, 0)),
			Ttl:          durationpb.New(time.Hour),
			Priority:     50,
		}))
	require.True(t, r.hasPolicy("test-zone"), "pushed-to zone is managed")
	require.False(t, r.hasPolicy("other-zone"), "other zone still not managed")
}

// TestReconciler_PushUnknownComputer_Errors — operator typo in
// computer_name surfaces at push time. The reconciler doesn't
// silently accept and ignore later.
func TestReconciler_PushUnknownComputer_Errors(t *testing.T) {
	r, zk, _ := newTestReconciler(t)
	err := r.PushActivation(context.Background(), "test-zone", iotv1proto.AxisKind_AXIS_KIND_STATE,
		&iotv1proto.Activation{
			ComputerName: "this-computer-does-not-exist",
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
			SourceName:   "src",
			PushedAt:     timestamppb.New(time.Unix(1000, 0)),
			Ttl:          durationpb.New(time.Hour),
			Priority:     50,
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown computer")
	require.Equal(t, 0, zk.applyCount(), "failed push must not produce an ApplyValues")
}

// TestReconciler_ValidationErrors — required fields are checked at
// the API surface. Empty zone, AXIS_KIND_UNSPECIFIED, nil activation
// all produce errors.
func TestReconciler_ValidationErrors(t *testing.T) {
	r, _, _ := newTestReconciler(t)
	ctx := context.Background()

	// Empty zone
	err := r.PushActivation(ctx, "", iotv1proto.AxisKind_AXIS_KIND_STATE, &iotv1proto.Activation{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "zone")

	// AXIS_KIND_UNSPECIFIED
	err = r.PushActivation(ctx, "z", iotv1proto.AxisKind_AXIS_KIND_UNSPECIFIED, &iotv1proto.Activation{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "axis")

	// nil activation
	err = r.PushActivation(ctx, "z", iotv1proto.AxisKind_AXIS_KIND_STATE, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "activation")
}
