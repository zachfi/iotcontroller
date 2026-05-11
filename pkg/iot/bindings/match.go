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
type Matcher struct {
	kc        kubeclient.Client
	namespace string
	logger    *slog.Logger
}

// New returns a Matcher that lists Bindings from namespace ns through kc.
// kc should be the shared cached kubeclient so List is served from cache.
func New(kc kubeclient.Client, ns string, logger *slog.Logger) *Matcher {
	return &Matcher{
		kc:        kc,
		namespace: ns,
		logger:    logger.With("component", "bindings.Matcher"),
	}
}

// FindCondition returns the Condition name for the most-specific Binding
// matching ev, or "" if no Binding matches. Ties on specificity are broken
// by Binding name (sorted ascending) for deterministic behavior.
//
// Returns "" without erroring on List failure: bindings are an
// optimization layer over ActionHandler, not a hard requirement.
func (m *Matcher) FindCondition(ctx context.Context, ev events.DeviceEvent) string {
	if m == nil || m.kc == nil || ev.Device == nil {
		return ""
	}

	list := &apiv1.BindingList{}
	if err := m.kc.List(ctx, list, kubeclient.InNamespace(m.namespace)); err != nil {
		m.logger.Debug("list bindings failed", slog.String("error", err.Error()))
		return ""
	}

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
			name:      b.Name,
			condition: b.Spec.Condition,
			score:     specificity(b.Spec.Event.Selector),
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

	return candidates[0].condition
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
	name      string
	condition string
	score     int
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
