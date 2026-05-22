package conditioner

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/grafana/dskit/flagext"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/modules/conditioner/computer"
)

// bridge_test.go — coverage for the eval-loop → reconciler bridge.
//
// The shared assertion shape: after c.evaluate runs, inspect the
// reconciler's per-zone policy via the test-helper hasPolicy(zone) and,
// when the apply path is exercised, the recordingZoneKeeper-style
// ApplyValues capture.

// bridgeTestClient is a kubeclient.Client that serves Condition lists,
// Binding lists, and Scene Gets. Tests pass a list of Conditions to
// drive the eval loop, Bindings for the binding-referenced skip, and a
// map of Scene name → SceneSpec to drive the bridge's active_scene
// resolution.
type bridgeTestClient struct {
	conditions []apiv1.Condition
	bindings   []apiv1.Binding
	scenes     map[string]apiv1.SceneSpec
}

func (b *bridgeTestClient) List(_ context.Context, obj kubeclient.ObjectList, _ ...kubeclient.ListOption) error {
	if cl, ok := obj.(*apiv1.ConditionList); ok {
		cl.Items = append(cl.Items[:0], b.conditions...)
		return nil
	}
	if bl, ok := obj.(*apiv1.BindingList); ok {
		bl.Items = append(bl.Items[:0], b.bindings...)
		return nil
	}
	return nil
}

func (b *bridgeTestClient) Get(_ context.Context, key kubeclient.ObjectKey, obj kubeclient.Object, _ ...kubeclient.GetOption) error {
	if scene, ok := obj.(*apiv1.Scene); ok {
		if spec, ok := b.scenes[key.Name]; ok {
			scene.Spec = spec
			scene.Name = key.Name
			scene.Namespace = key.Namespace
			return nil
		}
		return apierrors.NewNotFound(schema.GroupResource{Resource: "scenes"}, key.Name)
	}
	return apierrors.NewNotFound(schema.GroupResource{Resource: "unknown"}, key.Name)
}

func (b *bridgeTestClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...kubeclient.ApplyOption) error {
	return nil
}
func (b *bridgeTestClient) Create(_ context.Context, _ kubeclient.Object, _ ...kubeclient.CreateOption) error {
	return nil
}
func (b *bridgeTestClient) Delete(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteOption) error {
	return nil
}
func (b *bridgeTestClient) Update(_ context.Context, _ kubeclient.Object, _ ...kubeclient.UpdateOption) error {
	return nil
}
func (b *bridgeTestClient) Patch(_ context.Context, _ kubeclient.Object, _ kubeclient.Patch, _ ...kubeclient.PatchOption) error {
	return nil
}
func (b *bridgeTestClient) DeleteAllOf(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteAllOfOption) error {
	return nil
}
func (b *bridgeTestClient) Status() kubeclient.SubResourceWriter { return noopSubResourceWriter{} }
func (b *bridgeTestClient) SubResource(_ string) kubeclient.SubResourceClient {
	return nil
}
func (b *bridgeTestClient) Scheme() *runtime.Scheme {
	return runtime.NewScheme()
}
func (b *bridgeTestClient) RESTMapper() meta.RESTMapper { return nil }
func (b *bridgeTestClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}
func (b *bridgeTestClient) IsObjectNamespaced(_ runtime.Object) (bool, error) { return true, nil }

// TestBridge_ActiveStateAndScene_PushesPerAxis — a Condition with
// active_state + active_scene on a reconcile-managed zone should:
//
//   - skip the imperative SetState / SetScene path
//   - push one Activation per axis (STATE from active_state,
//     BRIGHTNESS + COLOR_TEMPERATURE from the resolved Scene)
//   - cause the reconciler to ApplyValues once (composed target)
func TestBridge_ActiveStateAndScene_PushesPerAxis(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	cond := apiv1.Condition{
		ObjectMeta: metav1Meta("foo-dusk"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "managed",
				ActiveState:   "on",
				ActiveScene:   "dusk",
				TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
			}},
		},
	}
	kc := &bridgeTestClient{
		conditions: []apiv1.Condition{cond},
		scenes: map[string]apiv1.SceneSpec{
			"dusk": {
				Brightness:       "BRIGHTNESS_DIM",
				ColorTemperature: "COLOR_TEMPERATURE_EVENING",
			},
		},
	}

	zk := &recordingZoneKeeperForReconciler{}
	cfg := Config{ReconcileZones: flagext.StringSliceCSV{"managed"}}
	c, err := New(cfg, logger, zk, kc)
	require.NoError(t, err)

	c.evaluate(ctx)

	require.True(t, c.reconciler.hasPolicy("managed"),
		"bridge should have pushed at least one Activation onto the reconciler")
	require.GreaterOrEqual(t, zk.applyCount(), 1,
		"reconciler should have applied the composed target at least once")

	last := zk.lastApply()
	require.NotNil(t, last)
	require.Equal(t, "managed", last.Name)
	// active_state="on" + scene resolution → ON / DIM / EVENING
	require.Equal(t, "ZONE_STATE_ON", last.State.String())
	require.Equal(t, "BRIGHTNESS_DIM", last.Brightness.String())
	require.Equal(t, "COLOR_TEMPERATURE_EVENING", last.ColorTemperature.String())
}

// TestBridge_OutsideWindow_NoPush — a Remediation whose TimeInterval
// window doesn't cover `now` produces no push. Mirrors the imperative
// path's withinActiveWindow gate.
func TestBridge_OutsideWindow_NoPush(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Zero-width 24:00-24:00 window — never matches.
	never := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "24:00", EndTime: "24:00"}},
	}
	cond := apiv1.Condition{
		ObjectMeta: metav1Meta("foo-never"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "managed",
				ActiveState:   "on",
				TimeIntervals: []apiv1.TimeIntervalSpec{never},
			}},
		},
	}
	kc := &bridgeTestClient{conditions: []apiv1.Condition{cond}}

	zk := &recordingZoneKeeperForReconciler{}
	cfg := Config{ReconcileZones: flagext.StringSliceCSV{"managed"}}
	c, err := New(cfg, logger, zk, kc)
	require.NoError(t, err)

	c.evaluate(ctx)

	require.False(t, c.reconciler.hasPolicy("managed"),
		"outside-window bridge should not push onto the reconciler")
	require.Equal(t, 0, zk.applyCount(),
		"outside-window: no ApplyValues call")
}

// TestBridge_ActiveCompute_PushesToClaimedAxes — an active_compute
// Remediation pushes only to the axes the Computer's output populates.
// Uses a stub Computer that only sets ColorTemperature; the bridge
// should push to AXIS_KIND_COLOR_TEMPERATURE only.
func TestBridge_ActiveCompute_PushesToClaimedAxes(t *testing.T) {
	computer.Register("bridge-stub-ct-only", stubComputer{vals: computer.ApplyValues{
		ColorTemperature:       4000,
		ColorTemperatureKelvin: 4000,
	}})

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cond := apiv1.Condition{
		ObjectMeta: metav1Meta("ct-only-cond"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "managed",
				ActiveCompute: "bridge-stub-ct-only",
			}},
		},
	}
	kc := &bridgeTestClient{conditions: []apiv1.Condition{cond}}

	zk := &recordingZoneKeeperForReconciler{}
	cfg := Config{ReconcileZones: flagext.StringSliceCSV{"managed"}}
	c, err := New(cfg, logger, zk, kc)
	require.NoError(t, err)

	c.evaluate(ctx)

	require.True(t, c.reconciler.hasPolicy("managed"))
	require.GreaterOrEqual(t, zk.applyCount(), 1)
	last := zk.lastApply()
	require.NotNil(t, last)
	// CT was set; State / Brightness / Color should be at zero values.
	require.Equal(t, int32(4000), last.ColorTemperatureKelvin)
	require.Equal(t, "ZONE_STATE_UNSPECIFIED", last.State.String())
	require.Equal(t, "BRIGHTNESS_UNSPECIFIED", last.Brightness.String())
}

// TestBridge_ReBridgeDeduplicates — calling evaluate twice for the
// same Condition + zone refreshes the existing Activation rather than
// stacking a second entry. The reconciler observes one apply on first
// push (delta vs nothing) and zero applies on the second push (target
// unchanged).
func TestBridge_ReBridgeDeduplicates(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	cond := apiv1.Condition{
		ObjectMeta: metav1Meta("dedup-cond"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "managed",
				ActiveState:   "on",
				TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
			}},
		},
	}
	kc := &bridgeTestClient{conditions: []apiv1.Condition{cond}}

	zk := &recordingZoneKeeperForReconciler{}
	cfg := Config{ReconcileZones: flagext.StringSliceCSV{"managed"}}
	c, err := New(cfg, logger, zk, kc)
	require.NoError(t, err)

	c.evaluate(ctx)
	c.evaluate(ctx)

	require.Equal(t, 1, zk.applyCount(),
		"two evaluates with the same target produce one ApplyValues call (cache absorbs the second)")
}

// fakeSchemeBridgeClient is a lightweight wrapper that satisfies the
// kubeclient.Client interface via a real fake client. Useful for tests
// that need both Condition lists AND a Status().Patch path that
// actually persists to the fake's store (so we can read back the Zone
// CR after evaluate).
func newFakeSchemeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	sch := runtime.NewScheme()
	require.NoError(t, apiv1.AddToScheme(sch))
	return fake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(objs...).
		WithStatusSubresource(&apiv1.Zone{}).
		Build()
}

// TestBridge_BindingReferencedCondition_Skipped — Conditions referenced
// by a Binding fire via the Binding-match → ActivateCondition path,
// not the eval loop. The bridge must NOT push for those even when
// their Remediation has TimeIntervals (which act as gates on the
// Binding-driven path). This is the regression test for the relay-
// click ping-pong on foyer's state axis caused by motion-on /
// motion-off Conditions both being eval-loop-bridged.
func TestBridge_BindingReferencedCondition_Skipped(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}

	// One eval-driven Condition (foo-dusk) and one Binding-driven
	// (foo-motion-on). Both target the same zone and the same axis;
	// without the skip, they'd alternate top-of-stack each tick.
	conditions := []apiv1.Condition{
		{
			ObjectMeta: metav1Meta("foo-dusk"),
			Spec: apiv1.ConditionSpec{
				Enabled: true,
				Remediations: []apiv1.Remediation{{
					Zone:          "managed",
					ActiveState:   "on",
					TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
				}},
			},
		},
		{
			ObjectMeta: metav1Meta("foo-motion-on"),
			Spec: apiv1.ConditionSpec{
				Enabled: true,
				Remediations: []apiv1.Remediation{{
					Zone:          "managed",
					ActiveState:   "off", // disagrees with foo-dusk
					TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
				}},
			},
		},
	}
	bindings := []apiv1.Binding{
		{
			ObjectMeta: metav1Meta("motion-binding"),
			Spec: apiv1.BindingSpec{
				Event:     apiv1.EventTrigger{Property: "occupancy", Value: "true"},
				Condition: "foo-motion-on",
			},
		},
	}
	kc := &bridgeTestClient{conditions: conditions, bindings: bindings}

	zk := &recordingZoneKeeperForReconciler{}
	cfg := Config{ReconcileZones: flagext.StringSliceCSV{"managed"}}
	c, err := New(cfg, logger, zk, kc)
	require.NoError(t, err)

	c.evaluate(ctx)
	c.evaluate(ctx)

	// Only foo-dusk should have pushed (state=ON). foo-motion-on is
	// binding-referenced, so the bridge skipped it.
	require.True(t, c.reconciler.hasPolicy("managed"))
	last := zk.lastApply()
	require.NotNil(t, last)
	require.Equal(t, "ZONE_STATE_ON", last.State.String(),
		"top of stack should be foo-dusk's state=on; foo-motion-on should NOT have been bridged")

	// And exactly ONE apply across two evaluate cycles — no ping-pong.
	require.Equal(t, 1, zk.applyCount(),
		"two evaluates with binding-referenced condition skipped produces one apply (cache absorbs the second)")
}

// TestBridge_ImperativeActivate_PushesAsBinding — activateRemediation
// on a reconcile-managed zone routes through bridgeImperativeActivate
// instead of applyDesired. The push carries SOURCE_KIND_BINDING and
// priority bridgeImperativePriority (100) so it composes correctly
// above any time-window background pushes on the same axis.
func TestBridge_ImperativeActivate_PushesAsBinding(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	zoneCR := &apiv1.Zone{}
	zoneCR.Namespace = "iot"
	zoneCR.Name = "managed"

	kc := newFakeSchemeClient(t, zoneCR)
	zk := &recordingZoneKeeperForReconciler{}
	cfg := Config{ReconcileZones: flagext.StringSliceCSV{"managed"}}
	c, err := New(cfg, logger, zk, kc)
	require.NoError(t, err)

	// Call activateRemediation directly (mirrors what ActivateCondition
	// RPC does after kubeClient.Get(condition) returns).
	rem := apiv1.Remediation{
		Zone:        "managed",
		ActiveState: "on",
	}
	require.NoError(t, c.activateRemediation(ctx, "button-press-cond", rem))

	// One ApplyValues call from the immediate reconcile inside
	// PushActivation — NOT through applyDesired (no SetState/SetScene
	// on the recordingZoneKeeperForReconciler).
	require.Equal(t, 1, zk.applyCount(),
		"reconcile-managed activate produces one ApplyValues via the reconciler")

	last := zk.lastApply()
	require.NotNil(t, last)
	require.Equal(t, "ZONE_STATE_ON", last.State.String())

	// Status reflection should show source_kind=BINDING and the
	// configured imperative priority.
	got := &apiv1.Zone{}
	require.NoError(t, kc.Get(ctx, client.ObjectKey{Namespace: "iot", Name: "managed"}, got))
	require.NotEmpty(t, got.Status.ReconcilerStack)
	for _, entry := range got.Status.ReconcilerStack {
		if entry.Axis == "AXIS_KIND_STATE" {
			require.NotNil(t, entry.Top)
			require.Equal(t, "SOURCE_KIND_BINDING", entry.Top.SourceKind)
			require.Equal(t, int32(bridgeImperativePriority), entry.Top.Priority)
			return
		}
	}
	t.Fatalf("expected an AXIS_KIND_STATE entry in ReconcilerStack")
}

// TestBridge_ImperativeWins_TimeWindow — a binding push at priority
// 100 wins over a time-window push at priority 50 on the same axis.
// This is the composition contract: motion overrides background, and
// when the motion entry's TTL expires the background re-asserts.
func TestBridge_ImperativeWins_TimeWindow(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	// Time-window Remediation: state=off
	twCond := apiv1.Condition{
		ObjectMeta: metav1Meta("background-off"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "managed",
				ActiveState:   "off",
				TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
			}},
		},
	}
	// Imperative Remediation: state=on, triggered manually
	bindCond := apiv1.Condition{
		ObjectMeta: metav1Meta("button-on"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:        "managed",
				ActiveState: "on",
			}},
		},
	}
	kc := &bridgeTestClient{conditions: []apiv1.Condition{twCond, bindCond}}

	zk := &recordingZoneKeeperForReconciler{}
	cfg := Config{ReconcileZones: flagext.StringSliceCSV{"managed"}}
	c, err := New(cfg, logger, zk, kc)
	require.NoError(t, err)

	// Step 1: tick the eval loop — pushes background-off as TIME_WINDOW.
	c.evaluate(ctx)
	require.Equal(t, "ZONE_STATE_OFF", zk.lastApply().State.String(),
		"after eval-only tick, background TIME_WINDOW push wins")
	initialApplies := zk.applyCount()

	// Step 2: simulate the binding firing for button-on.
	require.NoError(t, c.activateRemediation(ctx, "button-on", bindCond.Spec.Remediations[0]))

	// Imperative push at priority 100 overrides the time-window push
	// at priority 50. Last apply should be ON.
	require.Greater(t, zk.applyCount(), initialApplies,
		"binding push should cause a new reconcile + apply (target changed)")
	require.Equal(t, "ZONE_STATE_ON", zk.lastApply().State.String(),
		"binding push at priority 100 wins over time-window push at priority 50")
}

// TestBridge_StatusReflectsAfterPush — exercise the full
// bridge → reconciler → reflectStatus chain. The Zone CR's
// Status.ReconcilerStack should populate with the bridge-pushed
// Activations after one evaluate pass.
func TestBridge_StatusReflectsAfterPush(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	zoneCR := &apiv1.Zone{}
	zoneCR.Namespace = "iot"
	zoneCR.Name = "managed"

	sceneCR := &apiv1.Scene{}
	sceneCR.Namespace = "iot"
	sceneCR.Name = "dusk"
	sceneCR.Spec = apiv1.SceneSpec{
		Brightness:       "BRIGHTNESS_DIM",
		ColorTemperature: "COLOR_TEMPERATURE_EVENING",
	}

	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}
	condCR := &apiv1.Condition{}
	condCR.Namespace = "iot"
	condCR.Name = "foo-dusk"
	condCR.Spec = apiv1.ConditionSpec{
		Enabled: true,
		Remediations: []apiv1.Remediation{{
			Zone:          "managed",
			ActiveState:   "on",
			ActiveScene:   "dusk",
			TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
		}},
	}

	kc := newFakeSchemeClient(t, zoneCR, sceneCR, condCR)
	zk := &recordingZoneKeeperForReconciler{}
	cfg := Config{ReconcileZones: flagext.StringSliceCSV{"managed"}}
	c, err := New(cfg, logger, zk, kc)
	require.NoError(t, err)

	c.evaluate(ctx)

	// Read the Zone back and verify ReconcilerStack populated.
	got := &apiv1.Zone{}
	require.NoError(t, kc.Get(ctx, client.ObjectKey{Namespace: "iot", Name: "managed"}, got))

	require.NotEmpty(t, got.Status.ReconcilerStack,
		"Status.ReconcilerStack should reflect bridge-pushed Activations")

	// The bridge should have pushed STATE (from active_state) +
	// BRIGHTNESS + COLOR_TEMPERATURE (from the resolved Scene).
	axes := map[string]apiv1.ReconcilerStackEntry{}
	for _, e := range got.Status.ReconcilerStack {
		axes[e.Axis] = e
	}
	require.Contains(t, axes, "AXIS_KIND_STATE")
	require.Contains(t, axes, "AXIS_KIND_BRIGHTNESS")
	require.Contains(t, axes, "AXIS_KIND_COLOR_TEMPERATURE")

	require.Equal(t, "static", axes["AXIS_KIND_STATE"].Top.Computer)
	require.Equal(t, "SOURCE_KIND_TIME_WINDOW", axes["AXIS_KIND_STATE"].Top.SourceKind)
	require.Equal(t, "foo-dusk", axes["AXIS_KIND_STATE"].Top.SourceName)
	require.Equal(t, int32(bridgePushPriority), axes["AXIS_KIND_STATE"].Top.Priority)

	require.NotNil(t, got.Status.LastReconciledAt)
	require.WithinDuration(t, time.Now(), got.Status.LastReconciledAt.Time, time.Minute)
}
