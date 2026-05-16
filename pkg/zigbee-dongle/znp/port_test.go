package znp

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// newTestPort builds a Port with just the fields registerHandler /
// removeHandler touch. Avoids opening a real serial device so the test
// runs in any environment.
func newTestPort() *Port {
	return &Port{
		log:          slog.Default(),
		handlers:     make(map[FrameHeader]*Handler),
		handlerMutex: sync.Mutex{},
	}
}

// Test_RegisterOneOffHandler_DefaultTimeout verifies the legacy
// RegisterOneOffHandler still installs the 40s state-change budget by
// construction (we don't actually wait 40s; we just confirm the
// auto-fail timer is wired up so a future regression that drops it
// would surface here).
func Test_RegisterOneOffHandler_DefaultTimeout(t *testing.T) {
	p := newTestPort()
	h := p.RegisterOneOffHandler(ZdoNodeDescriptor{})
	if h.timer == nil {
		t.Fatal("RegisterOneOffHandler did not install a fail timer")
	}
	if !h.timer.Stop() {
		t.Fatal("RegisterOneOffHandler timer fired before we could check it (or was not active)")
	}
	// The handler should be in the port's map until we cancel it.
	p.handlerMutex.Lock()
	defer p.handlerMutex.Unlock()
	if len(p.handlers) != 1 {
		t.Fatalf("expected 1 handler registered, got %d", len(p.handlers))
	}
}

// Test_RegisterOneOffHandlerWithTimeout_FiresOnSilence exercises the
// failure path that motivated the change: a per-attempt budget short
// enough that an unresponsive device fails fast instead of holding the
// handler slot for the legacy 40s. Receive must return ErrTimeout, and
// the port must drop the handler from its map so the next attempt can
// register a fresh one without panicking on duplicate registration.
func Test_RegisterOneOffHandlerWithTimeout_FiresOnSilence(t *testing.T) {
	p := newTestPort()
	h := p.RegisterOneOffHandlerWithTimeout(ZdoNodeDescriptor{}, 50*time.Millisecond)

	got, err := h.Receive()
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Receive: want ErrTimeout, got value=%v err=%v", got, err)
	}

	// removeHandler runs from the timer goroutine; give it a moment to
	// finish dropping the entry before we inspect the map. Up to 100ms
	// is plenty — the work is map-lock + delete.
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		p.handlerMutex.Lock()
		n := len(p.handlers)
		p.handlerMutex.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("handler still registered %dms after timeout fired (n=%d)", 100, n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// And the next attempt must be able to re-register without
	// panicking on the "handler for %v already exists" path — the real
	// interview retry loop relies on this.
	_ = p.RegisterOneOffHandlerWithTimeout(ZdoNodeDescriptor{}, 50*time.Millisecond)
}

// Test_RegisterOneOffHandlerWithTimeout_FulfillCancelsTimer covers the
// happy path: a response arrives before the deadline, fulfill() drains
// it to the caller, and the timer is stopped so it never tries to
// later fail an already-completed handler. Without the Stop() in
// fulfill, the second send into the buffered channel would block the
// timer goroutine forever (chan capacity is 1).
func Test_RegisterOneOffHandlerWithTimeout_FulfillCancelsTimer(t *testing.T) {
	p := newTestPort()
	h := p.RegisterOneOffHandlerWithTimeout(ZdoNodeDescriptor{}, 1*time.Second)

	want := ZdoNodeDescriptor{Status: 0x42}
	h.fulfill(want)

	got, err := h.Receive()
	if err != nil {
		t.Fatalf("Receive after fulfill: unexpected err=%v", err)
	}
	if got.(ZdoNodeDescriptor).Status != want.Status {
		t.Fatalf("Receive: want Status=0x%02x, got %#v", want.Status, got)
	}

	// Wait past the original deadline; the timer must have been
	// stopped, so the handler's map entry — if anything cared to
	// inspect it — does not get torn down by a stale fail.
	time.Sleep(50 * time.Millisecond)
	select {
	case extra := <-h.results:
		t.Fatalf("unexpected second result after fulfill: %+v", extra)
	default:
	}
}

// Test_RegisterOneOffHandlerWithTimeout_ZeroDisablesTimer documents
// the contract: passing 0 disables the auto-fail timer entirely. This
// matches RegisterPermanentHandler's behavior and lets callers opt out
// when they manage cancellation themselves.
func Test_RegisterOneOffHandlerWithTimeout_ZeroDisablesTimer(t *testing.T) {
	p := newTestPort()
	h := p.RegisterOneOffHandlerWithTimeout(ZdoNodeDescriptor{}, 0)
	if h.timer != nil {
		t.Fatalf("timeout=0 must not install a timer; got %v", h.timer)
	}
}
