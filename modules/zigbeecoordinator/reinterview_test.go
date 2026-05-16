package zigbeecoordinator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	zigbeedongle "github.com/zachfi/iotcontroller/pkg/zigbee-dongle"
	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// stubDongle satisfies enough of the zigbeedongle.Dongle interface for
// the reinterview unit tests — InterviewDevice + Send. Send returns a
// sentinel error so that interviewAndDispatch's ZCL Basic cluster
// enrichment step (which calls Send via sendZclAndAwait) fails fast
// and is logged-and-skipped rather than blocking on a 10-second tap
// timeout. Tests that DO want to exercise Send override this method
// via embedding (see sendCapturingDongle / dynamicResponseDongle in
// zcl_response_test.go).
type stubDongle struct {
	zigbeedongle.Dongle

	interviewCalls atomic.Int32
	interviewFunc  func(ctx context.Context, nwk uint16) (*types.DeviceInterviewInfo, error)
}

func (s *stubDongle) InterviewDevice(ctx context.Context, nwk uint16) (*types.DeviceInterviewInfo, error) {
	s.interviewCalls.Add(1)
	if s.interviewFunc != nil {
		return s.interviewFunc(ctx, nwk)
	}
	return &types.DeviceInterviewInfo{NetworkAddress: nwk}, nil
}

// Send is a no-op that returns a sentinel error. interviewAndDispatch's
// readBasicClusterAttributes call will see this, log a warning, and
// proceed without the Basic-cluster overlay — exactly the behavior we
// want for tests focused on the reinterview-tick / annotation flow.
var errStubDongleSendNotImplemented = errors.New("stubDongle.Send: not implemented for this test")

func (s *stubDongle) Send(_ context.Context, _ types.OutgoingMessage) error {
	return errStubDongleSendNotImplemented
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, apiv1.AddToScheme(s))
	return s
}

// newTestCoordinator builds a ZigbeeCoordinator wired with a stub dongle
// and a fake kube client preloaded with `devices`. routeClient is nil
// because sendInterviewResult bails out early when routeClient is nil
// (we're testing the polling + annotation-clearing path, not the gRPC
// dispatch).
func newTestCoordinator(t *testing.T, devices ...*apiv1.Device) (*ZigbeeCoordinator, *stubDongle, client.Client) {
	t.Helper()

	objs := make([]client.Object, 0, len(devices))
	for _, d := range devices {
		objs = append(objs, d)
	}
	kc := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(objs...).
		Build()

	stub := &stubDongle{}

	z := &ZigbeeCoordinator{
		cfg:          &Config{},
		logger:       slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
		tracer:       otel.Tracer("test"),
		routeClient:  nil,
		kubeClient:   kc,
		dongle:       stub,
		interviewing: make(map[uint16]bool),
		nwkToIEEE:    make(map[uint16]uint64),
		ieeeToNWK:    make(map[string]uint16),
	}

	return z, stub, kc
}

// Test_deviceAddressesFromSpec covers the hex-string parsing contract
// for both addresses, including the "0x" prefix variants and the
// failure cases the reinterview tick must keep its hands off.
func Test_deviceAddressesFromSpec(t *testing.T) {
	cases := []struct {
		name          string
		spec          apiv1.DeviceSpec
		wantIEEE      uint64
		wantNWK       uint16
		wantErrSubstr string
	}{
		{
			name: "canonical 0x-prefixed",
			spec: apiv1.DeviceSpec{
				IEEEAddress:    "0xa4c138025b31ffff",
				NetworkAddress: "0xf88e",
			},
			wantIEEE: 0xa4c138025b31ffff,
			wantNWK:  0xf88e,
		},
		{
			name: "no prefix is accepted",
			spec: apiv1.DeviceSpec{
				IEEEAddress:    "a4c138025b31ffff",
				NetworkAddress: "f88e",
			},
			wantIEEE: 0xa4c138025b31ffff,
			wantNWK:  0xf88e,
		},
		{
			name: "mixed case is accepted",
			spec: apiv1.DeviceSpec{
				IEEEAddress:    "0xA4C138025B31FFFF",
				NetworkAddress: "0xF88E",
			},
			wantIEEE: 0xa4c138025b31ffff,
			wantNWK:  0xf88e,
		},
		{
			name:          "empty IEEE rejected",
			spec:          apiv1.DeviceSpec{NetworkAddress: "0xf88e"},
			wantErrSubstr: "empty Spec.IEEEAddress",
		},
		{
			name:          "empty NWK rejected (re-interview requires it)",
			spec:          apiv1.DeviceSpec{IEEEAddress: "0xa4c138025b31ffff"},
			wantErrSubstr: "empty Spec.NetworkAddress",
		},
		{
			name: "garbage IEEE",
			spec: apiv1.DeviceSpec{
				IEEEAddress:    "0xnotahex",
				NetworkAddress: "0xf88e",
			},
			wantErrSubstr: "invalid Spec.IEEEAddress",
		},
		{
			name: "NWK too wide",
			spec: apiv1.DeviceSpec{
				IEEEAddress:    "0xa4c138025b31ffff",
				NetworkAddress: "0x1ffff", // 17 bits, won't fit uint16
			},
			wantErrSubstr: "invalid Spec.NetworkAddress",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &apiv1.Device{Spec: tc.spec}
			ieee, nwk, err := deviceAddressesFromSpec(d)
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErrSubstr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantIEEE, ieee)
			require.Equal(t, tc.wantNWK, nwk)
		})
	}
}

// pendingNwks snapshots the keys of z.pendingReinterview for assertions.
func pendingNwks(z *ZigbeeCoordinator) []uint16 {
	z.pendingReinterviewMu.Lock()
	defer z.pendingReinterviewMu.Unlock()
	out := make([]uint16, 0, len(z.pendingReinterview))
	for nwk := range z.pendingReinterview {
		out = append(out, nwk)
	}
	return out
}

// Test_reinterviewTick_NoAnnotation: a Device without the reinterview
// annotation is left alone — no pending registration, no CR mutations.
func Test_reinterviewTick_NoAnnotation(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "0xa4c138025b31ffff"
	d.Spec.IEEEAddress = "0xa4c138025b31ffff"
	d.Spec.NetworkAddress = "0xf88e"

	z, _, _ := newTestCoordinator(t, d)
	z.reinterviewTick(context.Background())

	require.Empty(t, pendingNwks(z), "no annotation → no pending registration")
}

// Test_reinterviewTick_RegistersPending: with the opportunistic design
// (2026-05-15 SNZB-02 finding: sleepy devices poll every ~10min and
// don't answer requests in between), the tick now REGISTERS a
// pendingReinterview entry rather than dispatching immediately. The
// actual enrichment fires later from the parse path when a message
// from the target NWK is observed.
func Test_reinterviewTick_RegistersPending(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "0xa4c138025b31ffff"
	d.Annotations = map[string]string{apiv1.AnnotationReinterviewRequested: "v0.7.1-test"}
	d.Spec.IEEEAddress = "0xa4c138025b31ffff"
	d.Spec.NetworkAddress = "0xf88e"

	z, _, kc := newTestCoordinator(t, d)
	z.reinterviewTick(context.Background())

	pending := pendingNwks(z)
	require.Equal(t, []uint16{0xf88e}, pending, "expected one pending nwk")

	z.pendingReinterviewMu.Lock()
	entry := z.pendingReinterview[0xf88e]
	z.pendingReinterviewMu.Unlock()
	require.Equal(t, uint64(0xa4c138025b31ffff), entry.ieee)
	require.Equal(t, "0xa4c138025b31ffff", entry.deviceCRName)
	require.Equal(t, "v0.7.1-test", entry.requestedBy)

	// Annotation MUST still be set — the enrichment hasn't run yet, so
	// clearing it now would lose the operator's intent if the
	// coordinator restarts before the device next chirps.
	var got apiv1.Device
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(d), &got))
	require.Equal(t, "v0.7.1-test", got.Annotations[apiv1.AnnotationReinterviewRequested])
}

// Test_reinterviewTick_Idempotent: re-running the tick over a device
// with the annotation still set just refreshes the pending entry's
// registeredAt — doesn't duplicate the entry or break invariants.
func Test_reinterviewTick_Idempotent(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "0xa4c138025b31ffff"
	d.Annotations = map[string]string{apiv1.AnnotationReinterviewRequested: "first"}
	d.Spec.IEEEAddress = "0xa4c138025b31ffff"
	d.Spec.NetworkAddress = "0xf88e"

	z, _, _ := newTestCoordinator(t, d)
	z.reinterviewTick(context.Background())
	firstAt := timeRegistered(z, 0xf88e)

	time.Sleep(10 * time.Millisecond)
	z.reinterviewTick(context.Background())
	secondAt := timeRegistered(z, 0xf88e)

	require.True(t, secondAt.After(firstAt), "re-registration refreshes the timestamp")
	require.Len(t, pendingNwks(z), 1, "still exactly one pending entry")
}

// Test_reinterviewTick_MissingNWKLeavesAnnotation: a device whose Spec
// has the annotation but no NetworkAddress cannot be re-interviewed.
// The annotation must stay so an operator notices the failure mode.
// Nothing gets registered as pending either.
func Test_reinterviewTick_MissingNWKLeavesAnnotation(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "0xa4c138025b31ffff"
	d.Annotations = map[string]string{apiv1.AnnotationReinterviewRequested: "true"}
	d.Spec.IEEEAddress = "0xa4c138025b31ffff"
	// Spec.NetworkAddress deliberately empty

	z, _, kc := newTestCoordinator(t, d)
	z.reinterviewTick(context.Background())

	require.Empty(t, pendingNwks(z), "no NWK → not registered as pending")

	var got apiv1.Device
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(d), &got))
	require.Equal(t, "true", got.Annotations[apiv1.AnnotationReinterviewRequested],
		"annotation stays set so the operator sees the unmet precondition")
}

// Test_reinterviewTick_MultipleDevicesIndependent: a malformed device
// in the list doesn't block other annotated devices in the same tick
// from being registered.
func Test_reinterviewTick_MultipleDevicesIndependent(t *testing.T) {
	dGood := &apiv1.Device{}
	dGood.Name = "0xaaaa"
	dGood.Annotations = map[string]string{apiv1.AnnotationReinterviewRequested: "true"}
	dGood.Spec.IEEEAddress = "0xaaaa"
	dGood.Spec.NetworkAddress = "0xbbbb"

	dBroken := &apiv1.Device{}
	dBroken.Name = "0xcccc"
	dBroken.Annotations = map[string]string{apiv1.AnnotationReinterviewRequested: "true"}
	dBroken.Spec.IEEEAddress = "0xcccc"
	// missing NetworkAddress

	z, _, kc := newTestCoordinator(t, dGood, dBroken)
	z.reinterviewTick(context.Background())

	require.Equal(t, []uint16{0xbbbb}, pendingNwks(z),
		"only the good device gets registered as pending")

	var gotGood, gotBroken apiv1.Device
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(dGood), &gotGood))
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(dBroken), &gotBroken))
	require.Equal(t, "true", gotGood.Annotations[apiv1.AnnotationReinterviewRequested],
		"good device annotation stays — opportunistic enrichment hasn't fired yet")
	require.Equal(t, "true", gotBroken.Annotations[apiv1.AnnotationReinterviewRequested],
		"broken device annotation also stays (still ineligible)")
}

// Test_triggerPendingEnrichmentIfAny_NoPending: trigger called for a NWK
// with no pending entry must be a silent no-op — does not panic, does
// not modify state.
func Test_triggerPendingEnrichmentIfAny_NoPending(t *testing.T) {
	z, _, _ := newTestCoordinator(t)
	require.NotPanics(t, func() {
		z.triggerPendingEnrichmentIfAny(0xf88e)
	})
}

// Test_triggerPendingEnrichmentIfAny_ConsumesEntry: when activity is
// observed for a pending NWK, the entry is removed from the map
// atomically with goroutine kickoff so a flurry of subsequent
// messages doesn't spawn parallel enrichments. The actual dongle
// activity is deferred to a goroutine; we just verify the map state
// flips immediately.
func Test_triggerPendingEnrichmentIfAny_ConsumesEntry(t *testing.T) {
	z, _, _ := newTestCoordinator(t)
	z.registerPendingReinterview(pendingReinterview{
		ieee: 0xa4c138025b31ffff, nwk: 0xf88e,
		deviceCRName: "0xa4c138025b31ffff", requestedBy: "test", registeredAt: time.Now(),
	})
	require.Equal(t, []uint16{0xf88e}, pendingNwks(z))

	z.triggerPendingEnrichmentIfAny(0xf88e)

	require.Empty(t, pendingNwks(z), "entry consumed synchronously by trigger")

	// A subsequent trigger for the same NWK is a no-op.
	require.NotPanics(t, func() {
		z.triggerPendingEnrichmentIfAny(0xf88e)
	})
}

// Test_triggerPendingEnrichmentIfAny_UnrelatedNwk: a message from a
// different NWK than the pending one must not consume the pending
// entry.
func Test_triggerPendingEnrichmentIfAny_UnrelatedNwk(t *testing.T) {
	z, _, _ := newTestCoordinator(t)
	z.registerPendingReinterview(pendingReinterview{
		ieee: 0xaaaa, nwk: 0xf88e,
		deviceCRName: "0xaaaa", requestedBy: "test", registeredAt: time.Now(),
	})

	z.triggerPendingEnrichmentIfAny(0x1234) // different NWK

	require.Equal(t, []uint16{0xf88e}, pendingNwks(z),
		"unrelated NWK leaves the pending entry intact")
}

func timeRegistered(z *ZigbeeCoordinator, nwk uint16) time.Time {
	z.pendingReinterviewMu.Lock()
	defer z.pendingReinterviewMu.Unlock()
	return z.pendingReinterview[nwk].registeredAt
}

// Test_runReinterviewPoll_NilKubeClientNoOp: if the coordinator was
// constructed without a kube client (e.g. unit test wiring), the poll
// goroutine must exit immediately rather than panicking.
func Test_runReinterviewPoll_NilKubeClientNoOp(t *testing.T) {
	z := &ZigbeeCoordinator{
		logger:     slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})),
		kubeClient: nil,
	}

	// Should return promptly. Use a wait group so test fails if the
	// goroutine somehow blocks.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		z.runReinterviewPoll(context.Background())
	}()
	wg.Wait()
}
