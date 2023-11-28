package client

import (
	"flag"

	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	ServerAddress string `yaml:"server_address,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(
		&cfg.ServerAddress,
		util.PrefixConfig(prefix, "server-address"),
		":9090",
		"The address of the server to connect to",
	)
}
