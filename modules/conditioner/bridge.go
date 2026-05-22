package conditioner

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// bridge.go — Phase B push bridge. Translates declared Remediations
// (the operator-authored YAML on Condition CRs) into PushActivation
// events for the reconciler. Called from the eval loop for zones in
// cfg.ReconcileZones — the same per-tick walk that previously
// dispatched to activateRemediation now dispatches to the bridge.
//
// Two flavors of Remediation:
//
//   - active_compute: push the named Computer to whichever axes its
//     output populates. The bridge calls Compute once to discover the
//     non-zero fields, then pushes one Activation per axis. Reconcile-
//     time re-evaluation is the authoritative output; the bridge's
//     call is only for axis discovery.
//
//   - active_state / active_scene: bridge resolves the Scene CR to
//     per-axis values, then pushes one Activation per claimed axis
//     using the "static" Computer. Args carry the resolved enum
//     strings (state, brightness, color_temperature, color); the
//     reconciler invokes staticComputer.Compute each tick, which just
//     unpacks args into ApplyValues.
//
// TimeInterval gating: same withinActiveWindow check as the imperative
// path. Outside the window → no push. The Activations don't ride past
// their window — TTL is sized to 3 evaluation intervals, so an empty
// next tick (window closed) starts the activation aging out within 60s
// and fully gone within ~180s.
//
// PUSH_POLICY_REFRESH: a re-push within window bumps PushedAt + TTL in
// place on the existing entry (same source_kind:source_name id),
// keeping the stack bounded at one entry per Condition+axis.
//
// What's NOT in this commit (deliberate scope cut):
//
//   - Binding-driven activateRemediation push (motion events) — those
//     still flow through the imperative path. Concordant-overlap
//     between time-window and motion writers during their shared
//     window (e.g. foyer-motion-evening + foyer-dusk both wanting
//     state=on) is absorbed by the dedup caches on both sides.
//
//   - Alert-driven activateRemediation push — same reason, follow-up.

const (
	// bridgePushPriority is the priority the bridge assigns to all
	// TimeInterval / active_compute pushes. Background-ish: lower than
	// motion bindings (which will land at 100 in the follow-up) and
	// lower than alerts (200). Bumping this requires considering how
	// it composes with future SOURCE_KIND_BINDING / SOURCE_KIND_ALERT
	// pushes — the stack picks max(priority) with ties broken by
	// recency.
	bridgePushPriority = 50

	// bridgeTTLMultiplier multiplies cfg.EvaluationInterval to get the
	// TTL for bridge-pushed Activations. 3× means a missed tick (one
	// 60s gap in evaluator output) doesn't expire the Activation;
	// 2 missed ticks does. Reasonable on a 60s tick: a 3-minute outage
	// in the conditioner itself is the only way to lose state.
	bridgeTTLMultiplier = 3
)

// bridgeReconciledRemediation translates a single Remediation into
// PushActivation calls. No-ops outside the Remediation's active
// window. Error logging only; no errors propagated — the bridge is
// best-effort and the next tick will retry.
func (c *Conditioner) bridgeReconciledRemediation(ctx context.Context, condName string, rem apiv1.Remediation) {
	now := time.Now()

	// Time-gate matches evaluateCompute / activateRemediation: only
	// push while in the window. Outside the window, existing
	// Activations age out via their TTL.
	if len(rem.TimeIntervals) > 0 && !c.withinActiveWindow(ctx, rem, now) {
		return
	}

	ttlDuration := bridgeTTLMultiplier * c.cfg.EvaluationInterval
	if ttlDuration <= 0 {
		ttlDuration = 3 * time.Minute
	}
	ttl := durationpb.New(ttlDuration)
	pushedAt := timestamppb.New(now)

	push := func(axis iotv1proto.AxisKind, computerName string, args map[string]string) {
		act := &iotv1proto.Activation{
			ComputerName: computerName,
			Args:         args,
			SourceKind:   iotv1proto.SourceKind_SOURCE_KIND_TIME_WINDOW,
			SourceName:   condName,
			PushedAt:     pushedAt,
			Ttl:          ttl,
			Priority:     bridgePushPriority,
			PushPolicy:   iotv1proto.PushPolicy_PUSH_POLICY_REFRESH,
		}
		if err := c.reconciler.PushActivation(ctx, rem.Zone, axis, act); err != nil {
			c.logger.Debug("bridge: push failed",
				slog.String("condition", condName),
				slog.String("zone", rem.Zone),
				slog.String("axis", axis.String()),
				slog.String("computer", computerName),
				slog.Any("err", err),
			)
		}
	}

	// Branch 1: active_compute — discover claimed axes by calling
	// Compute once, then push to each axis whose field came back
	// non-zero. Reconcile-time Compute is authoritative; the bridge's
	// call is purely for axis discovery.
	if rem.ActiveCompute != "" {
		c.bridgeActiveCompute(ctx, condName, rem, now, push)
		return
	}

	// Branch 2: active_state / active_scene — pre-resolve to per-axis
	// values, push via "static" Computer with axis-specific args.
	if rem.ActiveState != "" {
		state := zoneState(rem.ActiveState)
		if state != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
			push(iotv1proto.AxisKind_AXIS_KIND_STATE, computer.StaticName, map[string]string{
				"state": state.String(),
			})
		}
	}

	if rem.ActiveScene != "" {
		c.bridgeActiveScene(ctx, condName, rem, push)
	}
}

// bridgeActiveCompute handles the active_compute branch: call the
// named Computer once to discover its claimed axes, then push to each.
func (c *Conditioner) bridgeActiveCompute(
	ctx context.Context,
	condName string,
	rem apiv1.Remediation,
	now time.Time,
	push func(iotv1proto.AxisKind, string, map[string]string),
) {
	comp, ok := computer.Get(rem.ActiveCompute)
	if !ok {
		c.logger.Debug("bridge: unknown computer",
			slog.String("condition", condName),
			slog.String("zone", rem.Zone),
			slog.String("compute", rem.ActiveCompute),
		)
		return
	}

	// Build the same augmented args the imperative path uses, so a
	// Computer that reads `_condition` / `_zone` (for metric labels or
	// per-zone caching) sees the same context in both paths.
	augmented := make(map[string]string, len(rem.ActiveComputeArgs)+2)
	for k, v := range rem.ActiveComputeArgs {
		augmented[k] = v
	}
	augmented["_condition"] = condName
	augmented["_zone"] = rem.Zone

	loc := computer.Location{Lat: c.cfg.Location.Lat, Lon: c.cfg.Location.Lon}
	out, err := comp.Compute(ctx, now, loc, augmented)
	if err != nil {
		c.logger.Debug("bridge: compute for axis discovery failed",
			slog.String("condition", condName),
			slog.String("zone", rem.Zone),
			slog.String("compute", rem.ActiveCompute),
			slog.Any("err", err),
		)
		return
	}

	for _, axis := range claimedAxes(out) {
		push(axis, rem.ActiveCompute, augmented)
	}
}

// bridgeActiveScene resolves the Scene CR and pushes per-axis static
// Activations for whichever fields the Scene actually populates.
func (c *Conditioner) bridgeActiveScene(
	ctx context.Context,
	condName string,
	rem apiv1.Remediation,
	push func(iotv1proto.AxisKind, string, map[string]string),
) {
	if c.kubeClient == nil {
		return
	}
	scene := &apiv1.Scene{}
	if err := c.kubeClient.Get(ctx, kubeclient.ObjectKey{Name: rem.ActiveScene, Namespace: "iot"}, scene); err != nil {
		c.logger.Debug("bridge: scene lookup failed",
			slog.String("condition", condName),
			slog.String("zone", rem.Zone),
			slog.String("scene", rem.ActiveScene),
			slog.Any("err", err),
		)
		return
	}

	if scene.Spec.Brightness != "" {
		push(iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS, computer.StaticName, map[string]string{
			"brightness": scene.Spec.Brightness,
		})
	}
	if scene.Spec.ColorTemperature != "" {
		push(iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE, computer.StaticName, map[string]string{
			"color_temperature": scene.Spec.ColorTemperature,
		})
	}
	if scene.Spec.Color != "" {
		push(iotv1proto.AxisKind_AXIS_KIND_COLOR, computer.StaticName, map[string]string{
			"color": scene.Spec.Color,
		})
	}
}

// claimedAxes returns the AxisKinds whose corresponding field on v is
// non-zero. Used by the bridge's active_compute branch for axis
// discovery — a Computer that only sets ColorTemperature claims only
// the AXIS_KIND_COLOR_TEMPERATURE axis, regardless of which axes other
// Computers might populate from the same args.
//
// BrightnessValue (continuous) counts as a brightness-axis claim
// independently of Brightness (enum), since the reconciler's
// per-axis switch passes both fields through together.
func claimedAxes(v computer.ApplyValues) []iotv1proto.AxisKind {
	var axes []iotv1proto.AxisKind
	if v.State != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
		axes = append(axes, iotv1proto.AxisKind_AXIS_KIND_STATE)
	}
	if v.Brightness != iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED || v.BrightnessValue != 0 {
		axes = append(axes, iotv1proto.AxisKind_AXIS_KIND_BRIGHTNESS)
	}
	if v.ColorTemperature != iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED || v.ColorTemperatureKelvin != 0 {
		axes = append(axes, iotv1proto.AxisKind_AXIS_KIND_COLOR_TEMPERATURE)
	}
	if v.Color != "" {
		axes = append(axes, iotv1proto.AxisKind_AXIS_KIND_COLOR)
	}
	return axes
}
