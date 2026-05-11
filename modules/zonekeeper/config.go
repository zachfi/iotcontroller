package zonekeeper

import (
	"flag"
	"time"

	"github.com/zachfi/zkit/pkg/util"

	"github.com/zachfi/iotcontroller/internal/common"
)

type Config struct {
	OccupancyTimeout    time.Duration       `yaml:"occupancy_timeout,omitempty"`
	ZigbeeCommandClient common.ClientConfig `yaml:"zigbee_command_client,omitempty"`

	// FlushRefreshAge is the per-device idempotent-flush cache TTL used
	// by zones whose only actuators are lights (BASIC_LIGHT, COLOR_LIGHT).
	// Lighting devices are well-behaved and rarely drift, so this can
	// be relatively long. Default 5 minutes.
	FlushRefreshAge time.Duration `yaml:"flush_refresh_age,omitempty"`

	// FlushRefreshAgeRelay is the cache TTL used for zones that contain
	// at least one DEVICE_TYPE_RELAY (heaters, pumps, fans, smart plugs).
	// Relays are usually controlling something safety-relevant, so we
	// want drift to be corrected faster. Default 60 seconds.
	FlushRefreshAgeRelay time.Duration `yaml:"flush_refresh_age_relay,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&cfg.OccupancyTimeout, util.PrefixConfig(prefix, "occupancy-timeout"), 7*time.Minute, "The time to disable the zone after the last occupancy event")
	f.DurationVar(&cfg.FlushRefreshAge, util.PrefixConfig(prefix, "flush-refresh-age"), 5*time.Minute, "Idempotent-flush cache TTL for zones whose actuators are only lights (BASIC_LIGHT, COLOR_LIGHT).")
	f.DurationVar(&cfg.FlushRefreshAgeRelay, util.PrefixConfig(prefix, "flush-refresh-age-relay"), 60*time.Second, "Idempotent-flush cache TTL for zones containing at least one RELAY (heaters, pumps, smart plugs). Shorter than the lighting default so drift on safety-relevant relays is corrected quickly.")
	cfg.ZigbeeCommandClient.ServerAddress = "" // empty = disabled by default
}
