package mocks

import (
	"context"
	"sync"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	"google.golang.org/grpc"
)

var _ iotv1proto.EventReceiverServiceClient = (*EventReceiverClientMock)(nil)

// EventReceiverClientMock records ActivateCondition + PushActivation calls
// for use in tests.
type EventReceiverClientMock struct {
	mu                     sync.Mutex
	activateConditionCalls []string
	pushActivationCalls    []*iotv1proto.PushActivationRequest
}

func (e *EventReceiverClientMock) Alert(_ context.Context, _ *iotv1proto.AlertRequest, _ ...grpc.CallOption) (*iotv1proto.AlertResponse, error) {
	return &iotv1proto.AlertResponse{}, nil
}

func (e *EventReceiverClientMock) Epoch(_ context.Context, _ *iotv1proto.EpochRequest, _ ...grpc.CallOption) (*iotv1proto.EpochResponse, error) {
	return &iotv1proto.EpochResponse{}, nil
}

func (e *EventReceiverClientMock) ActivateCondition(_ context.Context, req *iotv1proto.ActivateConditionRequest, _ ...grpc.CallOption) (*iotv1proto.ActivateConditionResponse, error) {
	e.mu.Lock()
	e.activateConditionCalls = append(e.activateConditionCalls, req.Condition)
	e.mu.Unlock()
	return &iotv1proto.ActivateConditionResponse{}, nil
}

// ActivateConditionCalls returns a copy of the condition names passed to ActivateCondition.
func (e *EventReceiverClientMock) ActivateConditionCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.activateConditionCalls))
	copy(out, e.activateConditionCalls)
	return out
}

func (e *EventReceiverClientMock) PushActivation(_ context.Context, req *iotv1proto.PushActivationRequest, _ ...grpc.CallOption) (*iotv1proto.PushActivationResponse, error) {
	e.mu.Lock()
	e.pushActivationCalls = append(e.pushActivationCalls, req)
	e.mu.Unlock()
	return &iotv1proto.PushActivationResponse{}, nil
}

// PushActivationCalls returns a copy of the recorded PushActivation requests.
func (e *EventReceiverClientMock) PushActivationCalls() []*iotv1proto.PushActivationRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*iotv1proto.PushActivationRequest, len(e.pushActivationCalls))
	copy(out, e.pushActivationCalls)
	return out
}
