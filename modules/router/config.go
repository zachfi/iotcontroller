package router

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
		util.PrefixConfig(prefix, "concurrency"),
		10,
		"The number of route jobs to run at a time",
	)
}
