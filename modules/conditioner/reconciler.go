package conditioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

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

	// lastApplied caches the most recent target written via
	// ApplyValues per zone. The reconciler flushes only when the
	// composed target differs from lastApplied — the per-zone
	// equivalent of today's per-(condition, zone) applyDesired cache.
	lastAppliedMu sync.Mutex
	lastApplied   map[string]computer.ApplyValues

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
func NewReconciler(zk iotv1proto.ZoneKeeperServiceClient, loc computer.Location, logger *slog.Logger, tracer trace.Tracer) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	if tracer == nil {
		tracer = tracenoop.NewTracerProvider().Tracer("conditioner.reconciler")
	}
	return &Reconciler{
		zonekeeperClient: zk,
		location:         loc,
		logger:           logger.With("component", "reconciler"),
		tracer:           tracer,
		policies:         make(map[string]*zonePolicy),
		zoneLocks:        make(map[string]*sync.Mutex),
		lastApplied:      make(map[string]computer.ApplyValues),
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
	if zone == "" {
		return errors.New("PushActivation: zone is required")
	}
	if axis == iotv1proto.AxisKind_AXIS_KIND_UNSPECIFIED {
		return errors.New("PushActivation: axis is required")
	}
	if act == nil {
		return errors.New("PushActivation: activation is required")
	}

	policy := r.policy(zone)
	if err := policy.pushActivation(axis, act, r.logger); err != nil {
		return err
	}

	if act.Ttl != nil {
		ttl := act.Ttl.AsDuration()
		if ttl > 0 {
			zoneCapture := zone
			r.afterFunc(ttl, func() {
				// Use Background context — the original caller's
				// context may have closed by the time the timer
				// fires. The reconcile is the safety net for TTL
				// expiration anyway.
				if err := r.ReconcileZone(context.Background(), zoneCapture, r.now()); err != nil {
					r.logger.Debug("reconciler: TTL-driven reconcile failed",
						slog.String("zone", zoneCapture),
						slog.Any("err", err),
					)
				}
			})
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

	tStart := time.Now()
	defer func() {
		metricReconcileTickDuration.Observe(time.Since(tStart).Seconds())
	}()

	lock := r.zoneLock(zone)
	lock.Lock()
	defer lock.Unlock()

	r.policiesMu.RLock()
	policy, ok := r.policies[zone]
	r.policiesMu.RUnlock()
	if !ok {
		// Zone has never been pushed to; nothing to reconcile.
		return nil
	}

	// Memory bookkeeping: drop expired entries before the read.
	// top() also filters via expired() so this is non-load-bearing
	// for correctness; it keeps the slice bounded.
	for _, axis := range []iotv1proto.AxisKind{
		iotv1proto.AxisKind_AXIS_KIND_STATE,
		iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS,
		iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE,
		iotv1proto.AxisKind_AXIS_KIND_COLOR,
	} {
		if s, ok := policy.stacks[axis]; ok {
			s.removeExpired(now)
		}
	}

	// Compose target from the top of each axis stack.
	target, err := policy.applyTopToValues(ctx, now, r.location)
	if err != nil {
		span.RecordError(err)
		metricReconcileComputeError.WithLabelValues(zone).Inc()
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
		return nil
	}

	// Empty target (no contributing Activations on any axis) is a
	// no-op rather than "write UNSPECIFIED everywhere." Operators
	// get the same behavior as today's imperative path when no
	// Condition is active.
	if isEmptyTarget(target) {
		metricReconcileApplySuppressed.WithLabelValues(zone, "empty_target").Inc()
		span.SetAttributes(attribute.Bool("delta", false), attribute.Bool("empty", true))
		return nil
	}

	req := &iotv1proto.ApplyValuesRequest{
		Name:                   zone,
		State:                  target.State,
		Brightness:             target.Brightness,
		ColorTemperature:       target.ColorTemperature,
		Color:                  target.Color,
		BrightnessValue:        target.BrightnessValue,
		ColorTemperatureKelvin: target.ColorTemperatureKelvin,
	}
	if _, err := r.zonekeeperClient.ApplyValues(ctx, req); err != nil {
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
	return nil
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

// IsManaged reports whether `zone` is in the reconciler's managed
// set. Used by the evaluator branch to decide which apply path
// handles each zone.
func (r *Reconciler) IsManaged(zone string) bool {
	r.policiesMu.RLock()
	defer r.policiesMu.RUnlock()
	_, ok := r.policies[zone]
	return ok
}
