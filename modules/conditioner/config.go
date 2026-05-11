package conditioner

import (
	"flag"
	"time"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

// Config holds the conditioner module configuration: ZoneKeeper client settings,
// how often the timer loop runs, and the default epoch window when WhenGate
// does not specify a stop time.
type Config struct {
	ZoneKeeperClient       common.ClientConfig `yaml:"zone_keeper_client,omitempty"`
	TimerLoopInterval      time.Duration       `yaml:"timer_loop_interval,omitempty"`
	EpochTimeWindow        time.Duration       `yaml:"epoch_time_window"`
	ApplyDesiredRefreshAge time.Duration       `yaml:"apply_desired_refresh_age,omitempty"`
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
}
