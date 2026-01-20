package zigbeecoordinator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	zigbeedongle "github.com/zachfi/iotcontroller/pkg/zigbee-dongle"
	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/znp"
)

const (
	module    = "zigbee-coordinator"
	namespace = "iot"
)

type ZigbeeCoordinator struct {
	services.Service

	cfg    *Config
	logger *slog.Logger
	tracer trace.Tracer

	dongle zigbeedongle.Dongle
}

func New(cfg Config, logger *slog.Logger) (*ZigbeeCoordinator, error) {
	z := &ZigbeeCoordinator{
		cfg:    &cfg,
		logger: logger.With("module", module),
		tracer: otel.Tracer(module, trace.WithInstrumentationAttributes(attribute.String("module", module))),
	}

	dongleCfg := cfg.ToDongleConfig()
	z.logger.Info("Creating Zigbee dongle",
		slog.String("port", dongleCfg.Port),
		slog.String("stack", string(dongleCfg.StackType)),
		slog.Bool("log_commands", dongleCfg.LogCommands),
		slog.Bool("log_errors", dongleCfg.LogErrors),
	)

	dongle, err := zigbeedongle.NewDongle(dongleCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create zigbee dongle: %w", err)
	}

	z.dongle = dongle

	z.Service = services.NewBasicService(z.starting, z.running, z.stopping)

	return z, nil
}

func (z *ZigbeeCoordinator) starting(ctx context.Context) error {
	z.logger.Info("Starting Zigbee coordinator")

	// Perform a health check to verify device communication
	if znpController, ok := z.dongle.(*znp.Controller); ok {
		z.logger.Info("Performing device health check")
		if err := znpController.HealthCheck(ctx); err != nil {
			z.logger.Warn("Health check failed, but continuing", slog.String("error", err.Error()))
		} else {
			z.logger.Info("Device health check passed")
		}
	}

	return nil
}

func (z *ZigbeeCoordinator) running(ctx context.Context) error {
	z.logger.Info("Starting Zigbee dongle and message reception loop")

	// Start the dongle - this initializes communication, sends magic byte,
	// checks version, gets device info, and starts the coordinator if needed
	messages, err := z.dongle.Start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start dongle: %w", err)
	}

	z.logger.Info("Dongle started successfully, verifying network state")

	// Get network info to verify communication and log network state
	info, err := z.dongle.GetNetworkInfo(ctx)
	if err != nil {
		z.logger.Warn("Failed to get network info", slog.String("error", err.Error()))
	} else {
		z.logger.Info("Network information retrieved",
			slog.String("state", info.State),
			slog.Uint64("pan_id", uint64(info.PanID)),
			slog.Uint64("short_address", uint64(info.ShortAddress)),
			slog.Uint64("channel", uint64(info.Channel)),
			slog.String("extended_pan_id", fmt.Sprintf("%016x", info.ExtendedPanID)),
		)
	}

	z.logger.Info("Listening for Zigbee messages")

	messageCount := 0
	for {
		select {
		case <-ctx.Done():
			z.logger.Info("Context cancelled, stopping message loop")
			return nil

		case msg, ok := <-messages:
			if !ok {
				z.logger.Warn("Message channel closed")
				return fmt.Errorf("message channel closed unexpectedly")
			}

			messageCount++
			z.logger.Debug("Received Zigbee message",
				slog.Int("message_count", messageCount),
				slog.String("source", fmt.Sprintf("%04x", msg.Source.Short)),
				slog.String("source_mode", msg.Source.Mode.String()),
				slog.Int("source_endpoint", int(msg.SourceEndpoint)),
				slog.Int("dest_endpoint", int(msg.DestinationEndpoint)),
				slog.String("cluster_id", fmt.Sprintf("0x%04x", msg.ClusterID)),
				slog.Int("link_quality", int(msg.LinkQuality)),
				slog.Int("data_len", len(msg.Data)),
			)

			// Log first few messages at info level to verify communication
			if messageCount <= 5 {
				z.logger.Info("Received Zigbee message",
					slog.Int("message_count", messageCount),
					slog.String("source", fmt.Sprintf("%04x", msg.Source.Short)),
					slog.String("cluster_id", fmt.Sprintf("0x%04x", msg.ClusterID)),
					slog.String("data", fmt.Sprintf("%x", msg.Data)),
				)
			}
		}
	}
}

func (z *ZigbeeCoordinator) stopping(_ error) error {
	z.logger.Info("Stopping Zigbee coordinator")
	if z.dongle != nil {
		if err := z.dongle.Close(); err != nil {
			z.logger.Warn("Error closing dongle", slog.String("error", err.Error()))
			return err
		}
	}
	return nil
}

func formatMessage(message, prefix string) string {
	if prefix == "" {
		return message
	}
	return fmt.Sprintf("%s-%s", prefix, message)
}
