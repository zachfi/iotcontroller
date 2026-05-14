package conditioner

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// runEvaluator is the periodic eval-loop goroutine. It ticks every
// cfg.EvaluationInterval and evaluates Remediations whose behaviour is
// time-driven rather than event-driven: today that's just
// `active_compute` Remediations. Event-driven paths (Alert RPC,
// ActivateCondition RPC from a Binding match) bypass this loop and
// apply immediately.
//
// Kept separate from the existing runTimerLoop (cron schedules + alert
// window closure) so the eval cadence (60 s, fine for sun tracking) is
// independent of the existing 15 s timer (used for sub-minute cron
// precision and prompt alert window closure). Stages 5–6 of the
// unified-evaluator plan consolidate the two loops; this PR preserves
// both.
func (c *Conditioner) runEvaluator(ctx context.Context) {
	if c.cfg.EvaluationInterval <= 0 {
		c.logger.Info("evaluator disabled (interval <= 0)")
		return
	}

	c.logger.Info("starting evaluator",
		slog.Duration("interval", c.cfg.EvaluationInterval),
		slog.Any("computers", computer.Names()),
	)

	t := time.NewTicker(c.cfg.EvaluationInterval)
	defer t.Stop()

	// Fire once immediately so the operator gets a first apply without
	// waiting a full tick after pod start.
	c.evaluate(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.evaluate(ctx)
		}
	}
}

// evaluate is one tick of the eval loop. Walks enabled Conditions and
// invokes any `active_compute` Remediations.
func (c *Conditioner) evaluate(ctx context.Context) {
	var err error
	ctx, span := c.tracer.Start(ctx, "Conditioner.evaluate")
	defer span.End()

	metricEvalTotal.Inc()
	start := time.Now()
	defer func() {
		metricEvalDuration.Observe(time.Since(start).Seconds())
	}()

	list := &apiv1.ConditionList{}
	if err = c.kubeClient.List(ctx, list, &kubeclient.ListOptions{}); err != nil {
		c.logger.Error("evaluator: failed to list conditions", "err", err)
		span.RecordError(err)
		return
	}

	var applied int
	for i := range list.Items {
		cond := &list.Items[i]
		if !cond.Spec.Enabled {
			continue
		}
		// Stage 5 migration thermometer: if any Condition still
		// carries a Spec.Schedule, surface it via a once-per-process
		// log so the operator notices what's left to migrate. The
		// runTimer cron path still runs alongside in v0.5.x; this
		// is just a signal, not an outage.
		c.maybeWarnDeprecatedSchedule(cond)

		// Alert-driven Conditions live on the Alert RPC path; time_intervals
		// on them are *gates* for the alert path ("only act when the alert
		// fires AND we're in this window"), not standalone triggers. If the
		// eval loop also fires them, two heater-style Conditions on the same
		// zone with opposite active_state (low-temp=on / high-temp=off) end
		// up activating sequentially each tick — ON then OFF, the rapid
		// relay-click pattern. Skip alert-matched Conditions; the Alert RPC
		// path handles them.
		alertDriven := len(cond.Spec.Matches) > 0

		for _, rem := range cond.Spec.Remediations {
			switch {
			case rem.ActiveCompute != "":
				if c.evaluateCompute(ctx, cond.Name, rem) {
					applied++
				}
			case len(rem.TimeIntervals) > 0 && !alertDriven:
				// Stage 5: TimeInterval-driven state/scene
				// Remediations are now eval-loop-applied, replacing
				// the cron path in pkg/conditioner/schedule for the
				// "always-on within window" pattern.
				// activateRemediation already handles
				// withinActiveWindow + applyDesired idempotency, so
				// the eval loop just delegates.
				if err := c.activateRemediation(ctx, cond.Name, rem); err != nil {
					c.logger.Debug("evaluator: timeWindow apply failed",
						slog.String("condition", cond.Name),
						slog.String("zone", rem.Zone),
						"err", err,
					)
				}
				applied++
			}
		}
	}

	span.SetAttributes(
		attribute.Int("conditions", len(list.Items)),
		attribute.Int("applied", applied),
	)
}

// maybeWarnDeprecatedSchedule emits a once-per-Condition warning when
// Spec.Schedule is set. The cron path keeps working — this is just the
// operator-visible signal that the Condition should be migrated to
// TimeIntervals before Stage 6 deletes the field.
//
// Tracking the warned set in-process is fine: a pod restart re-warns,
// which is the right cadence for ongoing visibility without spamming
// every 60s tick.
func (c *Conditioner) maybeWarnDeprecatedSchedule(cond *apiv1.Condition) {
	if cond.Spec.Schedule == "" {
		return
	}
	c.deprecatedScheduleMu.Lock()
	defer c.deprecatedScheduleMu.Unlock()
	if c.deprecatedSchedule == nil {
		c.deprecatedSchedule = make(map[string]bool)
	}
	if c.deprecatedSchedule[cond.Name] {
		return
	}
	c.deprecatedSchedule[cond.Name] = true
	c.logger.Warn("Condition.Spec.Schedule is deprecated; migrate to time_intervals on each Remediation. Will be removed in a future release.",
		slog.String("condition", cond.Name),
		slog.String("schedule", cond.Spec.Schedule),
	)
}

// evaluateCompute invokes the named computer for one Remediation and
// applies the result to its zone via ZoneKeeper.ApplyValues. Returns
// true when an apply was attempted (success or failure both count).
//
// Time-gated by withinActiveWindow if the Remediation has TimeIntervals
// — a computer that should only run after sunset uses
// `time_intervals.sun_relative` to gate the eval loop, not the computer
// itself.
func (c *Conditioner) evaluateCompute(ctx context.Context, condName string, rem apiv1.Remediation) bool {
	if len(rem.TimeIntervals) > 0 && !c.withinActiveWindow(ctx, rem, time.Now()) {
		metricApplySuppressed.WithLabelValues(condName, rem.Zone, "time-gated").Inc()
		return false
	}

	comp, ok := computer.Get(rem.ActiveCompute)
	if !ok {
		c.logger.Warn("evaluator: unknown computer",
			slog.String("condition", condName),
			slog.String("zone", rem.Zone),
			slog.String("compute", rem.ActiveCompute),
		)
		metricEvalComputeUnknown.WithLabelValues(rem.ActiveCompute).Inc()
		return false
	}

	loc := computer.Location{
		Lat: c.cfg.Location.Lat,
		Lon: c.cfg.Location.Lon,
	}

	ctx, span := c.tracer.Start(ctx, "Conditioner.evaluateCompute",
		trace.WithAttributes(
			attribute.String("condition", condName),
			attribute.String("zone", rem.Zone),
			attribute.String("compute", rem.ActiveCompute),
		),
	)
	defer span.End()

	// Inject the parent Condition + Zone so Computers that emit
	// metrics or logs can label them by the operator-visible name
	// rather than args-hash. Reserved keys (`_condition`, `_zone`)
	// are underscored to stay out of operator-authored arg space;
	// any args.* the operator sets that collide will be overwritten
	// here on purpose.
	augmentedArgs := make(map[string]string, len(rem.ActiveComputeArgs)+2)
	for k, v := range rem.ActiveComputeArgs {
		augmentedArgs[k] = v
	}
	augmentedArgs["_condition"] = condName
	augmentedArgs["_zone"] = rem.Zone

	vals, err := comp.Compute(ctx, time.Now(), loc, augmentedArgs)
	if err != nil {
		c.logger.Error("evaluator: compute failed",
			slog.String("condition", condName),
			slog.String("zone", rem.Zone),
			slog.String("compute", rem.ActiveCompute),
			"err", err,
		)
		metricEvalComputeError.WithLabelValues(rem.ActiveCompute).Inc()
		span.RecordError(err)
		return false
	}

	req := &iotv1proto.ApplyValuesRequest{
		Name:             rem.Zone,
		State:            vals.State,
		Brightness:       vals.Brightness,
		ColorTemperature: vals.ColorTemperature,
		Color:            vals.Color,
	}
	if _, err = c.zonekeeperClient.ApplyValues(ctx, req); err != nil {
		c.logger.Error("evaluator: apply values failed",
			slog.String("condition", condName),
			slog.String("zone", rem.Zone),
			"err", err,
		)
		metricEvalApplyError.WithLabelValues(rem.ActiveCompute).Inc()
		span.RecordError(err)
		return true
	}

	span.SetAttributes(
		attribute.String("state", vals.State.String()),
		attribute.String("brightness", vals.Brightness.String()),
		attribute.String("color_temperature", vals.ColorTemperature.String()),
		attribute.String("color", vals.Color),
	)
	return true
}
