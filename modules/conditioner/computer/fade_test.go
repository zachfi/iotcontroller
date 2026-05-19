package computer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pkgiot "github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// stubZoneReader returns fixed values for CurrentBrightness /
// CurrentColorTemperatureKelvin so window-mode "from = current" tests
// don't need a real kube client.
type stubZoneReader struct {
	brightness float64
	kelvin     int32
}

func (s *stubZoneReader) CurrentBrightness(_ context.Context, _ string) float64 { return s.brightness }
func (s *stubZoneReader) CurrentColorTemperatureKelvin(_ context.Context, _ string) int32 {
	return s.kelvin
}

func TestFade_ParseArgs_DurationRequired(t *testing.T) {
	_, err := parseFadeArgs(map[string]string{})
	require.Error(t, err, "missing duration must error")
}

func TestFade_ParseArgs_NegativeDurationErrors(t *testing.T) {
	_, err := parseFadeArgs(map[string]string{"duration": "-5s"})
	require.Error(t, err)
}

func TestFade_ParseArgs_DefaultsAnchorToWindow(t *testing.T) {
	p, err := parseFadeArgs(map[string]string{"duration": "1m", "start_at": "12:00"})
	require.NoError(t, err)
	require.Equal(t, "window", p.anchor)
	require.Equal(t, "UTC", p.timezone)
}

func TestFade_ParseArgs_WindowRequiresStartAt(t *testing.T) {
	_, err := parseFadeArgs(map[string]string{"duration": "1m", "anchor": "window"})
	require.Error(t, err, "window mode without start_at must error")
}

func TestFade_ParseArgs_EventModeDoesNotRequireStartAt(t *testing.T) {
	_, err := parseFadeArgs(map[string]string{"duration": "30s", "anchor": "event"})
	require.NoError(t, err)
}

func TestFade_ParseArgs_TerminalStateForms(t *testing.T) {
	for _, s := range []string{"off", "OFF", "ZONE_STATE_OFF"} {
		p, err := parseFadeArgs(map[string]string{
			"duration": "1m", "start_at": "00:00", "terminal_state": s,
		})
		require.NoError(t, err, "terminal_state=%q", s)
		require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, p.terminalState, "terminal_state=%q", s)
	}
	_, err := parseFadeArgs(map[string]string{
		"duration": "1m", "start_at": "00:00", "terminal_state": "bogus",
	})
	require.Error(t, err)
}

func TestFade_ParseArgs_AxesResolveToCanonical(t *testing.T) {
	p, err := parseFadeArgs(map[string]string{
		"duration": "1m", "start_at": "00:00",
		"brightness_from":      "BRIGHTNESS_FULL",
		"brightness_to":        "BRIGHTNESS_VERYLOW",
		"color_temperature_to": "COLOR_TEMPERATURE_EVENING",
	})
	require.NoError(t, err)
	require.Equal(t, 1.0, p.brightnessFrom)
	require.InDelta(t, 70.0/254.0, p.brightnessTo, 1e-9)
	require.Equal(t, int32(2000), p.colorTempToKelvin)
}

func TestFade_Window_BeforeStartAtReturnsEmpty(t *testing.T) {
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{brightness: 1.0}, nil)

	args := map[string]string{
		"_condition":      "living-auto-off",
		"_zone":           "living",
		"duration":        "3m",
		"start_at":        "23:27",
		"timezone":        "UTC",
		"brightness_from": "BRIGHTNESS_FULL",
		"brightness_to":   "BRIGHTNESS_VERYLOW",
		"terminal_state":  "off",
	}
	beforeWindow := time.Date(2026, 5, 19, 23, 26, 30, 0, time.UTC)
	vals, err := f.Compute(context.Background(), beforeWindow, Location{}, args)
	require.NoError(t, err)
	require.Equal(t, ApplyValues{}, vals, "before window: no apply")
	require.Equal(t, 0, store.Len(), "no snapshot seeded before window")
}

func TestFade_Window_FirstTickSeedsSnapshot(t *testing.T) {
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{brightness: 1.0}, nil)

	args := map[string]string{
		"_condition":      "living-auto-off",
		"_zone":           "living",
		"duration":        "3m",
		"start_at":        "23:27",
		"timezone":        "UTC",
		"brightness_from": "BRIGHTNESS_FULL",
		"brightness_to":   "BRIGHTNESS_VERYLOW",
		"terminal_state":  "off",
	}
	// Inside the window at progress ~= 0.
	tickStart := time.Date(2026, 5, 19, 23, 27, 0, 0, time.UTC)
	_, err := f.Compute(context.Background(), tickStart, Location{}, args)
	require.NoError(t, err)

	entry, ok := store.Get("living-auto-off", "living")
	require.True(t, ok, "snapshot seeded on first tick inside window")
	require.Equal(t, tickStart, entry.StartedAt, "startedAt anchored to nominal start, not tick time")
	require.Equal(t, 1.0, entry.BrightnessFrom, "explicit from-value pinned")
}

func TestFade_Window_FromCurrentReadsZoneStatus(t *testing.T) {
	// No explicit brightness_from → read from zone status. The stub
	// reader returns 0.5 (half-brightness).
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{brightness: 0.5}, nil)

	args := map[string]string{
		"_condition":    "living-auto-off",
		"_zone":         "living",
		"duration":      "3m",
		"start_at":      "23:27",
		"timezone":      "UTC",
		"brightness_to": "BRIGHTNESS_VERYLOW",
	}
	tickStart := time.Date(2026, 5, 19, 23, 27, 0, 0, time.UTC)
	_, err := f.Compute(context.Background(), tickStart, Location{}, args)
	require.NoError(t, err)

	entry, _ := store.Get("living-auto-off", "living")
	require.Equal(t, 0.5, entry.BrightnessFrom, "from = current reads zone status")
}

func TestFade_Window_MidwayInterpolates(t *testing.T) {
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{brightness: 1.0}, nil)

	args := map[string]string{
		"_condition":      "living-auto-off",
		"_zone":           "living",
		"duration":        "4m",
		"start_at":        "23:27",
		"timezone":        "UTC",
		"brightness_from": "BRIGHTNESS_FULL",
		"brightness_to":   "BRIGHTNESS_VERYLOW",
	}
	// Halfway through the window.
	halfway := time.Date(2026, 5, 19, 23, 29, 0, 0, time.UTC)
	vals, err := f.Compute(context.Background(), halfway, Location{}, args)
	require.NoError(t, err)

	// Brightness should be halfway between FULL (1.0) and VERYLOW (~0.276).
	want := 0.5 * (1.0 + 70.0/254.0)
	require.InDelta(t, want, vals.BrightnessValue, 1e-9, "midway = mean of endpoints")
	require.NotEqual(t, iotv1proto.Brightness_BRIGHTNESS_UNSPECIFIED, vals.Brightness, "enum set for status writeback")
}

func TestFade_Window_TerminalApplyEmitsStateAndClearsSnapshot(t *testing.T) {
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{brightness: 1.0}, nil)

	args := map[string]string{
		"_condition":      "living-auto-off",
		"_zone":           "living",
		"duration":        "3m",
		"start_at":        "23:27",
		"timezone":        "UTC",
		"brightness_from": "BRIGHTNESS_FULL",
		"brightness_to":   "BRIGHTNESS_VERYLOW",
		"terminal_state":  "off",
	}

	// Seed snapshot via first tick.
	tickStart := time.Date(2026, 5, 19, 23, 27, 0, 0, time.UTC)
	_, _ = f.Compute(context.Background(), tickStart, Location{}, args)
	require.Equal(t, 1, store.Len())

	// At end-of-window (progress = 1.0).
	end := time.Date(2026, 5, 19, 23, 30, 0, 0, time.UTC)
	vals, err := f.Compute(context.Background(), end, Location{}, args)
	require.NoError(t, err)

	require.InDelta(t, pkgiot.BrightnessCanonical(iotv1proto.Brightness_BRIGHTNESS_VERYLOW), vals.BrightnessValue, 1e-9)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, vals.State, "terminal_state emitted at progress=1")
	require.Equal(t, 0, store.Len(), "snapshot cleared after terminal apply")
}

func TestFade_Window_AfterWindowReturnsEmpty(t *testing.T) {
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{brightness: 1.0}, nil)

	args := map[string]string{
		"_condition":      "living-auto-off",
		"_zone":           "living",
		"duration":        "3m",
		"start_at":        "23:27",
		"timezone":        "UTC",
		"brightness_from": "BRIGHTNESS_FULL",
		"brightness_to":   "BRIGHTNESS_VERYLOW",
		"terminal_state":  "off",
	}
	afterWindow := time.Date(2026, 5, 19, 23, 35, 0, 0, time.UTC)
	vals, err := f.Compute(context.Background(), afterWindow, Location{}, args)
	require.NoError(t, err)
	require.Equal(t, ApplyValues{}, vals)
}

func TestFade_Event_WithoutSnapshotReturnsEmpty(t *testing.T) {
	// Event mode without ActivateCondition's seed = no envelope.
	// Eval ticks pass through silently.
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{}, nil)

	args := map[string]string{
		"_condition":     "foyer-motion-off",
		"_zone":          "foyer",
		"duration":       "30s",
		"anchor":         "event",
		"brightness_to":  "BRIGHTNESS_VERYLOW",
		"terminal_state": "off",
	}
	vals, err := f.Compute(context.Background(), time.Now(), Location{}, args)
	require.NoError(t, err)
	require.Equal(t, ApplyValues{}, vals)
}

func TestFade_Event_SeededSnapshotDrivesEnvelope(t *testing.T) {
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{}, nil)

	args := map[string]string{
		"_condition":     "foyer-motion-off",
		"_zone":          "foyer",
		"duration":       "30s",
		"anchor":         "event",
		"brightness_to":  "BRIGHTNESS_VERYLOW",
		"terminal_state": "off",
	}

	// Simulate ActivateCondition seeding the snapshot.
	now := time.Date(2026, 5, 19, 22, 0, 0, 0, time.UTC)
	entry, ok := SeedEventSnapshot(context.Background(),
		&stubZoneReader{brightness: 1.0}, now, "foyer", args)
	require.True(t, ok, "seed produced an entry")
	require.Equal(t, 1.0, entry.BrightnessFrom)
	store.Set("foyer-motion-off", "foyer", entry)

	// Halfway through the fade.
	half := now.Add(15 * time.Second)
	vals, err := f.Compute(context.Background(), half, Location{}, args)
	require.NoError(t, err)
	require.Greater(t, vals.BrightnessValue, 0.0)
	require.Less(t, vals.BrightnessValue, 1.0)

	// At end.
	end := now.Add(30 * time.Second)
	vals, err = f.Compute(context.Background(), end, Location{}, args)
	require.NoError(t, err)
	require.Equal(t, iotv1proto.ZoneState_ZONE_STATE_OFF, vals.State)
	require.Equal(t, 0, store.Len())
}

func TestFade_Event_ReSeedRetriggersFromNewCurrent(t *testing.T) {
	// "Monophonic retrigger": a new ActivateCondition while a fade is
	// in progress overwrites the snapshot, restarting from current.
	store := NewFadeSnapshotStore()
	f := NewFade(store, &stubZoneReader{}, nil)

	args := map[string]string{
		"_condition":     "foyer-motion-off",
		"_zone":          "foyer",
		"duration":       "60s",
		"anchor":         "event",
		"brightness_to":  "BRIGHTNESS_VERYLOW",
		"terminal_state": "off",
	}

	t0 := time.Date(2026, 5, 19, 22, 0, 0, 0, time.UTC)
	entry, _ := SeedEventSnapshot(context.Background(),
		&stubZoneReader{brightness: 1.0}, t0, "foyer", args)
	store.Set("foyer-motion-off", "foyer", entry)

	// Halfway through, motion returns and re-activates.
	t1 := t0.Add(30 * time.Second)
	// Simulate the activate path running SeedEventSnapshot with a
	// stubReader returning the current (mid-fade) brightness 0.65.
	entry2, _ := SeedEventSnapshot(context.Background(),
		&stubZoneReader{brightness: 0.65}, t1, "foyer", args)
	store.Set("foyer-motion-off", "foyer", entry2)

	// Next eval at t1 + 5s should now be running against the re-seeded
	// snapshot (start=t1, from=0.65).
	tick := t1.Add(5 * time.Second)
	vals, err := f.Compute(context.Background(), tick, Location{}, args)
	require.NoError(t, err)

	// 5s into a 60s fade from 0.65 → 0.276 should be ~0.65 - (0.65-0.276)*5/60.
	wantFrom := 0.65
	wantTo := pkgiot.BrightnessCanonical(iotv1proto.Brightness_BRIGHTNESS_VERYLOW)
	wantProgress := 5.0 / 60.0
	want := wantFrom + wantProgress*(wantTo-wantFrom)
	require.InDelta(t, want, vals.BrightnessValue, 1e-9)
}

func TestFade_NilSnapshotStore_NilZoneReader_NoPanic(t *testing.T) {
	// Defensive: a Computer built without dependencies (e.g. a test
	// fixture that doesn't care about snapshots) should not panic on
	// Compute. Returning empty ApplyValues is the safe default.
	f := NewFade(nil, nil, nil)

	args := map[string]string{
		"_condition":    "x",
		"_zone":         "y",
		"duration":      "1m",
		"start_at":      "00:00",
		"brightness_to": "BRIGHTNESS_VERYLOW",
	}
	// Outside any meaningful window since we used 00:00 UTC.
	_, err := f.Compute(context.Background(), time.Now(), Location{}, args)
	require.NoError(t, err, "nil deps must not panic")
}
