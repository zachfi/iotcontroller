package app

import (
	"flag"

	ztrace "github.com/zachfi/zkit/pkg/tracing"
)

type Config struct {
	Target string `yaml:"target"`

	Tracing ztrace.Config `yaml:"tracing,omitempty"`
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Target = All
	f.StringVar(&c.Target, "target", All, "target module")
	c.Tracing.RegisterFlagsAndApplyDefaults("tracing", f)
}
