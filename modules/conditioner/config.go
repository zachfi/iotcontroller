package conditioner

import (
	"flag"
	"time"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

// Config holds the conditioner module configuration: ZoneKeeper client settings,
// how often the timer loop runs, the default epoch window when WhenGate
// does not specify a stop time, and the eval-loop interval + location used
// by computer-driven Remediations.
type Config struct {
	ZoneKeeperClient       common.ClientConfig `yaml:"zone_keeper_client,omitempty"`
	TimerLoopInterval      time.Duration       `yaml:"timer_loop_interval,omitempty"`
	EpochTimeWindow        time.Duration       `yaml:"epoch_time_window"`
	ApplyDesiredRefreshAge time.Duration       `yaml:"apply_desired_refresh_age,omitempty"`

	// EvaluationInterval is the tick interval for the periodic evaluator.
	// On each tick the evaluator walks enabled Conditions and applies
	// computer-driven Remediations (those with active_compute set) and
	// closes any alert-driven Remediations whose TimeIntervals window has
	// expired. The eval loop does not sit on the motion / button hot path:
	// Binding-driven ActivateCondition and the Alert RPC still apply
	// immediately, regardless of this interval.
	EvaluationInterval time.Duration `yaml:"evaluation_interval,omitempty"`

	// Location is the (lat, lon) used by computers that depend on solar
	// position and by SunRelative time-interval windows. A single value
	// is shared across the conditioner. Plumbed from `znet.location` in
	// deployment_tools.
	Location LocationConfig `yaml:"location,omitempty"`
}

// LocationConfig is the operator-configured (lat, lon).
type LocationConfig struct {
	Lat float64 `yaml:"lat,omitempty"`
	Lon float64 `yaml:"lon,omitempty"`
}

// RegisterFlagsAndApplyDefaults adds conditioner flags to f and sets defaults.
// EpochTimeWindow is used when a remediation's WhenGate.Stop is empty (window
// extends that long after the epoch event).
func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.ZoneKeeperClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "zone-keeper-client"), f)

	f.DurationVar(&cfg.TimerLoopInterval, util.PrefixConfig(prefix, "timer-loop-interval"), 15*time.Second, "The interval at which to run the timer loop")
	f.DurationVar(&cfg.EpochTimeWindow, util.PrefixConfig(prefix, "epoch-time-window"), 4*time.Hour, "The window before and after the epoch to indicate that the epoch is considered valid. A sunrise event left over from the previous day should not be actioned.")
	// applyDesired suppresses repeated activate/deactivate calls when the
	// desired (state, scene) for a (condition, zone) pair has not changed
	// since the last successful apply. Refresh age is the TTL after which
	// the cache forces a re-apply to absorb drift (e.g. someone toggled
	// the zone externally). 30 minutes is conservative for the alert /
	// epoch / cron cadences we drive.
	f.DurationVar(&cfg.ApplyDesiredRefreshAge, util.PrefixConfig(prefix, "apply-desired-refresh-age"), 30*time.Minute, "Cache TTL for applyDesired: a (condition, zone) entry forces a re-apply after this duration even if desired matches last applied.")

	f.DurationVar(&cfg.EvaluationInterval, util.PrefixConfig(prefix, "evaluation-interval"), 60*time.Second, "Tick interval for the periodic evaluator (computer-driven Remediations + alert-window closure).")
	f.Float64Var(&cfg.Location.Lat, util.PrefixConfig(prefix, "location.lat"), 0, "Latitude for solar calculations (sun_color_temperature computer, SunRelative time intervals).")
	f.Float64Var(&cfg.Location.Lon, util.PrefixConfig(prefix, "location.lon"), 0, "Longitude for solar calculations.")
}
