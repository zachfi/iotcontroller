package computer

import (
	"sync"
	"time"
)

// FadeSnapshot is the per-voice state the fade Computer keeps for one
// (condition, zone) pair. Seeded when an envelope opens (window-mode
// first eval tick inside the time_intervals window, or event-mode
// ActivateCondition); cleared at terminal apply. Held in
// FadeSnapshotStore so both the Computer (read during eval) and the
// activate path (seed during event-mode gate-on) can reach it.
//
// Zero-value fields are interpreted as "axis not used" — the fade
// Computer only interpolates axes whose snapshot value was populated.
// StartedAt is the time anchor for progress computation; for window
// mode it's the window's nominal start (not the tick time, so a
// delayed eval still ends at the right wall-clock moment).
type FadeSnapshot struct {
	StartedAt              time.Time
	BrightnessFrom         float64 // [0, 1] normalized; 0 = axis not used
	ColorTemperatureKelvin int32   // Kelvin; 0 = axis not used
	ColorFrom              string  // hex; "" = axis not used
}

// FadeSnapshotStore is a thread-safe map of (condition, zone) →
// FadeSnapshot. The conditioner owns one instance; the fade Computer
// reads and clears entries; the activate path seeds them for event-
// anchored fades.
type FadeSnapshotStore struct {
	mu    sync.RWMutex
	store map[fadeSnapshotKey]FadeSnapshot
}

type fadeSnapshotKey struct {
	condition string
	zone      string
}

// NewFadeSnapshotStore returns an empty store.
func NewFadeSnapshotStore() *FadeSnapshotStore {
	return &FadeSnapshotStore{
		store: make(map[fadeSnapshotKey]FadeSnapshot),
	}
}

// Get returns the snapshot for (cond, zone) and whether it exists.
// Treats nil receiver as "no snapshot" so tests can pass nil to
// indicate the store is unwired without forcing a fixture.
func (s *FadeSnapshotStore) Get(condition, zone string) (FadeSnapshot, bool) {
	if s == nil {
		return FadeSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.store[fadeSnapshotKey{condition, zone}]
	return entry, ok
}

// Set stores or replaces the snapshot for (cond, zone). Used by both
// the lazy seed in window mode and the eager seed in
// ActivateCondition for event mode.
func (s *FadeSnapshotStore) Set(condition, zone string, entry FadeSnapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[fadeSnapshotKey{condition, zone}] = entry
}

// Delete removes the snapshot for (cond, zone). Called by the fade
// Computer at terminal apply (progress = 1.0), and by Step 4's
// out-of-band invalidation when something external moves the zone
// mid-fade.
func (s *FadeSnapshotStore) Delete(condition, zone string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, fadeSnapshotKey{condition, zone})
}

// Len returns the number of live snapshots. For diagnostic / metrics use.
func (s *FadeSnapshotStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.store)
}
