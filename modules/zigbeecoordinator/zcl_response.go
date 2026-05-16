package zigbeecoordinator

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

// ZCL response filter (in-process tap).
//
// Problem this solves: the coordinator's incoming-message loop forwards every
// parsed ZclMessage to the router via gRPC. Some senders inside the
// coordinator pod (initially the interview path; later potentially other
// active reads against attached devices) need to await a specific response
// keyed on (srcNwk, clusterID, transactionSequence). The router doesn't
// know about pending in-flight requests and lives in a different pod, so
// routing the response there and back would be a needless round-trip
// across processes for data that's already in hand locally.
//
// Design: a small map on ZigbeeCoordinator keyed by zclResponseKey. The
// existing parse path calls notifyResponseWaiter immediately after a
// successful ZCL parse, before forwarding to the router. If a waiter is
// registered for the key, it receives the parsed proto on a buffered
// channel (non-blocking — the parse goroutine cannot stall on a slow or
// dead consumer); the forwarding path continues unchanged. The tap is
// purely additive and never consumes the message.
//
// Cross-process boundaries (e.g. exposing this primitive to a different
// pod) are deliberately left to gRPC: any future "let me request a ZCL
// read against this device" surface from outside the coordinator pod
// becomes an RPC on the coordinator, with sendZclAndAwait as the
// in-process implementation under the hood.

// zclResponseKey identifies a pending awaiter. All three components are
// required:
//
//   - srcNwk: the device we sent to; isolates responses per device when
//     multiple parallel requests are in flight.
//   - clusterID: the ZCL cluster of the response; the same transaction
//     sequence can be re-used across different clusters by the same device.
//   - txnSeq: matches the byte the sender placed in the outgoing ZCL frame
//     header; devices echo it back unchanged.
type zclResponseKey struct {
	srcNwk    uint16
	clusterID uint16
	txnSeq    uint8
}

func (k zclResponseKey) String() string {
	return fmt.Sprintf("nwk=0x%04x cluster=0x%04x seq=%d", k.srcNwk, k.clusterID, k.txnSeq)
}

// registerResponseWaiter atomically reserves a slot in the response map
// for the caller. Returns the channel the caller should read from (size 1
// — only one response is expected per request) and an unregister function
// that the caller MUST call when done, typically in a defer, to free the
// slot regardless of whether a response arrived.
//
// Returns an error if a waiter already exists for this key. The expected
// caller flow is "reserve seq → send request → await response on chan →
// unregister", so a duplicate registration almost always indicates a
// caller bug (re-use of the same sequence number while a previous one is
// still in flight).
func (z *ZigbeeCoordinator) registerResponseWaiter(key zclResponseKey) (<-chan *zclv1proto.ZclMessage, func(), error) {
	z.responseWaitersMu.Lock()
	defer z.responseWaitersMu.Unlock()

	if z.responseWaiters == nil {
		z.responseWaiters = make(map[zclResponseKey]chan<- *zclv1proto.ZclMessage)
	}
	if _, exists := z.responseWaiters[key]; exists {
		return nil, nil, fmt.Errorf("response waiter already registered for %s", key)
	}

	ch := make(chan *zclv1proto.ZclMessage, 1)
	z.responseWaiters[key] = ch

	cleanup := func() {
		z.responseWaitersMu.Lock()
		// Only delete if our channel is still the registered one — a
		// well-behaved caller will always satisfy this, but defensive
		// in case of bizarre reuse.
		if got, ok := z.responseWaiters[key]; ok && got == (chan<- *zclv1proto.ZclMessage)(ch) {
			delete(z.responseWaiters, key)
		}
		z.responseWaitersMu.Unlock()
	}
	return ch, cleanup, nil
}

// notifyResponseWaiter is invoked from the parse path on every successfully-
// parsed ZclMessage. It looks up the (srcNwk, clusterID, txnSeq) tuple in
// the waiters map and, if a waiter is registered, delivers the message to
// it.
//
// Delivery is non-blocking on a size-1 channel: if the channel is already
// full (the awaiter hasn't yet drained the previous message — should not
// happen in well-formed usage, since we only register one waiter per key)
// the additional message is dropped on the floor with a debug log. We
// MUST NOT block here because this runs synchronously on the goroutine
// that's about to forward the message to the router; any blocking would
// add latency to unrelated traffic on the dongle.
//
// Returns true iff a waiter was found and the message was delivered
// (regardless of whether the channel had room). Caller can use the return
// for tracing or metrics; today it's purely advisory.
func (z *ZigbeeCoordinator) notifyResponseWaiter(msg *zclv1proto.ZclMessage) bool {
	if msg == nil || msg.Frame == nil {
		return false
	}

	key := zclResponseKey{
		srcNwk:    uint16(msg.GetSourceNetwork()),
		clusterID: uint16(msg.GetClusterId()),
		txnSeq:    uint8(msg.Frame.GetTransactionSequence()),
	}

	z.responseWaitersMu.RLock()
	ch, ok := z.responseWaiters[key]
	z.responseWaitersMu.RUnlock()
	if !ok {
		return false
	}

	select {
	case ch <- msg:
		return true
	default:
		// Awaiter is full — already received one. Drop. Logged at
		// debug because in normal usage this path should never fire.
		z.logger.Debug("ZCL response channel full, dropping additional message", "key", key.String())
		return true
	}
}

// nextZclTxnSequence returns the next 8-bit transaction sequence number
// to place in an outgoing ZCL frame. Wraps at 256 naturally via uint8
// truncation; the in-flight window for any given cluster on any given
// device is far smaller than 256, so wraparound collisions are not a
// concern in practice.
func (z *ZigbeeCoordinator) nextZclTxnSequence() uint8 {
	return uint8(atomic.AddUint32(&z.cmdSequence, 1))
}

// sendZclAndAwait sends a pre-encoded ZCL frame to a device and blocks
// until a response with the same (srcNwk, clusterID, txnSeq) tuple comes
// back, or the context is cancelled, or the timeout fires.
//
// `frameBytes` is the full ZCL payload that will be placed in the
// AfDataRequest's Data field — frame control byte, txn seq byte,
// command id byte, then the command-specific payload. The caller MUST
// have placed `txnSeq` at offset 1 of frameBytes (the standard ZCL header
// layout for non-manufacturer-specific frames); we don't re-parse and
// rewrite it. This keeps the API honest about who owns the wire format.
//
// Returns the parsed response message on success, or:
//   - error from the dongle Send call (e.g. device unreachable)
//   - context.DeadlineExceeded if the timeout fires
//   - ctx.Err() if the caller's context is cancelled
//   - error from registerResponseWaiter if the key is already in flight
//
// Cleanup of the waiter slot happens unconditionally via defer; callers
// don't need to do anything special on failure paths.
func (z *ZigbeeCoordinator) sendZclAndAwait(
	ctx context.Context,
	nwk uint16,
	endpoint uint8,
	clusterID uint16,
	txnSeq uint8,
	frameBytes []byte,
	timeout time.Duration,
) (*zclv1proto.ZclMessage, error) {
	key := zclResponseKey{srcNwk: nwk, clusterID: clusterID, txnSeq: txnSeq}

	respCh, cleanup, err := z.registerResponseWaiter(key)
	if err != nil {
		return nil, fmt.Errorf("registering ZCL response waiter: %w", err)
	}
	defer cleanup()

	// Send is invoked AFTER registering the waiter so a fast-responding
	// device cannot land its reply before we're listening. The
	// non-blocking semantics of notifyResponseWaiter further protect us
	// against any residual races on the read side.
	if err := z.dongle.Send(ctx, types.OutgoingMessage{
		Destination: types.Address{
			Mode:  types.AddressModeNWK,
			Short: nwk,
		},
		SourceEndpoint:      1, // ZCL clients conventionally use endpoint 1
		DestinationEndpoint: endpoint,
		ClusterID:           clusterID,
		Radius:              30,
		Data:                frameBytes,
	}); err != nil {
		return nil, fmt.Errorf("sending ZCL frame to nwk=0x%04x: %w", nwk, err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case msg := <-respCh:
		return msg, nil
	case <-timer.C:
		return nil, fmt.Errorf("ZCL response timeout after %s for %s", timeout, key)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
