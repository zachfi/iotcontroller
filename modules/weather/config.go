package weather

import (
	"flag"
	"time"

	"github.com/zachfi/iotcontroller/internal/common"
	"github.com/zachfi/zkit/pkg/util"
)

type Config struct {
	Interval  time.Duration `yaml:"interval"`
	APIKey    string        `yaml:"apikey"`
	Locations []Location    `yaml:"locations"`
	Timeout   time.Duration `yaml:"timeout"`

	NOAA NOAAConfig `yaml:"noaa"`

	EventReceiverClient common.ClientConfig `yaml:"event_receiver_client,omitempty"`
}

type NOAAConfig struct {
	Enabled bool `yaml:"enabled"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	cfg.EventReceiverClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "event-receiver-client"), f)

	f.StringVar(&cfg.APIKey, "api_key", "", "An OpenweatherMap API key")
	f.DurationVar(&cfg.Interval, "interval", time.Minute*5, "The interval at which to refresh the data")
	f.DurationVar(&cfg.Timeout, "timeout", time.Second*30, "The timeout for the API requests")
}

type Location struct {
	Name      string
	Latitude  float64
	Longitude float64
}
