package app

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/grafana/dskit/flagext"
	"github.com/pkg/errors"
	"github.com/weaveworks/common/server"
	"github.com/zachfi/iotcontroller/modules/controller"
	"github.com/zachfi/iotcontroller/modules/mqttclient"
	ztrace "github.com/zachfi/zkit/pkg/tracing"
	"github.com/zachfi/zkit/pkg/util"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Target string `yaml:"target"`

	Tracing ztrace.Config `yaml:"tracing,omitempty"`

	Server server.Config `yaml:"server,omitempty"`

	Controller controller.Config `yaml:"controller"`
	MQTT       mqttclient.Config `yaml:"mqttclient"`
}

func (c *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	c.Target = All
	f.StringVar(&c.Target, "target", All, "target module")
	c.Tracing.RegisterFlagsAndApplyDefaults("tracing", f)

	flagext.DefaultValues(&c.Server)
	c.Server.LogLevel.RegisterFlags(f)

	f.IntVar(&c.Server.HTTPListenPort, "server.http-listen-port", 8080, "HTTP server listen port.")
	f.IntVar(&c.Server.GRPCListenPort, "server.grpc-listen-port", 9095, "gRPC server listen port.")

	c.Controller.RegisterFlagsAndApplyDefaults(util.PrefixConfig(prefix, "controller"), f)
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
