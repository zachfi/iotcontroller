package harvester

import (
	"flag"
)

type Config struct{}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	// f.StringVar(
	// 	&cfg.ServerAddress,
	// 	util.PrefixConfig(prefix, "server-address"),
	// 	":9090",
	// 	"The address of the server to connect to",
	// )
}
