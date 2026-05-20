// Package bindings resolves a normalized DeviceEvent to a Condition name by
// matching against Binding CRs in the cluster. The matcher returns the
// most-specific Binding that matches; specificity is broken down by
// selector field with deterministic tie-breaking.
//
// Lookup is served from the controller-runtime informer cache that backs
// the shared kubeclient (see plans/cached-kubeclient-via-cluster.md), so
// per-event List is in-memory and effectively free.
package bindings

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	"github.com/zachfi/iotcontroller/pkg/iot/events"
)

// Specificity weights. Higher = more specific. A binding's score is the sum
// of weights for each non-empty selector field; the highest-scoring binding
// matching the event wins. LabelSelector contributes per-key.
const (
	weightIEEE       = 16
	weightDevice     = 8
	weightLabelKey   = 4
	weightDeviceType = 2
	weightZone       = 1
)

// Matcher resolves device events to Condition names via Binding CRs.
//
// When a matching Binding carries MinDuration, the Matcher holds per-
// (Binding, Device) state — the first matching event starts a timer,
// subsequent matching events of the same value advance it, and an event
// with a different value resets it. The Condition fires only when the
// matching event has been continuously observed for at least MinDuration.
// State is in-process and resets on pod restart.
//
// Deferred-fire path: if a debounced Binding's dwell hasn't elapsed
// when an event arrives, the Matcher arms an internal timer that fires
// at firstSeen+MinDuration+ε and dispatches the Condition directly via
// the injected ActivateConditionFunc (without waiting for another
// inbound event to re-sample the dwell). A value transition cancels
// every pending deferred fire for that (property, device) key. A
// successful inline fire from a follow-up event suppresses the
// corresponding timer's re-dispatch attempt via the per-Binding
// `fired` flag.
type Matcher struct {
	kc        kubeclient.Client
	namespace string
	logger    *slog.Logger

	debounceMu sync.Mutex
	debounce   map[debounceKey]debounceEntry
	now        func() time.Time                          // overridable for tests
	afterFunc  func(time.Duration, func()) deferredTimer // overridable for tests
	activateFn ActivateConditionFunc                     // nil disables the deferred path
	epsilon    time.Duration                             // padding past min_duration to avoid early-fire races
}

// ActivateConditionFunc is the callback the Matcher uses to dispatch a
// Condition when a deferred fire elapses without an inbound event to
// piggyback on. Wired by the router (zigbee2mqtt/nativezigbee) at
// startup to invoke ActivateCondition on the conditioner client.
type ActivateConditionFunc func(ctx context.Context, condition string) error

// deferredTimer is the minimal contract a *time.Timer satisfies — Stop
// is the only operation the Matcher needs. Lets tests inject a fake
// scheduler that fires callbacks on demand instead of in real time.
type deferredTimer interface {
	Stop() bool
}

// New returns a Matcher that lists Bindings from namespace ns through kc.
// kc should be the shared cached kubeclient so List is served from cache.
//
// The returned Matcher has the deferred-fire path disabled by default;
// call WithActivateFunc to enable it. Tests that want to drive timer
// callbacks deterministically also call WithAfterFunc with a fake
// scheduler.
func New(kc kubeclient.Client, ns string, logger *slog.Logger) *Matcher {
	return &Matcher{
		kc:        kc,
		namespace: ns,
		logger:    logger.With("component", "bindings.Matcher"),
		debounce:  make(map[debounceKey]debounceEntry),
		now:       time.Now,
		afterFunc: realAfterFunc,
		epsilon:   100 * time.Millisecond,
	}
}

// WithActivateFunc wires the dispatcher the deferred-fire path uses.
// Without this, deferred fires are inert: pending events whose dwell
// elapses without a follow-up event simply never activate, and the
// Matcher's observable behaviour matches its pre-deferred-fire form.
func (m *Matcher) WithActivateFunc(fn ActivateConditionFunc) *Matcher {
	m.activateFn = fn
	return m
}

// realAfterFunc is the production scheduler. Tests swap in a fake via
// the unexported field directly (set by the test helper) so simulated
// time advances synchronously.
func realAfterFunc(d time.Duration, f func()) deferredTimer {
	return time.AfterFunc(d, f)
}

// debounceKey identifies the observed-value state for one (property,
// device) pair. Per-property-per-device — sharing across Bindings is
// intentional: a value transition (water_leak=true → false → true)
// observed by ANY Binding's property must reset the debounce timers
// of every Binding watching that property on that device, even if
// some of those Bindings' target values don't match the intermediate
// transition.
//
// Per-Binding state still exists, in the entry's fired map.
type debounceKey struct {
	property string
	device   string
}

// debounceEntry tracks the current observed value for one (property,
// device) pair. firstSeen is the timestamp at which the current value
// was first observed. fired records, per Binding name, whether that
// Binding has already dispatched its Condition during this
// stable-value-window — set true on dispatch and cleared by the value
// transition that opens the next window.
//
// Two Bindings with different MinDuration values can both watch the
// same (property, device); they share firstSeen but each tracks its
// own fired flag here, so a 30s-Binding fires at t+30 while a
// 60s-Binding waits until t+60 and then fires from the same entry.
//
// device + timers support the deferred-fire path. device is the full
// Device pointer captured at observation time so the timer callback
// can rebuild a DeviceEvent (selectorMatches reads several fields off
// it) without re-fetching from the kube cache. timers is keyed by
// Binding name and holds the pending deferred fire for each
// candidate Binding whose dwell hasn't yet elapsed; a value-transition
// reset stops every timer in the map.
type debounceEntry struct {
	value     string
	firstSeen time.Time
	fired     map[string]bool          // keyed by Binding name
	device    *apiv1.Device            // for deferred-fire reconstruction
	timers    map[string]deferredTimer // keyed by Binding name
}

// FindCondition returns the Condition name for the most-specific Binding
// matching ev, or "" if no Binding matches. Ties on specificity are broken
// by Binding name (sorted ascending) for deterministic behavior.
//
// Returns "" without erroring on List failure: a missed match falls
// through to the router's observability counter, not a behavioural
// path — so a List blip degrades to "no Condition fires" rather than
// surfacing a 500 to the caller.
func (m *Matcher) FindCondition(ctx context.Context, ev events.DeviceEvent) string {
	if m == nil || m.kc == nil || ev.Device == nil {
		return ""
	}

	list := &apiv1.BindingList{}
	if err := m.kc.List(ctx, list, kubeclient.InNamespace(m.namespace)); err != nil {
		m.logger.Debug("list bindings failed", slog.String("error", err.Error()))
		return ""
	}

	// Track the observed value on every event, BEFORE the per-Binding
	// match loop. A value transition observed for a (property, device)
	// must reset the debounce state of every Binding watching that
	// property — even Bindings whose target value the new event
	// doesn't match. Without this update happening unconditionally, a
	// true→false→true sequence for water_leak (where the "false"
	// matches no Binding) would never clear the "true" Binding's
	// fired flag, suppressing the next true forever.
	m.observeValue(ev)

	var candidates []candidate

	for i := range list.Items {
		b := &list.Items[i]
		if !propertyMatches(b.Spec.Event, ev) {
			continue
		}
		if !selectorMatches(b.Spec.Event.Selector, ev.Device) {
			continue
		}
		candidates = append(candidates, candidate{
			name:        b.Name,
			condition:   b.Spec.Condition,
			score:       specificity(b.Spec.Event.Selector),
			minDuration: b.Spec.Event.MinDuration.Duration,
		})
	}

	if len(candidates) == 0 {
		return ""
	}

	// Highest score wins; ties broken by name ascending.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})

	if len(candidates) > 1 && candidates[0].score == candidates[1].score {
		m.logger.Debug("binding match tie",
			slog.String("property", ev.Property),
			slog.String("value", ev.Value),
			slog.String("device", ev.Device.Name),
			slog.String("chosen", candidates[0].name),
			slog.Int("ties", countTies(candidates)),
		)
	}

	winner := candidates[0]

	// Fast path: no debounce configured. Historical behaviour — fire
	// every match. Record the fire on the same outcome counter the
	// debounce path uses so operators can answer "did this Binding
	// fire today?" with one PromQL query that works uniformly across
	// dwell types — before this increment was added, fast-path
	// Bindings (those with empty min_duration, e.g. motion-evening
	// after the foyer immediate-fire change) were invisible to the
	// counter even though they were firing on every match.
	if winner.minDuration <= 0 {
		metricBindingDebounced.WithLabelValues(winner.name, "fired").Inc()
		return winner.condition
	}

	result := m.debounceDispatch(winner, ev)
	if result == "" {
		m.scheduleDeferred(winner, ev)
	}
	return result
}

// observeValue records the current observed value for one (property,
// device) pair. Called unconditionally per event, before any Binding
// match decision — this is what lets a value transition reset every
// Binding's fired flag, including Bindings whose target value the new
// event doesn't match.
//
// On a same-value re-observation: keep firstSeen as-is (the dwell
// timer keeps running) but refresh the cached device pointer (label
// changes etc. can land between events). On a value transition:
// reset the entry with a fresh firstSeen and empty fired/timers
// maps, stopping every pending deferred-fire timer first.
func (m *Matcher) observeValue(ev events.DeviceEvent) {
	key := debounceKey{property: ev.Property, device: ev.Device.Name}
	now := m.now()

	m.debounceMu.Lock()
	defer m.debounceMu.Unlock()

	entry, exists := m.debounce[key]
	if exists && entry.value == ev.Value {
		// Refresh the device pointer so the deferred-fire callback
		// sees current labels if they've drifted since open.
		entry.device = ev.Device
		m.debounce[key] = entry
		return
	}
	if exists {
		for name, t := range entry.timers {
			if t != nil {
				t.Stop()
			}
			delete(entry.timers, name)
		}
	}
	m.debounce[key] = debounceEntry{
		value:     ev.Value,
		firstSeen: now,
		fired:     map[string]bool{},
		device:    ev.Device,
		timers:    map[string]deferredTimer{},
	}
	if exists {
		m.logger.Debug("binding debounce window reset (value transition)",
			slog.String("property", ev.Property),
			slog.String("device", ev.Device.Name),
			slog.String("from", entry.value),
			slog.String("to", ev.Value),
		)
	} else {
		m.logger.Debug("binding debounce window opened",
			slog.String("property", ev.Property),
			slog.String("device", ev.Device.Name),
			slog.String("value", ev.Value),
		)
	}
}

// scheduleDeferred arms a timer for `winner` if its dwell hasn't yet
// elapsed and no timer is already pending for it. Called from the
// FindCondition pending-return path after debounceDispatch returned ""
// — i.e. the dwell is *known* to be unsatisfied. The timer fires at
// firstSeen + min_duration + epsilon and dispatches via activateFn.
//
// No-op when activateFn is unset (production wiring not yet hooked
// in, or tests that intentionally disable the path).
func (m *Matcher) scheduleDeferred(winner candidate, ev events.DeviceEvent) {
	if m.activateFn == nil {
		return
	}
	key := debounceKey{property: ev.Property, device: ev.Device.Name}

	m.debounceMu.Lock()
	entry, ok := m.debounce[key]
	if !ok {
		m.debounceMu.Unlock()
		return
	}
	if entry.fired[winner.name] {
		m.debounceMu.Unlock()
		return
	}
	if _, pending := entry.timers[winner.name]; pending {
		m.debounceMu.Unlock()
		return
	}

	fireAt := entry.firstSeen.Add(winner.minDuration).Add(m.epsilon)
	delay := fireAt.Sub(m.now())
	if delay <= 0 {
		// Past-due. Schedule for epsilon ahead so the dispatch
		// happens off the calling goroutine; the dwell check inside
		// debounceDispatch will satisfy on entry.
		delay = m.epsilon
	}

	bindingName := winner.name
	t := m.afterFunc(delay, func() {
		m.fireDeferred(key, bindingName)
	})
	entry.timers[bindingName] = t
	m.debounce[key] = entry
	m.debounceMu.Unlock()
}

// fireDeferred runs in the timer's goroutine. Re-discovers the current
// matching Binding by name from the cached list, calls debounceDispatch
// to check the dwell (which by now should be satisfied), and dispatches
// via activateFn if it fires. The lookup-by-name guards against
// Bindings being deleted/edited between schedule and fire.
func (m *Matcher) fireDeferred(key debounceKey, bindingName string) {
	m.debounceMu.Lock()
	entry, ok := m.debounce[key]
	if !ok {
		m.debounceMu.Unlock()
		return
	}
	delete(entry.timers, bindingName)
	if entry.fired[bindingName] {
		// Inline path beat us to it (a same-value event arrived after
		// dwell and the standard FindCondition dispatched). Nothing
		// to do.
		m.debounce[key] = entry
		m.debounceMu.Unlock()
		return
	}
	device := entry.device
	value := entry.value
	m.debounce[key] = entry
	m.debounceMu.Unlock()

	if device == nil {
		return
	}

	ev := events.DeviceEvent{
		Property: key.property,
		Value:    value,
		Device:   device,
	}

	// Re-list and find the named Binding. observe is intentionally
	// skipped here — value hasn't changed, firstSeen must stay.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	list := &apiv1.BindingList{}
	if err := m.kc.List(ctx, list, kubeclient.InNamespace(m.namespace)); err != nil {
		m.logger.Debug("deferred fire list failed",
			slog.String("binding", bindingName),
			slog.String("error", err.Error()),
		)
		return
	}

	var winner candidate
	found := false
	for i := range list.Items {
		b := &list.Items[i]
		if b.Name != bindingName {
			continue
		}
		if !propertyMatches(b.Spec.Event, ev) {
			break
		}
		if !selectorMatches(b.Spec.Event.Selector, ev.Device) {
			break
		}
		winner = candidate{
			name:        b.Name,
			condition:   b.Spec.Condition,
			score:       specificity(b.Spec.Event.Selector),
			minDuration: b.Spec.Event.MinDuration.Duration,
		}
		found = true
		break
	}
	if !found || winner.minDuration <= 0 {
		// Binding removed, no longer matches, or MinDuration was
		// edited away between schedule and fire. Drop the fire.
		return
	}

	result := m.debounceDispatch(winner, ev)
	if result == "" {
		return
	}

	if err := m.activateFn(ctx, result); err != nil {
		m.logger.Debug("deferred activate failed",
			slog.String("condition", result),
			slog.String("error", err.Error()),
		)
	}
}

// debounceDispatch returns winner.condition once the dwell threshold
// has been satisfied for this (property, device) by the currently
// observed value, and only one fire per Binding per stable-value-window.
//
// Assumes observeValue has already run for `ev`; reads the entry the
// observeValue created/updated and consults the per-Binding fired
// flag.
func (m *Matcher) debounceDispatch(winner candidate, ev events.DeviceEvent) string {
	key := debounceKey{property: ev.Property, device: ev.Device.Name}
	now := m.now()

	m.debounceMu.Lock()
	defer m.debounceMu.Unlock()

	entry, exists := m.debounce[key]
	if !exists {
		// Defensive: observeValue should have created the entry above.
		return ""
	}

	if entry.fired[winner.name] {
		metricBindingDebounced.WithLabelValues(winner.name, "suppressed").Inc()
		return ""
	}

	if now.Sub(entry.firstSeen) >= winner.minDuration {
		entry.fired[winner.name] = true
		m.debounce[key] = entry
		m.logger.Debug("binding debounce fired",
			slog.String("binding", winner.name),
			slog.String("property", ev.Property),
			slog.String("device", ev.Device.Name),
			slog.String("value", ev.Value),
			slog.Duration("elapsed", now.Sub(entry.firstSeen)),
		)
		metricBindingDebounced.WithLabelValues(winner.name, "fired").Inc()
		return winner.condition
	}

	metricBindingDebounced.WithLabelValues(winner.name, "pending").Inc()
	return ""
}

// propertyMatches returns true when the binding's property/value
// constraint matches the event. Rules:
//
//   - trigger.Property must always match exactly.
//   - trigger.Values takes precedence when non-empty: ev.Value must
//     equal one of the listed values.
//   - trigger.Value (singular) is checked only when Values is empty.
//   - When both Value and Values are empty, the trigger is a wildcard
//     for the value field — any value of the property matches.
func propertyMatches(trigger apiv1.EventTrigger, ev events.DeviceEvent) bool {
	if trigger.Property == "" {
		return false
	}
	if trigger.Property != ev.Property {
		return false
	}

	if len(trigger.Values) > 0 {
		for _, v := range trigger.Values {
			if v == ev.Value {
				return true
			}
		}
		return false
	}

	if trigger.Value != "" && trigger.Value != ev.Value {
		return false
	}
	return true
}

// selectorMatches returns true when every non-empty selector field matches
// the device. Empty selector matches every device that emitted the event.
func selectorMatches(sel apiv1.EventSelector, d *apiv1.Device) bool {
	if sel.IEEE != "" && sel.IEEE != d.Spec.IEEEAddress {
		return false
	}
	if sel.Device != "" && sel.Device != d.Name {
		return false
	}
	if sel.DeviceType != "" && sel.DeviceType != d.Spec.Type {
		return false
	}
	if sel.Zone != "" && sel.Zone != d.Labels[iot.DeviceZoneLabel] {
		return false
	}
	for k, v := range sel.LabelSelector {
		if d.Labels[k] != v {
			return false
		}
	}
	return true
}

// specificity returns a score for selector. Sum of per-field weights.
func specificity(sel apiv1.EventSelector) int {
	score := 0
	if sel.IEEE != "" {
		score += weightIEEE
	}
	if sel.Device != "" {
		score += weightDevice
	}
	if sel.DeviceType != "" {
		score += weightDeviceType
	}
	if sel.Zone != "" {
		score += weightZone
	}
	score += len(sel.LabelSelector) * weightLabelKey
	return score
}

// candidate is a Binding that matched the event, used for sorting and
// tie-break diagnostics.
type candidate struct {
	name        string
	condition   string
	score       int
	minDuration time.Duration
}

func countTies(cands []candidate) int {
	if len(cands) == 0 {
		return 0
	}
	top := cands[0].score
	n := 0
	for _, c := range cands {
		if c.score == top {
			n++
		}
	}
	return n
}
