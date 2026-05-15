package zigbeecoordinator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

// newResponseFilterCoordinator constructs a minimal ZigbeeCoordinator with
// just enough state to exercise the response filter API. No dongle or kube
// client wiring — tests that need to exercise sendZclAndAwait use the
// sendDongle stub below to capture the outgoing AfDataRequest and
// optionally trigger a synthetic response on the tap.
func newResponseFilterCoordinator(t *testing.T) *ZigbeeCoordinator {
	t.Helper()
	return &ZigbeeCoordinator{
		logger:          slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
		responseWaiters: make(map[zclResponseKey]chan<- *zclv1proto.ZclMessage),
	}
}

// sendCapturingDongle records the last OutgoingMessage handed to Send and
// optionally calls notifyResponseWaiter on the coordinator to simulate the
// device's response landing on the tap. Used to exercise sendZclAndAwait
// end-to-end without a real dongle.
type sendCapturingDongle struct {
	stubDongle // embed for unused-method panics

	z              *ZigbeeCoordinator
	sentMu         sync.Mutex
	sent           []types.OutgoingMessage
	sendErr        error
	respondWith    *zclv1proto.ZclMessage
	respondAfter   time.Duration
	respondCalls   atomic.Int32
}

func (d *sendCapturingDongle) Send(ctx context.Context, msg types.OutgoingMessage) error {
	d.sentMu.Lock()
	d.sent = append(d.sent, msg)
	d.sentMu.Unlock()
	if d.sendErr != nil {
		return d.sendErr
	}
	if d.respondWith != nil {
		// Fire the synthetic response from a goroutine so Send returns
		// immediately — matches real-dongle behavior.
		resp := d.respondWith
		delay := d.respondAfter
		go func() {
			if delay > 0 {
				time.Sleep(delay)
			}
			d.respondCalls.Add(1)
			d.z.notifyResponseWaiter(resp)
		}()
	}
	return nil
}

// dynamicResponseDongle is a richer test stub: instead of returning a
// pre-canned response, it lets the test compute the response from the
// outgoing frame's actual bytes. Necessary for tests that exercise the
// real txnSeq-matching contract (sendZclAndAwait picks the seq via
// nextZclTxnSequence, so the test can't know it in advance).
//
// `respond` receives the OutgoingMessage.Data bytes and returns the
// ZclMessage to publish on the tap. Returning nil skips the publish.
type dynamicResponseDongle struct {
	stubDongle
	coordinator *ZigbeeCoordinator
	inner       interface{ Send(context.Context, types.OutgoingMessage) error }
	respond     func(outFrame []byte) *zclv1proto.ZclMessage
}

func (d *dynamicResponseDongle) Send(ctx context.Context, msg types.OutgoingMessage) error {
	if d.inner != nil {
		if err := d.inner.Send(ctx, msg); err != nil {
			return err
		}
	}
	if d.respond != nil {
		resp := d.respond(msg.Data)
		if resp != nil {
			go d.coordinator.notifyResponseWaiter(resp)
		}
	}
	return nil
}

// Test_registerResponseWaiter_BasicLifecycle: register, receive, unregister.
// The "is the slot free again after cleanup" property matters because if
// it isn't, every subsequent request with the same txn seq would fail.
func Test_registerResponseWaiter_BasicLifecycle(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	key := zclResponseKey{srcNwk: 0xf88e, clusterID: 0x0000, txnSeq: 7}

	ch, cleanup, err := z.registerResponseWaiter(key)
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.NotNil(t, cleanup)

	// Slot is held — second registration for the same key fails.
	_, _, err2 := z.registerResponseWaiter(key)
	require.Error(t, err2)
	require.Contains(t, err2.Error(), "already registered")

	cleanup()

	// After cleanup the slot is free for a fresh waiter.
	ch3, cleanup3, err3 := z.registerResponseWaiter(key)
	require.NoError(t, err3)
	require.NotNil(t, ch3)
	cleanup3()
}

// Test_notifyResponseWaiter_DeliversMatchingMessage pins the core contract
// of the tap: when a parsed ZclMessage's (srcNwk, clusterID, txnSeq) matches
// a registered waiter, the message lands on the waiter's channel — no
// blocking, no copy required by the caller.
func Test_notifyResponseWaiter_DeliversMatchingMessage(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	key := zclResponseKey{srcNwk: 0xf88e, clusterID: 0x0000, txnSeq: 42}

	ch, cleanup, err := z.registerResponseWaiter(key)
	require.NoError(t, err)
	defer cleanup()

	msg := &zclv1proto.ZclMessage{
		SourceNetwork: uint32(key.srcNwk),
		ClusterId:     uint32(key.clusterID),
		Frame: &zclv1proto.ZclFrame{
			TransactionSequence: uint32(key.txnSeq),
		},
	}

	require.True(t, z.notifyResponseWaiter(msg), "waiter present → should report true")

	select {
	case got := <-ch:
		require.Equal(t, msg, got)
	case <-time.After(time.Second):
		t.Fatal("expected message on response channel")
	}
}

// Test_notifyResponseWaiter_NoMatchSilent: a message with no registered
// waiter must not panic, must return false, and must not block the parse
// goroutine (verified implicitly by the test completing quickly).
func Test_notifyResponseWaiter_NoMatchSilent(t *testing.T) {
	z := newResponseFilterCoordinator(t)

	msg := &zclv1proto.ZclMessage{
		SourceNetwork: 0xabcd,
		ClusterId:     0x0006,
		Frame:         &zclv1proto.ZclFrame{TransactionSequence: 1},
	}

	require.False(t, z.notifyResponseWaiter(msg), "no waiter → should report false")
}

// Test_notifyResponseWaiter_NilSafe: defensive — neither a nil ZclMessage
// nor a message with a nil Frame should crash the parse path.
func Test_notifyResponseWaiter_NilSafe(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	require.False(t, z.notifyResponseWaiter(nil))
	require.False(t, z.notifyResponseWaiter(&zclv1proto.ZclMessage{}))
}

// Test_notifyResponseWaiter_DropsExtraIfChannelFull: a misbehaving device
// (or a poorly-coordinated test) could trigger notifyResponseWaiter twice
// for the same key. The second delivery must not block the parse path.
func Test_notifyResponseWaiter_DropsExtraIfChannelFull(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	key := zclResponseKey{srcNwk: 0xf88e, clusterID: 0x0000, txnSeq: 5}

	_, cleanup, err := z.registerResponseWaiter(key)
	require.NoError(t, err)
	defer cleanup()

	msg := &zclv1proto.ZclMessage{
		SourceNetwork: uint32(key.srcNwk),
		ClusterId:     uint32(key.clusterID),
		Frame:         &zclv1proto.ZclFrame{TransactionSequence: uint32(key.txnSeq)},
	}

	require.True(t, z.notifyResponseWaiter(msg), "first delivery succeeds")
	// Channel is full now; the second delivery must not block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		z.notifyResponseWaiter(msg)
	}()
	select {
	case <-done:
		// Good — the second call returned promptly.
	case <-time.After(time.Second):
		t.Fatal("notifyResponseWaiter blocked on full channel")
	}
}

// Test_notifyResponseWaiter_KeysOnAllThreeFields: identical txnSeq across
// different clusters or devices must NOT cross-deliver. The same physical
// device can have two requests on different clusters with the same seq;
// each must land on the right awaiter.
func Test_notifyResponseWaiter_KeysOnAllThreeFields(t *testing.T) {
	z := newResponseFilterCoordinator(t)

	keyA := zclResponseKey{srcNwk: 0xf88e, clusterID: 0x0000, txnSeq: 1}
	keyB := zclResponseKey{srcNwk: 0xf88e, clusterID: 0x0006, txnSeq: 1}

	chA, cleanupA, err := z.registerResponseWaiter(keyA)
	require.NoError(t, err)
	defer cleanupA()

	chB, cleanupB, err := z.registerResponseWaiter(keyB)
	require.NoError(t, err)
	defer cleanupB()

	msgB := &zclv1proto.ZclMessage{
		SourceNetwork: uint32(keyB.srcNwk),
		ClusterId:     uint32(keyB.clusterID),
		Frame:         &zclv1proto.ZclFrame{TransactionSequence: uint32(keyB.txnSeq)},
	}
	require.True(t, z.notifyResponseWaiter(msgB))

	// B should have it.
	select {
	case got := <-chB:
		require.Equal(t, uint32(keyB.clusterID), got.ClusterId)
	case <-time.After(time.Second):
		t.Fatal("expected message on B")
	}

	// A must NOT have it.
	select {
	case got := <-chA:
		t.Fatalf("A unexpectedly received %v", got)
	case <-time.After(100 * time.Millisecond):
		// good
	}
}

// Test_sendZclAndAwait_HappyPath: register waiter → send → device responds
// via the tap → receive parsed message.
func Test_sendZclAndAwait_HappyPath(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	dongle := &sendCapturingDongle{z: z}
	z.dongle = dongle

	expected := &zclv1proto.ZclMessage{
		SourceNetwork: 0xf88e,
		ClusterId:     0x0000,
		Frame:         &zclv1proto.ZclFrame{TransactionSequence: 99},
	}
	dongle.respondWith = expected

	got, err := z.sendZclAndAwait(context.Background(), 0xf88e, 1, 0x0000, 99,
		[]byte{0x00, 99, 0x00, 0x04, 0x00, 0x05, 0x00}, // dummy ReadAttributes frame
		2*time.Second,
	)
	require.NoError(t, err)
	require.Equal(t, expected, got)

	// Confirm we actually sent something the dongle.
	dongle.sentMu.Lock()
	sentCount := len(dongle.sent)
	var sent types.OutgoingMessage
	if sentCount > 0 {
		sent = dongle.sent[0]
	}
	dongle.sentMu.Unlock()
	require.Equal(t, 1, sentCount, "exactly one Send call")
	require.Equal(t, uint16(0xf88e), sent.Destination.Short)
	require.Equal(t, uint16(0x0000), sent.ClusterID)
	require.Equal(t, uint8(1), sent.DestinationEndpoint)
}

// Test_sendZclAndAwait_Timeout: when no response arrives within the
// timeout, return a timeout error, NOT a nil message. The waiter slot is
// freed so future requests with the same key succeed.
func Test_sendZclAndAwait_Timeout(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	dongle := &sendCapturingDongle{z: z}
	z.dongle = dongle
	// dongle.respondWith intentionally nil — no response will arrive.

	_, err := z.sendZclAndAwait(context.Background(), 0xf88e, 1, 0x0000, 50,
		[]byte{0x00}, 50*time.Millisecond,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")

	// Slot is freed: a subsequent call with the same key can register.
	_, cleanup, regErr := z.registerResponseWaiter(zclResponseKey{srcNwk: 0xf88e, clusterID: 0x0000, txnSeq: 50})
	require.NoError(t, regErr)
	cleanup()
}

// Test_sendZclAndAwait_ContextCancelled: when the caller's context is
// cancelled mid-wait, return ctx.Err() instead of a timeout error so the
// caller can distinguish "I gave up" from "the device didn't answer."
func Test_sendZclAndAwait_ContextCancelled(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	dongle := &sendCapturingDongle{z: z}
	z.dongle = dongle

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := z.sendZclAndAwait(ctx, 0xf88e, 1, 0x0000, 60,
		[]byte{0x00}, 5*time.Second,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
}

// Test_sendZclAndAwait_SendError: if the dongle rejects the send, we
// surface the underlying error and free the waiter slot.
func Test_sendZclAndAwait_SendError(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	dongle := &sendCapturingDongle{
		z:       z,
		sendErr: errors.New("dongle offline"),
	}
	z.dongle = dongle

	_, err := z.sendZclAndAwait(context.Background(), 0xf88e, 1, 0x0000, 70,
		[]byte{0x00}, time.Second,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dongle offline")

	// Slot freed (defer cleanup ran even though we never received).
	_, cleanup, regErr := z.registerResponseWaiter(zclResponseKey{srcNwk: 0xf88e, clusterID: 0x0000, txnSeq: 70})
	require.NoError(t, regErr)
	cleanup()
}

// Test_nextZclTxnSequence_Monotonic: successive calls return distinct
// values modulo 256. Verifies the atomic increment + uint8 truncation.
func Test_nextZclTxnSequence_Monotonic(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	first := z.nextZclTxnSequence()
	second := z.nextZclTxnSequence()
	require.NotEqual(t, first, second, "consecutive calls must return different sequences")
	// Sanity: the difference should be 1 modulo 256.
	require.Equal(t, uint8(1), second-first)
}
