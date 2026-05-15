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

// Test_reinterviewTick_NoAnnotation: a Device without the reinterview
// annotation is left alone — no dongle calls, no CR mutations.
func Test_reinterviewTick_NoAnnotation(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "0xa4c138025b31ffff"
	d.Spec.IEEEAddress = "0xa4c138025b31ffff"
	d.Spec.NetworkAddress = "0xf88e"

	z, stub, _ := newTestCoordinator(t, d)
	z.reinterviewTick(context.Background())

	require.Equal(t, int32(0), stub.interviewCalls.Load(), "no annotation → no interview call")
}

// Test_reinterviewTick_HappyPath: a Device with the annotation set AND
// valid IEEE/NWK addresses triggers an interview and has the annotation
// cleared after dispatch. The fake dongle returns a useful interview
// result so the dispatch reaches the success branch in
// interviewAndDispatch.
func Test_reinterviewTick_HappyPath(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "0xa4c138025b31ffff"
	d.Annotations = map[string]string{apiv1.AnnotationReinterviewRequested: "true"}
	d.Spec.IEEEAddress = "0xa4c138025b31ffff"
	d.Spec.NetworkAddress = "0xf88e"

	z, stub, kc := newTestCoordinator(t, d)
	stub.interviewFunc = func(_ context.Context, nwk uint16) (*types.DeviceInterviewInfo, error) {
		require.Equal(t, uint16(0xf88e), nwk, "interview must use parsed NWK from Spec")
		return &types.DeviceInterviewInfo{
			NetworkAddress: nwk,
			ManufacturerID: 0x1037, // arbitrary non-zero so dispatch treats it as useful
		}, nil
	}

	z.reinterviewTick(context.Background())

	require.Equal(t, int32(1), stub.interviewCalls.Load(), "exactly one interview call")

	var got apiv1.Device
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(d), &got))
	_, present := got.Annotations[apiv1.AnnotationReinterviewRequested]
	require.False(t, present, "annotation must be cleared after successful dispatch")
}

// Test_reinterviewTick_AnnotationClearedEvenOnFailedInterview: a partial-
// or no-info interview result still clears the annotation. Rationale is
// documented in reinterview.go — we don't want to loop on a permanently-
// quirky device. The operator re-sets the annotation when they want to
// retry.
func Test_reinterviewTick_AnnotationClearedEvenOnFailedInterview(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "0xa4c138025b31ffff"
	d.Annotations = map[string]string{apiv1.AnnotationReinterviewRequested: "true"}
	d.Spec.IEEEAddress = "0xa4c138025b31ffff"
	d.Spec.NetworkAddress = "0xf88e"

	z, stub, kc := newTestCoordinator(t, d)
	stub.interviewFunc = func(_ context.Context, nwk uint16) (*types.DeviceInterviewInfo, error) {
		// "We learned nothing" — matches the failure-mode the
		// SNZB-02 produced in production on 2026-05-15.
		return &types.DeviceInterviewInfo{NetworkAddress: nwk}, nil
	}

	z.reinterviewTick(context.Background())

	require.Equal(t, int32(1), stub.interviewCalls.Load())

	var got apiv1.Device
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(d), &got))
	_, present := got.Annotations[apiv1.AnnotationReinterviewRequested]
	require.False(t, present, "even a useless interview clears the annotation; operator re-sets to retry")
}

// Test_reinterviewTick_MissingNWKLeavesAnnotation: a device whose Spec
// has the annotation but no NetworkAddress cannot be re-interviewed.
// The annotation must stay so an operator notices the failure mode and
// fills in the NWK (or it lands organically on the next coordinator
// restart when we'll patch in NetworkAddress).
func Test_reinterviewTick_MissingNWKLeavesAnnotation(t *testing.T) {
	d := &apiv1.Device{}
	d.Name = "0xa4c138025b31ffff"
	d.Annotations = map[string]string{apiv1.AnnotationReinterviewRequested: "true"}
	d.Spec.IEEEAddress = "0xa4c138025b31ffff"
	// Spec.NetworkAddress deliberately empty

	z, stub, kc := newTestCoordinator(t, d)
	z.reinterviewTick(context.Background())

	require.Equal(t, int32(0), stub.interviewCalls.Load(), "no NWK → no interview attempt")

	var got apiv1.Device
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(d), &got))
	require.Equal(t, "true", got.Annotations[apiv1.AnnotationReinterviewRequested],
		"annotation stays set so the operator sees the unmet precondition")
}

// Test_reinterviewTick_MultipleDevicesIndependent: a malformed device in
// the list doesn't block other annotated devices in the same tick from
// being processed.
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

	z, stub, kc := newTestCoordinator(t, dGood, dBroken)
	stub.interviewFunc = func(_ context.Context, _ uint16) (*types.DeviceInterviewInfo, error) {
		return &types.DeviceInterviewInfo{ManufacturerID: 0x1}, nil
	}

	z.reinterviewTick(context.Background())

	require.Equal(t, int32(1), stub.interviewCalls.Load(), "the good device gets interviewed")

	var gotGood, gotBroken apiv1.Device
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(dGood), &gotGood))
	require.NoError(t, kc.Get(context.Background(), client.ObjectKeyFromObject(dBroken), &gotBroken))

	_, goodPresent := gotGood.Annotations[apiv1.AnnotationReinterviewRequested]
	require.False(t, goodPresent, "good device annotation cleared")
	require.Equal(t, "true", gotBroken.Annotations[apiv1.AnnotationReinterviewRequested],
		"broken device annotation untouched")
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
