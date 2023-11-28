package hookreceiver

import (
	"flag"
)

type Config struct{}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
}
