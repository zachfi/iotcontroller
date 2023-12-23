package weather

import (
	"flag"
	"time"
)

type Config struct {
	Interval  time.Duration `mapstructure:"interval"`
	APIKey    string        `mapstructure:"apikey"`
	Locations []Location    `mapstructure:"locations"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.APIKey, "api_key", "", "An OpenweatherMap API key")
	f.DurationVar(&cfg.Interval, "interval", time.Second*90, "The interval at which to refresh the data")
}

type Location struct {
	Name      string
	Latitude  float64
	Longitude float64
}
