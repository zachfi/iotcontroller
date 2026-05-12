package computer

import (
	"context"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"
)

// RotateColorsName is the registered name. Conditions reference the
// computer via Remediation.ActiveCompute = RotateColorsName.
const RotateColorsName = "rotate_colors"

// rotateColors cycles a color pool, returning a different `Color` on
// each interval boundary. Sequential mode walks the pool in order;
// random mode appears scrambled but is fully deterministic — the same
// `now` always picks the same color (the same pool position seeds a
// per-tick PRNG).
//
// Args (Remediation.ActiveComputeArgs):
//
//	pool       comma-separated hex colors (e.g. "#FF0000,#00FF00,#0000FF")
//	mode       "sequential" or "random". Defaults to "sequential".
//	interval   Go duration; how long each color stays active.
//	            Defaults to 5s. Minimum 1s — anything shorter than
//	            the eval loop tick can't actually be observed.
//
// Returns ApplyValues with only Color set — other dimensions stay
// untouched, so this computer layers over whatever brightness / CT
// / state another Condition (or button press) established. Pair with
// a Scene that sets `state: COLOR` if you want the rotation to drive
// actual color output rather than just record an unused field.
//
// Subsumes the legacy ZONE_STATE_RANDOMCOLOR semantic, which baked
// "rotate colors at some implicit cadence" into a state enum. After
// rotate_colors is in production for a settling period, the
// RANDOMCOLOR state value can be retired.
type rotateColors struct{}

// hexColorRE matches "#RRGGBB" (case-insensitive, leading '#' required).
// Operators occasionally drop the '#', so we accept either and
// normalize on output.
var hexColorRE = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)

func (rotateColors) Compute(_ context.Context, now time.Time, _ Location, args map[string]string) (ApplyValues, error) {
	poolStr := strings.TrimSpace(args["pool"])
	if poolStr == "" {
		return ApplyValues{}, fmt.Errorf("rotate_colors: args.pool is required")
	}

	mode := strings.TrimSpace(args["mode"])
	if mode == "" {
		mode = "sequential"
	}
	if mode != "sequential" && mode != "random" {
		return ApplyValues{}, fmt.Errorf("rotate_colors: unknown mode %q (want sequential | random)", mode)
	}

	intervalStr := strings.TrimSpace(args["interval"])
	if intervalStr == "" {
		intervalStr = "5s"
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return ApplyValues{}, fmt.Errorf("rotate_colors: parse interval %q: %w", intervalStr, err)
	}
	if interval < time.Second {
		return ApplyValues{}, fmt.Errorf("rotate_colors: interval must be >= 1s, got %s", interval)
	}

	pool, err := parseColorPool(poolStr)
	if err != nil {
		return ApplyValues{}, err
	}
	// parseColorPool guarantees non-empty on err == nil, so len(pool) > 0 below.

	// The "bucket" is `floor(now / interval)`. Same bucket → same
	// color regardless of how many ticks fall into it; bucket
	// boundary changes → new color. This is what makes the computer
	// pure: any caller with the same `now` and `interval` derives
	// the same bucket and thus the same color.
	bucket := now.UnixNano() / interval.Nanoseconds()

	var idx int
	switch mode {
	case "sequential":
		idx = int(bucket % int64(len(pool)))
		if idx < 0 {
			idx += len(pool) // pre-epoch defensive (can't happen in practice)
		}
	case "random":
		// rand.New(rand.NewPCG(seed1, seed2)) is the v2 API. Seed
		// the PRNG from the bucket so the same time always picks
		// the same color. Drawing one IntN value gives us an
		// even distribution over [0, len(pool)).
		r := rand.New(rand.NewPCG(uint64(bucket), 0x9E3779B97F4A7C15)) // golden ratio constant for the second seed
		idx = r.IntN(len(pool))
	}

	return ApplyValues{Color: pool[idx]}, nil
}

// parseColorPool splits a comma-separated list of hex colors, trims
// whitespace, validates each entry, and normalizes to "#RRGGBB" form.
// Returns an error if the pool is empty or any entry doesn't match
// hexColorRE.
func parseColorPool(s string) ([]string, error) {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !hexColorRE.MatchString(p) {
			return nil, fmt.Errorf("rotate_colors: invalid color %q (want #RRGGBB)", p)
		}
		if !strings.HasPrefix(p, "#") {
			p = "#" + p
		}
		out = append(out, strings.ToUpper(p))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rotate_colors: pool has no valid colors")
	}
	return out, nil
}

func init() {
	Register(RotateColorsName, rotateColors{})
}
