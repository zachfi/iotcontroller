package controller

import (
	"flag"

	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	MetricsAddr          string `yaml:"metrics_addr"`
	ProbeAddr            string `yaml:"probe_addr"`
	EnableLeaderElection bool   `yaml:"enable_leader_election"`
	Namespace            string `yaml:"namespace"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.MetricsAddr, util.PrefixConfig(prefix, "metrics-bind-address"), ":8090", "The address the metric endpoint binds to.")
	f.StringVar(&cfg.ProbeAddr, util.PrefixConfig(prefix, "health-probe-bind-address"), ":8091", "The address the probe endpoint binds to.")
	f.StringVar(&cfg.Namespace, util.PrefixConfig(prefix, "namespace"), "iot", "The namespace for this controller to operate within")
	f.BoolVar(&cfg.EnableLeaderElection, util.PrefixConfig(prefix, "leader-elect"), false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
}
