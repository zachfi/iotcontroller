package zonekeeper

import (
	"flag"
	"time"

	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	OccupancyTimeout time.Duration `yaml:"occupancy_timeout,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&cfg.OccupancyTimeout, util.PrefixConfig(prefix, "occupancy-timeout"), 7*time.Minute, "The time to disable the zone after the last occupancy event")
}
