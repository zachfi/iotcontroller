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
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.DurationVar(&cfg.OccupancyTimeout, util.PrefixConfig(prefix, "occupancy-timeout"), 7*time.Minute, "The time to disable the zone after the last occupancy event")
	cfg.ZigbeeCommandClient.ServerAddress = "" // empty = disabled by default
}
