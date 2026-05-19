// Package computer holds the Computer interface and a process-wide
// registry of registered computers. The Conditioner's evaluation loop
// calls Get to resolve a Remediation's `active_compute` to its
// implementation, invokes Compute with the current tick context, and
// applies the returned ApplyValues to the named zone.
//
// Computers are pure functions of (now, location, args). They have no
// state between ticks — anything the computer needs to derive across
// time should be encoded in `args` (e.g. ramp `start_at`/`duration`)
// or recomputed from `now` directly (e.g. rotate_colors picks an index
// from `floor(now / interval) mod len(pool)`). This makes the eval
// loop trivially resumable across pod restarts and removes any
// in-memory state to lose lock discipline over.
package computer

import (
	"context"
	"sync"
	"time"

	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// Location is the configured (lat, lon) used by computers that depend on
// solar position. Conditioner constructs this once from configuration
// and passes it to every Compute call.
type Location struct {
	Lat float64
	Lon float64
}

// ApplyValues is what a Computer produces and the
// ZoneKeeper.ApplyValues RPC accepts. Any field at its zero value
// (UNSPECIFIED enums, empty string) is not applied — the zone keeps
// its current value for that dimension.
//
// In-memory analog of a Scene CR; lets the eval loop apply
// computer-produced values without round-tripping through scene name
// lookup.
type ApplyValues struct {
	State            iotv1proto.ZoneState
	Brightness       iotv1proto.Brightness
	ColorTemperature iotv1proto.ColorTemperature
	Color            string

	// Continuous sibling fields. Populated by Computers that interpolate
	// smoothly (today: fade). When non-zero these override the discrete
	// enum fields at the ZoneKeeper apply layer; the enum is still set
	// to the nearest step for Status reporting and dashboard
	// continuity. See pkg/iot/canonical.go for the enum↔continuous
	// mappings.
	BrightnessValue        float64
	ColorTemperatureKelvin int32
}

// Computer is the contract every active_compute implementation
// satisfies. The receiver is invoked once per evaluation tick (default
// 60 s); it should return quickly and without side effects.
//
// `now` is the tick time. `loc` is the operator-configured location.
// `args` is the Remediation.ActiveComputeArgs map, passed through
// untouched; each computer documents its own keys.
type Computer interface {
	Compute(ctx context.Context, now time.Time, loc Location, args map[string]string) (ApplyValues, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Computer{}
)

// Register makes a Computer available by name. Intended for package
// init() of each computer implementation. Re-registering an existing
// name overwrites — tests can swap stubs in without coordination.
func Register(name string, c Computer) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = c
}

// Get returns the Computer registered under `name`. ok=false when no
// such name is registered (eval loop logs and skips the Remediation).
func Get(name string) (Computer, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

// Names returns the registered computer names in undefined order. For
// diagnostic / startup logging.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}
