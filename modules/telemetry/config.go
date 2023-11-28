package telemetry

import (
	"flag"

	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	ReportConcurrency uint
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.UintVar(
		&cfg.ReportConcurrency,
		util.PrefixConfig(prefix, "server-address"),
		10,
		"The address of the server to connect to",
	)
}
