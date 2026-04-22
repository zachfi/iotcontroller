package conditioner

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// getKubeClient extends fakeKubeClient with Get support for Condition lookups.
type getKubeClient struct {
	byName map[string]apiv1.Condition
}

func (g *getKubeClient) Get(_ context.Context, key kubeclient.ObjectKey, obj kubeclient.Object, _ ...kubeclient.GetOption) error {
	if cond, ok := obj.(*apiv1.Condition); ok {
		if c, found := g.byName[key.Name]; found {
			*cond = c
			return nil
		}
		return apierrors.NewNotFound(schema.GroupResource{Resource: "conditions"}, key.Name)
	}
	panic("unexpected Get type")
}
func (g *getKubeClient) List(_ context.Context, _ kubeclient.ObjectList, _ ...kubeclient.ListOption) error {
	panic("not implemented")
}
func (g *getKubeClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...kubeclient.ApplyOption) error {
	panic("not implemented")
}
func (g *getKubeClient) Create(_ context.Context, _ kubeclient.Object, _ ...kubeclient.CreateOption) error {
	panic("not implemented")
}
func (g *getKubeClient) Delete(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteOption) error {
	panic("not implemented")
}
func (g *getKubeClient) Update(_ context.Context, _ kubeclient.Object, _ ...kubeclient.UpdateOption) error {
	panic("not implemented")
}
func (g *getKubeClient) Patch(_ context.Context, _ kubeclient.Object, _ kubeclient.Patch, _ ...kubeclient.PatchOption) error {
	panic("not implemented")
}
func (g *getKubeClient) DeleteAllOf(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteAllOfOption) error {
	panic("not implemented")
}
func (g *getKubeClient) Status() kubeclient.SubResourceWriter              { panic("not implemented") }
func (g *getKubeClient) SubResource(_ string) kubeclient.SubResourceClient { panic("not implemented") }
func (g *getKubeClient) Scheme() *runtime.Scheme                           { panic("not implemented") }
func (g *getKubeClient) RESTMapper() meta.RESTMapper                       { panic("not implemented") }
func (g *getKubeClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	panic("not implemented")
}
func (g *getKubeClient) IsObjectNamespaced(_ runtime.Object) (bool, error) {
	panic("not implemented")
}

func TestActivateCondition(t *testing.T) {
	ctx := context.Background()
	testLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	makeConditioner := func(conds map[string]apiv1.Condition, zk *recordingZoneKeeper) *Conditioner {
		c, err := New(Config{}, testLogger, zk, &getKubeClient{byName: conds})
		require.NoError(t, err)
		return c
	}

	t.Run("activates remediations for enabled condition", func(t *testing.T) {
		zk := &recordingZoneKeeper{}
		c := makeConditioner(map[string]apiv1.Condition{
			"zone-on": {
				ObjectMeta: metav1.ObjectMeta{Name: "zone-on"},
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Remediations: []apiv1.Remediation{
						{Zone: "living-area", ActiveState: "ZONE_STATE_ON"},
					},
				},
			},
		}, zk)

		_, err := c.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{Condition: "zone-on"})
		require.NoError(t, err)
		require.Equal(t, 1, zk.setStateCount())
		name, state := zk.firstSetState()
		require.Equal(t, "living-area", name)
		require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_ON, state)
	})

	t.Run("disabled condition applies no remediations", func(t *testing.T) {
		zk := &recordingZoneKeeper{}
		c := makeConditioner(map[string]apiv1.Condition{
			"zone-off": {
				ObjectMeta: metav1.ObjectMeta{Name: "zone-off"},
				Spec: apiv1.ConditionSpec{
					Enabled: false,
					Remediations: []apiv1.Remediation{
						{Zone: "living-area", ActiveState: "ZONE_STATE_ON"},
					},
				},
			},
		}, zk)

		_, err := c.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{Condition: "zone-off"})
		require.NoError(t, err)
		require.Equal(t, 0, zk.setStateCount())
	})

	t.Run("missing condition returns error", func(t *testing.T) {
		zk := &recordingZoneKeeper{}
		c := makeConditioner(map[string]apiv1.Condition{}, zk)

		_, err := c.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{Condition: "does-not-exist"})
		require.Error(t, err)
	})

	t.Run("activates scene remediation", func(t *testing.T) {
		zk := &recordingZoneKeeper{}
		c := makeConditioner(map[string]apiv1.Condition{
			"zone-dusk": {
				ObjectMeta: metav1.ObjectMeta{Name: "zone-dusk"},
				Spec: apiv1.ConditionSpec{
					Enabled: true,
					Remediations: []apiv1.Remediation{
						{Zone: "foyer", ActiveScene: "dusk"},
					},
				},
			},
		}, zk)

		_, err := c.ActivateCondition(ctx, &iotv1proto.ActivateConditionRequest{Condition: "zone-dusk"})
		require.NoError(t, err)
		require.Equal(t, 1, zk.setSceneCount())
	})
}
