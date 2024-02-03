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
	reqs   chan *iotv1proto.SetStateRequest
}

type event struct {
	cancel context.CancelFunc
	t      time.Time
	req    *iotv1proto.SetStateRequest
}

func (s *schedule) add(ctx context.Context, name string, t time.Time, req *iotv1proto.SetStateRequest) {
	s.Lock()
	defer s.Unlock()

	if v, ok := s.events[name]; ok {
		if v.t.Equal(t) && v.req.Name == req.Name && v.req.State == req.State {
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

	go func(ctx context.Context) {
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
	}(ctx)
}

// run calls the client for each request
func (s *schedule) run(ctx context.Context, client iotv1proto.ZoneKeeperServiceClient, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-s.reqs:
			_, err := client.SetState(ctx, req)
			if err != nil {
				logger.Error("failed to set state", "err", err)
			}
		}
	}
}
