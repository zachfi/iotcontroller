// Package conditioner evaluates Conditions (CRDs) and drives zone state and
// scenes via the ZoneKeeper. It handles three inputs:
//
//   - Alert: webhook from Alertmanager. Matching conditions are activated
//     (firing) or deactivated (resolved). Remediations can use TimeIntervals
//     so activation only applies during certain times; a background check
//     deactivates when the window closes.
//   - Epoch: time-based events (e.g. sunrise). WhenGate defines a window
//     around the event; activations and deactivations are scheduled at
//     window start/stop.
//   - Timer: cron-style schedules on Conditions run remediations at the next
//     cron time.
//
// All activation/deactivation is performed by sending SetState/SetScene
// to the ZoneKeeper client.
package conditioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gorhill/cronexpr"
	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zachfi/zkit/pkg/tracing"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

const (
	module = "conditioner"

	// Alertmanager webhook payload
	alertStatusFiring   = "firing"
	alertStatusResolved = "resolved"
)

// alertActiveKey returns a unique key for a condition+remediation used to track
// which remediations are currently active due to an alert time window.
func alertActiveKey(conditionName, zone string) string {
	return conditionName + "/" + zone
}

// condStateKey is the same shape as alertActiveKey — kept as a separate
// helper so the two caches can diverge if needed (e.g. if condState
// later needs a per-direction component).
func condStateKey(conditionName, zone string) string {
	return conditionName + "/" + zone
}

// applyDesired sends req to the ZoneKeeper unless the (condName, zone)
// cache already shows the same desired (state, scene) within
// cfg.ApplyDesiredRefreshAge. Suppressed calls increment
// metricApplySuppressed{direction}.
//
// direction is a label ("activate" / "deactivate") for the metric;
// the cache key intentionally does NOT include direction, so a
// deactivate after an activate (or vice versa) correctly invalidates
// the cache and re-applies.
func (c *Conditioner) applyDesired(ctx context.Context, condName, zone string, req *request, direction string) error {
	if req == nil {
		return nil
	}

	var (
		desiredState iotv1proto.ZoneState
		desiredScene string
	)
	if req.stateReq != nil {
		desiredState = req.stateReq.State
	}
	if req.sceneReq != nil {
		desiredScene = req.sceneReq.Scene
	}

	key := condStateKey(condName, zone)
	now := time.Now()

	c.condStateMu.Lock()
	prev, ok := c.condState[key]
	fresh := ok && !prev.ts.IsZero() && now.Sub(prev.ts) < c.cfg.ApplyDesiredRefreshAge
	c.condStateMu.Unlock()

	if ok && fresh && prev.state == desiredState && prev.scene == desiredScene {
		metricApplySuppressed.WithLabelValues(condName, zone, direction).Inc()
		return nil
	}

	err := c.sched.execRequest(ctx, req, c.zonekeeperClient)
	if err != nil {
		return err
	}

	c.condStateMu.Lock()
	c.condState[key] = conditionState{state: desiredState, scene: desiredScene, ts: now}
	c.condStateMu.Unlock()
	return nil
}

// activateRemediation routes through applyDesired for idempotency.
// Scheduled-fire paths (cron, epoch-add-then-fire) call execRequest
// directly via schedule.run() — those are unique time-bound events that
// have their own dedup at schedule.add time, so they bypass the cache.
//
// Time-gating: if rem.TimeIntervals is non-empty and `now` is not in
// any of them, the activation is suppressed and metricApplySuppressed
// records "time-gated". This makes TimeIntervals honored uniformly
// across all activation sources (Alert RPC, Binding-driven
// ActivateCondition, Epoch); it's the prerequisite for using
// multi-remediation Conditions to express time-of-day-aware behaviour
// like motion-detector responses.
//
// If rem.ActiveState is the "toggle" shorthand, it's resolved to "on"
// or "off" here (based on the zone's current CRD status.state) so
// downstream apply / cache layers only ever see concrete states.
func (c *Conditioner) activateRemediation(ctx context.Context, condName string, rem apiv1.Remediation) error {
	if len(rem.TimeIntervals) > 0 && !c.withinActiveWindow(ctx, rem, time.Now()) {
		metricApplySuppressed.WithLabelValues(condName, rem.Zone, "time-gated").Inc()
		return nil
	}
	// Relative brightness adjust: bypass applyDesired entirely. The
	// cache is for absolute values where "we already sent this" makes
	// sense; deltas must fire every time (each press = one more step).
	// Other Remediation fields (ActiveState/ActiveScene) are ignored
	// when ActiveBrightnessDelta is set — the underlying RPC sets the
	// zone ON as a side effect, which is what you want for a "press
	// brighter" intent.
	if rem.ActiveBrightnessDelta != 0 {
		return c.adjustBrightness(ctx, condName, rem.Zone, rem.ActiveBrightnessDelta)
	}
	if strings.EqualFold(rem.ActiveState, shortHandStateToggle) {
		rem.ActiveState = c.resolveToggleState(ctx, rem.Zone)
	}
	return c.applyDesired(ctx, condName, rem.Zone, activateRequest(ctx, rem), "activate")
}

// adjustBrightness sends an AdjustBrightness RPC to the ZoneKeeper.
// Records the call in metricApplySuppressed for parity with the cache
// path? No — adjust is non-idempotent and should not be in a
// suppression metric. The flush_total / state_changes metrics on the
// zonekeeper side are sufficient to observe its effects.
func (c *Conditioner) adjustBrightness(ctx context.Context, condName, zone string, delta int) error {
	_, err := c.zonekeeperClient.AdjustBrightness(ctx, &iotv1proto.AdjustBrightnessRequest{
		Name:  zone,
		Delta: int32(delta),
	})
	return err
}

// deactivateRemediation is the inverse of activateRemediation. Toggle
// on inactive_state is intentionally NOT supported: deactivation has
// a definite direction (revert to the alert-resolved state) and using
// toggle there would make alert-resolved behaviour depend on whatever
// state the zone happens to be in when the alert clears.
//
// Deactivation is NOT time-gated. An alert resolving outside the
// remediation's TimeIntervals still needs to revert the zone to its
// inactive state — gating that would leave the zone stuck in whatever
// state the previous activation set.
func (c *Conditioner) deactivateRemediation(ctx context.Context, condName string, rem apiv1.Remediation) error {
	return c.applyDesired(ctx, condName, rem.Zone, deactivateRequest(ctx, rem), "deactivate")
}

// resolveToggleState reads the named Zone's CRD status.state and
// returns "off" if the zone looks lights-on right now (ON, OFFTIMER,
// COLOR, RANDOMCOLOR) or "on" otherwise. On any read error the safer
// default is "on" — easier for the user to recover from than a dark
// room. The read is served from the local informer cache, so this is
// cheap.
//
// Two consecutive toggle calls within the cache-update window will
// resolve to the same value and the second is suppressed by
// applyDesired — that's a deliberate physical-button-bounce debounce,
// not a missed toggle.
func (c *Conditioner) resolveToggleState(ctx context.Context, zone string) string {
	var z apiv1.Zone
	if err := c.kubeClient.Get(ctx, kubeclient.ObjectKey{Name: zone, Namespace: "iot"}, &z); err != nil {
		c.logger.Debug("toggle: zone status read failed; defaulting to on",
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
		return shortHandStateOn
	}
	switch z.Status.State {
	case iotv1proto.ZoneState_ZONE_STATE_ON.String(),
		iotv1proto.ZoneState_ZONE_STATE_OFFTIMER.String(),
		iotv1proto.ZoneState_ZONE_STATE_COLOR.String(),
		iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR.String():
		return shortHandStateOff
	}
	return shortHandStateOn
}

// Conditioner evaluates Conditions from the cluster and applies remediations
// (zone state/scene) via the ZoneKeeper. It runs a timer loop to process
// cron schedules and to deactivate alert-driven remediations when their
// TimeIntervals window has closed.
type Conditioner struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	zonekeeperClient iotv1proto.ZoneKeeperServiceClient
	kubeClient       kubeclient.Client

	sched *schedule

	// alertActive tracks remediations that were activated by an alert and have
	// TimeIntervals. When the time window closes (and no further alert arrives),
	// a background check deactivates them.
	alertActiveMu sync.Mutex
	alertActive   map[string]apiv1.Remediation

	// condState is the per-(condition, zone) cache of the last desired
	// (state, scene) we successfully applied. activateRemediation and
	// deactivateRemediation route through applyDesired, which consults
	// this cache and skips the underlying ZoneKeeper SetState/SetScene
	// when nothing has changed. The TTL (cfg.ApplyDesiredRefreshAge)
	// forces a re-apply after the window expires to absorb drift.
	condStateMu sync.Mutex
	condState   map[string]conditionState
}

// conditionState records the last desired (state, scene) we sent for a
// (condition, zone) pair, along with when it was applied. zero ts =
// never applied (treat as cache miss regardless of value).
type conditionState struct {
	state iotv1proto.ZoneState
	scene string
	ts    time.Time
}

// New builds a Conditioner. ZoneKeeper client is required for activation and
// deactivation; kube client is required for listing Conditions. Either may be
// nil only in tests that do not call Alert, Epoch, or the timer.
func New(cfg Config, logger *slog.Logger, zoneKeeperClient iotv1proto.ZoneKeeperServiceClient, k kubeclient.Client) (*Conditioner, error) {
	c := &Conditioner{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		zonekeeperClient: zoneKeeperClient,
		kubeClient:       k,

		sched:       newSchedule(logger),
		alertActive: make(map[string]apiv1.Remediation),
		condState:   make(map[string]conditionState),
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	return c, nil
}

// Alert handles an Alertmanager webhook. It lists Conditions, matches on
// alert name/zone (and status firing/resolved), and activates or deactivates
// remediations. Remediations with TimeIntervals are only considered active
// when "now" is inside one of the intervals; if an alert fires during a
// window, the remediation is tracked so runAlertWindowCheck can deactivate
// it when the window closes.
func (c *Conditioner) Alert(ctx context.Context, req *iotv1proto.AlertRequest) (*iotv1proto.AlertResponse, error) {
	var (
		err  error
		errs []error
	)

	attributes := []attribute.KeyValue{
		attribute.String("name", req.Name),
	}

	ctx, span := c.tracer.Start(ctx, "Conditioner.Alert",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attributes...),
	)
	defer tracing.ErrHandler(span, err, "conditioner alert failed", c.logger)

	list := &apiv1.ConditionList{}

	err = c.kubeClient.List(ctx, list, &kubeclient.ListOptions{})
	if err != nil {
		return &iotv1proto.AlertResponse{}, fmt.Errorf("failed to list conditions: %w", err)
	}

	for _, cond := range list.Items {
		// NOTE: We're matching the condition against the alert for location and
		// zone, but below the remediation may take action against a different
		// zone.

		labels := map[string]string{
			iot.AlertNameLabel: req.Name,
			iot.ZoneLabel:      req.Zone,
		}

		if ok := c.matchCondition(ctx, labels, cond); !ok {
			continue
		}

		for _, rem := range cond.Spec.Remediations {
			// Alert windowing semantics:
			//   firing  + no TimeIntervals          → active
			//   firing  + currently in TimeInterval → active
			//   firing  + outside TimeInterval      → INACTIVE (suppress
			//       repeated activations from Alertmanager re-firing for
			//       an alert whose window has already closed)
			//   resolved                            → inactive
			//   other                               → skip
			//
			// The dedup in applyDesired further collapses repeat-firing
			// webhooks for the same alert into a single SetState per
			// transition.
			var active bool
			status := req.Status
			if status == "" {
				continue
			}
			span.SetAttributes(attribute.String(iot.StatusLabel, status))
			switch status {
			case alertStatusFiring:
				active = len(rem.TimeIntervals) == 0 || c.withinActiveWindow(ctx, rem, time.Now())
			case alertStatusResolved:
				active = false
			default:
				continue
			}

			if active {
				err = c.activateRemediation(ctx, cond.Name, rem)
				if err != nil {
					c.logger.Error("failed to activate condition alert", "err", err)
				} else if len(rem.TimeIntervals) > 0 {
					// Track so the background window check can deactivate when the window closes.
					c.trackAlertActive(cond.Name, rem)
				}
			} else {
				c.untrackAlertActive(cond.Name, rem)
				err = c.deactivateRemediation(ctx, cond.Name, rem)
				if err != nil {
					c.logger.Error("failed to deactivate condition alert", "err", err)
				}
			}

		}
	}

	if len(errs) > 0 {
		return &iotv1proto.AlertResponse{}, errors.Join(errs...)
	}

	return &iotv1proto.AlertResponse{}, nil
}

// Epoch handles a time-based event (e.g. sunrise/sunset). It lists Conditions
// matching the event location/name, computes each remediation's window from
// WhenGate (duration strings relative to the event time), and either
// activates/deactivates immediately or schedules add/remove at window
// start/stop.
func (c *Conditioner) Epoch(ctx context.Context, req *iotv1proto.EpochRequest) (*iotv1proto.EpochResponse, error) {
	now := time.Now()
	var err error
	var errs []error

	attributes := []attribute.KeyValue{
		attribute.String("name", req.Name),
		attribute.String("location", req.Location),
		attribute.String("when", time.Unix(req.When, 0).Format(time.RFC3339)),
	}

	ctx, span := c.tracer.Start(ctx, "Conditioner.Epoch",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attributes...),
	)
	defer tracing.ErrHandler(span, err, "conditioner epoch failed", c.logger)

	list := &apiv1.ConditionList{}

	err = c.kubeClient.List(ctx, list, &kubeclient.ListOptions{})
	if err != nil {
		return &iotv1proto.EpochResponse{}, fmt.Errorf("failed to list conditions: %w", err)
	}

	for _, cond := range list.Items {
		if req.Location == "" || req.Name == "" {
			continue
		}

		// For the condition, match only the location and epoch.
		labels := map[string]string{
			iot.LocationLabel: req.Location,
			iot.EpochLabel:    req.Name,
		}

		if ok := c.matchCondition(ctx, labels, cond); !ok {
			continue
		}
		span.AddEvent("matched condition", trace.WithAttributes(attribute.String("condition", cond.Name)))

		// Handle the remdiations for this condition
		for _, rem := range cond.Spec.Remediations {
			start, stop, err := c.epochWindow(ctx, time.Unix(req.When, 0), rem.WhenGate)
			if err != nil {
				c.logger.Error("failed to calculate epoch window", "err", err)
				errs = append(errs, fmt.Errorf("condition %q: %w", cond.Name, err))

				continue
			}

			// If we are within the time window for this remediation, activate it.
			if c.timeContains(ctx, now, start, stop) {
				span.AddEvent("within of epoch window",
					trace.WithAttributes(
						attribute.String("now", now.Format(time.RFC3339)),
						attribute.String("start", start.Format(time.RFC3339)),
						attribute.String("stop", stop.Format(time.RFC3339)),
					),
				)

				err = c.activateRemediation(ctx, cond.Name, rem)
				if err != nil {
					c.logger.Error("failed to run condition epoch", "err", err)
				}
			} else {
				// Outside of the time window, deactivate the remediation.
				span.AddEvent("outside of epoch window",
					trace.WithAttributes(
						attribute.String("now", now.Format(time.RFC3339)),
						attribute.String("start", start.Format(time.RFC3339)),
						attribute.String("stop", stop.Format(time.RFC3339)),
					),
				)

				// If we have an Epoch event in the future schedule the activation and deactivation.
				if now.Before(start) {
					// Schedule the zone activation
					if activate := activateRequest(ctx, rem); activate != nil {
						err = c.sched.add(ctx, strings.Join([]string{req.Location, req.Name, cond.Name, rem.Zone, "activate"}, "-"), start, activate)
						if err != nil && !errors.Is(err, ErrEmptyRequest) {
							c.logger.Error("failed to schedule activation", "err", err)
							errs = append(errs, fmt.Errorf("condition %q: %w", cond.Name, err))
						}
					}

					// Schedule the zone deactivation
					if deactivate := deactivateRequest(ctx, rem); deactivate != nil {
						err = c.sched.add(ctx, strings.Join([]string{req.Location, req.Name, cond.Name, rem.Zone, "deactivate"}, "-"), stop, deactivate)
						if err != nil && !errors.Is(err, ErrEmptyRequest) {
							c.logger.Error("failed to schedule deactivation", "err", err)
							errs = append(errs, fmt.Errorf("condition %q: %w", cond.Name, err))
						}
					}
				} else if now.After(stop) {
					// If we are past the stop time, deactivate immediately.
					err = c.deactivateRemediation(ctx, cond.Name, rem)
					if err != nil {
						c.logger.Error("failed to run condition epoch", "err", err)
					}
				}
			}
		}

	}

	if len(errs) > 0 {
		return &iotv1proto.EpochResponse{}, errors.Join(errs...)
	}

	return nil, nil
}

// ActivateCondition looks up the named Condition and immediately applies all of
// its remediations. This is the entry point for binding-triggered activations
// (ZCL commands, MQTT messages, or any other input that has already been mapped
// to a Condition name).
func (c *Conditioner) ActivateCondition(ctx context.Context, req *iotv1proto.ActivateConditionRequest) (*iotv1proto.ActivateConditionResponse, error) {
	var err error
	ctx, span := c.tracer.Start(ctx, "Conditioner.ActivateCondition",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("condition", req.Condition)),
	)
	defer tracing.ErrHandler(span, err, "activate condition failed", c.logger)

	var cond apiv1.Condition
	if err = c.kubeClient.Get(ctx, kubeclient.ObjectKey{Name: req.Condition, Namespace: "iot"}, &cond); err != nil {
		return &iotv1proto.ActivateConditionResponse{}, fmt.Errorf("condition %q not found: %w", req.Condition, err)
	}

	if !cond.Spec.Enabled {
		span.AddEvent("condition disabled")
		return &iotv1proto.ActivateConditionResponse{}, nil
	}

	var errs []error
	for _, rem := range cond.Spec.Remediations {
		if activateErr := c.activateRemediation(ctx, cond.Name, rem); activateErr != nil {
			errs = append(errs, activateErr)
		}
	}

	if len(errs) > 0 {
		err = errors.Join(errs...)
	}

	return &iotv1proto.ActivateConditionResponse{}, err
}

// Status returns the current scheduled events (name, next run time, scene/state).
func (c *Conditioner) Status() []scheduleStatus {
	return c.sched.Status()
}

// setSchedule updates the schedule for a Condition: on the next cron tick,
// each of its remediations is activated. Disabled or empty schedule
// removes any existing events for this condition.
func (c *Conditioner) setSchedule(ctx context.Context, cond apiv1.Condition) {
	var err error

	ctx, span := c.tracer.Start(ctx, "Conditioner.setSchedule", trace.WithAttributes(attribute.String("name", cond.Name)))
	defer tracing.ErrHandler(span, err, "set schedule failed", c.logger)

	if !cond.Spec.Enabled {
		span.AddEvent("condition disabled")
		c.sched.remove(ctx, cond.Name)
		return
	}

	if cond.Spec.Schedule == "" {
		span.AddEvent("no schedule defined")
		c.sched.remove(ctx, cond.Name)
		return
	}

	cron, err := cronexpr.Parse(cond.Spec.Schedule)
	if err != nil {
		c.logger.Error("failed to parse cron expression from schedule", "err", err)
		return
	}

	next := cron.Next(time.Now())
	if next.IsZero() {
		span.AddEvent("zero time")
		return
	}

	// The schedule is on the condition, so we execute each remediation at the next cron event.

	var req *request
	for _, rem := range cond.Spec.Remediations {
		req = activateRequest(ctx, rem)

		err = c.sched.add(ctx, strings.Join([]string{cond.Name, "schedule", rem.Zone, "activate"}, "-"), next, req)
		if err != nil && !errors.Is(err, ErrEmptyRequest) {
			span.AddEvent("failed to set schedule", trace.WithAttributes(attribute.String("err", err.Error())))
			c.logger.Error("failed to set schedule", "err", err)
		}
	}
}

// matchCondition returns true if the condition is enabled and every
// Spec.Matches entry has a matching label in labels (same key and value).
func (c *Conditioner) matchCondition(_ context.Context, labels map[string]string, cond apiv1.Condition) bool {
	if !cond.Spec.Enabled {
		return false
	}

	if len(cond.Spec.Matches) == 0 {
		return false
	}

	// Check that the labels are matched for each of the condition matchers.
	for _, match := range cond.Spec.Matches {
		for k, v := range match.Labels {
			if vv, ok := labels[k]; ok {
				if vv != v {
					return false
				}
			} else {
				return false
			}
		}
	}

	return true
}

// timeContains reports whether t is inside [start, stop] (inclusive).
func (c *Conditioner) timeContains(ctx context.Context, t, start, stop time.Time) bool {
	_, span := c.tracer.Start(ctx, "Conditioner.timeContains")
	defer span.End()

	span.SetAttributes(
		attribute.String("time", t.Format(time.RFC3339)),
		attribute.String("start", start.Format(time.RFC3339)),
		attribute.String("stop", stop.Format(time.RFC3339)),
	)

	if t.Equal(start) || t.Equal(stop) {
		return true
	}

	if t.After(start) && t.Before(stop) {
		return true
	}

	return false
}

// epochWindow computes the activation window [start, stop] for an epoch event.
// When.Start and When.Stop are Go duration strings (e.g. "-30m", "1h") applied
// relative to eventTime. If Start is empty, start is eventTime - 1 minute; if
// Stop is empty, stop is eventTime + EpochTimeWindow.
func (c *Conditioner) epochWindow(ctx context.Context, eventTime time.Time, when apiv1.When) (start, stop time.Time, err error) {
	_, span := c.tracer.Start(ctx, "Conditioner.epochWindow")
	defer span.End()

	if eventTime.IsZero() {
		span.AddEvent("event time is zero")
		return start, stop, fmt.Errorf("event time is zero")
	}

	var dur time.Duration

	if when.Start != "" {
		dur, err = time.ParseDuration(when.Start)
		if err != nil {
			return start, stop, err
		}

		start = eventTime.Add(dur)
	} else {
		// If we have no start defined, use a negative one minute to account for
		// the race between sending and receiving the event.
		start = eventTime.Add(-time.Minute)
	}

	span.SetAttributes(attribute.String("windowStart", start.Format(time.RFC3339)))

	if when.Stop != "" {
		dur, err = time.ParseDuration(when.Stop)
		if err != nil {
			return start, stop, err
		}

		stop = eventTime.Add(dur)
		span.SetAttributes(attribute.String("windowStop", stop.Format(time.RFC3339)))
	} else {
		// If we have no stop defined, use the configured epoch time window.
		stop = eventTime.Add(c.cfg.EpochTimeWindow)
	}

	return start, stop, nil
}

// trackAlertActive records that this remediation is currently active due to an
// alert and has TimeIntervals, so the background window check can deactivate
// when the window closes.
func (c *Conditioner) trackAlertActive(conditionName string, rem apiv1.Remediation) {
	c.alertActiveMu.Lock()
	defer c.alertActiveMu.Unlock()
	if c.alertActive == nil {
		c.alertActive = make(map[string]apiv1.Remediation)
	}
	c.alertActive[alertActiveKey(conditionName, rem.Zone)] = rem
}

// untrackAlertActive removes this remediation from the set of alert-active
// remediations (e.g. when we deactivate due to resolved or outside window).
func (c *Conditioner) untrackAlertActive(conditionName string, rem apiv1.Remediation) {
	c.alertActiveMu.Lock()
	defer c.alertActiveMu.Unlock()
	delete(c.alertActive, alertActiveKey(conditionName, rem.Zone))
}

// runAlertWindowCheck runs periodically; for each remediation that is active
// due to an alert and has TimeIntervals, if the current time is outside the
// active window, deactivates it so the zone does not stay in the wrong state
// when no further alert arrives.
func (c *Conditioner) runAlertWindowCheck(ctx context.Context) {
	c.runAlertWindowCheckAt(ctx, time.Now())
}

// runAlertWindowCheckAt is the same as runAlertWindowCheck but accepts a fixed
// time for testing. Production code uses runAlertWindowCheck.
func (c *Conditioner) runAlertWindowCheckAt(ctx context.Context, now time.Time) {
	c.alertActiveMu.Lock()
	snapshot := make(map[string]apiv1.Remediation, len(c.alertActive))
	for k, v := range c.alertActive {
		snapshot[k] = v
	}
	c.alertActiveMu.Unlock()

	if len(snapshot) == 0 {
		return
	}

	for key, rem := range snapshot {
		if !c.withinActiveWindow(ctx, rem, now) {
			// alertActive keys are "<condName>/<zone>"; we need the
			// condName for the applyDesired cache.
			condName, _, _ := strings.Cut(key, "/")
			c.logger.Info("alert time window closed, deactivating remediation",
				slog.String("key", key),
				slog.String("zone", rem.Zone),
			)
			if err := c.deactivateRemediation(ctx, condName, rem); err != nil {
				c.logger.Error("failed to deactivate remediation after window close", "err", err, "zone", rem.Zone)
			}
			c.alertActiveMu.Lock()
			delete(c.alertActive, key)
			c.alertActiveMu.Unlock()
		}
	}
}

// withinActiveWindow checks if the current time is within the active window
// defined in the remediation.  It considers both time intervals and when
// gates.
func (c *Conditioner) withinActiveWindow(ctx context.Context, rem apiv1.Remediation, now time.Time) (active bool) {
	var err error

	_, span := c.tracer.Start(ctx, "Conditioner.withinActiveWindow")
	defer func() {
		span.SetAttributes(attribute.Bool("active", active))
		defer tracing.ErrHandler(span, err, "failed to check active window", c.logger)
	}()

	if len(rem.TimeIntervals) == 0 {
		active = true
		return
	}

	// If any window is active, then set the remdiation as active.
	for _, ti := range rem.TimeIntervals {
		tip, err := ti.AsPrometheus()
		if err != nil {
			c.logger.Error("invalid time interval configuration", "err", err, "interval", fmt.Sprintf("%+v", ti))
			continue
		}

		if tip.ContainsTime(now) {
			active = true
			return
		}
	}

	active = false
	return
}

// runTimerLoop runs the timer ticker: on each tick it processes cron-based
// schedules (runTimer) and then runs the alert window check so remediations
// whose TimeIntervals window has closed are deactivated.
func (c *Conditioner) runTimerLoop(ctx context.Context) {
	t := time.NewTicker(c.cfg.TimerLoopInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.runTimer(ctx)
			c.runAlertWindowCheck(ctx)
		}
	}
}

// runTimer lists Conditions, updates schedules from Spec.Schedule (cron), and
// adds the next activation event for each remediation at the next cron time.
func (c *Conditioner) runTimer(ctx context.Context) {
	var (
		list  = &apiv1.ConditionList{}
		names = make(map[string]struct{}, 10)
		err   error
	)

	ctx, span := c.tracer.Start(ctx, "Conditioner.runTimer")
	defer tracing.ErrHandler(span, err, "failed to run timer", c.logger)

	err = c.kubeClient.List(ctx, list, &kubeclient.ListOptions{})
	if err != nil {
		c.logger.Error("failed to list conditions", "err", err)
		return
	}

	for _, cond := range list.Items {
		c.setSchedule(ctx, cond)
		names[cond.Name] = struct{}{}
	}
}

func (c *Conditioner) starting(ctx context.Context) error {
	go c.sched.run(ctx, c.zonekeeperClient)

	return nil
}

func (c *Conditioner) running(ctx context.Context) error {
	go c.runTimerLoop(ctx)
	<-ctx.Done()
	return nil
}

func (c *Conditioner) stopping(_ error) error {
	return nil
}

// activateRequest builds a request that applies the remediation's ActiveScene
// and/or ActiveState to the zone. Returns nil if the remediation has neither.
func activateRequest(_ context.Context, rem apiv1.Remediation) *request {
	req := &request{}

	if rem.ActiveScene != "" {
		req.sceneReq = &iotv1proto.SetSceneRequest{
			Name:  rem.Zone,
			Scene: rem.ActiveScene,
		}
	}

	state := zoneState(rem.ActiveState)
	// Set the state if we have one
	if state > iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
		req.stateReq = &iotv1proto.SetStateRequest{
			Name:  rem.Zone,
			State: state,
		}
	}

	if req.stateReq != nil || req.sceneReq != nil {
		return req
	}

	return nil
}

// deactivateRequest builds a request that applies the remediation's
// InactiveScene and/or InactiveState to the zone. Returns nil if the
// remediation has neither.
func deactivateRequest(_ context.Context, rem apiv1.Remediation) *request {
	req := &request{}

	if rem.InactiveScene != "" {
		req.sceneReq = &iotv1proto.SetSceneRequest{
			Name:  rem.Zone,
			Scene: rem.InactiveScene,
		}
	}

	state := zoneState(rem.InactiveState)
	// Set the state if we have one
	if state > iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
		req.stateReq = &iotv1proto.SetStateRequest{
			Name:  rem.Zone,
			State: state,
		}
	}

	if req.stateReq != nil || req.sceneReq != nil {
		return req
	}

	return nil
}

const (
	shortHandStateOn     = "on"
	shortHandStateOff    = "off"
	shortHandStateToggle = "toggle"
)

var shortHandStates = map[string]iotv1proto.ZoneState{
	shortHandStateOn:  iotv1proto.ZoneState_ZONE_STATE_ON,
	shortHandStateOff: iotv1proto.ZoneState_ZONE_STATE_OFF,
	"offtimer":        iotv1proto.ZoneState_ZONE_STATE_OFFTIMER,
	"color":           iotv1proto.ZoneState_ZONE_STATE_COLOR,
	"randomcolor":     iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR,
	// Note: "toggle" is intentionally NOT in this map. It's resolved
	// to "on" or "off" at the Conditioner level (resolveToggleState)
	// before reaching zoneState(), so the rest of the stack only sees
	// concrete states.
}

// Using the known short-hand strings for zone states, return the appropriate
// enum value, or the string representing the enum.  A return of
// ZONE_STATE_UNSPECIFIED indicates no match.
func zoneState(state string) iotv1proto.ZoneState {
	if s, ok := shortHandStates[state]; ok {
		return s
	} else if s, ok := iotv1proto.ZoneState_value[state]; ok {
		return iotv1proto.ZoneState(s)
	}

	return iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED
}
