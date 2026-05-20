package bindings

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	"github.com/zachfi/iotcontroller/pkg/iot"
	"github.com/zachfi/iotcontroller/pkg/iot/events"
)

// fakeKC returns a fixed BindingList; everything else panics. Same pattern
// the routers use; not exported because tests in this package own the
// matcher contract.
type fakeKC struct{ items []apiv1.Binding }

func (f *fakeKC) List(_ context.Context, list kubeclient.ObjectList, _ ...kubeclient.ListOption) error {
	if bl, ok := list.(*apiv1.BindingList); ok {
		bl.Items = append(bl.Items, f.items...)
	}
	return nil
}
func (f *fakeKC) Get(_ context.Context, _ kubeclient.ObjectKey, _ kubeclient.Object, _ ...kubeclient.GetOption) error {
	panic("not impl")
}
func (f *fakeKC) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...kubeclient.ApplyOption) error {
	panic("not impl")
}
func (f *fakeKC) Create(_ context.Context, _ kubeclient.Object, _ ...kubeclient.CreateOption) error {
	panic("not impl")
}
func (f *fakeKC) Delete(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteOption) error {
	panic("not impl")
}
func (f *fakeKC) Update(_ context.Context, _ kubeclient.Object, _ ...kubeclient.UpdateOption) error {
	panic("not impl")
}
func (f *fakeKC) Patch(_ context.Context, _ kubeclient.Object, _ kubeclient.Patch, _ ...kubeclient.PatchOption) error {
	panic("not impl")
}
func (f *fakeKC) DeleteAllOf(_ context.Context, _ kubeclient.Object, _ ...kubeclient.DeleteAllOfOption) error {
	panic("not impl")
}
func (f *fakeKC) Status() kubeclient.SubResourceWriter              { panic("not impl") }
func (f *fakeKC) SubResource(_ string) kubeclient.SubResourceClient { panic("not impl") }
func (f *fakeKC) Scheme() *runtime.Scheme                           { panic("not impl") }
func (f *fakeKC) RESTMapper() meta.RESTMapper                       { panic("not impl") }
func (f *fakeKC) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	panic("not impl")
}
func (f *fakeKC) IsObjectNamespaced(_ runtime.Object) (bool, error) { panic("not impl") }

func makeMatcher(t *testing.T, items ...apiv1.Binding) *Matcher {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	return New(&fakeKC{items: items}, "iot", logger)
}

// b builds a Binding with the given name, trigger, and condition. Reduces
// table-driven test boilerplate.
func b(name string, trig apiv1.EventTrigger, condition string) apiv1.Binding {
	out := apiv1.Binding{
		Spec: apiv1.BindingSpec{Event: trig, Condition: condition},
	}
	out.Name = name
	return out
}

func dev(name, ieee, dtype, zone string, labels map[string]string) *apiv1.Device {
	d := &apiv1.Device{}
	d.Name = name
	d.Spec.IEEEAddress = ieee
	d.Spec.Type = dtype
	if labels == nil {
		labels = make(map[string]string)
	}
	if zone != "" {
		labels[iot.DeviceZoneLabel] = zone
	}
	d.Labels = labels
	return d
}

// TestPropertyMatch_Exact: a Binding with property+value matches only the
// exact event property+value pair.
func TestPropertyMatch_Exact(t *testing.T) {
	m := makeMatcher(t, b("press-single", apiv1.EventTrigger{
		Property: events.PropertyAction, Value: "single",
	}, "cond-a"))
	d := dev("button-1", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)

	require.Equal(t, "cond-a", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: d,
	}))
	// wrong property
	require.Equal(t, "", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyState, Value: "single", Device: d,
	}))
	// wrong value
	require.Equal(t, "", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "double", Device: d,
	}))
}

// TestPropertyMatch_Values: a Binding with a Values list matches when
// ev.Value is in the list. This is the cross-device-vocabulary case:
// one Binding handles "single" from a single-button remote AND
// "1_single" from a multi-button scene controller.
func TestPropertyMatch_Values(t *testing.T) {
	m := makeMatcher(t, b("press-primary", apiv1.EventTrigger{
		Property: events.PropertyAction,
		Values:   []string{"single", "1_single", "button_1_press"},
	}, "cond-primary"))
	d := dev("btn", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)

	for _, v := range []string{"single", "1_single", "button_1_press"} {
		got := m.FindCondition(context.Background(), events.DeviceEvent{
			Property: events.PropertyAction, Value: v, Device: d,
		})
		require.Equal(t, "cond-primary", got, "value %q must match the list", v)
	}
	// Not in the list — no match.
	got := m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "double", Device: d,
	})
	require.Equal(t, "", got, "value not in Values list should not match")
}

// TestPropertyMatch_ValuesPrecedence: when both Value and Values are
// set, Values wins. (Documented behaviour; preferred over silent
// AND/OR semantics.)
func TestPropertyMatch_ValuesPrecedence(t *testing.T) {
	m := makeMatcher(t, b("trigger", apiv1.EventTrigger{
		Property: events.PropertyAction,
		Value:    "this-is-ignored",
		Values:   []string{"single"},
	}, "cond"))
	d := dev("btn", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)

	require.Equal(t, "cond", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: d,
	}))
	require.Equal(t, "", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "this-is-ignored", Device: d,
	}))
}

// TestPropertyMatch_AnyValue: empty trigger.Value is a wildcard for value;
// property must still match exactly.
func TestPropertyMatch_AnyValue(t *testing.T) {
	m := makeMatcher(t, b("any-action", apiv1.EventTrigger{
		Property: events.PropertyAction, // Value: "" → wildcard
	}, "cond-any"))
	d := dev("button-1", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)

	for _, v := range []string{"single", "double", "hold"} {
		got := m.FindCondition(context.Background(), events.DeviceEvent{
			Property: events.PropertyAction, Value: v, Device: d,
		})
		require.Equal(t, "cond-any", got, "value %s", v)
	}
}

// TestSelector_IEEE: an IEEE-scoped Binding only fires for that device.
func TestSelector_IEEE(t *testing.T) {
	m := makeMatcher(t, b("scoped", apiv1.EventTrigger{
		Property: events.PropertyAction, Value: "single",
		Selector: apiv1.EventSelector{IEEE: "0xaa"},
	}, "cond"))

	matched := dev("d1", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)
	other := dev("d2", "0xbb", "DEVICE_TYPE_BUTTON", "office", nil)

	require.Equal(t, "cond", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: matched,
	}))
	require.Equal(t, "", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: other,
	}))
}

// TestSelector_DeviceType: a fleet binding scoped by DeviceType matches all
// devices of that type.
func TestSelector_DeviceType(t *testing.T) {
	m := makeMatcher(t, b("all-buttons", apiv1.EventTrigger{
		Property: events.PropertyAction, Value: "single",
		Selector: apiv1.EventSelector{DeviceType: "DEVICE_TYPE_BUTTON"},
	}, "fleet-default"))

	btn := dev("btn", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)
	plug := dev("plug", "0xbb", "DEVICE_TYPE_RELAY", "office", nil)

	require.Equal(t, "fleet-default", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: btn,
	}))
	require.Equal(t, "", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: plug,
	}))
}

// TestSelector_Zone: zone label match.
func TestSelector_Zone(t *testing.T) {
	m := makeMatcher(t, b("office-default", apiv1.EventTrigger{
		Property: events.PropertyAction, Value: "single",
		Selector: apiv1.EventSelector{Zone: "office"},
	}, "office-on"))

	office := dev("d1", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)
	library := dev("d2", "0xbb", "DEVICE_TYPE_BUTTON", "library", nil)

	require.Equal(t, "office-on", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: office,
	}))
	require.Equal(t, "", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: library,
	}))
}

// TestSelector_LabelSet: exact-match label set; every key must match.
func TestSelector_LabelSet(t *testing.T) {
	m := makeMatcher(t, b("by-labels", apiv1.EventTrigger{
		Property: events.PropertyAction, Value: "single",
		Selector: apiv1.EventSelector{LabelSelector: map[string]string{
			"floor": "main", "role": "primary",
		}},
	}, "scope-cond"))

	matched := dev("d1", "0xaa", "DEVICE_TYPE_BUTTON", "office", map[string]string{
		"floor": "main", "role": "primary", "extra": "irrelevant",
	})
	missing := dev("d2", "0xbb", "DEVICE_TYPE_BUTTON", "office", map[string]string{
		"floor": "main", // "role" missing
	})

	require.Equal(t, "scope-cond", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: matched,
	}))
	require.Equal(t, "", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: missing,
	}))
}

// TestSpecificity_IEEEBeatsZone: a per-device override (IEEE) wins over a
// zone-wide default. This is the per-device override pattern.
func TestSpecificity_IEEEBeatsZone(t *testing.T) {
	m := makeMatcher(t,
		b("zone-default", apiv1.EventTrigger{
			Property: events.PropertyAction, Value: "single",
			Selector: apiv1.EventSelector{Zone: "office"},
		}, "office-toggle"),
		b("specific-button", apiv1.EventTrigger{
			Property: events.PropertyAction, Value: "single",
			Selector: apiv1.EventSelector{IEEE: "0xaa"},
		}, "specific-cond"),
	)
	d := dev("d1", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)

	require.Equal(t, "specific-cond", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: d,
	}))
}

// TestSpecificity_DeviceBeatsType: device-name match beats device-type match.
func TestSpecificity_DeviceBeatsType(t *testing.T) {
	m := makeMatcher(t,
		b("all-buttons", apiv1.EventTrigger{
			Property: events.PropertyAction, Value: "single",
			Selector: apiv1.EventSelector{DeviceType: "DEVICE_TYPE_BUTTON"},
		}, "fleet"),
		b("specific-name", apiv1.EventTrigger{
			Property: events.PropertyAction, Value: "single",
			Selector: apiv1.EventSelector{Device: "the-one"},
		}, "named"),
	)
	d := dev("the-one", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)

	require.Equal(t, "named", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: d,
	}))
}

// TestSpecificity_TieBreakByName: when two bindings tie on score, the
// alphabetically-first name wins. Determinism matters more than the
// specific choice here.
func TestSpecificity_TieBreakByName(t *testing.T) {
	m := makeMatcher(t,
		b("zzz", apiv1.EventTrigger{
			Property: events.PropertyAction, Value: "single",
			Selector: apiv1.EventSelector{Zone: "office"},
		}, "zzz-cond"),
		b("aaa", apiv1.EventTrigger{
			Property: events.PropertyAction, Value: "single",
			Selector: apiv1.EventSelector{Zone: "office"},
		}, "aaa-cond"),
	)
	d := dev("d", "0xaa", "DEVICE_TYPE_BUTTON", "office", nil)

	require.Equal(t, "aaa-cond", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: d,
	}))
}

// TestEmptySelector_MatchesAny: an empty selector matches every device that
// emitted the event property.
func TestEmptySelector_MatchesAny(t *testing.T) {
	m := makeMatcher(t, b("global-leak", apiv1.EventTrigger{
		Property: events.PropertyWaterLeak, Value: "true",
	}, "alert-leak"))

	for _, d := range []*apiv1.Device{
		dev("a", "0x1", "DEVICE_TYPE_LEAK", "basement", nil),
		dev("b", "0x2", "DEVICE_TYPE_LEAK", "kitchen", nil),
	} {
		require.Equal(t, "alert-leak", m.FindCondition(context.Background(), events.DeviceEvent{
			Property: events.PropertyWaterLeak, Value: "true", Device: d,
		}))
	}
}

// TestNilDevice_NoMatch: a nil device cannot satisfy any selector, so the
// matcher returns "" rather than panicking.
func TestNilDevice_NoMatch(t *testing.T) {
	m := makeMatcher(t, b("any", apiv1.EventTrigger{
		Property: events.PropertyAction, Value: "single",
	}, "cond"))
	require.Equal(t, "", m.FindCondition(context.Background(), events.DeviceEvent{
		Property: events.PropertyAction, Value: "single", Device: nil,
	}))
}

// TestFastPath_IncrementsFiredMetric guards the metric-coverage gap
// caught with the foyer immediate-fire deploy (v0.8.4): Bindings
// without min_duration take the fast path that bypassed
// debounceDispatch, which is the only call site that incremented
// metricBindingDebounced. Operators querying "did this Binding fire
// today?" would conclude "no" for any immediate-fire Binding.
//
// Three FindCondition calls against a fast-path Binding must produce
// three fires on the {binding, outcome="fired"} series. The condition
// return value must also be the Binding's condition (proves the fast
// path's success behaviour is preserved, not just the metric side
// effect).
func TestFastPath_IncrementsFiredMetric(t *testing.T) {
	const bindingName = "fast-path-bind"
	m := makeMatcher(t, b(bindingName, apiv1.EventTrigger{
		Property: events.PropertyOccupancy, Value: "true",
		// no MinDuration → fast path
	}, "cond-evening"))
	d := dev("motion-sensor", "0xff", "DEVICE_TYPE_MOTION", "foyer", nil)

	before := testutil.ToFloat64(metricBindingDebounced.WithLabelValues(bindingName, "fired"))

	for i := 0; i < 3; i++ {
		require.Equal(t, "cond-evening", m.FindCondition(context.Background(), events.DeviceEvent{
			Property: events.PropertyOccupancy, Value: "true", Device: d,
		}))
	}

	after := testutil.ToFloat64(metricBindingDebounced.WithLabelValues(bindingName, "fired"))
	require.Equal(t, before+3, after,
		"fast-path Binding must increment metricBindingDebounced(outcome=fired) on every match")
}
