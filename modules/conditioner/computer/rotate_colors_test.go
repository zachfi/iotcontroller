package computer

import (
	"context"
	"testing"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

func TestRotateColors_SequentialWalksInOrder(t *testing.T) {
	c, ok := Get(RotateColorsName)
	if !ok {
		t.Fatalf("computer %q not registered", RotateColorsName)
	}

	args := map[string]string{
		"pool":     "#FF0000,#00FF00,#0000FF",
		"mode":     "sequential",
		"interval": "5s",
	}

	// Start at a known absolute time. The bucket is
	// floor(now.UnixNano() / 5s), so 100s after epoch is bucket 20
	// (idx 20 % 3 = 2 = #0000FF).
	t0 := time.Unix(100, 0)
	want := []string{"#0000FF", "#FF0000", "#00FF00", "#0000FF", "#FF0000"}
	for i, expected := range want {
		now := t0.Add(time.Duration(i) * 5 * time.Second)
		got, err := c.Compute(context.Background(), now, Location{}, args)
		if err != nil {
			t.Fatalf("Compute %d: %v", i, err)
		}
		if got.Color != expected {
			t.Errorf("step %d at %s: color = %q; want %q", i, now, got.Color, expected)
		}
		// Color is the only field this computer should ever set.
		if got.State != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
			t.Errorf("step %d: state set unexpectedly: %s", i, got.State)
		}
		if got.Brightness != iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED {
			t.Errorf("step %d: brightness set unexpectedly: %s", i, got.Brightness)
		}
		if got.ColorTemperature != iotv1proto.ColorTemperature_COLOR_TEMPERATURE_UNSPECIFIED {
			t.Errorf("step %d: color_temperature set unexpectedly: %s", i, got.ColorTemperature)
		}
	}
}

func TestRotateColors_SequentialStableWithinBucket(t *testing.T) {
	// Within one interval bucket every Compute returns the same color.
	c, _ := Get(RotateColorsName)
	args := map[string]string{
		"pool":     "#AA0000,#00AA00",
		"mode":     "sequential",
		"interval": "10s",
	}

	base := time.Unix(1000, 0)
	want := mustCompute(t, c, base, args).Color

	for _, offset := range []time.Duration{0, time.Second, 5 * time.Second, 9 * time.Second} {
		got := mustCompute(t, c, base.Add(offset), args)
		if got.Color != want {
			t.Errorf("offset %s: color = %q; want %q (same bucket should be stable)", offset, got.Color, want)
		}
	}

	// Crossing the bucket boundary picks a different color.
	got := mustCompute(t, c, base.Add(10*time.Second), args)
	if got.Color == want {
		t.Errorf("after bucket boundary: color = %q; should have rotated", got.Color)
	}
}

func TestRotateColors_RandomIsDeterministic(t *testing.T) {
	// Same `now` always picks the same color, even in random mode.
	c, _ := Get(RotateColorsName)
	args := map[string]string{
		"pool":     "#111111,#222222,#333333,#444444,#555555",
		"mode":     "random",
		"interval": "5s",
	}
	now := time.Unix(7777777, 0)

	v1 := mustCompute(t, c, now, args)
	v2 := mustCompute(t, c, now, args)
	if v1.Color != v2.Color {
		t.Errorf("random mode is not deterministic for same `now`: %q vs %q", v1.Color, v2.Color)
	}
}

func TestRotateColors_RandomCoversPool(t *testing.T) {
	// Across many buckets, random mode should eventually hit every
	// pool entry. Not a strict mathematical guarantee, but with a
	// well-seeded PCG and 200 samples over a 3-element pool, missing
	// any entry would indicate a real bug.
	c, _ := Get(RotateColorsName)
	args := map[string]string{
		"pool":     "#FF0000,#00FF00,#0000FF",
		"mode":     "random",
		"interval": "1s",
	}
	hits := map[string]bool{}
	for i := range 200 {
		now := time.Unix(int64(i), 0)
		hits[mustCompute(t, c, now, args).Color] = true
	}
	for _, want := range []string{"#FF0000", "#00FF00", "#0000FF"} {
		if !hits[want] {
			t.Errorf("random mode never picked %q in 200 samples; PRNG seed may be degenerate", want)
		}
	}
}

func TestRotateColors_NormalizesHex(t *testing.T) {
	// Operator wrote colors without '#' and in lowercase; the
	// computer should normalize on output.
	c, _ := Get(RotateColorsName)
	args := map[string]string{
		"pool":     "ff0000,00ff00",
		"mode":     "sequential",
		"interval": "5s",
	}
	got := mustCompute(t, c, time.Unix(0, 0), args)
	if got.Color != "#FF0000" && got.Color != "#00FF00" {
		t.Errorf("expected normalized #RRGGBB, got %q", got.Color)
	}
}

func TestRotateColors_DefaultsMatchSpec(t *testing.T) {
	// Mode defaults to sequential; interval defaults to 5s. Smoke
	// test that omitting them works rather than erroring.
	c, _ := Get(RotateColorsName)
	args := map[string]string{
		"pool": "#AB1234",
	}
	got, err := c.Compute(context.Background(), time.Unix(100, 0), Location{}, args)
	if err != nil {
		t.Fatalf("Compute with defaults: %v", err)
	}
	if got.Color != "#AB1234" {
		t.Errorf("single-color pool should always return that color; got %q", got.Color)
	}
}

func TestRotateColors_ArgErrors(t *testing.T) {
	c, _ := Get(RotateColorsName)
	cases := []struct {
		name string
		args map[string]string
	}{
		{"missing pool", map[string]string{}},
		{"empty pool string", map[string]string{"pool": "   "}},
		{"all entries empty", map[string]string{"pool": ", , ,"}},
		{"bad hex", map[string]string{"pool": "#FF0000,#GGGGGG"}},
		{"3-char hex (not supported)", map[string]string{"pool": "#F00"}},
		{"bad mode", map[string]string{"pool": "#FF0000", "mode": "weighted"}},
		{"bad interval", map[string]string{"pool": "#FF0000", "interval": "nope"}},
		{"interval too small", map[string]string{"pool": "#FF0000", "interval": "100ms"}},
		{"zero interval", map[string]string{"pool": "#FF0000", "interval": "0s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Compute(context.Background(), time.Unix(0, 0), Location{}, tc.args)
			if err == nil {
				t.Errorf("expected error for %s; got nil", tc.name)
			}
		})
	}
}

func TestRotateColors_RegistryEntry(t *testing.T) {
	if _, ok := Get(RotateColorsName); !ok {
		t.Errorf("rotate_colors not registered")
	}
	var found bool
	for _, n := range Names() {
		if n == RotateColorsName {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() does not contain %q", RotateColorsName)
	}
}

func mustCompute(t *testing.T, c Computer, now time.Time, args map[string]string) ApplyValues {
	t.Helper()
	v, err := c.Compute(context.Background(), now, Location{}, args)
	if err != nil {
		t.Fatalf("Compute(%s, %v): %v", now, args, err)
	}
	return v
}
