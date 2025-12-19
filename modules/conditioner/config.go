package conditioner

import (
	"flag"
	"time"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	ZoneKeeperClient  common.ClientConfig `yaml:"zone_keeper_client,omitempty"`
	TimerLoopInterval time.Duration       `yaml:"timer_loop_interval,omitempty"`
	EpochTimeWindow   time.Duration       `yaml:"epoch_time_window"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.ZoneKeeperClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "zone-keeper-client"), f)

	f.DurationVar(&cfg.TimerLoopInterval, util.PrefixConfig(prefix, "timer-loop-interval"), 15*time.Second, "The interval at which to run the timer loop")
	f.DurationVar(&cfg.EpochTimeWindow, util.PrefixConfig(prefix, "epoch-time-window"), 4*time.Hour, "The wnidow before and after the epoch to indicate that the epoch is considered valid.  A sunrise event left over from the previous day should not be actioned.")
}
