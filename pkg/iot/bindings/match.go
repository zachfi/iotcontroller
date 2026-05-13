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
type Matcher struct {
	kc        kubeclient.Client
	namespace string
	logger    *slog.Logger

	debounceMu sync.Mutex
	debounce   map[debounceKey]debounceEntry
	now        func() time.Time // overridable for tests
}

// New returns a Matcher that lists Bindings from namespace ns through kc.
// kc should be the shared cached kubeclient so List is served from cache.
func New(kc kubeclient.Client, ns string, logger *slog.Logger) *Matcher {
	return &Matcher{
		kc:        kc,
		namespace: ns,
		logger:    logger.With("component", "bindings.Matcher"),
		debounce:  make(map[debounceKey]debounceEntry),
		now:       time.Now,
	}
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
type debounceEntry struct {
	value     string
	firstSeen time.Time
	fired     map[string]bool // keyed by Binding name
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
	// every match.
	if winner.minDuration <= 0 {
		return winner.condition
	}

	return m.debounceDispatch(winner, ev)
}

// observeValue records the current observed value for one (property,
// device) pair. Called unconditionally per event, before any Binding
// match decision — this is what lets a value transition reset every
// Binding's fired flag, including Bindings whose target value the new
// event doesn't match.
//
// On a same-value re-observation: keep firstSeen as-is (the dwell
// timer keeps running). On a value transition: reset the entry with a
// fresh firstSeen and an empty fired map.
func (m *Matcher) observeValue(ev events.DeviceEvent) {
	key := debounceKey{property: ev.Property, device: ev.Device.Name}
	now := m.now()

	m.debounceMu.Lock()
	defer m.debounceMu.Unlock()

	entry, exists := m.debounce[key]
	if exists && entry.value == ev.Value {
		return
	}
	m.debounce[key] = debounceEntry{
		value:     ev.Value,
		firstSeen: now,
		fired:     map[string]bool{},
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
