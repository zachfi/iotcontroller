package harvester

import (
	"flag"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	RouterClient common.ClientConfig `yaml:"router_address,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.RouterClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "router-client"), f)
}
