package conditioner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// statusZoneNamespace is where Zone CRs live. Hardcoded today (same
// constant the Conditioner uses); revisit if we ever multi-tenant.
const statusZoneNamespace = "iot"

// reconciler.go — the writer side of Phase B. Reads Active Computer
// Stacks (from stack.go), composes a per-zone target, and flushes via
// ZoneKeeper.ApplyValues when the target differs from last-applied.
//
// Lifecycle:
//
//   1. PushActivation(zone, axis, act) — called by the RPC handler
//      and by in-process push sources (matcher, alert, time-window).
//      Resolves the Computer name, wraps in runtimeActivation, inserts
//      into the appropriate axis stack. Schedules an immediate
//      reconcile + a TTL timer (if act.Ttl > 0) so expiration wakes
//      the reconciler at the exact moment.
//
//   2. ReconcileZone(ctx, zone, now) — invoked by the periodic
//      evaluator, by PushActivation's immediate-wake, and by TTL
//      timers. Reads each axis's top non-expired Activation,
//      evaluates the cached Computer function pointer, composes a
//      target, and flushes via ZoneKeeper.ApplyValues only on delta.
//      Per-zone mutex serializes concurrent reconciles for the same
//      zone (timer + periodic + push can all race).
//
//   3. The "currently running" Computer is a non-concept. Each tick
//      reads the top fresh; between ticks nothing runs.
//
// Read-only contract: ReconcileZone NEVER calls a Computer that
// hasn't been pushed; NEVER writes to a zone whose target hasn't
// changed; NEVER scans Conditions. All authority flows from
// PushActivation events.

// Reconciler owns the runtime state for the reconcile-loop
// architecture. One instance per controller process, embedded in
// the Conditioner.
type Reconciler struct {
	zonekeeperClient iotv1proto.ZoneKeeperServiceClient
	kubeClient       kubeclient.Client
	location         computer.Location
	logger           *slog.Logger
	tracer           trace.Tracer

	// policies holds the per-zone state. Read frequently (each
	// ReconcileZone), written less frequently (each PushActivation).
	policiesMu sync.RWMutex
	policies   map[string]*zonePolicy

	// zoneLocks serializes concurrent reconciles for the same zone.
	// One mutex per zone, lazily created on first reconcile.
	zoneLocksMu sync.Mutex
	zoneLocks   map[string]*sync.Mutex

	// ttlTimers holds the most-recent outstanding TTL wakeup per
	// (zone, axis, activation_id). On REFRESH, we stop the previous
	// timer and replace it so we don't accumulate stale wakeups that
	// race after the activation's pushed_at gets bumped forward.
	ttlTimersMu sync.Mutex
	ttlTimers   map[string]reconcileTimer

	// lastApplied caches the most recent target written via
	// ApplyValues per zone. The reconciler flushes only when the
	// composed target differs from lastApplied — the per-zone
	// equivalent of today's per-(condition, zone) applyDesired cache.
	lastAppliedMu sync.Mutex
	lastApplied   map[string]computer.ApplyValues

	// prevTopSource caches the previous-tick top-of-stack source per
	// (zone, axis). Lets ReconcileZone emit a "top changed" log + metric
	// only on actual transitions (a 30-second motion refresh produces
	// hundreds of redundant pushes; we want one log when the lamp
	// behavior actually changed, not one per push). Key is the same
	// (zone/axis/id) shape we use for TTL timers.
	prevTopSourceMu sync.Mutex
	prevTopSource   map[string]string

	// now is the time function; tests inject a fake clock.
	now func() time.Time
	// afterFunc schedules a delayed callback; tests inject a fake
	// scheduler. Returns a Timer with a Stop method (mirror of
	// time.AfterFunc).
	afterFunc func(time.Duration, func()) reconcileTimer
}

// reconcileTimer is the minimal interface tests can satisfy without
// pulling in the full time.Timer surface.
type reconcileTimer interface {
	Stop() bool
}

// realTimer adapts *time.Timer to reconcileTimer.
type realTimer struct{ t *time.Timer }

func (r realTimer) Stop() bool { return r.t.Stop() }

// NewReconciler builds a Reconciler ready to serve PushActivation and
// ReconcileZone calls. Loc is the operator-configured (lat, lon)
// passed through to Computer.Compute. The Reconciler is dormant
// until the first PushActivation arrives; no goroutines started here.
// NewReconciler builds a Reconciler ready to serve PushActivation and
// ReconcileZone calls. `kc` is optional — when nil, Status reflection
// is silently skipped (the rest of the reconcile loop still works).
// Tests typically pass nil; production wires the same controller-
// runtime client the Conditioner uses for Condition CR reads.
func NewReconciler(zk iotv1proto.ZoneKeeperServiceClient, kc kubeclient.Client, loc computer.Location, logger *slog.Logger, tracer trace.Tracer) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	if tracer == nil {
		tracer = tracenoop.NewTracerProvider().Tracer("conditioner.reconciler")
	}
	return &Reconciler{
		zonekeeperClient: zk,
		kubeClient:       kc,
		location:         loc,
		logger:           logger.With("component", "reconciler"),
		tracer:           tracer,
		policies:         make(map[string]*zonePolicy),
		zoneLocks:        make(map[string]*sync.Mutex),
		lastApplied:      make(map[string]computer.ApplyValues),
		ttlTimers:        make(map[string]reconcileTimer),
		prevTopSource:    make(map[string]string),
		now:              time.Now,
		afterFunc: func(d time.Duration, f func()) reconcileTimer {
			return realTimer{t: time.AfterFunc(d, f)}
		},
	}
}

// PushActivation inserts an Activation onto the named (zone, axis)
// stack and schedules a reconcile. Returns an error if act.Computer
// name doesn't resolve in the registry — surfaces operator typos at
// push time rather than at next tick.
//
// Side effects:
//
//   - The Activation is inserted via PUSH_POLICY_REFRESH semantics
//     (or REPLACE if explicitly set) — see stack.go.
//   - An immediate reconcile is scheduled (synchronous; the caller's
//     goroutine drives it). Pushers that want async should call
//     PushActivation from their own goroutine.
//   - If act.Ttl > 0, an afterFunc timer is scheduled to wake the
//     reconciler at expiration so the override pops within
//     milliseconds rather than at the next 60s tick. Multiple TTLs
//     for the same id leave the latest timer; the older one becomes
//     a harmless no-op when it fires (lazy expiration in top()
//     handles it).
func (r *Reconciler) PushActivation(ctx context.Context, zone string, axis iotv1proto.AxisKind, act *iotv1proto.Activation) error {
	ctx, span := r.tracer.Start(ctx, "Reconciler.PushActivation",
		trace.WithAttributes(
			attribute.String("zone", zone),
			attribute.String("axis", axis.String()),
		),
	)
	defer span.End()

	if zone == "" {
		return errors.New("PushActivation: zone is required")
	}
	if axis == iotv1proto.AxisKind_AXIS_KIND_UNSPECIFIED {
		return errors.New("PushActivation: axis is required")
	}
	if act == nil {
		return errors.New("PushActivation: activation is required")
	}
	span.SetAttributes(
		attribute.String("computer", act.ComputerName),
		attribute.String("source_kind", act.SourceKind.String()),
		attribute.String("source_name", act.SourceName),
		attribute.String("push_policy", act.PushPolicy.String()),
		attribute.Int64("priority", int64(act.Priority)),
		attribute.Int64("ttl_ms", act.Ttl.AsDuration().Milliseconds()),
	)

	policy := r.policy(zone)
	if err := policy.pushActivation(axis, act, r.logger); err != nil {
		span.RecordError(err)
		return err
	}
	metricReconcilePushTotal.WithLabelValues(
		zone, axis.String(), act.SourceKind.String(), act.PushPolicy.String(),
	).Inc()

	if act.Ttl != nil {
		ttl := act.Ttl.AsDuration()
		if ttl > 0 {
			// Stop any existing TTL timer for this (zone, axis, source)
			// before scheduling a fresh one. Without this, every
			// REFRESH push leaves a stale timer that fires later and
			// produces a redundant no-op reconcile — noise in metrics
			// and traces for motion sensors that refresh every few
			// seconds. The key includes the activation id so different
			// sources on the same (zone, axis) don't cancel each other.
			timerKey := fmt.Sprintf("%s/%s/%s", zone, axis, activationID(act.SourceKind, act.SourceName))
			zoneCapture := zone

			r.ttlTimersMu.Lock()
			if old, ok := r.ttlTimers[timerKey]; ok {
				old.Stop()
			}
			r.ttlTimers[timerKey] = r.afterFunc(ttl, func() {
				if err := r.ReconcileZone(context.Background(), zoneCapture, r.now()); err != nil {
					r.logger.Debug("reconciler: TTL-driven reconcile failed",
						slog.String("zone", zoneCapture),
						slog.Any("err", err),
					)
				}
			})
			r.ttlTimersMu.Unlock()
		}
	}

	// Immediate reconcile after push so the operator sees the lamp
	// respond without waiting for the periodic tick.
	if err := r.ReconcileZone(ctx, zone, r.now()); err != nil {
		return fmt.Errorf("reconcile after push: %w", err)
	}
	return nil
}

// ReconcileZone composes the per-axis target for `zone` at time `now`
// and flushes via ZoneKeeper.ApplyValues only if the target differs
// from the last-applied value. Safe to call concurrently from
// multiple goroutines for different zones; per-zone serialized.
//
// Returns nil when the zone has no stack (unknown to the reconciler)
// — operators get a no-op for unmanaged zones rather than an error.
func (r *Reconciler) ReconcileZone(ctx context.Context, zone string, now time.Time) error {
	ctx, span := r.tracer.Start(ctx, "Reconciler.ReconcileZone",
		trace.WithAttributes(attribute.String("zone", zone)),
	)
	defer span.End()

	lock := r.zoneLock(zone)
	lock.Lock()
	defer lock.Unlock()

	r.policiesMu.RLock()
	policy, ok := r.policies[zone]
	r.policiesMu.RUnlock()
	if !ok {
		// Zone has never been pushed to; nothing to reconcile. Skip the
		// duration metric — the periodic tick calls ReconcileZone for
		// every cfg.ReconcileZones entry whether or not it has been
		// pushed to, and including those near-instant no-ops would
		// drag the histogram's percentiles toward zero.
		return nil
	}

	tStart := time.Now()
	defer func() {
		metricReconcileTickDuration.Observe(time.Since(tStart).Seconds())
	}()

	// Memory bookkeeping: drop expired entries before the read.
	// top() also filters via expired() so this is non-load-bearing
	// for correctness; it keeps the slice bounded.
	axes := []iotv1proto.AxisKind{
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE,
		iotv1proto.AxisKind_AXIS_KIND_COLOR,
	}
	for _, axis := range axes {
		if s, ok := policy.stacks[axis]; ok {
			s.removeExpired(now)
		}
	}

	// Capture the per-axis top for this tick — used both to compose
	// the target and to detect transitions vs the previous tick.
	tops := policy.activeContributorsByAxis(now)
	r.observeTopChanges(zone, tops, span)
	r.sampleStackDepths(zone, policy)

	// Compose target from the top of each axis stack.
	target, err := policy.applyTopToValues(ctx, now, r.location)
	if err != nil {
		span.RecordError(err)
		metricReconcileComputeError.WithLabelValues(zone).Inc()
		// Still reflect what we observed; operators want to see the
		// stack state even when its top's Computer errored.
		r.reflectStatus(ctx, zone, policy, now)
		return err
	}

	// Compare to last-applied. If the composed target matches what
	// we last sent for this zone, suppress the ZoneKeeper RPC.
	r.lastAppliedMu.Lock()
	last, hadLast := r.lastApplied[zone]
	same := hadLast && reflect.DeepEqual(target, last)
	r.lastAppliedMu.Unlock()
	if same {
		metricReconcileApplySuppressed.WithLabelValues(zone, "no_delta").Inc()
		span.SetAttributes(attribute.Bool("delta", false))
		// Refresh-only pushes (motion sensor heartbeat) bump pushed_at
		// but compose the same target. The operator still wants to see
		// the updated last_reconciled_at + a refreshed expires_at on
		// the active override.
		r.reflectStatus(ctx, zone, policy, now)
		return nil
	}

	// Empty target (no contributing Activations on any axis) is a
	// no-op rather than "write UNSPECIFIED everywhere." Operators
	// get the same behavior as today's imperative path when no
	// Condition is active.
	if isEmptyTarget(target) {
		metricReconcileApplySuppressed.WithLabelValues(zone, "empty_target").Inc()
		span.SetAttributes(attribute.Bool("delta", false), attribute.Bool("empty", true))
		r.reflectStatus(ctx, zone, policy, now)
		return nil
	}

	if _, err := r.zonekeeperClient.ApplyValues(ctx, target.ToApplyValuesRequest(zone)); err != nil {
		span.RecordError(err)
		metricReconcileApplyError.WithLabelValues(zone).Inc()
		return fmt.Errorf("ApplyValues: %w", err)
	}

	r.lastAppliedMu.Lock()
	r.lastApplied[zone] = target
	r.lastAppliedMu.Unlock()

	metricReconcileApplied.WithLabelValues(zone).Inc()
	span.SetAttributes(
		attribute.Bool("delta", true),
		attribute.String("state", target.State.String()),
		attribute.String("brightness", target.Brightness.String()),
		attribute.String("color_temperature", target.ColorTemperature.String()),
		attribute.String("color", target.Color),
	)

	// Reflect the new stack state into Zone.Status. Errors are
	// logged but don't fail the reconcile — the apply already
	// succeeded, status is observability.
	r.reflectStatus(ctx, zone, policy, now)
	return nil
}

// observeTopChanges compares this tick's top-of-stack per axis against
// the previous tick and emits a counter + Info log on transitions.
// "Source" here means the (kind, name) tuple — same kind with a new
// name (e.g. SOURCE_KIND_BINDING/foyer-motion-evening →
// SOURCE_KIND_BINDING/foyer-motion-nightvision) counts as a change.
//
// Empty-top → present-top transitions show as
// from_source_kind=SOURCE_KIND_UNSPECIFIED → to_source_kind=<actual>.
// Present → empty (override expired with no fallback) is the inverse.
func (r *Reconciler) observeTopChanges(zone string, tops map[iotv1proto.AxisKind]*runtimeActivation, span trace.Span) {
	r.prevTopSourceMu.Lock()
	defer r.prevTopSourceMu.Unlock()

	for _, axis := range []iotv1proto.AxisKind{
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE,
		iotv1proto.AxisKind_AXIS_KIND_COLOR,
	} {
		key := fmt.Sprintf("%s/%s", zone, axis)
		var newID, newKind string
		if top, ok := tops[axis]; ok && top != nil {
			newID = top.id
			newKind = top.SourceKind.String()
		} else {
			newKind = iotv1proto.SourceKind_SOURCE_KIND_UNSPECIFIED.String()
		}

		prev, hadPrev := r.prevTopSource[key]
		if !hadPrev && newID == "" {
			// Was empty, stayed empty — no event.
			continue
		}
		if prev == newID {
			continue
		}

		// Decode previous kind for the metric label. Stored format
		// matches `activationID` (kind:name); empty string means
		// "previously empty," so we report UNSPECIFIED there.
		fromKind := iotv1proto.SourceKind_SOURCE_KIND_UNSPECIFIED.String()
		if hadPrev && prev != "" {
			// id is "SOURCE_KIND_XXX:name"; everything before the colon
			// is the kind. Cheap parse — we control the format in
			// stack.go's activationID.
			if idx := strings.IndexByte(prev, ':'); idx >= 0 {
				fromKind = prev[:idx]
			}
		}

		metricReconcileTopChangedTotal.WithLabelValues(
			zone, axis.String(), fromKind, newKind,
		).Inc()

		r.logger.Info("reconciler: top changed",
			slog.String("zone", zone),
			slog.String("axis", axis.String()),
			slog.String("from", prev),
			slog.String("to", newID),
		)
		span.AddEvent("top_changed",
			trace.WithAttributes(
				attribute.String("axis", axis.String()),
				attribute.String("from", prev),
				attribute.String("to", newID),
			),
		)
		r.prevTopSource[key] = newID
	}
}

// sampleStackDepths emits a gauge sample per axis with at least one
// entry. Axes that have never been pushed don't get a gauge sample —
// avoids labels with permanent zero values.
func (r *Reconciler) sampleStackDepths(zone string, policy *zonePolicy) {
	for axis, s := range policy.stacks {
		depth := len(s.snapshot())
		metricReconcileStackDepth.WithLabelValues(zone, axis.String()).Set(float64(depth))
	}
}

// reflectStatus writes the per-axis stack snapshot into the Zone CR's
// Status sub-resource via a strategic-merge Patch. The patch touches
// ONLY the reconciler-owned fields (`reconciler_stack`,
// `last_reconciled_at`); the zonekeeper's State / Brightness /
// ColorTemperature / Color fields are preserved by virtue of not
// appearing in the patch body.
//
// kubeClient may be nil in tests; the call is a no-op in that case.
// Errors are logged at Debug — Status reflection is observability,
// not load-bearing for the apply path.
func (r *Reconciler) reflectStatus(ctx context.Context, zone string, policy *zonePolicy, now time.Time) {
	if r.kubeClient == nil {
		return
	}

	entries := make([]apiv1.ReconcilerStackEntry, 0, 4)
	tops := policy.activeContributorsByAxis(now)
	for _, axis := range []iotv1proto.AxisKind{
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE,
		iotv1proto.AxisKind_AXIS_KIND_COLOR,
	} {
		s, ok := policy.stacks[axis]
		if !ok {
			continue
		}
		snap := s.snapshot()
		if len(snap) == 0 {
			continue
		}
		entry := apiv1.ReconcilerStackEntry{
			Axis:  axis.String(),
			Depth: int32(len(snap)),
		}
		if top, ok := tops[axis]; ok && top != nil {
			entry.Top = &apiv1.ReconcilerStackTop{
				Computer:   top.ComputerName,
				SourceKind: top.SourceKind.String(),
				SourceName: top.SourceName,
				Priority:   top.Priority,
				PushedAt:   metav1.NewTime(top.pushedAt()),
			}
			if top.Ttl.IsValid() && top.Ttl.AsDuration() > 0 {
				deadline := top.pushedAt().Add(top.Ttl.AsDuration())
				expires := metav1.NewTime(deadline)
				entry.Top.ExpiresAt = &expires
			}
		}
		entries = append(entries, entry)
	}

	statusPatch := map[string]any{
		"status": map[string]any{
			"reconciler_stack":   entries,
			"last_reconciled_at": metav1.NewTime(now),
		},
	}
	body, err := json.Marshal(statusPatch)
	if err != nil {
		r.logger.Debug("reconciler: status patch marshal failed",
			slog.String("zone", zone), slog.Any("err", err))
		return
	}

	// Use a typed Zone object so the controller-runtime client routes
	// to the right resource + Status sub-resource. Merge patch (RFC
	// 7396) on the Status sub-resource — disjoint fields with the
	// zonekeeper-written ones, so the merge composes correctly.
	z := &apiv1.Zone{}
	z.Namespace = statusZoneNamespace
	z.Name = zone
	if err := r.kubeClient.Status().Patch(ctx, z, kubeclient.RawPatch(types.MergePatchType, body)); err != nil {
		// NotFound is benign — a reconcile-managed zone in cfg might not
		// have a CR yet (operator-typo or pre-creation race). Log at
		// Debug so it doesn't drown out real failures.
		if apierrors.IsNotFound(err) {
			r.logger.Debug("reconciler: zone CR not found for status reflection",
				slog.String("zone", zone))
			return
		}
		r.logger.Debug("reconciler: status patch failed",
			slog.String("zone", zone), slog.Any("err", err))
	}
}


// policy returns the zonePolicy for `zone`, creating it lazily on
// first access. Write-locked so concurrent first-pushes for the same
// zone don't race on map creation.
func (r *Reconciler) policy(zone string) *zonePolicy {
	r.policiesMu.RLock()
	if p, ok := r.policies[zone]; ok {
		r.policiesMu.RUnlock()
		return p
	}
	r.policiesMu.RUnlock()

	r.policiesMu.Lock()
	defer r.policiesMu.Unlock()
	if p, ok := r.policies[zone]; ok {
		return p
	}
	p := newZonePolicy(zone)
	r.policies[zone] = p
	return p
}

// zoneLock returns the per-zone mutex, creating it lazily.
func (r *Reconciler) zoneLock(zone string) *sync.Mutex {
	r.zoneLocksMu.Lock()
	defer r.zoneLocksMu.Unlock()
	if m, ok := r.zoneLocks[zone]; ok {
		return m
	}
	m := &sync.Mutex{}
	r.zoneLocks[zone] = m
	return m
}

// isEmptyTarget reports whether every axis field is at its zero
// value. Used to suppress no-op ApplyValues calls when the stack
// has no contributors (e.g. all overrides expired and no background
// is defined).
func isEmptyTarget(t computer.ApplyValues) bool {
	return t.State == iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED &&
		t.Brightness == iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED &&
		t.BrightnessValue == 0 &&
		t.ColorTemperature == iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED &&
		t.ColorTemperatureKelvin == 0 &&
		t.Color == ""
}

// hasPolicy reports whether the reconciler has any state for `zone`.
// Returns true iff at least one PushActivation has arrived for the
// zone. Used by reconciler tests; the evaluator's routing decision
// uses Conditioner.isReconcileManaged (config-driven, not state-
// driven) which is the right source of truth for "should this zone
// route through the reconciler?"
func (r *Reconciler) hasPolicy(zone string) bool {
	r.policiesMu.RLock()
	defer r.policiesMu.RUnlock()
	_, ok := r.policies[zone]
	return ok
}
