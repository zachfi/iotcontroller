package conditioner

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// shadow.go is the read-only resolver experiment. Per the architecture
// direction parked in memory on 2026-05-21: we want to evaluate whether
// today's imperative Condition path is equivalent to a declarative
// "compose per axis from all currently-gated Remediations" reconciler.
// If it is, the rewrite is purely about expressing composition
// explicitly. If it isn't, the disagreements name the bugs we keep
// catching by hand.
//
// The shadow function walks Conditions and composes a per-axis target
// — never applying it. It's invoked once per zone at the end of each
// evaluate() tick, after the imperative path has already done its
// work. The output is logged and counted via metrics; the imperative
// path is unchanged.
//
// V1 scope (intentionally narrow to ship quickly with no behavioral
// risk):
//
//   IN — Remediations with TimeIntervals AND no Matches. These are
//        the eval-loop-applicable Conditions (foyer-off,
//        foyer-color-nightvision, living-area-on-dusk,
//        foyer-motion-nightvision, etc.). The headline conflicts
//        we've caught all live here.
//
//   OUT — Remediations with active_compute. Skipped to avoid the
//        fade Computer's snapshot-store side effects; circadian /
//        sun_color_temperature / query are partial-output and
//        compose harmlessly but the value of including them is
//        smaller than the safety risk in v1.
//
//   OUT — Remediations with Matches (alert-driven). Activation state
//        depends on alert history we don't track here.
//
//   OUT — Binding-driven activations as a separate trigger source.
//        Note: if a Binding-referenced Condition ALSO has
//        TimeIntervals (most do — see foyer-motion-nightvision),
//        it's still in scope via the TimeIntervals gate. We just
//        don't model the per-event firing.
//
// Comparison target: the in-cluster Zone.Status (last applied state /
// brightness / color_temperature / color). Disagreements between
// shadow's composed target and Zone.Status mean either:
//
//   - a Condition outside shadow's scope (active_compute, alert,
//     transient Binding fire) wrote the current Status, or
//   - two in-scope Conditions disagreed on an axis and last-write-
//     wins picked the one we didn't pick — i.e. a structural
//     conflict.
//
// Multi-contributor axes are also tracked separately. If two in-scope
// Conditions both claim `state` on the same zone in the same tick,
// that's a hard conflict regardless of what Zone.Status currently
// reads.

// zoneTarget is the composed per-axis target. The fields mirror
// ApplyValuesRequest so a future reconciler implementation can pass
// it through with minimal translation. Values use the discrete enums
// only — continuous Kelvin / brightness values come from Computers
// which are out-of-scope for v1.
type zoneTarget struct {
	State            iotv1proto.ZoneState
	Brightness       iotv1proto.Brightness
	ColorTemperature iotv1proto.ColorTemperature
	Color            string

	// Scene name when set via a scene-applying Remediation. Recorded
	// for diagnostic logging; the scene's underlying values aren't
	// resolved here (would require a Scene cache, deferred to v2).
	Scene string
}

// axis names — used as metric labels and in trace logs.
const (
	axisState            = "state"
	axisBrightness       = "brightness"
	axisColorTemperature = "color_temperature"
	axisColor            = "color"
	axisScene            = "scene"
)

// shadowContributor records one Remediation's claim on one axis. The
// `value` field is the rendered enum string (or hex for color). Used
// to surface multi-contributor conflicts with operator-readable names.
type shadowContributor struct {
	condition string
	value     string
}

// shadowTrace is the full result for one zone for one eval tick. The
// composed target is what a reconciler would apply; the per-axis
// contributor lists name the Remediations that claimed each axis,
// in declared-list order (matching today's last-write-wins resolution
// when there are multiple).
type shadowTrace struct {
	zone   string
	target zoneTarget

	// Per-axis contributor lists. A list of length > 1 means multiple
	// Remediations on the same zone in the same tick claimed the same
	// axis — a structural conflict in the declarative model.
	contributors map[string][]shadowContributor
}

// hasConflict reports whether any axis has more than one contributor.
func (t shadowTrace) hasConflict() bool {
	for _, list := range t.contributors {
		if len(list) > 1 {
			return true
		}
	}
	return false
}

// conflictAxes returns the sorted list of axis names with > 1
// contributor. Used to emit one metric increment per conflicting axis
// per tick.
func (t shadowTrace) conflictAxes() []string {
	var out []string
	for axis, list := range t.contributors {
		if len(list) > 1 {
			out = append(out, axis)
		}
	}
	sort.Strings(out)
	return out
}

// computeZoneTarget walks all Conditions and composes the target for
// `zone` at time `now`. Read-only: no Computer.Compute calls, no
// ZoneKeeper RPCs, no kube writes. Safe to call from any goroutine.
//
// Resolution within an axis is last-write-wins in declared list
// order, matching today's imperative path. The contributor list is
// preserved so an operator can see all claims, not just the winner.
func (c *Conditioner) computeZoneTarget(ctx context.Context, zone string, now time.Time) (shadowTrace, error) {
	trace := shadowTrace{
		zone:         zone,
		contributors: make(map[string][]shadowContributor, 5),
	}

	list := &apiv1.ConditionList{}
	if err := c.kubeClient.List(ctx, list, &kubeclient.ListOptions{}); err != nil {
		return trace, err
	}

	for i := range list.Items {
		cond := &list.Items[i]
		if !cond.Spec.Enabled {
			continue
		}

		// Out-of-scope filter #1: alert-driven Conditions.
		// Activation depends on alert history; v1 doesn't model it.
		if len(cond.Spec.Matches) > 0 {
			continue
		}

		for _, rem := range cond.Spec.Remediations {
			if rem.Zone != zone {
				continue
			}

			// Out-of-scope filter #2: active_compute Remediations.
			// Skipped in v1 to avoid Computer side effects (fade's
			// snapshot store) and because the partial-output
			// Computers don't drive the conflicts we want to catch.
			if rem.ActiveCompute != "" {
				continue
			}

			// In-scope filter: must be TimeInterval-driven (no
			// TimeIntervals + no active_compute + no Matches = a
			// Remediation that only fires when externally triggered,
			// e.g. Binding-only activations).
			if len(rem.TimeIntervals) == 0 {
				continue
			}

			// Time-window gate using the imperative path's helper —
			// matching semantics is the whole point of the
			// comparison.
			if !c.withinActiveWindow(ctx, rem, now) {
				continue
			}

			// In scope. Aggregate this Remediation's contributions.
			condName := cond.Name

			if s := strings.TrimSpace(rem.ActiveState); s != "" {
				stateEnum := zoneState(s)
				// "toggle" resolves to UNSPECIFIED here because it
				// can't be evaluated without state history (the
				// imperative path's resolveToggleState reads
				// Zone.Status at fire time). Record the contributor
				// as the raw string so the trace log still shows
				// which Remediation asserted "toggle"; just don't
				// let it move the target.
				if stateEnum != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
					trace.target.State = stateEnum
				}
				trace.contributors[axisState] = append(
					trace.contributors[axisState],
					shadowContributor{condition: condName, value: s},
				)
			}

			if s := strings.TrimSpace(rem.ActiveScene); s != "" {
				trace.target.Scene = s
				trace.contributors[axisScene] = append(
					trace.contributors[axisScene],
					shadowContributor{condition: condName, value: s},
				)
				// Note: scene's underlying brightness/CT/color are
				// not resolved here. v2 will look those up via a
				// Scene cache and back-propagate to the
				// per-axis contributor lists.
			}
		}
	}

	return trace, nil
}

// runShadow is the per-tick hook from evaluator.go. Computes targets
// for each zone referenced by an enabled Condition's Remediations,
// compares against in-cluster Zone.Status, and emits log lines +
// metrics for disagreements and multi-contributor conflicts. Never
// triggers a write.
//
// Performance: bounded by (zones touched by Conditions) * (work per
// zone). One List call per zone for Status read. For ~25 zones in
// production this adds ~25 kube GETs per tick — well under the eval
// loop's 60s budget.
func (c *Conditioner) runShadow(ctx context.Context) {
	ctx, span := c.tracer.Start(ctx, "Conditioner.runShadow")
	defer span.End()

	list := &apiv1.ConditionList{}
	if err := c.kubeClient.List(ctx, list, &kubeclient.ListOptions{}); err != nil {
		span.RecordError(err)
		return
	}

	// Collect the set of zones any enabled Condition references.
	zones := make(map[string]struct{})
	for i := range list.Items {
		cond := &list.Items[i]
		if !cond.Spec.Enabled {
			continue
		}
		for _, rem := range cond.Spec.Remediations {
			if rem.Zone != "" {
				zones[rem.Zone] = struct{}{}
			}
		}
	}

	now := time.Now()
	for zone := range zones {
		t, err := c.computeZoneTarget(ctx, zone, now)
		if err != nil {
			c.logger.Debug("shadow: compute failed",
				slog.String("zone", zone),
				"err", err,
			)
			continue
		}

		// Conflict: multiple in-scope Conditions claim the same axis
		// on this zone in this tick. Emit one metric increment per
		// conflicting axis; log once per zone with the axis list.
		if t.hasConflict() {
			conflictAxes := t.conflictAxes()
			for _, axis := range conflictAxes {
				metricShadowConflict.WithLabelValues(zone, axis).Inc()
			}
			c.logger.Info("shadow: multi-contributor conflict on zone",
				slog.String("zone", zone),
				slog.String("axes", strings.Join(conflictAxes, ",")),
				slog.String("contributors", formatContributors(t)),
			)
			span.AddEvent("shadow conflict",
				trace.WithAttributes(
					attribute.String("zone", zone),
					attribute.String("axes", strings.Join(conflictAxes, ",")),
				),
			)
		}

		// Disagreement: shadow's composed target differs from
		// Zone.Status (last applied). Symptoms: an out-of-scope path
		// (active_compute Computer, alert, transient Binding fire,
		// manual button press) wrote the current Status. Useful to
		// correlate with motion / button / alert events overnight.
		actual, err := c.readZoneStatus(ctx, zone)
		if err != nil {
			// Zone not yet observed — first apply hasn't happened.
			// Not a disagreement, just no comparison possible.
			continue
		}
		if disagrees(t.target, actual) {
			metricShadowDisagreement.WithLabelValues(zone).Inc()
			c.logger.Info("shadow: target disagrees with Zone.Status",
				slog.String("zone", zone),
				slog.String("shadow_state", t.target.State.String()),
				slog.String("actual_state", actual.State.String()),
				slog.String("shadow_scene", t.target.Scene),
				slog.String("contributors", formatContributors(t)),
			)
		}
	}
}

// readZoneStatus reads the in-cluster Zone.Status for comparison.
// Cheap (informer-cache-backed kube GET). Returns the relevant
// fields as a zoneTarget for symmetric comparison.
func (c *Conditioner) readZoneStatus(ctx context.Context, zone string) (zoneTarget, error) {
	var z apiv1.Zone
	// Namespace is hardcoded "iot" elsewhere in conditioner.go (e.g.
	// activateRemediation's Zone Status read). When we factor that
	// out into config or a constant, this should follow.
	if err := c.kubeClient.Get(ctx, kubeclient.ObjectKey{Name: zone, Namespace: "iot"}, &z); err != nil {
		return zoneTarget{}, err
	}
	return zoneTarget{
		State:            zoneState(z.Status.State),
		Brightness:       parseBrightnessEnum(z.Status.Brightness),
		ColorTemperature: parseColorTemperatureEnum(z.Status.ColorTemperature),
		Color:            z.Status.Color,
	}, nil
}

// disagrees returns true if the shadow target differs from observed
// Status on the axes the shadow claims to know about. Empty/UNSPECIFIED
// fields on the shadow side are treated as "no claim" and don't count
// as disagreement — a v1 shadow that only knows state will not flag
// brightness mismatches.
func disagrees(shadow, actual zoneTarget) bool {
	if shadow.State != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED &&
		shadow.State != actual.State {
		return true
	}
	// Scene-driven Remediations don't directly assert brightness/CT
	// at the shadow layer (v1 doesn't resolve scenes). We compare
	// only the fields the shadow actually set. v2 will resolve
	// scenes and start comparing those axes too.
	return false
}

// formatContributors renders the per-axis contributor map as a
// compact diagnostic string. Stable axis order (alpha) and sorted
// contributors per axis so log lines are diffable across runs.
func formatContributors(t shadowTrace) string {
	if len(t.contributors) == 0 {
		return "(none)"
	}
	axes := make([]string, 0, len(t.contributors))
	for axis := range t.contributors {
		axes = append(axes, axis)
	}
	sort.Strings(axes)

	var b strings.Builder
	for i, axis := range axes {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(axis)
		b.WriteString("=[")
		contribs := t.contributors[axis]
		sort.Slice(contribs, func(i, j int) bool {
			return contribs[i].condition < contribs[j].condition
		})
		for j, c := range contribs {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(c.condition)
			b.WriteString(":")
			b.WriteString(c.value)
		}
		b.WriteString("]")
	}
	return b.String()
}

// parseBrightnessEnum / parseColorTemperatureEnum take Zone.Status's
// string form (the enum name, e.g. "BRIGHTNESS_DIM") and return the
// proto enum. Used only by the shadow comparison; the imperative path
// uses iotv1proto.Brightness_value directly inside zonekeeper. Empty
// string returns UNSPECIFIED.
func parseBrightnessEnum(s string) iotv1proto.Brightness {
	if s == "" {
		return iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED
	}
	if v, ok := iotv1proto.Brightness_value[s]; ok {
		return iotv1proto.Brightness(v)
	}
	return iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED
}

func parseColorTemperatureEnum(s string) iotv1proto.ColorTemperature {
	if s == "" {
		return iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED
	}
	if v, ok := iotv1proto.ColorTemperature_value[s]; ok {
		return iotv1proto.ColorTemperature(v)
	}
	return iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED
}

