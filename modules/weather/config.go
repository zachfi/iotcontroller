package weather

import (
	"flag"
	"time"
)

type Config struct {
	Interval  time.Duration `yaml:"interval"`
	APIKey    string        `yaml:"apikey"`
	Locations []Location    `yaml:"locations"`
	Timeout   time.Duration `yaml:"timeout"`

	NOAA NOAAConfig `yaml:"noaa"`
}

type NOAAConfig struct {
	Enabled bool `yaml:"enabled"`
}

func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.APIKey, "api_key", "", "An OpenweatherMap API key")
	f.DurationVar(&cfg.Interval, "interval", time.Minute*5, "The interval at which to refresh the data")
	f.DurationVar(&cfg.Timeout, "timeout", time.Second*30, "The timeout for the API requests")
}

type Location struct {
	Name      string
	Latitude  float64
	Longitude float64
}
