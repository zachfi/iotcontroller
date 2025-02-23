package conditioner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

type schedule struct {
	sync.Mutex

	logger *slog.Logger

	events map[string]*event
	reqs   chan request
}

type event struct {
	cancel context.CancelFunc
	t      time.Time
	req    request
}

type request struct {
	stateReq *iotv1proto.SetStateRequest
	sceneReq *iotv1proto.SetSceneRequest
}

func matched(a, b request) bool {
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

func (s *schedule) add(ctx context.Context, name string, t time.Time, req request) {
	if req.sceneReq == nil && req.stateReq == nil {
		s.logger.Error("unable to schedule request with nil scene and nil state")
		return
	}

	s.Lock()
	defer s.Unlock()

	// cancel and clear out the previous events
	if v, ok := s.events[name]; ok {
		if matched(v.req, req) && t.Equal(v.t) {
			return
		}

		v.cancel()
		delete(s.events, name)
	}

	ctx, cancel := context.WithCancel(ctx)
	s.events[name] = &event{
		cancel: cancel,
		t:      t,
		req:    req,
	}

	go func(ctx context.Context, req request) {
		timer := time.NewTimer(time.Until(t))
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.reqs <- req
				return
			}
		}
	}(ctx, req)
}

// Any names not listed in the map are removed from the event.  This allows us
// to clean up the currenting runnign schedules when the backing condition has
// been removed or renamed.
func (s *schedule) removeExtraneous(names map[string]struct{}) {
	s.Lock()
	defer s.Unlock()

	var namesToDelete []string
	for k := range s.events {
		if _, ok := names[k]; !ok {
			namesToDelete = append(namesToDelete, k)
		}
	}

	for _, n := range namesToDelete {
		delete(s.events, n)
	}
}

// run calls the client for each request
func (s *schedule) run(ctx context.Context, client iotv1proto.ZoneKeeperServiceClient) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-s.reqs:
			if req.sceneReq != nil {
				_, err := client.SetScene(ctx, req.sceneReq)
				if err != nil {
					s.logger.Error("failed to set scene", "err", err)
				}
			}

			if req.stateReq != nil {
				_, err := client.SetState(ctx, req.stateReq)
				if err != nil {
					s.logger.Error("failed to set state", "err", err)
				}
			}
		}
	}
}

type scheduleStatus struct {
	Name  string
	Next  string
	Scene string
	State string
}

func (s *schedule) Status() []scheduleStatus {
	s.Lock()
	defer s.Unlock()

	var stati []scheduleStatus

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

		stati = append(stati, ss)
	}

	return stati
}
