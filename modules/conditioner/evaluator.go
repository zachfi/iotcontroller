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
		for _, rem := range cond.Spec.Remediations {
			if rem.ActiveCompute == "" {
				continue
			}
			if c.evaluateCompute(ctx, cond.Name, rem) {
				applied++
			}
		}
	}

	span.SetAttributes(
		attribute.Int("conditions", len(list.Items)),
		attribute.Int("applied", applied),
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

	vals, err := comp.Compute(ctx, time.Now(), loc, rem.ActiveComputeArgs)
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
