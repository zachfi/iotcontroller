// Package delay provides a mechanism to schedule functions to be executed
// after a specified delay, with the ability to cancel or reschedule them.
package delay

import (
	"context"
	"sync"
	"time"
)

type Item struct {
	ctx    context.Context
	cancel func()
	t      time.Time // TODO: should this be duration or time?
	f      Func
}

type Func func()

type Delay struct {
	mtx sync.Mutex
	m   map[string]Item
}

func New() *Delay {
	return &Delay{
		m: make(map[string]Item),
	}
}

func (d *Delay) Set(ctx context.Context, name string, t time.Time, f Func) {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	if ii, ok := d.m[name]; ok {
		if ii.t.Equal(t) {
			return
		}

		ii.cancel()
	}

	c, cancel := context.WithCancel(ctx)
	i := Item{
		ctx:    c,
		cancel: cancel,
		t:      t,
		f:      f,
	}

	d.m[name] = i
	go delay(i)
}

func delay(i Item) {
	d := time.Until(i.t)

	// If the duration is less than 0, execute and return immediately.
	if d < 0 {
		i.f()
		return
	}

	t := time.NewTimer(d)
	defer t.Stop()

	for {
		select {
		case <-i.ctx.Done():
			return
		case <-t.C:
			i.f()
			return
		}
	}
}
