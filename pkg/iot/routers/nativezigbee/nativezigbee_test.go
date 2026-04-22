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
	"github.com/zachfi/iotcontroller/pkg/mocks"
	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
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

func newTestRouter(bindings []apiv1.Binding, er *mocks.EventReceiverClientMock) *NativeZigbee {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	return &NativeZigbee{
		logger:              logger,
		tracer:              noop.NewTracerProvider().Tracer("test"),
		kubeclient:          &fakeKubeClient{bindings: bindings},
		eventReceiverClient: er,
		ieeeCache:           make(map[string]string),
	}
}

func TestZCLCommandIDs(t *testing.T) {
	cases := []struct {
		name      string
		msg       *zclv1proto.ZclMessage
		wantOK    bool
		wantClust uint16
		wantCmd   uint8
	}{
		{
			name:   "nil frame returns false",
			msg:    &zclv1proto.ZclMessage{},
			wantOK: false,
		},
		{
			name: "On/Off On",
			msg: &zclv1proto.ZclMessage{
				ClusterId: 0x0006,
				Frame: &zclv1proto.ZclFrame{
					Command: &zclv1proto.ZclCommand{
						Command: &zclv1proto.ZclCommand_GenOnoffOn{},
					},
				},
			},
			wantOK: true, wantClust: 0x0006, wantCmd: 0x01,
		},
		{
			name: "On/Off Off",
			msg: &zclv1proto.ZclMessage{
				ClusterId: 0x0006,
				Frame: &zclv1proto.ZclFrame{
					Command: &zclv1proto.ZclCommand{
						Command: &zclv1proto.ZclCommand_GenOnoffOff{},
					},
				},
			},
			wantOK: true, wantClust: 0x0006, wantCmd: 0x00,
		},
		{
			name: "On/Off Toggle",
			msg: &zclv1proto.ZclMessage{
				ClusterId: 0x0006,
				Frame: &zclv1proto.ZclFrame{
					Command: &zclv1proto.ZclCommand{
						Command: &zclv1proto.ZclCommand_GenOnoffToggle{},
					},
				},
			},
			wantOK: true, wantClust: 0x0006, wantCmd: 0x02,
		},
		{
			name: "LevelControl Step Down",
			msg: &zclv1proto.ZclMessage{
				ClusterId: 0x0008,
				Frame: &zclv1proto.ZclFrame{
					Command: &zclv1proto.ZclCommand{
						Command: &zclv1proto.ZclCommand_GenLevelcontrolStep{
							GenLevelcontrolStep: &zclv1proto.GenLevelControlStep{
								StepMode: zclv1proto.ZclStepMode_ZCL_STEP_MODE_DOWN,
							},
						},
					},
				},
			},
			wantOK: true, wantClust: 0x0008, wantCmd: 0x02,
		},
		{
			name: "unknown command returns false",
			msg: &zclv1proto.ZclMessage{
				ClusterId: 0x0006,
				Frame: &zclv1proto.ZclFrame{
					Command: &zclv1proto.ZclCommand{
						Command: &zclv1proto.ZclCommand_GlobalReportAttributes{},
					},
				},
			},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clust, cmd, ok := zclCommandIDs(tc.msg)
			require.Equal(t, tc.wantOK, ok)
			if ok {
				require.Equal(t, tc.wantClust, clust)
				require.Equal(t, tc.wantCmd, cmd)
			}
		})
	}
}

func TestFindZCLBinding(t *testing.T) {
	ctx := context.Background()

	binding := apiv1.Binding{
		Spec: apiv1.BindingSpec{
			ZCL: &apiv1.ZCLTrigger{
				IEEE:      "0xaabbccddeeff0011",
				ClusterID: 0x0006,
				CommandID: 0x01,
			},
			Condition: "my-zone-on",
		},
	}

	t.Run("match returns condition name", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, &mocks.EventReceiverClientMock{})
		got := r.findZCLBinding(ctx, "0xaabbccddeeff0011", 0x0006, 0x01)
		require.Equal(t, "my-zone-on", got)
	})

	t.Run("wrong ieee returns empty", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, &mocks.EventReceiverClientMock{})
		got := r.findZCLBinding(ctx, "0x0000000000000000", 0x0006, 0x01)
		require.Empty(t, got)
	})

	t.Run("wrong cluster returns empty", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, &mocks.EventReceiverClientMock{})
		got := r.findZCLBinding(ctx, "0xaabbccddeeff0011", 0x0008, 0x01)
		require.Empty(t, got)
	})

	t.Run("wrong command returns empty", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, &mocks.EventReceiverClientMock{})
		got := r.findZCLBinding(ctx, "0xaabbccddeeff0011", 0x0006, 0x00)
		require.Empty(t, got)
	})

	t.Run("nil eventReceiverClient returns empty", func(t *testing.T) {
		r := newTestRouter([]apiv1.Binding{binding}, nil)
		r.eventReceiverClient = nil
		got := r.findZCLBinding(ctx, "0xaabbccddeeff0011", 0x0006, 0x01)
		require.Empty(t, got)
	})

	t.Run("mqtt binding is skipped", func(t *testing.T) {
		mqttBinding := apiv1.Binding{
			Spec: apiv1.BindingSpec{
				MQTT:      &apiv1.MQTTTrigger{Topic: "t", Field: "f", Value: "v"},
				Condition: "some-condition",
			},
		}
		r := newTestRouter([]apiv1.Binding{mqttBinding}, &mocks.EventReceiverClientMock{})
		got := r.findZCLBinding(ctx, "0xaabbccddeeff0011", 0x0006, 0x01)
		require.Empty(t, got)
	})
}
