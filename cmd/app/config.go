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

	"github.com/zachfi/iotcontroller/modules/client"
	"github.com/zachfi/iotcontroller/modules/conditioner"
	"github.com/zachfi/iotcontroller/modules/controller"
	"github.com/zachfi/iotcontroller/modules/harvester"
	"github.com/zachfi/iotcontroller/modules/hookreceiver"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	"github.com/zachfi/iotcontroller/modules/telemetry"
	"github.com/zachfi/iotcontroller/modules/zonekeeper"
)

type Config struct {
	Target string `yaml:"target"`

	// Util
	Tracing zkitTracing.Config `yaml:"tracing,omitempty"`

	LogLevel log.Level `yaml:"log_level"`

	// Server
	Server server.Config `yaml:"server,omitempty"`

	// Modules
	Client       client.Config       `yaml:"client,omitempty"`
	Controller   controller.Config   `yaml:"controller,omitempty"`
	Harvester    harvester.Config    `yaml:"harvester,omitempty"`
	MQTTClient   mqttclient.Config   `yaml:"mqttclient,omitempty"`
	Telemetry    telemetry.Config    `yaml:"telemetry,omitempty"`
	HookReceiver hookreceiver.Config `yaml:"hookreceiver,omitempty"`
	Conditioner  conditioner.Config  `yaml:"conditioner,omitempty"`
	ZoneKeeper   zonekeeper.Config   `yaml:"zonekeeper,omitempty"`
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Target = All
	f.StringVar(&c.Target, "target", All, "target module")

	// Server
	// c.Server.RegisterFlags(f)
	flagext.DefaultValues(&c.Server)
	f.IntVar(&c.Server.HTTPListenPort, "server.http-listen-port", 3030, "HTTP server listen port.")
	f.IntVar(&c.Server.GRPCListenPort, "server.grpc-listen-port", 9090, "gRPC server listen port.")

	// Util
	c.Tracing.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "tracing"), f)

	// Modules
	c.Client.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "client"), f)
	c.Controller.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "controller"), f)
	c.Harvester.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "harvester"), f)
	c.MQTTClient.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "mqttclient"), f)
	c.Telemetry.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "telemetry"), f)

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
