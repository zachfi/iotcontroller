package client

import (
	"context"
	"log/slog"

	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var module = "client"

type Client struct {
	services.Service
	cfg *Config

	conn *grpc.ClientConn

	logger *slog.Logger
}

func New(cfg Config, logger *slog.Logger) (*Client, error) {
	var err error

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	}
	conn, err := grpc.Dial(cfg.ServerAddress, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to dial grpc")
	}

	c := &Client{
		cfg:    &cfg,
		logger: logger.With("module", module),
		conn:   conn,
	}

	c.Service = services.NewBasicService(c.starting, c.running, c.stopping)
	return c, nil
}

func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}

func (c *Client) starting(ctx context.Context) error {
	return nil
}

func (c *Client) running(ctx context.Context) error {
	err := c.run(ctx)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (t *Client) stopping(_ error) error {
	return nil
}

func (t *Client) run(ctx context.Context) error {
	return nil
}
