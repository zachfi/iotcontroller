package common

import (
	"flag"
	"log/slog"

	"github.com/pkg/errors"
	"github.com/zachfi/zkit/pkg/util"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	cfg *ClientConfig

	conn *grpc.ClientConn

	logger *slog.Logger
}

type ClientConfig struct {
	ServerAddress string `yaml:"server_address,omitempty"`
}

func (cfg *ClientConfig) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.ServerAddress, util.PrefixConfig(prefix, "address"), ":9090", "The address of the router to connect to")
}

func NewClientConn(cfg ClientConfig) (*grpc.ClientConn, error) {
	var err error

	opts := []grpc.DialOption{
		// TODO: implement optional secure connection
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}

	conn, err := grpc.NewClient(cfg.ServerAddress, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to dial grpc")
	}

	return conn, nil
}
