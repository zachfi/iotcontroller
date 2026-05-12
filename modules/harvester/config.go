package harvester

import (
	"flag"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	RouterClient common.ClientConfig `yaml:"router_client,omitempty"`

	// Concurrency caps the number of in-flight RouteService/Send calls the
	// harvester runs in parallel. The MQTT consumer was originally a single
	// goroutine, which meant a single slow Send stalled every subsequent
	// message — fully visible on startup when the gRPC client was redialing
	// controller-core and the kube-apiserver was warm-handshake-storming.
	// At 7 msg/s with a 340 ms p99 Send latency, a 16-wide fan-out gives
	// ~50× headroom over steady-state; cold-start spikes drain in one window
	// instead of accumulating queue depth.
	Concurrency uint `yaml:"concurrency,omitempty"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.RouterClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "router-client"), f)
	f.UintVar(&cfg.Concurrency, util.PrefixConfig(prefix, "concurrency"), 16, "Maximum number of concurrent RouteService/Send calls in flight from the harvester.")
}
