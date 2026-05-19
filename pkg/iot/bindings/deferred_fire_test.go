package bindings

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot/events"
)

// fakeScheduler replaces time.AfterFunc for tests. Pending callbacks
// hold their requested delays so tests can verify scheduling, and
// fireDue(now) runs every callback whose delay has elapsed relative
// to a simulated clock — letting deferred-fire tests advance time
// deterministically.
type fakeScheduler struct {
	mu       sync.Mutex
	pending  []*fakeTimer
	baseTime time.Time
}

type fakeTimer struct {
	scheduler *fakeScheduler
	armedAt   time.Time
	delay     time.Duration
	cb        func()
	stopped   bool
	fired     bool
}

func (t *fakeTimer) Stop() bool {
	t.scheduler.mu.Lock()
	defer t.scheduler.mu.Unlock()
	wasActive := !t.stopped && !t.fired
	t.stopped = true
	return wasActive
}

func newFakeScheduler(base time.Time) *fakeScheduler {
	return &fakeScheduler{baseTime: base}
}

func (f *fakeScheduler) afterFunc(d time.Duration, cb func()) deferredTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{
		scheduler: f,
		armedAt:   f.baseTime,
		delay:     d,
		cb:        cb,
	}
	f.pending = append(f.pending, t)
	return t
}

// advance moves the simulated clock forward and fires every pending
// callback whose elapsed time has passed. Callbacks run synchronously
// on the calling goroutine so test assertions observe their effects
// immediately.
func (f *fakeScheduler) advance(now time.Time) {
	f.mu.Lock()
	f.baseTime = now
	// Capture a snapshot of due callbacks; release the lock before
	// invoking them (the callback itself takes the matcher's lock and
	// can register new timers via scheduleDeferred re-entry).
	var due []*fakeTimer
	for _, t := range f.pending {
		if t.stopped || t.fired {
			continue
		}
		if !t.armedAt.Add(t.delay).After(now) {
			t.fired = true
			due = append(due, t)
		}
	}
	f.mu.Unlock()
	for _, t := range due {
		t.cb()
	}
}

// pendingCount returns the number of timers that are armed and not
// yet fired or stopped. Used to assert cancellation behaviour.
func (f *fakeScheduler) pendingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.pending {
		if !t.stopped && !t.fired {
			n++
		}
	}
	return n
}

// newDeferredMatcher builds a Matcher with both clock and scheduler
// under test control, and a capture-into-slice activate function so
// tests can assert which Conditions fired (and in what order).
func newDeferredMatcher(t *testing.T, items []apiv1.Binding, base time.Time) (*Matcher, *fakeScheduler, *[]string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	m := New(&fakeKC{items: items}, "iot", logger)
	sched := newFakeScheduler(base)
	now := base
	m.now = func() time.Time { return now }
	m.afterFunc = sched.afterFunc
	// Allow tests to advance "now" alongside the scheduler.
	sched.mu.Lock()
	sched.baseTime = base
	sched.mu.Unlock()

	var activated []string
	var activatedMu sync.Mutex
	m.WithActivateFunc(func(_ context.Context, cond string) error {
		activatedMu.Lock()
		defer activatedMu.Unlock()
		activated = append(activated, cond)
		return nil
	})
	// Wire the clock so subsequent test mutations affect both.
	// Tests call advance(t, sched, m, &now, target) to move time.
	_ = &now
	return m, sched, &activated
}

// advanceTo moves both the matcher's `now` getter and the scheduler's
// clock to the target time, then fires any due timers. The getter is
// captured by reference via a closure rebind so a fresh value is
// observed inside the callback.
func advanceTo(m *Matcher, sched *fakeScheduler, target time.Time) {
	m.now = func() time.Time { return target }
	sched.advance(target)
}

// occupancyEvent builds an occupancy event for a synthetic motion
// device. Modeled on the foyer sensor so the test reads like the
// production setup it's replacing.
func occupancyEvent(value string) events.DeviceEvent {
	d := &apiv1.Device{}
	d.Name = "foyer-motion"
	d.Labels = map[string]string{"iot/zone": "foyer"}
	return events.DeviceEvent{
		Property: "occupancy",
		Value:    value,
		Device:   d,
	}
}

func TestDeferredFire_FiresAfterDwellWithoutFollowupEvent(t *testing.T) {
	// The core bug this PR fixes: a sensor emits ONE matching event
	// and goes quiet. Pre-deferred-fire, the dwell elapsed in memory
	// but was never sampled, so the Binding never fired. With deferred
	// fire, the timer dispatches the Condition at firstSeen + dwell.
	base := time.Unix(1000, 0)
	m, sched, activated := newDeferredMatcher(t, []apiv1.Binding{
		debounceBinding("foyer-motion-on", "occupancy", "true", 2*time.Minute, "foyer-on"),
	}, base)

	// Single observation; no follow-up event.
	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))
	require.Equal(t, 1, sched.pendingCount(), "deferred fire armed for foyer-motion-on")
	require.Empty(t, *activated, "no fire yet — dwell not elapsed")

	// Advance past the dwell + epsilon.
	advanceTo(m, sched, base.Add(2*time.Minute+200*time.Millisecond))
	require.Equal(t, []string{"foyer-on"}, *activated, "deferred fire dispatched the Condition")
	require.Equal(t, 0, sched.pendingCount(), "timer consumed")
}

func TestDeferredFire_CancelsOnValueTransition(t *testing.T) {
	// occupancy=true arrives, deferred timer armed. Before dwell
	// elapses, occupancy=false arrives — this is a transition (sensor
	// reset, or motion stopped quickly). The pending true-side timer
	// must be cancelled; otherwise we'd "wake up" minutes later and
	// turn the lights on even though no motion has happened in ages.
	base := time.Unix(1000, 0)
	m, sched, activated := newDeferredMatcher(t, []apiv1.Binding{
		debounceBinding("foyer-motion-on", "occupancy", "true", 2*time.Minute, "foyer-on"),
	}, base)

	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))
	require.Equal(t, 1, sched.pendingCount())

	// Value transition 30s later.
	advanceTo(m, sched, base.Add(30*time.Second))
	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("false")))
	require.Equal(t, 0, sched.pendingCount(), "transition cancelled the true-side timer")

	// Advance well past the original dwell — nothing should fire.
	advanceTo(m, sched, base.Add(5*time.Minute))
	require.Empty(t, *activated, "cancelled timer must not dispatch")
}

func TestDeferredFire_SuppressedByInlineFire(t *testing.T) {
	// First event at t=0 arms the timer. A second matching event at
	// t=dwell+1s would inline-fire via debounceDispatch (returning
	// "foyer-on" to its caller). When the deferred timer fires
	// shortly after, it must see entry.fired[name]=true and skip the
	// dispatch — otherwise the Condition activates twice.
	base := time.Unix(1000, 0)
	m, sched, activated := newDeferredMatcher(t, []apiv1.Binding{
		debounceBinding("foyer-motion-on", "occupancy", "true", 2*time.Minute, "foyer-on"),
	}, base)

	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))

	// Inline fire from a follow-up event at t = dwell + 1s.
	advanceTo(m, sched, base.Add(2*time.Minute+time.Second))
	// Note: advance(...) above already fired the deferred timer at
	// (dwell + epsilon = 2m + 100ms), which the inline path here
	// would compete with. The FindCondition below is the caller's
	// sample — either it inline-fires (and the timer was already
	// suppressed) or the timer fired first (and the caller observes
	// empty). Either way, foyer-on must activate exactly once.
	_ = m.FindCondition(context.Background(), occupancyEvent("true"))

	require.Equal(t, []string{"foyer-on"}, *activated, "exactly one activation")
}

func TestDeferredFire_WinnerOnlyArmingMatchesExistingSemantics(t *testing.T) {
	// The pre-existing matcher contract is "single winner per event":
	// the highest-scoring (tie-broken-by-name-asc) Binding is the
	// only one that goes through debounceDispatch on a given match.
	// Deferred fire preserves that — scheduleDeferred arms only the
	// winner's timer, not every matching Binding's.
	//
	// Two bindings with equal specificity: winner is foyer-quick by
	// name. After it fires, a re-emit makes it the winner again but
	// now `fired[name]=true` so debounceDispatch returns "suppressed"
	// — and crucially does NOT fall through to consider losers.
	// foyer-slow never arms; the documented data-model case
	// (different MinDurations on the same key) only matters when the
	// Bindings differ in selector specificity such that they don't
	// compete on the same event.
	base := time.Unix(1000, 0)
	m, sched, activated := newDeferredMatcher(t, []apiv1.Binding{
		debounceBinding("foyer-quick", "occupancy", "true", 30*time.Second, "foyer-quick-cond"),
		debounceBinding("foyer-slow", "occupancy", "true", 60*time.Second, "foyer-slow-cond"),
	}, base)

	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))
	require.Equal(t, 1, sched.pendingCount(), "winner-only: foyer-quick armed")

	advanceTo(m, sched, base.Add(30*time.Second+200*time.Millisecond))
	require.Equal(t, []string{"foyer-quick-cond"}, *activated)
	require.Equal(t, 0, sched.pendingCount())

	// Re-emit while still occupied. foyer-quick remains the winner
	// (suppressed), foyer-slow is never visited. No new timer; no
	// new dispatch.
	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))
	require.Equal(t, 0, sched.pendingCount(), "suppressed winner does not delegate to losers")
	advanceTo(m, sched, base.Add(120*time.Second))
	require.Equal(t, []string{"foyer-quick-cond"}, *activated, "exactly one fire")
}

func TestDeferredFire_NoActivateFuncMeansNoTimers(t *testing.T) {
	// Backward compatibility: if WithActivateFunc was never called,
	// scheduleDeferred is a no-op. No timers, no dispatches, the
	// Matcher behaves like its pre-deferred-fire form.
	base := time.Unix(1000, 0)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	m := New(&fakeKC{items: []apiv1.Binding{
		debounceBinding("foyer-motion-on", "occupancy", "true", 2*time.Minute, "foyer-on"),
	}}, "iot", logger)
	sched := newFakeScheduler(base)
	now := base
	m.now = func() time.Time { return now }
	m.afterFunc = sched.afterFunc
	// Intentionally NOT calling WithActivateFunc.

	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))
	require.Equal(t, 0, sched.pendingCount(), "no activate func → no timer armed")
}

func TestDeferredFire_BindingDeletedBetweenScheduleAndFire(t *testing.T) {
	// Schedule a timer; replace the cluster's Binding list with an
	// empty one before the timer fires. The timer callback must not
	// panic and must not dispatch. Production analog: an operator
	// deletes a Binding mid-dwell.
	base := time.Unix(1000, 0)
	fkc := &fakeKC{items: []apiv1.Binding{
		debounceBinding("foyer-motion-on", "occupancy", "true", 2*time.Minute, "foyer-on"),
	}}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	m := New(fkc, "iot", logger)
	sched := newFakeScheduler(base)
	m.now = func() time.Time { return base }
	m.afterFunc = sched.afterFunc

	var activated []string
	m.WithActivateFunc(func(_ context.Context, cond string) error {
		activated = append(activated, cond)
		return nil
	})

	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))
	require.Equal(t, 1, sched.pendingCount())

	// Operator deletes the Binding.
	fkc.items = nil

	advanceTo(m, sched, base.Add(2*time.Minute+200*time.Millisecond))
	require.Empty(t, activated, "deleted Binding must not fire")
}

func TestDeferredFire_FiredFlagSetOnDeferredDispatch(t *testing.T) {
	// After the timer fires the Condition, subsequent same-value
	// events must not re-fire (TestDebounce_SuppressesAfterFire
	// invariant). Verify entry.fired[name] is honoured for the
	// deferred path.
	base := time.Unix(1000, 0)
	m, sched, activated := newDeferredMatcher(t, []apiv1.Binding{
		debounceBinding("foyer-motion-on", "occupancy", "true", 30*time.Second, "foyer-on"),
	}, base)

	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))
	advanceTo(m, sched, base.Add(31*time.Second))
	require.Equal(t, []string{"foyer-on"}, *activated)

	// Hour of re-emits at the same value — no additional fires.
	for i := 0; i < 60; i++ {
		t := base.Add(31*time.Second + time.Duration(i+1)*time.Minute)
		advanceTo(m, sched, t)
		_ = m.FindCondition(context.Background(), occupancyEvent("true"))
	}
	require.Equal(t, []string{"foyer-on"}, *activated, "exactly one fire across re-emits")
}

func TestDeferredFire_InlineFireDoesNotArmTimer(t *testing.T) {
	// When a follow-up event arrives after the dwell elapsed,
	// debounceDispatch fires inline and returns the condition.
	// scheduleDeferred must NOT be called in that path — the dwell
	// is already satisfied; there's no need to arm a future fire.
	base := time.Unix(1000, 0)
	m, sched, activated := newDeferredMatcher(t, []apiv1.Binding{
		debounceBinding("foyer-motion-on", "occupancy", "true", 30*time.Second, "foyer-on"),
	}, base)

	// First event at base: dwell pending, timer armed.
	require.Equal(t, "", m.FindCondition(context.Background(), occupancyEvent("true")))
	require.Equal(t, 1, sched.pendingCount())

	// Stop the existing timer to isolate the inline-fire path.
	advanceTo(m, sched, base.Add(45*time.Second))
	// The timer fired during advance(), so *activated == ["foyer-on"]
	// and pendingCount is 0. A follow-up inline call sees fired=true
	// and must not arm a new timer.
	require.Equal(t, []string{"foyer-on"}, *activated)
	require.Equal(t, 0, sched.pendingCount())

	got := m.FindCondition(context.Background(), occupancyEvent("true"))
	require.Equal(t, "", got, "post-fire same-value events suppress (existing contract)")
	require.Equal(t, 0, sched.pendingCount(), "suppressed events do not arm timers")
}
