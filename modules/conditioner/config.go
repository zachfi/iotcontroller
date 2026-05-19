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

	// OOBGracePeriod is the minimum age a cache entry must reach before
	// the out-of-band Status drift check fires. Below this threshold,
	// stale Status reflects apiserver propagation lag from the
	// conditioner's own most-recent apply, not an external mover.
	// Tests set 0 to disable the grace and observe drift detection
	// directly. Production default: 2 seconds.
	OOBGracePeriod time.Duration `yaml:"oob_grace_period,omitempty"`

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

	// Query configures the `query` Computer (Prometheus pull). The
	// computer is registered only when Query.Endpoint is non-empty, so
	// deployments that don't want PromQL-driven Conditions don't have
	// to think about this block. One endpoint+tenant per pod by design:
	// the deployment_tools knob picks the Mimir / Prometheus the
	// operator scrapes from, and every `active_compute: query`
	// Condition pulls against it.
	Query QueryConfig `yaml:"query,omitempty"`
}

// LocationConfig is the operator-configured (lat, lon).
type LocationConfig struct {
	Lat float64 `yaml:"lat,omitempty"`
	Lon float64 `yaml:"lon,omitempty"`
}

// QueryConfig is the conditioner-level state the `query` Computer needs
// to issue PromQL HTTP requests. Endpoint + Tenant come from operator
// flags; AuthTokenEnvVar names a pod env var (typically populated via
// secretKeyRef) whose value is read at controller startup and sent as
// a Bearer token on every request. Empty AuthTokenEnvVar = no Authorization
// header; suitable for in-cluster Prometheus that lives behind a service
// account proxy or has no auth at all.
type QueryConfig struct {
	Endpoint        string        `yaml:"endpoint,omitempty"`
	Tenant          string        `yaml:"tenant,omitempty"`
	Timeout         time.Duration `yaml:"timeout,omitempty"`
	AuthTokenEnvVar string        `yaml:"auth_token_env_var,omitempty"`
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
	f.DurationVar(&cfg.OOBGracePeriod, util.PrefixConfig(prefix, "oob-grace-period"), 2*time.Second, "Minimum age a cache entry must reach before applyDesired inspects Zone CR Status for out-of-band drift. Inside this window, stale Status reflects apiserver propagation from our own apply rather than an external mover.")

	f.DurationVar(&cfg.EvaluationInterval, util.PrefixConfig(prefix, "evaluation-interval"), 60*time.Second, "Tick interval for the periodic evaluator (computer-driven Remediations + alert-window closure).")
	f.Float64Var(&cfg.Location.Lat, util.PrefixConfig(prefix, "location.lat"), 0, "Latitude for solar calculations (sun_color_temperature computer, SunRelative time intervals).")
	f.Float64Var(&cfg.Location.Lon, util.PrefixConfig(prefix, "location.lon"), 0, "Longitude for solar calculations.")

	f.StringVar(&cfg.Query.Endpoint, util.PrefixConfig(prefix, "query.endpoint"), "", "Prometheus/Mimir endpoint for the `query` Computer. Empty disables `query` registration entirely.")
	f.StringVar(&cfg.Query.Tenant, util.PrefixConfig(prefix, "query.tenant"), "", "X-Scope-OrgID for Mimir multi-tenancy. Empty omits the header.")
	f.DurationVar(&cfg.Query.Timeout, util.PrefixConfig(prefix, "query.timeout"), 5*time.Second, "HTTP timeout for `query` Computer PromQL requests.")
	f.StringVar(&cfg.Query.AuthTokenEnvVar, util.PrefixConfig(prefix, "query.auth-token-env-var"), "", "Name of an env var (typically populated via secretKeyRef) whose value is sent as a Bearer token on `query` requests. Empty = no Authorization header.")
}
