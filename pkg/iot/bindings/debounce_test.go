package bindings

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot/events"
)

// debounceBinding builds a Binding fixture with MinDuration. The
// helper exists so each test names only the relevant fields and the
// shared structure stays one piece of code.
func debounceBinding(name, property, value string, minDur time.Duration, conditionName string) apiv1.Binding {
	b := apiv1.Binding{}
	b.Name = name
	b.Spec.Condition = conditionName
	b.Spec.Event.Property = property
	b.Spec.Event.Value = value
	b.Spec.Event.MinDuration = metav1.Duration{Duration: minDur}
	return b
}

// newDebounceMatcher returns a Matcher with a controllable clock so
// tests can simulate elapsed time without sleeping.
func newDebounceMatcher(items []apiv1.Binding, clock func() time.Time) *Matcher {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	m := New(&fakeKC{items: items}, "iot", logger)
	m.now = clock
	return m
}

// newLeakEvent builds a water_leak event for a synthetic pond device.
func newLeakEvent(value string) events.DeviceEvent {
	d := &apiv1.Device{}
	d.Name = "pond-leak-1"
	d.Labels = map[string]string{"iot/zone": "pond"}
	return events.DeviceEvent{
		Property: "water_leak",
		Value:    value,
		Device:   d,
	}
}

func TestDebounce_FiresAfterDwell(t *testing.T) {
	// Binding requires 30s of continuous match before firing. First
	// event at t=0 starts the timer; second event at t=10s is still
	// pending; third event at t=31s fires.
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }

	m := newDebounceMatcher([]apiv1.Binding{
		debounceBinding("pond-leak-fire", "water_leak", "true", 30*time.Second, "pond-pump-on"),
	}, clock)

	require.Equal(t, "", m.FindCondition(context.Background(), newLeakEvent("true")), "first event should start debounce")

	now = now.Add(10 * time.Second)
	require.Equal(t, "", m.FindCondition(context.Background(), newLeakEvent("true")), "still under MinDuration")

	now = now.Add(21 * time.Second) // total elapsed: 31s
	require.Equal(t, "pond-pump-on", m.FindCondition(context.Background(), newLeakEvent("true")), "dwell satisfied; should fire")
}

func TestDebounce_SuppressesAfterFire(t *testing.T) {
	// Once fired, subsequent events for the same value should NOT
	// re-fire. Otherwise pondLeak=true persisting for hours would
	// activate the Condition every minute.
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }

	m := newDebounceMatcher([]apiv1.Binding{
		debounceBinding("pond-leak-fire", "water_leak", "true", 30*time.Second, "pond-pump-on"),
	}, clock)

	// Start + fire.
	_ = m.FindCondition(context.Background(), newLeakEvent("true"))
	now = now.Add(31 * time.Second)
	require.Equal(t, "pond-pump-on", m.FindCondition(context.Background(), newLeakEvent("true")))

	// Now keep arriving for the next hour with the same value.
	for i := 0; i < 60; i++ {
		now = now.Add(time.Minute)
		require.Equal(t, "", m.FindCondition(context.Background(), newLeakEvent("true")), "post-fire same-value events must suppress")
	}
}

func TestDebounce_ValueChangeResets(t *testing.T) {
	// water_leak=true for 25s then drops to false. The "true" timer
	// hadn't fired (25 < 30). Now value=false arrives. Reset the
	// timer for the new (true→false) transition; "false" needs its
	// own 30s before firing the off Condition.
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }

	m := newDebounceMatcher([]apiv1.Binding{
		debounceBinding("pond-leak-fire", "water_leak", "true", 30*time.Second, "pond-pump-on"),
		debounceBinding("pond-leak-clear", "water_leak", "false", 30*time.Second, "pond-pump-off"),
	}, clock)

	// 25s of true.
	_ = m.FindCondition(context.Background(), newLeakEvent("true"))
	now = now.Add(25 * time.Second)
	require.Equal(t, "", m.FindCondition(context.Background(), newLeakEvent("true")))

	// Value transitions to false. New debounce cycle begins for the
	// pond-leak-clear Binding. The pond-leak-fire entry doesn't
	// interfere — it's a separate (binding, device) key.
	now = now.Add(time.Second)
	require.Equal(t, "", m.FindCondition(context.Background(), newLeakEvent("false")), "false starts a fresh debounce")

	// Need 30s of false before pond-pump-off fires.
	now = now.Add(15 * time.Second)
	require.Equal(t, "", m.FindCondition(context.Background(), newLeakEvent("false")), "false still under MinDuration")

	now = now.Add(20 * time.Second) // total false: 35s
	require.Equal(t, "pond-pump-off", m.FindCondition(context.Background(), newLeakEvent("false")), "false dwell satisfied; off Condition fires")
}

func TestDebounce_PerDeviceIsolation(t *testing.T) {
	// Two leak sensors. The Binding has no IEEE/Device selector so
	// both can trigger it. Each device's debounce timer must be
	// independent — sensor A reaching dwell shouldn't fire the
	// Condition on behalf of sensor B, and a value transition on
	// sensor A shouldn't reset sensor B's pending state.
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }

	m := newDebounceMatcher([]apiv1.Binding{
		debounceBinding("pond-leak-fire", "water_leak", "true", 30*time.Second, "pond-pump-on"),
	}, clock)

	devA := &apiv1.Device{}
	devA.Name = "pond-leak-1"
	devA.Labels = map[string]string{"iot/zone": "pond"}
	devB := &apiv1.Device{}
	devB.Name = "pond-leak-2"
	devB.Labels = map[string]string{"iot/zone": "pond"}

	evA := events.DeviceEvent{Property: "water_leak", Value: "true", Device: devA}
	evB := events.DeviceEvent{Property: "water_leak", Value: "true", Device: devB}

	// Start A.
	_ = m.FindCondition(context.Background(), evA)

	// 31s later, A fires (per-device); B never sent an event, so B has
	// no debounce state at all.
	now = now.Add(31 * time.Second)
	require.Equal(t, "pond-pump-on", m.FindCondition(context.Background(), evA))

	// B's first event: starts B's debounce, doesn't fire.
	require.Equal(t, "", m.FindCondition(context.Background(), evB), "device B starts its own timer")

	// 31s later, B fires too.
	now = now.Add(31 * time.Second)
	require.Equal(t, "pond-pump-on", m.FindCondition(context.Background(), evB))
}

func TestDebounce_BackToOriginalValueResets(t *testing.T) {
	// Realistic flapping: true → false → true. The first true
	// reaches dwell and fires. Value goes to false, resetting the
	// (binding, device) entry. Value returns to true — this counts
	// as a NEW debounce cycle (matching old value but the entry was
	// already reset by the intermediate false).
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }

	m := newDebounceMatcher([]apiv1.Binding{
		debounceBinding("pond-leak-fire", "water_leak", "true", 30*time.Second, "pond-pump-on"),
	}, clock)

	_ = m.FindCondition(context.Background(), newLeakEvent("true"))
	now = now.Add(31 * time.Second)
	require.Equal(t, "pond-pump-on", m.FindCondition(context.Background(), newLeakEvent("true")), "first fire")

	now = now.Add(time.Second)
	require.Equal(t, "", m.FindCondition(context.Background(), newLeakEvent("false")), "transition to false resets to new timer")

	now = now.Add(time.Second)
	require.Equal(t, "", m.FindCondition(context.Background(), newLeakEvent("true")), "transition back to true is a fresh debounce cycle, NOT an immediate re-fire")

	now = now.Add(31 * time.Second)
	require.Equal(t, "pond-pump-on", m.FindCondition(context.Background(), newLeakEvent("true")), "fires again after fresh dwell")
}

func TestDebounce_ZeroMinDurationKeepsHistoricalBehavior(t *testing.T) {
	// Binding without MinDuration must fire on every match, just
	// like before this change. Critical for backward compatibility
	// of every existing Binding in the cluster.
	now := time.Unix(1000, 0)
	m := newDebounceMatcher([]apiv1.Binding{
		debounceBinding("pond-leak-fire", "water_leak", "true", 0, "pond-pump-on"),
	}, func() time.Time { return now })

	for i := 0; i < 5; i++ {
		require.Equal(t, "pond-pump-on", m.FindCondition(context.Background(), newLeakEvent("true")), "MinDuration=0 must fire every time")
	}
}
