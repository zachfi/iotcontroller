package conditioner

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
)

// metav1Meta builds a minimal ObjectMeta for fixture Conditions in tests.
// The eval loop reads .Name; everything else can stay zero-valued.
func metav1Meta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: "iot"}
}

// listKubeClient is the eval-loop test analog of getKubeClient — supports
// the List call the evaluator uses. Conditions are returned in the same
// order they're appended to .items.
type listKubeClient struct {
	items []apiv1.Condition
}

func (l *listKubeClient) List(_ context.Context, obj kubeclient.ObjectList, _ ...kubeclient.ListOption) error {
	if cl, ok := obj.(*apiv1.ConditionList); ok {
		cl.Items = append(cl.Items[:0], l.items...)
		return nil
	}
	// The evaluator only Lists ConditionList. Other Lists would be
	// a bug in the eval loop.
	panic("unexpected List type")
}
func (l *listKubeClient) Get(_ context.Context, _ kubeclient.ObjectKey, _ kubeclient.Object, _ ...kubeclient.GetOption) error {
	panic("not implemented")
}
func (l *listKubeClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...kubeclient.ApplyOption) error {
	panic("not implemented")
}
func (l *listKubeClient) Create(_ context.Context, _ kubeclient.Object, _ ...kubeclient.CreateOption) error {
	panic("not implemented")
}
func (l *listKubeClient) Delete(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteOption) error {
	panic("not implemented")
}
func (l *listKubeClient) Update(_ context.Context, _ kubeclient.Object, _ ...kubeclient.UpdateOption) error {
	panic("not implemented")
}
func (l *listKubeClient) Patch(_ context.Context, _ kubeclient.Object, _ kubeclient.Patch, _ ...kubeclient.PatchOption) error {
	panic("not implemented")
}
func (l *listKubeClient) DeleteAllOf(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteAllOfOption) error {
	panic("not implemented")
}
func (l *listKubeClient) Status() kubeclient.SubResourceWriter              { panic("not implemented") }
func (l *listKubeClient) SubResource(_ string) kubeclient.SubResourceClient { panic("not implemented") }
func (l *listKubeClient) Scheme() *runtime.Scheme                           { panic("not implemented") }
func (l *listKubeClient) RESTMapper() meta.RESTMapper                       { panic("not implemented") }
func (l *listKubeClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	panic("not implemented")
}
func (l *listKubeClient) IsObjectNamespaced(_ runtime.Object) (bool, error) {
	panic("not implemented")
}

func TestEvaluate_TimeWindowRemediationApplies(t *testing.T) {
	// A Remediation with time_intervals containing the current weekday
	// and a matching hour-range should fire applyDesired through
	// activateRemediation. The recordingZoneKeeper sees SetState +
	// SetScene calls.
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// A window covering all 24 hours, all weekdays — guaranteed to be
	// active no matter when the test runs.
	allDay := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "00:00", EndTime: "24:00"}},
	}

	zk := &recordingZoneKeeper{}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("zone-night"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "zone-night",
				ActiveState:   "on",
				ActiveScene:   "nightvision",
				TimeIntervals: []apiv1.TimeIntervalSpec{allDay},
			}},
		},
	}}

	c, err := New(Config{}, logger, zk, &listKubeClient{items: conds})
	require.NoError(t, err)

	c.evaluate(ctx)

	require.Equal(t, 1, zk.setStateCount(), "expected SetState within active window")
	require.Equal(t, 1, zk.setSceneCount(), "expected SetScene within active window")
}

func TestEvaluate_TimeWindowOutOfWindowSkips(t *testing.T) {
	// A Remediation whose time_intervals cover a zero-width window
	// outside any reasonable `now` should never fire. Use 24:00-24:00
	// — Prometheus's timeinterval treats "24:00" as end-of-day; the
	// range is empty.
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// "Now" of 12:00 — empty 24:00-24:00 window definitely excludes it.
	never := apiv1.TimeIntervalSpec{
		Times: []apiv1.TimePeriod{{StartTime: "24:00", EndTime: "24:00"}},
	}

	zk := &recordingZoneKeeper{}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("zone-never"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:          "zone-never",
				ActiveState:   "on",
				ActiveScene:   "dusk",
				TimeIntervals: []apiv1.TimeIntervalSpec{never},
			}},
		},
	}}

	c, err := New(Config{}, logger, zk, &listKubeClient{items: conds})
	require.NoError(t, err)

	c.evaluate(ctx)

	require.Equal(t, 0, zk.setStateCount(), "out-of-window Remediation must not SetState")
	require.Equal(t, 0, zk.setSceneCount(), "out-of-window Remediation must not SetScene")
}

func TestEvaluate_NoTimeIntervalsSkipped(t *testing.T) {
	// A Remediation without time_intervals AND without active_compute
	// is only triggered by RPC paths (Alert, ActivateCondition). The
	// eval loop must NOT fire it on tick — otherwise every binding-
	// targeted Condition would fire every minute regardless of intent.
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	zk := &recordingZoneKeeper{}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("zone-rpc-only"),
		Spec: apiv1.ConditionSpec{
			Enabled: true,
			Remediations: []apiv1.Remediation{{
				Zone:        "zone-rpc-only",
				ActiveState: "on",
			}},
		},
	}}

	c, err := New(Config{}, logger, zk, &listKubeClient{items: conds})
	require.NoError(t, err)

	c.evaluate(ctx)

	require.Equal(t, 0, zk.setStateCount(), "RPC-only Remediation must not be eval-loop applied")
}

func TestEvaluate_DeprecatedScheduleWarnedOnce(t *testing.T) {
	// Two ticks against a Condition with Spec.Schedule populated:
	// the warning should land in the deprecatedSchedule set on the
	// first tick and stay there. The recordingZoneKeeper sees no
	// activity (the Condition has no eval-loop-applicable
	// Remediations).
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	zk := &recordingZoneKeeper{}
	conds := []apiv1.Condition{{
		ObjectMeta: metav1Meta("legacy-cron"),
		Spec: apiv1.ConditionSpec{
			Enabled:  true,
			Schedule: "*/10 4 * * *",
			Remediations: []apiv1.Remediation{{
				Zone:        "zone-legacy",
				ActiveState: "off",
			}},
		},
	}}

	c, err := New(Config{}, logger, zk, &listKubeClient{items: conds})
	require.NoError(t, err)

	c.evaluate(ctx)
	c.evaluate(ctx)

	c.deprecatedScheduleMu.Lock()
	tracked := len(c.deprecatedSchedule)
	c.deprecatedScheduleMu.Unlock()
	require.Equal(t, 1, tracked, "Spec.Schedule warning should be tracked exactly once across ticks")
}
