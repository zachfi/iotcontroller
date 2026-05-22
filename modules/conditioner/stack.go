package conditioner

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// stack.go — Phase B foundation for the reconcile-loop architecture.
//
// At any moment, each zone has a target state expressed per axis. The
// target comes from a stack of Computer-Activations per axis. The
// reconciler reads the top non-expired Activation off each axis stack
// every tick, evaluates its Computer, flushes the composed target on
// delta. Events (motion, button, alert, time-window opening) push
// Activations with TTLs; expiration pops the override and the stack
// reveals the previous layer.
//
// This file defines the in-process data model only — the
// runtimeActivation wrapper, the per-axis stack, and the per-zone
// policy. The reconciler that uses these types (Reconciler in
// reconciler.go) is the next file in Phase B; the writer wiring
// gated by `-conditioner.reconcile-zones` arrives after.
//
// Full design context: docs/reconcile-design.md.
//
// Two invariants this file's types must preserve:
//
//   * No "currently running" Computer state. Each tick reads the top
//     fresh; between ticks nothing runs. Activations are pure data.
//
//   * Top selection is max(priority); ties broken by max(pushed_at).
//     Deterministic, reproducible, easy to reason about.

// runtimeActivation wraps a proto Activation with the resolved
// Computer function pointer cached at push time. The reconciler's
// hot loop calls ra.resolved.Compute(...) directly — one indirection,
// no per-tick registry map lookup.
type runtimeActivation struct {
	*iotv1proto.Activation
	resolved computer.Computer
	// id is the dedup key (sourceKind:sourceName). PUSH_POLICY_REFRESH
	// against an existing id updates pushed_at + ttl in place; a new
	// id appends.
	id string
}

// expired reports whether this Activation's TTL has elapsed at `now`.
// TTL=0 (zero duration) means "no expiration"; background entries set
// TTL=0 and never expire by this check.
func (ra *runtimeActivation) expired(now time.Time) bool {
	if ra.Activation == nil {
		return true
	}
	if !ra.Ttl.IsValid() || ra.Ttl.AsDuration() == 0 {
		return false
	}
	if !ra.PushedAt.IsValid() {
		return false
	}
	deadline := ra.PushedAt.AsTime().Add(ra.Ttl.AsDuration())
	return now.After(deadline)
}

// priority returns the Activation's priority (extracted helper for
// readability in the top() comparison).
func (ra *runtimeActivation) priority() int32 {
	if ra.Activation == nil {
		return 0
	}
	return ra.Activation.Priority
}

// pushedAt returns the Activation's push time as time.Time, defaulting
// to the zero Time when unset.
func (ra *runtimeActivation) pushedAt() time.Time {
	if ra.Activation == nil || !ra.Activation.PushedAt.IsValid() {
		return time.Time{}
	}
	return ra.Activation.PushedAt.AsTime()
}

// activationID is the dedup key for PUSH_POLICY_REFRESH semantics.
// Same (source_kind, source_name) → same id; refresh updates the
// existing entry rather than appending a duplicate.
func activationID(kind iotv1proto.SourceKind, name string) string {
	return fmt.Sprintf("%s:%s", kind, name)
}

// axisStack holds the prioritized list of Activations for one axis on
// one zone. Entries are appended in push order; sort is implicit in
// the top() walk so we don't pay an insertion-sort cost on push (push
// is more frequent than tick when motion is active).
type axisStack struct {
	mu      sync.Mutex
	entries []*runtimeActivation
}

// push inserts ra into the stack. PUSH_POLICY_REFRESH updates an
// existing entry with the same id in place (preserving args, just
// refreshing pushed_at + ttl). PUSH_POLICY_REPLACE swaps the existing
// entry entirely. A new id always appends.
func (s *axisStack) push(ra *runtimeActivation) {
	s.mu.Lock()
	defer s.mu.Unlock()

	policy := iotv1proto.PushPolicy_PUSH_POLICY_REFRESH
	if ra.Activation != nil && ra.Activation.PushPolicy != iotv1proto.PushPolicy_PUSH_POLICY_UNSPECIFIED {
		policy = ra.Activation.PushPolicy
	}

	for i, existing := range s.entries {
		if existing.id != ra.id {
			continue
		}
		if policy == iotv1proto.PushPolicy_PUSH_POLICY_REPLACE {
			s.entries[i] = ra
			return
		}
		// REFRESH: keep args + computer reference, update timing.
		existing.PushedAt = ra.PushedAt
		existing.Ttl = ra.Ttl
		return
	}
	s.entries = append(s.entries, ra)
}

// top returns the highest-priority non-expired Activation, breaking
// ties on more-recent pushed_at. nil when the stack is empty or all
// entries are expired.
//
// Linear scan: typical n is 1-5; pre-sorting on every push would cost
// more than walking the unsorted slice once per tick. If profiling
// ever shows this as a hot spot, switch to a heap or sorted-insert.
func (s *axisStack) top(now time.Time) *runtimeActivation {
	s.mu.Lock()
	defer s.mu.Unlock()

	var best *runtimeActivation
	for _, a := range s.entries {
		if a.expired(now) {
			continue
		}
		if best == nil {
			best = a
			continue
		}
		if a.priority() > best.priority() {
			best = a
			continue
		}
		if a.priority() == best.priority() && a.pushedAt().After(best.pushedAt()) {
			best = a
		}
	}
	return best
}

// removeExpired drops expired entries from the slice. Called lazily
// from the reconciler at the start of each tick so the stack doesn't
// grow unbounded across pushes that never get reconciled.
//
// Correctness primitive: top() also filters via expired() at read time,
// so removeExpired() is purely a memory-bookkeeping helper, not a
// safety gate. A timer that fails to fire (clock skew, process restart)
// leaves an expired entry around briefly; the next top() call skips it.
func (s *axisStack) removeExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := s.entries[:0]
	for _, a := range s.entries {
		if !a.expired(now) {
			kept = append(kept, a)
		}
	}
	// Zero out the tail so the backing array doesn't retain references
	// to expired runtimeActivations (which embed *iotv1proto.Activation
	// and potentially large Args maps). Without this, the GC cannot
	// reclaim the expired entries until the slice is grown past their
	// positions or replaced entirely.
	for i := len(kept); i < len(s.entries); i++ {
		s.entries[i] = nil
	}
	s.entries = kept
}

// find returns the runtimeActivation with the given id, or nil.
// Diagnostic helper; not used in the reconciler hot loop.
func (s *axisStack) find(id string) *runtimeActivation {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.entries {
		if a.id == id {
			return a
		}
	}
	return nil
}

// snapshot returns a copy of the current entries for diagnostic /
// audit-trail use. Holds the lock briefly; safe to call from
// metric-export paths.
func (s *axisStack) snapshot() []*runtimeActivation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*runtimeActivation, len(s.entries))
	copy(out, s.entries)
	// Stable order for diffable logs: priority desc, pushed_at desc, id asc.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].priority() != out[j].priority() {
			return out[i].priority() > out[j].priority()
		}
		if !out[i].pushedAt().Equal(out[j].pushedAt()) {
			return out[i].pushedAt().After(out[j].pushedAt())
		}
		return out[i].id < out[j].id
	})
	return out
}

// zonePolicy is the per-zone runtime view used by the reconciler.
// Maps each axis to its independent stack. Built lazily — a zone with
// no Activations on a given axis has no entry in the map (the
// reconciler treats a missing stack as "no claim, leave Zone.Status
// alone for this axis").
type zonePolicy struct {
	zone   string
	stacks map[iotv1proto.AxisKind]*axisStack
}

func newZonePolicy(zone string) *zonePolicy {
	return &zonePolicy{
		zone:   zone,
		stacks: make(map[iotv1proto.AxisKind]*axisStack, 4),
	}
}

// stack returns the axisStack for the given axis, creating it lazily
// on first push.
func (p *zonePolicy) stack(axis iotv1proto.AxisKind) *axisStack {
	if s, ok := p.stacks[axis]; ok {
		return s
	}
	s := &axisStack{}
	p.stacks[axis] = s
	return s
}

// pushActivation is the in-process entry point called by both the
// PushActivation RPC (external) and the internal handlers (matcher
// resolving a Binding match, alert handler resolving an Alert, etc.).
// Resolves the Computer name to a function pointer once, wraps in a
// runtimeActivation, and inserts into the appropriate axis stack.
//
// Returns an error if the named Computer isn't registered — surfaced
// to the caller so operators see misspelled Computer names
// immediately rather than at next-eval-tick.
func (p *zonePolicy) pushActivation(axis iotv1proto.AxisKind, act *iotv1proto.Activation, logger *slog.Logger) error {
	if act == nil {
		return fmt.Errorf("nil Activation")
	}
	if act.ComputerName == "" {
		return fmt.Errorf("Activation.computer_name is required")
	}
	comp, ok := computer.Get(act.ComputerName)
	if !ok {
		return fmt.Errorf("unknown computer %q", act.ComputerName)
	}
	ra := &runtimeActivation{
		Activation: act,
		resolved:   comp,
		id:         activationID(act.SourceKind, act.SourceName),
	}
	p.stack(axis).push(ra)
	if logger != nil {
		logger.Debug("reconciler: pushed activation",
			slog.String("zone", p.zone),
			slog.String("axis", axis.String()),
			slog.String("computer", act.ComputerName),
			slog.String("source", ra.id),
			slog.Int64("ttl_ms", act.Ttl.AsDuration().Milliseconds()),
			slog.Int("priority", int(act.Priority)),
		)
	}
	return nil
}

// activeContributorsByAxis returns the top-of-stack Activation per
// axis at time `now`. Used by the reconciler's per-tick walk and by
// the audit-trail / observability layer.
func (p *zonePolicy) activeContributorsByAxis(now time.Time) map[iotv1proto.AxisKind]*runtimeActivation {
	out := make(map[iotv1proto.AxisKind]*runtimeActivation, len(p.stacks))
	for axis, s := range p.stacks {
		if top := s.top(now); top != nil {
			out[axis] = top
		}
	}
	return out
}

// applyTopToValues evaluates the top of each axis stack and folds
// the per-axis Computer outputs into a single ApplyValues. The folding
// rule per axis: the top Activation's Computer's output for that axis
// wins; other axes from that same Computer's ApplyValues are
// IGNORED (a state-axis Computer returning a brightness value
// doesn't get to write brightness — only the brightness-axis top
// does). This enforces the "one Computer per axis" semantic.
//
// Returns the composed target. ApplyValues defaults are zero
// (UNSPECIFIED enums, empty color string) for any axis with no
// active contributor.
func (p *zonePolicy) applyTopToValues(ctx context.Context, now time.Time, loc computer.Location) (computer.ApplyValues, error) {
	target := computer.ApplyValues{}
	for axis, ra := range p.activeContributorsByAxis(now) {
		val, err := ra.resolved.Compute(ctx, now, loc, ra.Args)
		if err != nil {
			return target, fmt.Errorf("compute %s for axis %s: %w", ra.ComputerName, axis, err)
		}
		// Per axis: take only the axis's own field from the Computer's
		// output, ignore others. Enforces the "one Computer per axis"
		// contract — circadian setting only CT is correct; if it
		// accidentally set State, we still ignore that here.
		switch axis {
		case iotv1proto.AxisKind_AXIS_KIND_STATE:
			target.State = val.State
		case iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS:
			target.Brightness = val.Brightness
			target.BrightnessValue = val.BrightnessValue
		case iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE:
			target.ColorTemperature = val.ColorTemperature
			target.ColorTemperatureKelvin = val.ColorTemperatureKelvin
		case iotv1proto.AxisKind_AXIS_KIND_COLOR:
			target.Color = val.Color
		}
	}
	return target, nil
}
