package hookreceiver

import (
	"flag"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	EventReceiverClient common.ClientConfig `yaml:"event_receiver_client,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.EventReceiverClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "event-receiver-client"), f)
}
