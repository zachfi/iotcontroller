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

func newTestRouter(bindings []apiv1.Binding, er *mocks.EventReceiverClientMock) *Zigbee2Mqtt {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	return &Zigbee2Mqtt{
		logger:              logger,
		tracer:              noop.NewTracerProvider().Tracer("test"),
		kubeclient:          &fakeKubeClient{bindings: bindings},
		eventReceiverClient: er,
	}
}

func TestFindMQTTBinding(t *testing.T) {
	ctx := context.Background()

	binding := apiv1.Binding{
		Spec: apiv1.BindingSpec{
			MQTT: &apiv1.MQTTTrigger{
				Topic: "zigbee2mqtt/my-button",
				Field: "action",
				Value: "single",
			},
			Condition: "living-area-on",
		},
	}

	t.Run("match returns condition name", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, &mocks.EventReceiverClientMock{})
		got := r.findMQTTBinding(ctx, "zigbee2mqtt/my-button", "action", "single")
		require.Equal(t, "living-area-on", got)
	})

	t.Run("wrong topic returns empty", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, &mocks.EventReceiverClientMock{})
		got := r.findMQTTBinding(ctx, "zigbee2mqtt/other-button", "action", "single")
		require.Empty(t, got)
	})

	t.Run("wrong field returns empty", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, &mocks.EventReceiverClientMock{})
		got := r.findMQTTBinding(ctx, "zigbee2mqtt/my-button", "state", "single")
		require.Empty(t, got)
	})

	t.Run("wrong value returns empty", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, &mocks.EventReceiverClientMock{})
		got := r.findMQTTBinding(ctx, "zigbee2mqtt/my-button", "action", "double")
		require.Empty(t, got)
	})

	t.Run("nil eventReceiverClient returns empty", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, nil)
		r.eventReceiverClient = nil
		got := r.findMQTTBinding(ctx, "zigbee2mqtt/my-button", "action", "single")
		require.Empty(t, got)
	})

	t.Run("zcl binding is skipped", func(t *testing.T) {
		zclBinding := apiv1.Binding{
			Spec: apiv1.BindingSpec{
				ZCL:       &apiv1.ZCLTrigger{IEEE: "0x1234", ClusterID: 6, CommandID: 1},
				Condition: "some-condition",
			},
		}
		r := newTestRouter([]apiv1.Binding{zclBinding}, &mocks.EventReceiverClientMock{})
		got := r.findMQTTBinding(ctx, "zigbee2mqtt/my-button", "action", "single")
		require.Empty(t, got)
	})
}
