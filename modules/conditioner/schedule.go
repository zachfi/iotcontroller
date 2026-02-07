package conditioner

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	"github.com/zachfi/zkit/pkg/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// schedule holds named events that fire at a given time and send a single
// request (SetState/SetScene) to the ZoneKeeper. Events are run in a
// dedicated goroutine (run) that reads from itemCh; each event runs in its
// own goroutine and sends on itemCh when its timer fires, unless cancelled
// by remove or add (same name).
type schedule struct {
	sync.Mutex

	logger *slog.Logger

	events map[string]*event
	itemCh chan item

	tracer trace.Tracer
}

// event is a single scheduled fire: at time t it will send req on itemCh
// unless cancel is called (e.g. by remove or by re-add with the same name).
type event struct {
	cancel context.CancelFunc
	t      time.Time
	req    *request
}

// item is passed through the itemCh. The request is executed by run; name
// is used to remove the event from the schedule after execution.
type item struct {
	ctx context.Context
	*request
	name string
}

// request holds either a SetState and/or SetScene call for the ZoneKeeper.
type request struct {
	stateReq *iotv1proto.SetStateRequest
	sceneReq *iotv1proto.SetSceneRequest
}

// newSchedule creates a schedule with an empty events map and a single
// itemCh. The caller must start run() in a goroutine to process events.
func newSchedule(logger *slog.Logger) *schedule {
	return &schedule{
		events: make(map[string]*event, 1000),
		itemCh: make(chan item),
		logger: logger.With("conditioner", "schedule"),
		tracer: otel.Tracer(module + ".schedule"),
	}
}

// add schedules a new event or updates an existing event by name. If an
// event with the same name already exists, it is cancelled and replaced.
// Re-adding with the same name, time, and request is a no-op. Returns
// ErrEmptyRequest if req is nil or has neither stateReq nor sceneReq.
func (s *schedule) add(ctx context.Context, name string, t time.Time, req *request) error {
	var err error

	if ctx.Err() != nil {
		return ctx.Err()
	}

	ctx, span := s.tracer.Start(ctx, "schedule.add", trace.WithAttributes(
		attribute.String("name", name),
		attribute.String("time", t.Format(time.RFC3339)),
	))
	defer tracing.ErrHandler(span, err, "add failed", s.logger)

	if req == nil || (req.sceneReq == nil && req.stateReq == nil) {
		span.AddEvent(ErrEmptyRequest.Error())
		return ErrEmptyRequest
	}

	s.Lock()
	defer s.Unlock()

	// cancel and clear out the previous events
	if v, ok := s.events[name]; ok {
		if matched(v.req, req) && t.Equal(v.t) {
			return err
		}

		v.cancel()
		delete(s.events, name)
		span.AddEvent("removed existing event")
	}

	// Use a background context for the timer goroutine; cancellation is done
	// only via cancel() (from remove or re-add), not the incoming ctx.
	jobCtx, cancel := context.WithCancel(context.Background())
	s.events[name] = &event{
		cancel: cancel,
		t:      t,
		req:    req,
	}

	go func(jobCtx, reqCtx context.Context, req *request) {
		timer := time.NewTimer(time.Until(t))
		defer timer.Stop()

		spanCtx, span := s.tracer.Start(jobCtx, "schedule.event.execute", trace.WithAttributes(
			attribute.String("name", name),
		))
		defer span.End()
		span.AddLink(trace.LinkFromContext(reqCtx))

		if req == nil {
			span.AddEvent("nil request")
			return
		}

		for {
			select {
			case <-jobCtx.Done():
				span.AddEvent("canceled")
				return
			case <-timer.C:
				span.AddEvent("tick")
				s.itemCh <- item{
					// Name is used to identify the event and clean up after the request is executed.
					name:    name,
					ctx:     spanCtx,
					request: req,
				}
				return
			}
		}
	}(jobCtx, ctx, req)

	return nil
}

// remove cancels the event's timer goroutine and deletes it from the schedule.
// If the event was already firing (sent on itemCh), run will still process
// that item and then call remove again (no-op). Safe to call with a
// non-existent name.
func (s *schedule) remove(ctx context.Context, name string) {
	s.Lock()
	defer s.Unlock()

	if _, ok := s.events[name]; ok {
		s.events[name].cancel()
		delete(s.events, name)

		_, span := s.tracer.Start(ctx, "schedule.remove", trace.WithAttributes(
			attribute.String("name", name),
		))
		defer span.End()

		span.AddEvent(fmt.Sprintf("removed event %q", name))
	}
}

// run is the single consumer of itemCh: it blocks on itemCh or ctx.Done().
// When an item is received, it executes the request via the ZoneKeeper client
// and then removes the event by name. When ctx is cancelled, it stops all
// events and returns. Must be started once per schedule (e.g. from starting).
func (s *schedule) run(ctx context.Context, client iotv1proto.ZoneKeeperServiceClient) {
	var i item
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("stopping schedule runner")
			s.stop()
			return
		case i = <-s.itemCh:
			s.logger.Info("executing scheduled request")
			err := s.execRequest(i.ctx, i.request, client)
			if err != nil {
				s.logger.Error("failed to run request", "err", err)
			}

			s.remove(i.ctx, i.name)
		}
	}
}

// stop cancels all scheduled events and clears the events map. Used when
// the schedule runner is shutting down.
func (s *schedule) stop() {
	s.Lock()
	defer s.Unlock()

	for n, e := range s.events {
		e.cancel()
		delete(s.events, n)
	}
}

// execRequest sends the request to the ZoneKeeper: SetScene (if sceneReq is
// set) and/or SetState (if stateReq is set). Returns the first error from
// either call.
func (s *schedule) execRequest(ctx context.Context, req *request, zonekeeperClient iotv1proto.ZoneKeeperServiceClient) error {
	var err error

	ctx, span := s.tracer.Start(ctx, "schedule.execRequest")
	defer tracing.ErrHandler(span, err, "execRequest failed", s.logger)

	if req == nil {
		return nil
	}

	if req.sceneReq != nil {
		_, err = zonekeeperClient.SetScene(ctx, req.sceneReq)
		if err != nil {
			return fmt.Errorf("failed to deactivate zone %q scene: %w", req.sceneReq.Name, err)
		}
	}

	if req.stateReq != nil {
		_, err = zonekeeperClient.SetState(ctx, req.stateReq)
		if err != nil {
			return fmt.Errorf("failed to deactivate zone %q state: %w", req.stateReq.Name, err)
		}
	}

	return nil
}

// scheduleStatus is a snapshot of one scheduled event for Status().
type scheduleStatus struct {
	Name  string
	Next  string
	Scene string
	State string
}

// Status returns all scheduled events sorted by next run time.
func (s *schedule) Status() []scheduleStatus {
	s.Lock()
	defer s.Unlock()

	var states []scheduleStatus

	for n, e := range s.events {
		ss := scheduleStatus{
			Name: n,
			Next: e.t.Format(time.RFC3339),
		}

		if e.req.sceneReq != nil {
			ss.Scene = e.req.sceneReq.Scene
		}

		if e.req.stateReq != nil {
			ss.State = e.req.stateReq.State.String()
		}

		states = append(states, ss)
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].Next < states[j].Next
	})

	return states
}

// len returns the number of scheduled events (for tests).
func (s *schedule) len() int {
	s.Lock()
	defer s.Unlock()
	return len(s.events)
}

// matched reports whether two requests are equivalent (same zone/scene or
// zone/state). Used by add to avoid replacing an event when nothing changed.
func matched(a, b *request) bool {
	if a == nil && b == nil {
		return true
	}

	if a == nil || b == nil {
		return false
	}

	if a.sceneReq != nil && b.sceneReq == nil {
		return false
	}

	if a.stateReq != nil && b.stateReq == nil {
		return false
	}

	if b.sceneReq != nil && a.sceneReq == nil {
		return false
	}

	if b.stateReq != nil && a.stateReq == nil {
		return false
	}

	if a.sceneReq != nil && b.sceneReq != nil {
		if a.sceneReq.Name != b.sceneReq.Name || a.sceneReq.Scene != b.sceneReq.Scene {
			return false
		}
	}

	if a.stateReq != nil && b.stateReq != nil {
		if a.stateReq.Name != b.stateReq.Name || a.stateReq.State != b.stateReq.State {
			return false
		}
	}

	return true
}
