package harvester

import (
	"flag"

	"github.com/grafana/dskit/backoff"
	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	Backoff      backoff.Config      `yaml:"backoff,omitempty"`
	RouterClient common.ClientConfig `yaml:"router_client,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.RouterClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "router-client"), f)
	cfg.Backoff.RegisterFlagsWithPrefix("mqttclient", f)
}
