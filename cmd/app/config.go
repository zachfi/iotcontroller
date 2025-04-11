package app

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/grafana/dskit/flagext"
	"github.com/grafana/dskit/log"
	"github.com/grafana/dskit/server"
	"github.com/pkg/errors"
	zkitTracing "github.com/zachfi/zkit/pkg/tracing"
	"github.com/zachfi/zkit/pkg/util"
	"gopkg.in/yaml.v2"

	"github.com/zachfi/iotcontroller/modules/conditioner"
	"github.com/zachfi/iotcontroller/modules/controller"
	"github.com/zachfi/iotcontroller/modules/harvester"
	"github.com/zachfi/iotcontroller/modules/hookreceiver"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/modules/router"
	"github.com/zachfi/iotcontroller/modules/weather"
	"github.com/zachfi/iotcontroller/modules/zonekeeper"
)

type Config struct {
	Target                 string `yaml:"target"`
	EnableGoRuntimeMetrics bool   `yaml:"enable_go_runtime_metrics,omitempty"`

	// Util
	Tracing zkitTracing.Config `yaml:"tracing,omitempty"`

	LogLevel log.Level `yaml:"log_level"`

	// Server
	Server server.Config `yaml:"server,omitempty"`

	// Modules
	Conditioner  conditioner.Config  `yaml:"conditioner,omitempty"`
	Controller   controller.Config   `yaml:"controller,omitempty"`
	Harvester    harvester.Config    `yaml:"harvester,omitempty"`
	HookReceiver hookreceiver.Config `yaml:"hookreceiver,omitempty"`
	MQTTClient   mqttclient.Config   `yaml:"mqttclient,omitempty"`
	Router       router.Config       `yaml:"router,omitempty"`
	Weather      weather.Config      `yaml:"weather,omitempty"`
	ZoneKeeper   zonekeeper.Config   `yaml:"zonekeeper,omitempty"`
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Target = All
	f.StringVar(&c.Target, "target", All, "target module")
	f.BoolVar(&c.EnableGoRuntimeMetrics, "enable-go-runtime-metrics", false, "Set to true to enable all Go runtime metrics")

	// Server
	flagext.DefaultValues(&c.Server)
	// c.Server.LogLevel.RegisterFlags(f)
	f.IntVar(&c.Server.HTTPListenPort, "server.http-listen-port", 3030, "HTTP server listen port.")
	f.IntVar(&c.Server.GRPCListenPort, "server.grpc-listen-port", 9090, "gRPC server listen port.")

	// Util
	c.Tracing.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "tracing"), f)

	// Modules
	c.Controller.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "controller"), f)
	c.Conditioner.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "conditioner"), f)
	c.Harvester.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "harvester"), f)
	c.HookReceiver.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "hookreceiver"), f)
	c.MQTTClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "mqttclient"), f)
	c.Router.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "router"), f)
	c.Weather.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "weather"), f)
	c.ZoneKeeper.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "zonekeeper"), f)

	c.LogLevel.RegisterFlags(f)
}

// LoadConfig receives a file path for a configuration to load.
func LoadConfig(file string) (Config, error) {
	filename, _ := filepath.Abs(file)

	config := Config{}
	err := loadYamlFile(filename, &config)
	if err != nil {
		return config, errors.Wrap(err, "failed to load yaml file")
	}

	return config, nil
}

// loadYamlFile unmarshals a YAML file into the received interface{} or returns an error.
func loadYamlFile(filename string, d interface{}) error {
	yamlFile, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(yamlFile, d)
	if err != nil {
		return err
	}

	return nil
}
