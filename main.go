/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/go-logr/logr"
	"github.com/grafana/dskit/flagext"
	"github.com/pkg/errors"
	"github.com/zachfi/iotcontroller/cmd/app"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v2"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

// var needs to be used instead of const as ldflags is used to fill this
// information in the release process
var (
	goos      = "unknown"
	goarch    = "unknown"
	gitCommit = "$Format:%H$" // sha1 from git, output of $(git rev-parse HEAD)

	buildDate = "1970-01-01T00:00:00Z" // build date in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
)

// version contains all the information related to the CLI version
type version struct {
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	GoOs      string `json:"goOs"`
	GoArch    string `json:"goArch"`
}

// versionString returns the CLI version
func versionString() string {
	return fmt.Sprintf("Version: %#v", version{
		gitCommit,
		buildDate,
		goos,
		goarch,
	})
}

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func main() {
	logger := log.NewLogfmtLogger(os.Stdout)

	cfg, err := loadConfig()
	if err != nil {
		_ = level.Error(logger).Log("msg", "failed to load config file", "err", err)
		os.Exit(1)
	}

	var otelEndpoint string
	var orgID string
	flag.StringVar(&otelEndpoint, "otel-endpoint", "", "The URL to use when sending traces")
	flag.StringVar(&orgID, "org-id", "", "The X-Scope-OrgID header to set when sending traces")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if otelEndpoint != "" {
		shutdownTracer, err := installOpenTelemetryTracer(otelEndpoint, orgID, setupLog)
		if err != nil {
			setupLog.Error(err, "error initializing tracer")
			os.Exit(1)
		}
		defer shutdownTracer()
	}

	a, err := app.New(*cfg)
	if err != nil {
		_ = level.Error(logger).Log("msg", "failed to create App", "err", err)
		os.Exit(1)
	}

	if err := a.Run(); err != nil {
		_ = level.Error(logger).Log("msg", "error running App", "err", err)
		os.Exit(1)
	}
}

func installOpenTelemetryTracer(endpoint string, orgID string, log logr.Logger) (func(), error) {
	if endpoint == "" {
		return func() {}, nil
	}

	log.Info("initialising OpenTelemetry tracer", "endpoint", endpoint)

	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("nodemanager"),
			semconv.ServiceVersionKey.String(versionString()),
		),
		resource.WithHost(),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize trace resuorce")
	}

	conn, err := grpc.DialContext(ctx, endpoint, grpc.WithInsecure())
	if err != nil {
		return nil, errors.Wrap(err, "failed to dial otel grpc")
	}

	options := []otlptracegrpc.Option{otlptracegrpc.WithGRPCConn(conn)}
	if orgID != "" {
		options = append(options,
			otlptracegrpc.WithHeaders(map[string]string{"X-Scope-OrgID": orgID}))
	}

	traceExporter, err := otlptracegrpc.New(ctx, options...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to creat trace exporter")
	}

	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	otel.SetTracerProvider(tracerProvider)

	// set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tracerProvider.Shutdown(ctx); err != nil {
			log.Error(err, "OpenTelemetry trace provider failed to shutdown")
			os.Exit(1)
		}
	}

	return shutdown, nil
}

func loadConfig() (*app.Config, error) {
	const (
		configFileOption = "config.file"
	)

	var configFile string

	args := os.Args[1:]
	config := &app.Config{}

	// first get the config file
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&configFile, configFileOption, "", "")

	// Try to find -config.file & -config.expand-env flags. As Parsing stops on the first error, eg. unknown flag,
	// we simply try remaining parameters until we find config flag, or there are no params left.
	// (ContinueOnError just means that flag.Parse doesn't call panic or os.Exit, but it returns error, which we ignore)
	for len(args) > 0 {
		_ = fs.Parse(args)
		args = args[1:]
	}

	// load config defaults and register flags
	config.RegisterFlagsAndApplyDefaults("", flag.CommandLine)

	// overlay with config file if provided
	if configFile != "" {
		buff, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read configFile %s: %w", configFile, err)
		}

		err = yaml.UnmarshalStrict(buff, config)
		if err != nil {
			return nil, fmt.Errorf("failed to parse configFile %s: %w", configFile, err)
		}
	}

	// overlay with cli
	flagext.IgnoredFlag(flag.CommandLine, configFileOption, "Configuration file to load")
	flag.Parse()

	return config, nil
}
