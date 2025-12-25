package conditioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

type Conditioner struct {
	services.Service
	cfg *Config

	logger *slog.Logger
	tracer trace.Tracer

	zonekeeperClient iotv1proto.ZoneKeeperServiceClient
	kubeClient       kubeclient.Client

	sched *schedule
}

func New(cfg Config, logger *slog.Logger, zoneKeeperClient iotv1proto.ZoneKeeperServiceClient, k kubeclient.Client) (*Conditioner, error) {
	c := &Conditioner{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module),

		zonekeeperClient: zoneKeeperClient,
		kubeClient:       k,

		sched: newSchedule(logger),
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	return c, nil
}

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
			var active bool
			if status := req.Status; status != "" {
				span.SetAttributes(attribute.String(iot.StatusLabel, status))
				switch status {
				case alertStatusFiring:
					active = true
				case alertStatusResolved:
					// active = false
				default:
					continue
				}

				// Override based on the window.
				if c.withinActiveWindow(ctx, rem, time.Now()) {
					active = true
				}
			}

			if active {
				err = c.sched.activateRemediation(ctx, rem, c.zonekeeperClient)
				if err != nil {
					c.logger.Error("failed to activate condition alert", "err", err)
				}
			} else {
				err = c.sched.deactivateRemediation(ctx, rem, c.zonekeeperClient)
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

				err = c.sched.activateRemediation(ctx, rem, c.zonekeeperClient)
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
					if deactivate := deactivateRequest(ctx, rem); deactivate == nil {
						err = c.sched.add(ctx, strings.Join([]string{req.Location, req.Name, cond.Name, rem.Zone, "deactivate"}, "-"), stop, deactivate)
						if err != nil && !errors.Is(err, ErrEmptyRequest) {
							c.logger.Error("failed to schedule deactivation", "err", err)
							errs = append(errs, fmt.Errorf("condition %q: %w", cond.Name, err))
						}
					}
				} else if now.After(stop) {
					// If we are past the stop time, deactivate immediately.
					err = c.sched.deactivateRemediation(ctx, rem, c.zonekeeperClient)
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

func (c *Conditioner) Status() []scheduleStatus {
	return c.sched.Status()
}

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

// timeContains checks if the time t is within the start and stop times.
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

// epochWindow calculates the start and stop times for the epoch window based
// on the event time and the when configuration.
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

// withinActiveWindow checks if the current time is within the active window
// defined in the remediation.  It considers both time intervals and when
// gates.
func (c *Conditioner) withinActiveWindow(ctx context.Context, rem apiv1.Remediation, now time.Time) (active bool) {
	_, span := c.tracer.Start(ctx, "Conditioner.withinActiveWindow")
	defer func() {
		span.SetAttributes(attribute.Bool("active", active))
		span.End()
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

func (c *Conditioner) runTimerLoop(ctx context.Context) {
	t := time.NewTicker(c.cfg.TimerLoopInterval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.runTimer(ctx)
		}
	}
}

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

func (s *schedule) activateRemediation(ctx context.Context, rem apiv1.Remediation, zonekeeperClient iotv1proto.ZoneKeeperServiceClient) error {
	return s.execRequest(ctx, activateRequest(ctx, rem), zonekeeperClient)
}

func (s *schedule) deactivateRemediation(ctx context.Context, rem apiv1.Remediation, zonekeeperClient iotv1proto.ZoneKeeperServiceClient) error {
	return s.execRequest(ctx, deactivateRequest(ctx, rem), zonekeeperClient)
}

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

var shortHandStates = map[string]iotv1proto.ZoneState{
	"on":          iotv1proto.ZoneState_ZONE_STATE_ON,
	"off":         iotv1proto.ZoneState_ZONE_STATE_OFF,
	"offtimer":    iotv1proto.ZoneState_ZONE_STATE_OFFTIMER,
	"color":       iotv1proto.ZoneState_ZONE_STATE_COLOR,
	"randomcolor": iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR,
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
