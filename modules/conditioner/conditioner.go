package conditioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

		sched: &schedule{
			events: make(map[string]*event, 1000),
			itemCh: make(chan item),
			logger: logger.With("conditioner", "schedule"),
			tracer: otel.Tracer(module + ".schedule"),
		},
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)

	return c, nil
}

func (c *Conditioner) Event(ctx context.Context, req *iotv1proto.EventRequest) (*iotv1proto.EventResponse, error) {
	var err error

	attributes := []attribute.KeyValue{
		attribute.String("event", req.Name),
	}

	// Collect the event labels on the span
	for k, v := range req.Labels {
		attributes = append(attributes, attribute.String(k, v))
	}

	ctx, span := c.tracer.Start(ctx, "Conditioner.Event",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attributes...),
	)
	defer tracing.ErrHandler(span, err, "conditioner event failed", c.logger)

	var (
		list = &apiv1.ConditionList{}
		errs []error
	)

	err = c.kubeClient.List(ctx, list, &kubeclient.ListOptions{})
	if err != nil {
		return &iotv1proto.EventResponse{}, fmt.Errorf("failed to list conditions: %w", err)
	}

	for _, cond := range list.Items {
		if ok := c.matchCondition(ctx, req.Labels, cond); !ok {
			continue
		}

		err = c.runConditionEvent(ctx, req, cond)
		if err != nil {
			errs = append(errs, fmt.Errorf("condition %q: %w", cond.Name, err))
		}
	}

	if len(errs) > 0 {
		return &iotv1proto.EventResponse{}, errors.Join(errs...)
	}

	return &iotv1proto.EventResponse{}, nil
}

func (c *Conditioner) Status() []scheduleStatus {
	return c.sched.Status()
}

func (c *Conditioner) setSchedule(ctx context.Context, cond apiv1.Condition) {
	var err error

	ctx, span := c.tracer.Start(ctx, "Conditioner.setSchedule") // trace.WithAttributes(attributes...),
	defer tracing.ErrHandler(span, err, "set schedule failed", c.logger)

	if !cond.Spec.Enabled {
		c.sched.remove(ctx, cond.Name)
		return
	}

	if cond.Spec.Schedule == "" {
		span.AddEvent("no schedule defined")
		return
	}

	cron, err := cronexpr.Parse(cond.Spec.Schedule)
	if err != nil {
		c.logger.Error("failed to parse cron expression from schedule", "err", err)
		return
	}

	next := cron.Next(time.Now())
	if next.IsZero() {
		return
	}

	var req request
	for _, rem := range cond.Spec.Remediations {
		req.sceneReq = nil
		req.stateReq = nil

		if rem.ActiveScene != "" {
			req.sceneReq = &iotv1proto.SetSceneRequest{
				Name:  rem.Zone,
				Scene: rem.ActiveScene,
			}
		}

		if rem.ActiveState != "" {
			req.stateReq = &iotv1proto.SetStateRequest{
				Name:  rem.Zone,
				State: c.zoneState(rem.ActiveState),
			}
		}

		if req.sceneReq != nil || req.stateReq != nil {
			c.sched.add(ctx, cond.Name, next, req)
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

func (c *Conditioner) runConditionEvent(ctx context.Context, req *iotv1proto.EventRequest, cond apiv1.Condition) (err error) {
	attributes := []attribute.KeyValue{
		attribute.String("condition", cond.Name),
		attribute.Bool("enabled", cond.Spec.Enabled),
	}

	ctx, span := c.tracer.Start(ctx, "Conditioner.runConditionEvent",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attributes...),
	)
	defer span.End()

	for _, rem := range cond.Spec.Remediations {
		err = c.handleRemediation(ctx, req, rem)
		if err != nil {
			c.logger.Error("remediation failed", "err", err)
		}
	}

	return
}

func (c *Conditioner) zoneState(state string) iotv1proto.ZoneState {
	zoneState := iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED

	switch state {
	case "on":
		zoneState = iotv1proto.ZoneState_ZONE_STATE_ON
	case "off":
		zoneState = iotv1proto.ZoneState_ZONE_STATE_OFF
	case "offtimer":
		zoneState = iotv1proto.ZoneState_ZONE_STATE_OFFTIMER
	case "color":
		zoneState = iotv1proto.ZoneState_ZONE_STATE_COLOR
	case "randomcolor":
		zoneState = iotv1proto.ZoneState_ZONE_STATE_RANDOMCOLOR
	default:
		// Allow for the direct name lookup, rather than the short-hand strings above
		if s, ok := iotv1proto.ZoneState_value[state]; ok {
			zoneState = iotv1proto.ZoneState(s)
		}
	}

	return zoneState
}

func (c *Conditioner) handleRemediation(ctx context.Context, req *iotv1proto.EventRequest, rem apiv1.Remediation) error {
	var (
		err   error
		state string
		scene string

		zoneState = iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED
	)

	ctx, span := c.tracer.Start(ctx, "Conditioner.handleRemediation")
	// TODO: add the zone to the error, the req or something to indicate the failure.
	defer func() { _ = tracing.ErrHandler(span, err, "handle remediation", c.logger) }()

	scene, state = c.getSceneState(ctx, req, rem)

	// Set the scene if we have one
	if scene != "" {
		span.SetAttributes(attribute.String("scene", scene))

		_, err = c.zonekeeperClient.SetScene(ctx, &iotv1proto.SetSceneRequest{
			Name:  rem.Zone,
			Scene: scene,
		})
		if err != nil {
			c.logger.Error("failed to set zone scene", "err", err)
			// status.Code(err)
		}
	}

	zoneState = c.zoneState(state)
	span.SetAttributes(
		attribute.String("state", state),
		attribute.String("zoneState", zoneState.String()),
	)

	// Set the state if we have one
	if zoneState > iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
		_, err = c.zonekeeperClient.SetState(ctx, &iotv1proto.SetStateRequest{
			Name:  rem.Zone,
			State: zoneState,
		})
	}

	return err
}

// getSceneState determines the scene and state to set based on the received event request and remediation.
func (c *Conditioner) getSceneState(ctx context.Context, req *iotv1proto.EventRequest, rem apiv1.Remediation) (scene, state string) {
	_, span := c.tracer.Start(ctx, "Conditioner.runTimer")
	defer span.End()

	// Check if we have an alert status
	if status, ok := req.Labels[iot.StatusLabel]; ok {
		span.SetAttributes(attribute.String(iot.StatusLabel, status))

		// Only override the above if we have a value
		switch status {
		case alertStatusFiring:
			if rem.ActiveState != "" {
				state = rem.ActiveState
			}

			if rem.ActiveScene != "" {
				scene = rem.ActiveScene
			}
		case alertStatusResolved:
			if rem.InactiveState != "" {
				state = rem.InactiveState
			}

			if rem.InactiveScene != "" {
				scene = rem.InactiveScene
			}
		}
	}

	// Overriding the alert status using the window.  In either firing or
	// resolved state, the zone is disabled.
	activeWindow := c.withinActiveWindow(ctx, req, rem)
	span.SetAttributes(attribute.Bool("activeWindow", activeWindow))

	if !activeWindow {
		if rem.InactiveState != "" {
			state = rem.InactiveState
		}
		if rem.InactiveScene != "" {
			scene = rem.InactiveScene
		}
	}

	return scene, state
}

func (c *Conditioner) withinActiveWindow(ctx context.Context, req *iotv1proto.EventRequest, rem apiv1.Remediation) (active bool) {
	_, span := c.tracer.Start(ctx, "Conditioner.withinActiveWindow")
	defer func() {
		span.SetAttributes(attribute.Bool("active", active))
		span.End()
	}()

	for _, ti := range rem.TimeIntervals {
		tip, err := ti.AsPrometheus()
		if err != nil {
			c.logger.Error("invalid time interval configuration", "err", err)
			continue
		}

		if tip.ContainsTime(time.Now()) {
			active = true
			return
		}
	}

	// An event has a when label, so check for it.
	// The Event could have a "when" lebel indicating the time of the event.  Check of it.
	if when, ok := req.Labels[iot.WhenLabel]; ok {
		var (
			now         = time.Now()
			started     bool
			stopped     bool
			windowStart time.Time
			windowStop  time.Time
		)

		span.SetAttributes(attribute.String(iot.WhenLabel, when))

		eventTime, err := time.Parse(time.RFC3339, when)
		if err != nil {
			c.logger.Error("failed to parse duration", "err", err)
			active = false
			return
		}

		// TODO: consider the duration window to check of 6h.  Should this be configurable?

		// Skip events older than 6 hours to avoid using yesterdays epoch.
		if time.Since(eventTime) > 6*time.Hour {
			active = false
			return
		}

		// Skip events more than 6 hours in the future
		if time.Until(eventTime) > 6*time.Hour {
			active = false
			return
		}

		var (
			start time.Duration
			stop  time.Duration
		)

		start, err = time.ParseDuration(rem.WhenGate.Start)
		if err != nil {
			c.logger.Error("failed to parse duration", "err", err)
			active = false
			return
		}

		windowStart = eventTime.Add(start)
		span.SetAttributes(attribute.String("windowStart", windowStart.Format(time.RFC3339)))

		if now.After(windowStart) {
			started = true
		}

		stop, err = time.ParseDuration(rem.WhenGate.Stop)
		if err != nil {
			c.logger.Error("failed to parse duration", "err", err)
			active = false
			return
		}

		windowStop = eventTime.Add(stop)
		span.SetAttributes(attribute.String("windowStop", windowStop.Format(time.RFC3339)))

		if now.After(windowStop) {
			stopped = true
		}

		active = started && !stopped
		return
	}

	active = true
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

	c.sched.removeExtraneous(names)
}

func (c *Conditioner) starting(ctx context.Context) error {
	go c.sched.run(ctx, c.zonekeeperClient)

	// TODO: consider creating a watch for conditions in place of the timer loop,
	// which lists all conditions.  Currently a list of all conditions against
	// the k8s api is made to retrieve the schedules for each condition and set
	// them to fire accordingly.  Making this a watch would allow for immediate
	// update to the schedule upon change, and reduce teh load onthe k8s API.
	// watchlist := cache.NewListWatchFromClient(
	// 	c.kubeClientr
	// 	apiv1.GroupVersion.WithResource("conditions").Resource,
	// 	v1.NamespaceAll,
	// 	fields.Everything(),
	// )

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

func handleStatusLabel(status string, rem apiv1.Remediation) (state string, scene string) {
	switch status {
	case alertStatusFiring:
		if rem.ActiveState != "" {
			state = rem.ActiveState
		}

		if rem.ActiveScene != "" {
			scene = rem.ActiveScene
		}
	case alertStatusResolved:
		if rem.InactiveState != "" {
			state = rem.InactiveState
		}

		if rem.InactiveScene != "" {
			scene = rem.InactiveScene
		}
	}

	return
}
