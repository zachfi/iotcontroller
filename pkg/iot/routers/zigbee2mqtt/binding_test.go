package zigbee2mqtt

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot/bindings"
	"github.com/zachfi/iotcontroller/pkg/iot/events"
	"github.com/zachfi/iotcontroller/pkg/mocks"
)

// fakeKubeClient returns a fixed BindingList from List; all other methods panic.
// Kept here (not exported) so the router-level tests can run without spinning
// up envtest. The matcher itself is exhaustively tested in
// pkg/iot/bindings/match_test.go using the same fake.
type fakeKubeClient struct {
	bindings []apiv1.Binding
}

func (f *fakeKubeClient) List(_ context.Context, list kubeclient.ObjectList, _ ...kubeclient.ListOption) error {
	if bl, ok := list.(*apiv1.BindingList); ok {
		bl.Items = append(bl.Items, f.bindings...)
	}
	return nil
}
func (f *fakeKubeClient) Get(_ context.Context, _ kubeclient.ObjectKey, _ kubeclient.Object, _ ...kubeclient.GetOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...kubeclient.ApplyOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Create(_ context.Context, _ kubeclient.Object, _ ...kubeclient.CreateOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Delete(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Update(_ context.Context, _ kubeclient.Object, _ ...kubeclient.UpdateOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Patch(_ context.Context, _ kubeclient.Object, _ kubeclient.Patch, _ ...kubeclient.PatchOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) DeleteAllOf(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteAllOfOption) error {
	panic("not implemented")
}
func (f *fakeKubeClient) Status() kubeclient.SubResourceWriter              { panic("not implemented") }
func (f *fakeKubeClient) SubResource(_ string) kubeclient.SubResourceClient { panic("not implemented") }
func (f *fakeKubeClient) Scheme() *runtime.Scheme                           { panic("not implemented") }
func (f *fakeKubeClient) RESTMapper() meta.RESTMapper                       { panic("not implemented") }
func (f *fakeKubeClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	panic("not implemented")
}
func (f *fakeKubeClient) IsObjectNamespaced(_ runtime.Object) (bool, error) {
	panic("not implemented")
}

func newTestRouter(b []apiv1.Binding, er *mocks.EventReceiverClientMock) *Zigbee2Mqtt {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	kc := &fakeKubeClient{bindings: b}
	return &Zigbee2Mqtt{
		logger:              logger,
		tracer:              noop.NewTracerProvider().Tracer("test"),
		kubeclient:          kc,
		eventReceiverClient: er,
		matcher:             bindings.New(kc, "iot", logger),
	}
}

// TestDispatchEvent_Match: when a Binding matches the event, the matcher
// returns its condition name and dispatchEvent activates that condition.
func TestDispatchEvent_Match(t *testing.T) {
	binding := apiv1.Binding{
		Spec: apiv1.BindingSpec{
			Event: apiv1.EventTrigger{
				Property: events.PropertyAction,
				Value:    "single",
				Selector: apiv1.EventSelector{Device: "my-button"},
			},
			Condition: "living-area-on",
		},
	}
	er := &mocks.EventReceiverClientMock{}
	r := newTestRouter([]apiv1.Binding{binding}, er)

	dev := &apiv1.Device{}
	dev.Name = "my-button"

	got := r.dispatchEvent(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction,
		Value:    "single",
		Device:   dev,
	})
	require.True(t, got, "binding should have matched")
	require.Equal(t, []string{"living-area-on"}, er.ActivateConditionCalls())
}

// TestDispatchEvent_NoMatch: when no Binding matches, dispatchEvent
// returns false and does not invoke the eventReceiverClient. Caller
// records the unhandled action via the fallback counter and otherwise
// no-ops.
func TestDispatchEvent_NoMatch(t *testing.T) {
	er := &mocks.EventReceiverClientMock{}
	r := newTestRouter(nil, er)

	dev := &apiv1.Device{}
	dev.Name = "my-button"

	got := r.dispatchEvent(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction,
		Value:    "single",
		Device:   dev,
	})
	require.False(t, got, "no binding should not dispatch")
	require.Empty(t, er.ActivateConditionCalls())
}

// TestDispatchEvent_NilClient: with no eventReceiverClient configured the
// router must not panic; it returns false so the legacy fallback runs.
func TestDispatchEvent_NilClient(t *testing.T) {
	r := newTestRouter(nil, nil)
	r.eventReceiverClient = nil
	dev := &apiv1.Device{}
	dev.Name = "my-button"

	got := r.dispatchEvent(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction,
		Value:    "single",
		Device:   dev,
	})
	require.False(t, got)
}
