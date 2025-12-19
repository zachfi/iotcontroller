package app

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

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

func NewDefaultConfig() *Config {
	defaultConfig := &Config{}
	defaultFS := flag.NewFlagSet("", flag.PanicOnError)
	defaultConfig.RegisterFlagsAndApplyDefaults("", defaultFS)
	return defaultConfig
}

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

// yamlMarshalUnmarshal utility function that converts a YAML interface in a map
// doing marshal and unmarshal of the parameter
func yamlMarshalUnmarshal(in interface{}) (map[interface{}]interface{}, error) {
	yamlBytes, err := yaml.Marshal(in)
	if err != nil {
		return nil, err
	}

	object := make(map[interface{}]interface{})
	if err := yaml.Unmarshal(yamlBytes, object); err != nil {
		return nil, err
	}

	return object, nil
}

// diffConfig utility function that returns the diff between two config map objects
func diffConfig(defaultConfig, actualConfig map[interface{}]interface{}) (map[interface{}]interface{}, error) {
	output := make(map[interface{}]interface{})

	for key, value := range actualConfig {

		defaultValue, ok := defaultConfig[key]
		if !ok {
			output[key] = value
			continue
		}

		switch v := value.(type) {
		case int:
			defaultV, ok := defaultValue.(int)
			if !ok || defaultV != v {
				output[key] = v
			}
		case string:
			defaultV, ok := defaultValue.(string)
			if !ok || defaultV != v {
				output[key] = v
			}
		case bool:
			defaultV, ok := defaultValue.(bool)
			if !ok || defaultV != v {
				output[key] = v
			}
		case []interface{}:
			defaultV, ok := defaultValue.([]interface{})
			if !ok || !reflect.DeepEqual(defaultV, v) {
				output[key] = v
			}
		case float64:
			defaultV, ok := defaultValue.(float64)
			if !ok || !reflect.DeepEqual(defaultV, v) {
				output[key] = v
			}
		case nil:
			if defaultValue != nil {
				output[key] = v
			}
		case map[interface{}]interface{}:
			defaultV, ok := defaultValue.(map[interface{}]interface{})
			if !ok {
				output[key] = value
			}
			diff, err := diffConfig(defaultV, v)
			if err != nil {
				return nil, err
			}
			if len(diff) > 0 {
				output[key] = diff
			}
		default:
			return nil, fmt.Errorf("unsupported type %T", v)
		}
	}

	return output, nil
}
