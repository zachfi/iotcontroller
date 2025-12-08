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
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.ZoneKeeperClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "zone-keeper-client"), f)

	f.DurationVar(&cfg.TimerLoopInterval, util.PrefixConfig(prefix, "timer-loop-interval"), 15*time.Second, "The interval at which to run the timer loop")
}
