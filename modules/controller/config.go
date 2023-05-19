package controller

import "flag"

type Config struct {
	MetricsAddr          string `yaml:"metrics_addr"`
	ProbeAddr            string `yaml:"probe_addr"`
	EnableLeaderElection bool   `yaml:"enable_leader_election"`
}

func (cfg *Config) RegisterFlags(f *flag.FlagSet) {
	f.StringVar(&cfg.MetricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	f.StringVar(&cfg.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	f.BoolVar(&cfg.EnableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
}
