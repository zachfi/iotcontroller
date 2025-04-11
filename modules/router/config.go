package router

import (
	"flag"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	ReportConcurrency uint
	ZoneKeeperClient  common.ClientConfig `yaml:"zone_keeper_client,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.UintVar(&cfg.ReportConcurrency, util.PrefixConfig(prefix, "concurrency"), 100, "The number of route jobs to run at a time")
	cfg.ZoneKeeperClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "zone-keeper-client"), f)
}
