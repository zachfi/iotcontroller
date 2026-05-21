package conditioner

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// stubComputer is a test-only Computer that returns a fixed
// ApplyValues per axis. Registered under unique names so tests don't
// step on each other's registry entries.
type stubComputer struct {
	vals computer.ApplyValues
}

func (s stubComputer) Compute(_ context.Context, _ time.Time, _ computer.Location, _ map[string]string) (computer.ApplyValues, error) {
	return s.vals, nil
}

// registerStub registers a stubComputer under a unique name and
// returns the name. Tests should use distinct names so the registry
// stays predictable across package-parallel runs.
func registerStub(t *testing.T, name string, vals computer.ApplyValues) string {
	t.Helper()
	computer.Register(name, stubComputer{vals: vals})
	return name
}

// newActivation builds a proto Activation for the tests. Defaults to
// SOURCE_KIND_MANUAL + PUSH_POLICY_REFRESH; tests override as needed.
func newActivation(computerName, sourceName string, pushedAt time.Time, ttl time.Duration, priority int32) *iotv1proto.Activation {
	return &iotv1proto.Activation{
		ComputerName: computerName,
		SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_MANUAL,
		SourceName:   sourceName,
		PushedAt:     timestamppb.New(pushedAt),
		Ttl:          durationpb.New(ttl),
		Priority:     priority,
		PushPolicy:   iotv1proto.PushPolicy_PUSH_POLICY_REFRESH,
	}
}

// TestStack_PushSingleActivation_TopReturnsIt — the simplest possible
// invariant: push one Activation, top() returns it.
func TestStack_PushSingleActivation_TopReturnsIt(t *testing.T) {
	name := registerStub(t, "stub-stack-single", computer.ApplyValues{
		State: iotv1proto.ZoneState_ZONE_STATE_ON,
	})

	p := newZonePolicy("foyer")
	now := time.Unix(1000, 0)
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(name, "src-a", now, 5*time.Minute, 50),
		nil,
	))

	top := p.stack(iotv1proto.AxisKind_AXIS_KIND_STATE).top(now)
	require.NotNil(t, top, "single push should yield a top")
	require.Equal(t, name, top.ComputerName)
	require.Equal(t, "SOURCE_KIND_MANUAL:src-a", top.id)
}

// TestStack_PriorityResolution_HigherWins — two Activations on the
// same axis, different priorities. The higher-priority one wins
// regardless of push order.
func TestStack_PriorityResolution_HigherWins(t *testing.T) {
	low := registerStub(t, "stub-prio-low", computer.ApplyValues{State: iotv1proto.ZoneState_ZONE_STATE_OFF})
	high := registerStub(t, "stub-prio-high", computer.ApplyValues{State: iotv1proto.ZoneState_ZONE_STATE_ON})

	p := newZonePolicy("z")
	now := time.Unix(1000, 0)
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(low, "low", now, time.Hour, 10),
		nil,
	))
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(high, "high", now, time.Hour, 50),
		nil,
	))

	top := p.stack(iotv1proto.AxisKind_AXIS_KIND_STATE).top(now)
	require.NotNil(t, top)
	require.Equal(t, high, top.ComputerName, "higher priority should win")
}

// TestStack_TieBreaker_NewerPushedAtWins — equal priority is broken
// by pushed_at (newer wins). This matches today's last-write-wins
// semantic — operators get the recency they expect even within a
// priority tier.
func TestStack_TieBreaker_NewerPushedAtWins(t *testing.T) {
	older := registerStub(t, "stub-tie-older", computer.ApplyValues{Brightness: iotv1proto.Brightness_BRIGHTNESS_DIM})
	newer := registerStub(t, "stub-tie-newer", computer.ApplyValues{Brightness: iotv1proto.Brightness_BRIGHTNESS_FULL})

	p := newZonePolicy("z")
	earlier := time.Unix(1000, 0)
	later := time.Unix(2000, 0)
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		newActivation(older, "older", earlier, time.Hour, 50),
		nil,
	))
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		newActivation(newer, "newer", later, time.Hour, 50),
		nil,
	))

	top := p.stack(iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS).top(later)
	require.NotNil(t, top)
	require.Equal(t, newer, top.ComputerName, "newer pushed_at should win on priority tie")
}

// TestStack_TTLExpiration_TopPopsToBackground — push a background
// (TTL=0) Activation, then a higher-priority overlay with TTL=5m.
// Before TTL: overlay wins. After TTL: background reveals.
func TestStack_TTLExpiration_TopPopsToBackground(t *testing.T) {
	bg := registerStub(t, "stub-bg", computer.ApplyValues{State: iotv1proto.ZoneState_ZONE_STATE_OFF})
	overlay := registerStub(t, "stub-overlay", computer.ApplyValues{State: iotv1proto.ZoneState_ZONE_STATE_ON})

	p := newZonePolicy("z")
	t0 := time.Unix(1000, 0)
	// Background — TTL=0 means never expires.
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(bg, "background", t0, 0, 10),
		nil,
	))
	// Overlay — TTL=5m, higher priority.
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(overlay, "motion", t0, 5*time.Minute, 50),
		nil,
	))

	// Before TTL: overlay wins.
	top := p.stack(iotv1proto.AxisKind_AXIS_KIND_STATE).top(t0.Add(4 * time.Minute))
	require.NotNil(t, top)
	require.Equal(t, overlay, top.ComputerName, "before TTL: overlay wins")

	// After TTL: overlay expired, background reveals.
	top = p.stack(iotv1proto.AxisKind_AXIS_KIND_STATE).top(t0.Add(6 * time.Minute))
	require.NotNil(t, top)
	require.Equal(t, bg, top.ComputerName, "after TTL: background reveals")
}

// TestStack_RefreshPushUpdatesExisting — same source re-pushes
// refresh pushed_at + ttl in place, no stack growth. PUSH_POLICY_
// REFRESH is the default; this is the motion-event semantic — every
// new occupancy=true refreshes the 5m TTL.
func TestStack_RefreshPushUpdatesExisting(t *testing.T) {
	name := registerStub(t, "stub-refresh", computer.ApplyValues{State: iotv1proto.ZoneState_ZONE_STATE_ON})

	p := newZonePolicy("z")
	t0 := time.Unix(1000, 0)
	t1 := t0.Add(2 * time.Minute)

	// First push at t0 with 5m TTL.
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(name, "motion", t0, 5*time.Minute, 50),
		nil,
	))

	// Second push at t1 (same source) — REFRESH. Same id, no new entry.
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(name, "motion", t1, 5*time.Minute, 50),
		nil,
	))

	s := p.stack(iotv1proto.AxisKind_AXIS_KIND_STATE)
	require.Len(t, s.entries, 1, "REFRESH should not grow the stack")

	// At t0+6m: original would have expired (t0+5m). Refreshed
	// extends to t1+5m = t0+7m. So at t0+6m: still active.
	top := s.top(t0.Add(6 * time.Minute))
	require.NotNil(t, top, "TTL should be refreshed by second push")

	// At t0+8m: even refreshed TTL has elapsed.
	top = s.top(t0.Add(8 * time.Minute))
	require.Nil(t, top, "after refreshed TTL elapses, top is nil")
}

// TestStack_ReplacePushSwapsEntry — PUSH_POLICY_REPLACE swaps the
// entry entirely so new args can take effect. Used when an operator
// wants the second push's args to win, not just refresh timing.
func TestStack_ReplacePushSwapsEntry(t *testing.T) {
	first := registerStub(t, "stub-replace-first", computer.ApplyValues{Brightness: iotv1proto.Brightness_BRIGHTNESS_DIM})
	second := registerStub(t, "stub-replace-second", computer.ApplyValues{Brightness: iotv1proto.Brightness_BRIGHTNESS_FULL})

	p := newZonePolicy("z")
	t0 := time.Unix(1000, 0)
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		newActivation(first, "button", t0, time.Hour, 50),
		nil,
	))

	replace := newActivation(second, "button", t0.Add(time.Minute), time.Hour, 50)
	replace.PushPolicy = iotv1proto.PushPolicy_PUSH_POLICY_REPLACE
	require.NoError(t, p.pushActivation(iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS, replace, nil))

	s := p.stack(iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS)
	require.Len(t, s.entries, 1, "REPLACE should not grow the stack")
	require.Equal(t, second, s.entries[0].ComputerName, "REPLACE should swap to new Computer")
}

// TestStack_MultiAxisIndependence — Activations on different axes
// don't interfere. A push to state doesn't affect brightness, etc.
func TestStack_MultiAxisIndependence(t *testing.T) {
	stateCmp := registerStub(t, "stub-multi-state", computer.ApplyValues{State: iotv1proto.ZoneState_ZONE_STATE_ON})
	brightCmp := registerStub(t, "stub-multi-bright", computer.ApplyValues{Brightness: iotv1proto.Brightness_BRIGHTNESS_FULL})

	p := newZonePolicy("z")
	now := time.Unix(1000, 0)
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(stateCmp, "s", now, time.Hour, 50),
		nil,
	))
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		newActivation(brightCmp, "b", now, time.Hour, 50),
		nil,
	))

	require.NotNil(t, p.stack(iotv1proto.AxisKind_AXIS_KIND_STATE).top(now))
	require.NotNil(t, p.stack(iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS).top(now))
	require.Nil(t, p.stack(iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE).top(now), "untouched axis stays empty")
}

// TestStack_RemoveExpired_Bookkeeping — removeExpired drops expired
// entries from the slice. Memory-bookkeeping, not a safety gate (top()
// also filters via expired()).
func TestStack_RemoveExpired_Bookkeeping(t *testing.T) {
	name := registerStub(t, "stub-remove-expired", computer.ApplyValues{})

	p := newZonePolicy("z")
	t0 := time.Unix(1000, 0)
	// Three pushes with short TTLs.
	for i, src := range []string{"a", "b", "c"} {
		require.NoError(t, p.pushActivation(
			iotv1proto.AxisKind_AXIS_KIND_STATE,
			newActivation(name, src, t0, time.Duration(i+1)*time.Minute, 50),
			nil,
		))
	}

	s := p.stack(iotv1proto.AxisKind_AXIS_KIND_STATE)
	require.Len(t, s.entries, 3)

	// At t0+2.5m: 'a' (TTL=1m) and 'b' (TTL=2m) are expired. 'c' (TTL=3m) is alive.
	s.removeExpired(t0.Add(150 * time.Second))
	require.Len(t, s.entries, 1, "removeExpired should drop expired entries")
	require.Equal(t, "SOURCE_KIND_MANUAL:c", s.entries[0].id)
}

// TestStack_PushUnknownComputer_Errors — surfaces operator typos.
// Computer name resolution happens at push time so misspellings show
// up immediately rather than at next eval tick.
func TestStack_PushUnknownComputer_Errors(t *testing.T) {
	p := newZonePolicy("z")
	now := time.Unix(1000, 0)
	err := p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation("not-a-real-computer", "x", now, time.Hour, 50),
		nil,
	)
	require.Error(t, err, "unknown Computer name should surface as an error at push time")
	require.Contains(t, err.Error(), "unknown computer")
}

// TestStack_ApplyTopToValues_OneComputerPerAxis — enforces the "one
// Computer per axis" semantic. A Computer's output is folded into
// the target ONLY for the axis it was pushed onto. If a state-axis
// Computer accidentally sets Brightness in its ApplyValues, the
// brightness-axis-Computer's value still wins for brightness.
func TestStack_ApplyTopToValues_OneComputerPerAxis(t *testing.T) {
	// A "state" Computer that ALSO returns a brightness value (which
	// it shouldn't be allowed to write).
	misbehaving := registerStub(t, "stub-misbehaving-state", computer.ApplyValues{
		State:      iotv1proto.ZoneState_ZONE_STATE_ON,
		Brightness: iotv1proto.Brightness_BRIGHTNESS_FULL,
	})
	// A proper brightness Computer.
	brightCmp := registerStub(t, "stub-proper-bright", computer.ApplyValues{
		Brightness: iotv1proto.Brightness_BRIGHTNESS_DIM,
	})

	p := newZonePolicy("z")
	now := time.Unix(1000, 0)
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(misbehaving, "s", now, time.Hour, 50),
		nil,
	))
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		newActivation(brightCmp, "b", now, time.Hour, 50),
		nil,
	))

	target, err := p.applyTopToValues(context.Background(), now, computer.Location{})
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, target.State)
	require.Equal(t, iotv1proto.Brightness_BRIGHTNESS_DIM, target.Brightness,
		"brightness axis owner wins; state Computer's brightness output is ignored")
}

// TestStack_Snapshot_StableOrder — snapshot() returns entries in a
// stable, diffable order (priority desc, pushed_at desc, id asc).
// Used by the audit-trail / observability layer; stability matters
// so log lines and metric series are comparable across ticks.
func TestStack_Snapshot_StableOrder(t *testing.T) {
	name := registerStub(t, "stub-snapshot", computer.ApplyValues{})

	p := newZonePolicy("z")
	t0 := time.Unix(1000, 0)
	// Push in deliberately scrambled order.
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(name, "low-prio", t0, time.Hour, 10),
		nil,
	))
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(name, "high-prio", t0, time.Hour, 100),
		nil,
	))
	require.NoError(t, p.pushActivation(
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		newActivation(name, "mid-prio", t0, time.Hour, 50),
		nil,
	))

	snap := p.stack(iotv1proto.AxisKind_AXIS_KIND_STATE).snapshot()
	require.Len(t, snap, 3)
	require.Equal(t, "SOURCE_KIND_MANUAL:high-prio", snap[0].id, "highest priority first")
	require.Equal(t, "SOURCE_KIND_MANUAL:mid-prio", snap[1].id, "middle priority second")
	require.Equal(t, "SOURCE_KIND_MANUAL:low-prio", snap[2].id, "lowest priority last")
}

// silenceLogger returns a no-op slog.Logger for tests that want to
// pass a logger but don't want output. Kept for future tests that
// exercise pushActivation's logger path.
func silenceLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// _ keeps silenceLogger from triggering an unused-function lint
// before any test uses it.
var _ = silenceLogger
