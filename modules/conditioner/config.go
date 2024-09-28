package conditioner

import (
	"flag"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	ZoneKeeperClient common.ClientConfig `yaml:"zone_keeper_address,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.ZoneKeeperClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "zone-keeper-client"), f)
}
