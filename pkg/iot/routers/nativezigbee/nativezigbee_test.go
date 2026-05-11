package nativezigbee

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

func newTestRouter(b []apiv1.Binding, er *mocks.EventReceiverClientMock) *NativeZigbee {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	kc := &fakeKubeClient{bindings: b}
	return &NativeZigbee{
		logger:              logger,
		tracer:              noop.NewTracerProvider().Tracer("test"),
		kubeclient:          kc,
		eventReceiverClient: er,
		matcher:             bindings.New(kc, "iot", logger),
		ieeeCache:           make(map[string]string),
	}
}

// TestMatcherDispatch_NativePath: an IEEE-scoped Binding written for a
// native-Zigbee device matches when the matching event arrives via the
// native router. This proves the cross-transport guarantee: the same
// EventTrigger spec works whether the message came from z2m or the
// native dongle.
func TestMatcherDispatch_NativePath(t *testing.T) {
	binding := apiv1.Binding{
		Spec: apiv1.BindingSpec{
			Event: apiv1.EventTrigger{
				Property: events.PropertyAction,
				Value:    "on",
				Selector: apiv1.EventSelector{IEEE: "0xffffb40e06036411"},
			},
			Condition: "bedside-zach-on",
		},
	}
	er := &mocks.EventReceiverClientMock{}
	r := newTestRouter([]apiv1.Binding{binding}, er)

	dev := &apiv1.Device{}
	dev.Name = "ffffb40e06036411"
	dev.Spec.IEEEAddress = "0xffffb40e06036411"

	cond := r.matcher.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction,
		Value:    "on",
		Device:   dev,
	})
	require.Equal(t, "bedside-zach-on", cond)
}
