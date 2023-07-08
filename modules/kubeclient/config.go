package kubeclient

import (
	"flag"

	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	KubeConfig string `yaml:"kube_config"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(
		&cfg.KubeConfig,
		util.PrefixConfig(prefix, "kube-config"),
		"",
		"The path to the kubeconfig on disk",
	)
}
