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
	s.Lock()
	defer s.Unlock()

	if v, ok := s.events[name]; ok {
		if matched(v.req, req) {
			return
		}

		v.cancel()
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

// run calls the client for each request
func (s *schedule) run(ctx context.Context, client iotv1proto.ZoneKeeperServiceClient, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-s.reqs:
			if req.sceneReq != nil {
				_, err := client.SetScene(ctx, req.sceneReq)
				if err != nil {
					logger.Error("failed to set scene", "err", err)
				}
			}

			if req.stateReq != nil {
				_, err := client.SetState(ctx, req.stateReq)
				if err != nil {
					logger.Error("failed to set state", "err", err)
				}
			}
		}
	}
}
