package computer

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	kubeclient "sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/zachfi/iotcontroller/api/v1"
	pkgiot "github.com/zachfi/iotcontroller/pkg/iot"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// FadeName is the registered name for the fade Computer.
const FadeName = "fade"

// FadeZoneReader exposes the slice of Zone CR state the fade Computer
// needs at seed time when an axis is configured with "_to" but no
// explicit "_from": the operator's "from = current" semantics. The
// implementation lives in the conditioner; the interface is here so
// the package doesn't import its parent.
type FadeZoneReader interface {
	// CurrentBrightness returns the Zone's current normalized
	// brightness, or 0 if unknown / not set. Reads from the kube
	// informer cache.
	CurrentBrightness(ctx context.Context, zone string) float64
	// CurrentColorTemperatureKelvin returns the Zone's current CT in
	// Kelvin, or 0 if unknown.
	CurrentColorTemperatureKelvin(ctx context.Context, zone string) int32
}

// kubeFadeZoneReader is the production implementation: reads
// Zone CR Status from the kube informer cache and converts the enum
// values to canonical continuous values. Conditioner constructs one
// at startup with its own kube client.
type kubeFadeZoneReader struct {
	kc        kubeclient.Client
	namespace string
}

// NewKubeFadeZoneReader returns a FadeZoneReader backed by a kube
// client. Conditioner wires this when registering fade.
func NewKubeFadeZoneReader(kc kubeclient.Client, namespace string) FadeZoneReader {
	return &kubeFadeZoneReader{kc: kc, namespace: namespace}
}

func (r *kubeFadeZoneReader) CurrentBrightness(ctx context.Context, zone string) float64 {
	var z apiv1.Zone
	if err := r.kc.Get(ctx, kubeclient.ObjectKey{Name: zone, Namespace: r.namespace}, &z); err != nil {
		return 0
	}
	if z.Status.Brightness == "" {
		return 0
	}
	enum := iotv1proto.Brightness(iotv1proto.Brightness_value[z.Status.Brightness])
	return pkgiot.BrightnessCanonical(enum)
}

func (r *kubeFadeZoneReader) CurrentColorTemperatureKelvin(ctx context.Context, zone string) int32 {
	var z apiv1.Zone
	if err := r.kc.Get(ctx, kubeclient.ObjectKey{Name: zone, Namespace: r.namespace}, &z); err != nil {
		return 0
	}
	if z.Status.ColorTemperature == "" {
		return 0
	}
	enum := iotv1proto.ColorTemperature(iotv1proto.ColorTemperature_value[z.Status.ColorTemperature])
	return pkgiot.ColorTempCanonical(enum)
}

// fade implements the unified attack/sustain/release envelope from
// docs/fade-design.md. Two anchor modes:
//
//   - window: t=0 is today's start_at in the configured timezone.
//     Snapshot is seeded lazily on the first eval tick where now is
//     inside the time_intervals window. Per-day re-seed (the next
//     window-open after a terminal apply creates a fresh entry).
//   - event:  t=0 is the time the snapshot was seeded by
//     ActivateCondition (handled outside this Computer; we just read
//     the existing entry). Without a seed, the Computer returns empty.
//
// Axes: brightness (continuous in [0,1]) and color_temperature
// (Kelvin) interpolate on the same progress curve. Operator omits
// the axes they don't want to touch. Color is reserved for a
// follow-up (needs sRGB-decoded linear lerp).
type fade struct {
	snapshots *FadeSnapshotStore
	zones     FadeZoneReader
	logger    *slog.Logger
}

// NewFade returns a fade Computer wired to a snapshot store and a
// zone reader. Conditioner calls this from its startup wiring.
// Logger is optional; nil is fine and produces a no-op logger.
func NewFade(snapshots *FadeSnapshotStore, zones FadeZoneReader, logger *slog.Logger) Computer {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(nopWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return &fade{snapshots: snapshots, zones: zones, logger: logger}
}

// nopWriter discards everything. Used when a fade Computer is
// constructed without a logger (test fixtures).
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// SeedEventSnapshot builds an event-mode FadeSnapshot from a
// Remediation's args, resolving any "from = current" axes against
// the named zone's Status at seed time. Pure helper exported so the
// ActivateCondition handler in the conditioner can call it without
// re-deriving the arg parsing. Returns ok=false when the
// Remediation's args don't describe an event-anchored fade (wrong
// computer name, wrong anchor, or unparseable args) — caller treats
// that as "no seed needed".
//
// The returned snapshot's StartedAt is set to `now`, matching the
// "event anchor = activate time" contract from docs/fade-design.md.
func SeedEventSnapshot(ctx context.Context, zones FadeZoneReader, now time.Time, zone string, args map[string]string) (FadeSnapshot, bool) {
	p, err := parseFadeArgs(args)
	if err != nil || p.anchor != "event" {
		return FadeSnapshot{}, false
	}
	entry := resolveFromValues(ctx, zones, zone, p)
	entry.StartedAt = now
	return entry, true
}

func (f *fade) Compute(ctx context.Context, now time.Time, _ Location, args map[string]string) (ApplyValues, error) {
	p, err := parseFadeArgs(args)
	if err != nil {
		return ApplyValues{}, err
	}

	cond := args["_condition"]
	zone := args["_zone"]

	switch p.anchor {
	case "window":
		return f.computeWindow(ctx, now, cond, zone, p)
	case "event":
		return f.computeEvent(ctx, now, cond, zone, p)
	default:
		return ApplyValues{}, fmt.Errorf("fade: unknown anchor %q (want window | event)", p.anchor)
	}
}

func (f *fade) computeWindow(ctx context.Context, now time.Time, cond, zone string, p fadeParams) (ApplyValues, error) {
	loc, err := time.LoadLocation(p.timezone)
	if err != nil {
		return ApplyValues{}, fmt.Errorf("fade: load timezone %q: %w", p.timezone, err)
	}
	hh, mm, err := parseHHMM(p.startAt)
	if err != nil {
		return ApplyValues{}, fmt.Errorf("fade: parse start_at %q: %w", p.startAt, err)
	}
	nowLocal := now.In(loc)
	startToday := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), hh, mm, 0, 0, loc)
	end := startToday.Add(p.duration)

	if now.Before(startToday) || now.After(end) {
		// Outside the window. If we left a snapshot from yesterday's
		// window (terminal apply cleaned it up; this is the safety
		// net), drop it.
		if cond != "" && zone != "" {
			f.snapshots.Delete(cond, zone)
		}
		return ApplyValues{}, nil
	}

	// Inside the window. Seed-or-fetch the snapshot.
	entry, ok := f.snapshots.Get(cond, zone)
	if !ok {
		entry = resolveFromValues(ctx, f.zones, zone, p)
		entry.StartedAt = startToday
		f.snapshots.Set(cond, zone, entry)
	}

	return f.interpolate(p, entry, now, cond, zone), nil
}

func (f *fade) computeEvent(_ context.Context, now time.Time, cond, zone string, p fadeParams) (ApplyValues, error) {
	if cond == "" || zone == "" {
		return ApplyValues{}, nil
	}
	entry, ok := f.snapshots.Get(cond, zone)
	if !ok {
		// Event-mode fades only run after ActivateCondition has seeded
		// the snapshot. No seed = no envelope in progress.
		return ApplyValues{}, nil
	}
	return f.interpolate(p, entry, now, cond, zone), nil
}

// interpolate computes the apply values for one eval tick. Common
// path used by both anchor modes once a snapshot exists.
func (f *fade) interpolate(p fadeParams, entry FadeSnapshot, now time.Time, cond, zone string) ApplyValues {
	progress := float64(now.Sub(entry.StartedAt)) / float64(p.duration)
	if progress < 0 {
		return ApplyValues{}
	}
	if progress > 1 {
		progress = 1
	}

	var vals ApplyValues

	// Brightness axis
	if p.brightnessTo != 0 {
		from := p.brightnessFrom
		if from == 0 {
			from = entry.BrightnessFrom
		}
		v := from + progress*(p.brightnessTo-from)
		vals.BrightnessValue = v
		vals.Brightness = pkgiot.BrightnessNearestEnum(v)
	}

	// Color temperature axis
	if p.colorTempToKelvin != 0 {
		from := int32(p.colorTempFromKelvin)
		if from == 0 {
			from = entry.ColorTemperatureKelvin
		}
		k := int32(math.Round(float64(from) + progress*float64(p.colorTempToKelvin-from)))
		vals.ColorTemperatureKelvin = k
		vals.ColorTemperature = pkgiot.ColorTempNearestEnum(k)
	}

	// Terminal state + cleanup at progress = 1
	if progress >= 1 {
		if p.terminalState != iotv1proto.ZoneState_ZONE_STATE_UNSPECIFIED {
			vals.State = p.terminalState
		}
		if cond != "" && zone != "" {
			f.snapshots.Delete(cond, zone)
		}
	}

	return vals
}

// fadeParams holds the parsed-and-resolved fade Computer arguments.
// Keeps parsing centralized so SeedEventSnapshot and Compute share
// the same validation rules.
type fadeParams struct {
	anchor   string
	duration time.Duration
	startAt  string
	timezone string

	brightnessTo   float64 // 0 = axis unused
	brightnessFrom float64 // 0 = use snapshot/current

	colorTempToKelvin   int32 // 0 = axis unused
	colorTempFromKelvin int32 // 0 = use snapshot/current

	terminalState iotv1proto.ZoneState
}

// parseFadeArgs validates and normalizes args. Returns an error on
// missing duration or unparseable values; the eval loop logs and
// skips the Remediation.
func parseFadeArgs(args map[string]string) (fadeParams, error) {
	var p fadeParams

	anchor := strings.TrimSpace(args["anchor"])
	if anchor == "" {
		anchor = "window"
	}
	p.anchor = anchor

	durationStr := strings.TrimSpace(args["duration"])
	if durationStr == "" {
		return p, fmt.Errorf("fade: args.duration is required")
	}
	d, err := time.ParseDuration(durationStr)
	if err != nil {
		return p, fmt.Errorf("fade: parse duration %q: %w", durationStr, err)
	}
	if d <= 0 {
		return p, fmt.Errorf("fade: duration must be > 0, got %s", d)
	}
	p.duration = d

	if p.anchor == "window" {
		p.startAt = strings.TrimSpace(args["start_at"])
		if p.startAt == "" {
			return p, fmt.Errorf("fade: args.start_at is required when anchor=window")
		}
		p.timezone = strings.TrimSpace(args["timezone"])
		if p.timezone == "" {
			p.timezone = "UTC"
		}
	}

	if s, ok := args["brightness_to"]; ok && s != "" {
		enum, ok := parseBrightness(s)
		if !ok {
			return p, fmt.Errorf("fade: unknown brightness_to %q", s)
		}
		p.brightnessTo = pkgiot.BrightnessCanonical(enum)
	}
	if s, ok := args["brightness_from"]; ok && s != "" {
		enum, ok := parseBrightness(s)
		if !ok {
			return p, fmt.Errorf("fade: unknown brightness_from %q", s)
		}
		p.brightnessFrom = pkgiot.BrightnessCanonical(enum)
	}

	if s, ok := args["color_temperature_to"]; ok && s != "" {
		enum, ok := parseColorTemp(s)
		if !ok {
			return p, fmt.Errorf("fade: unknown color_temperature_to %q", s)
		}
		p.colorTempToKelvin = pkgiot.ColorTempCanonical(enum)
	}
	if s, ok := args["color_temperature_from"]; ok && s != "" {
		enum, ok := parseColorTemp(s)
		if !ok {
			return p, fmt.Errorf("fade: unknown color_temperature_from %q", s)
		}
		p.colorTempFromKelvin = pkgiot.ColorTempCanonical(enum)
	}

	if s, ok := args["terminal_state"]; ok && s != "" {
		s = strings.TrimSpace(s)
		switch strings.ToLower(s) {
		case "off":
			p.terminalState = iotv1proto.ZoneState_ZONE_STATE_OFF
		case "on":
			p.terminalState = iotv1proto.ZoneState_ZONE_STATE_ON
		case "offtimer":
			p.terminalState = iotv1proto.ZoneState_ZONE_STATE_OFFTIMER
		default:
			// Accept the exact proto enum name as well, mirroring the
			// existing parseBrightness ergonomics.
			if v, ok := iotv1proto.ZoneState_value[s]; ok {
				p.terminalState = iotv1proto.ZoneState(v)
			} else {
				return p, fmt.Errorf("fade: unknown terminal_state %q", s)
			}
		}
	}

	return p, nil
}

// resolveFromValues fills in the "from" axes for an envelope at seed
// time. When the operator didn't pin a from-value, read the zone's
// current Status. Used by both window-mode lazy seed and event-mode
// eager seed in ActivateCondition.
//
// Explicit operator-supplied from-values override the zone read,
// matching the doc's "from: <enum> wins over from: current" semantics.
func resolveFromValues(ctx context.Context, zones FadeZoneReader, zone string, p fadeParams) FadeSnapshot {
	entry := FadeSnapshot{}
	if p.brightnessTo != 0 {
		if p.brightnessFrom != 0 {
			entry.BrightnessFrom = p.brightnessFrom
		} else if zones != nil {
			entry.BrightnessFrom = zones.CurrentBrightness(ctx, zone)
		}
	}
	if p.colorTempToKelvin != 0 {
		if p.colorTempFromKelvin != 0 {
			entry.ColorTemperatureKelvin = p.colorTempFromKelvin
		} else if zones != nil {
			entry.ColorTemperatureKelvin = zones.CurrentColorTemperatureKelvin(ctx, zone)
		}
	}
	return entry
}
