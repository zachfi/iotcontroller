package zigbeecoordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/grafana/dskit/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	zigbeedongle "github.com/zachfi/iotcontroller/pkg/zigbee-dongle"
	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/znp"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
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

	dongle      zigbeedongle.Dongle
	routeClient iotv1proto.RouteServiceClient // gRPC client for sending messages to router

	// Permit join state
	permitJoinMux   sync.Mutex
	permitJoinTimer *time.Timer
	permitJoinEnd   *time.Time

	// Interview state - track devices being interviewed to prevent duplicates
	interviewingMux sync.Mutex
	interviewing    map[uint16]bool // network address -> is interviewing
}

func New(cfg Config, logger *slog.Logger, routeClient iotv1proto.RouteServiceClient) (*ZigbeeCoordinator, error) {
	z := &ZigbeeCoordinator{
		cfg:          &cfg,
		logger:       logger.With("module", module),
		tracer:       otel.Tracer(module, trace.WithInstrumentationAttributes(attribute.String("module", module))),
		routeClient:  routeClient,
		interviewing: make(map[uint16]bool),
	}

	dongleCfg := cfg.ToDongleConfig(z.logger)
	z.logger.Info("creating Zigbee dongle",
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
	z.logger.Info("starting Zigbee coordinator")

	// Perform a health check to verify device communication (ZNP only for now).
	// Skip when force-forming: the device may be in bootloader or need reset first; Start() will reset and then talk.
	if !z.cfg.ForceForm {
		if znpController, ok := z.dongle.(*znp.Controller); ok {
			z.logger.Info("performing device health check")
			if err := znpController.HealthCheck(ctx); err != nil {
				z.logger.Warn("health check failed, but continuing", slog.String("error", err.Error()))
			} else {
				z.logger.Info("device health check passed")
			}
		}
	}
	// TODO: Add health check for Ember stack when implemented

	// Load network state from disk if available
	// This allows restoring the same network when swapping devices
	if z.cfg.StateFile != "" {
		z.logger.Info("loading network state", slog.String("state_file", z.cfg.StateFile))
		savedParams, err := zigbeedongle.LoadNetworkState(z.cfg.StateFile)
		if err != nil {
			z.logger.Warn("failed to load network state", slog.String("error", err.Error()))
		} else if savedParams != nil {
			z.logger.Info("network state loaded from disk",
				slog.Uint64("pan_id", uint64(savedParams.PanID)),
				slog.String("extended_pan_id", fmt.Sprintf("%016x", savedParams.ExtendedPanID)),
				slog.Int("channel", int(savedParams.Channel)),
			)
			// State is loaded - we'll check if we need to form the network in running()
		} else {
			z.logger.Info("no saved network state found - network will need to be formed")
		}
	}

	return nil
}

func (z *ZigbeeCoordinator) running(ctx context.Context) error {
	z.logger.Info("starting Zigbee dongle and message reception loop")

	// Start the dongle - this initializes communication, sends magic byte,
	// checks version, gets device info, and starts the coordinator if needed
	messages, err := z.dongle.Start(ctx)
	if err != nil {
		z.logger.Error("failed to start dongle", slog.String("error", err.Error()))
		return fmt.Errorf("failed to start dongle: %w", err)
	}

	z.logger.Info("dongle started successfully, verifying network state")

	// Get network info to verify communication and log network state
	info, err := z.dongle.GetNetworkInfo(ctx)
	if err != nil {
		z.logger.Warn("failed to get network info", slog.String("error", err.Error()))
	} else {
		z.logger.Info("network information retrieved",
			slog.String("state", info.State.String()),
			slog.Uint64("pan_id", uint64(info.PanID)),
			slog.Uint64("short_address", uint64(info.ShortAddress)),
			slog.Uint64("channel", uint64(info.Channel)),
			slog.String("extended_pan_id", fmt.Sprintf("%016x", info.ExtendedPanID)),
		)

		// Check if device is not joined to a network
		// NotJoined, NetworkDown, or empty network parameters indicate no network
		needsNetwork := info.State == types.NetworkStateNotJoined ||
			info.State == types.NetworkStateDown ||
			(info.PanID == 0 && info.Channel == 0 && info.ExtendedPanID == 0)

		// ForceForm: treat as needing to form (leave current network and form from config or generate)
		if z.cfg.ForceForm {
			needsNetwork = true
			z.logger.Info("force-form requested; will form network from config or generate new parameters")
		}

		if !needsNetwork {
			// Device is already on a network. Config is source of truth: only write to disk
			// when we're adopting (no config or config doesn't match device). Otherwise keep config as-is.
			if z.cfg.StateFile != "" {
				savedParams, err := zigbeedongle.LoadNetworkState(z.cfg.StateFile)
				deviceParamsMatch := err == nil && savedParams != nil &&
					savedParams.PanID == info.PanID &&
					savedParams.ExtendedPanID == info.ExtendedPanID &&
					savedParams.Channel == uint8(info.Channel)

				if deviceParamsMatch {
					// Config matches device; don't overwrite (preserves user-edited key in config)
					z.logger.Info("config matches device network; keeping config as source of truth",
						slog.Bool("network_key_in_config", !zigbeedongle.IsZeroNetworkKey(savedParams.NetworkKey)),
					)
				} else {
					// Adopt: save device state to config. We cannot read the key from the device.
					var networkKey [16]byte
					if err == nil && savedParams != nil {
						networkKey = savedParams.NetworkKey
						z.logger.Info("using network key from saved state file")
					} else {
						z.logger.Warn("no saved network state found - network key cannot be read from device. " +
							"Set networkkey in config (e.g. hex from: openssl rand -hex 16) for device swap support.")
					}
					params := zigbeedongle.NetworkParameters{
						PanID:         info.PanID,
						ExtendedPanID: info.ExtendedPanID,
						Channel:       uint8(info.Channel),
						NetworkKey:    networkKey,
					}
					if err := zigbeedongle.SaveNetworkState(z.cfg.StateFile, params); err != nil {
						z.logger.Warn("failed to save existing network state", slog.String("error", err.Error()))
					} else {
						z.logger.Info("saved existing network state to disk (adopt)",
							slog.Uint64("pan_id", uint64(params.PanID)),
							slog.String("extended_pan_id", fmt.Sprintf("%016x", params.ExtendedPanID)),
							slog.Int("channel", int(params.Channel)),
							slog.Bool("network_key_preserved", !zigbeedongle.IsZeroNetworkKey(networkKey)),
						)
					}
				}
			}
		} else if needsNetwork {
			// Try to load saved network state first
			var params *zigbeedongle.NetworkParameters
			var err error

			if z.cfg.StateFile != "" {
				savedParams, err := zigbeedongle.LoadNetworkState(z.cfg.StateFile)
				if err != nil {
					z.logger.Warn("failed to load network state for auto-formation", slog.String("error", err.Error()))
				} else if savedParams != nil {
					params = savedParams
					// Never form a network with an all-zero key (insecure).
					// This can happen when state was saved as a placeholder after
					// adopting an already-joined device (key cannot be read from dongle).
					if zigbeedongle.IsZeroNetworkKey(params.NetworkKey) {
						newKey, err := zigbeedongle.GenerateRandomNetworkParameters()
						if err != nil {
							z.logger.Error("failed to generate secure network key", slog.String("error", err.Error()))
							return fmt.Errorf("state file has all-zero network key; failed to generate secure key: %w", err)
						}
						params.NetworkKey = newKey.NetworkKey
						z.logger.Warn("state file had all-zero network key (insecure); generated new random key for network formation")
					}
					z.logger.Info("forming network from saved state",
						slog.Uint64("pan_id", uint64(params.PanID)),
						slog.String("extended_pan_id", fmt.Sprintf("%016x", params.ExtendedPanID)),
						slog.Int("channel", int(params.Channel)),
					)
				}
			}

			// If no saved state, generate random network parameters
			if params == nil {
				z.logger.Info("no saved network state found - generating random network parameters")
				params, err = zigbeedongle.GenerateRandomNetworkParameters()
				if err != nil {
					z.logger.Error("failed to generate random network parameters", slog.String("error", err.Error()))
					return fmt.Errorf("failed to generate network parameters: %w", err)
				}
				z.logger.Info("generated random network parameters",
					slog.Uint64("pan_id", uint64(params.PanID)),
					slog.String("extended_pan_id", fmt.Sprintf("%016x", params.ExtendedPanID)),
					slog.Int("channel", int(params.Channel)),
				)
			}

			// Form the network with the parameters (saved or random)
			if err := z.formNetworkAndSave(ctx, *params); err != nil {
				z.logger.Error("failed to form network", slog.String("error", err.Error()))
				return fmt.Errorf("failed to form network: %w", err)
			}
			z.logger.Info("network formed successfully")
		}
	}

	z.logger.Info("listening for Zigbee messages")

	// Enable permit join by default for 60 seconds to allow initial device pairing
	// This matches common practice in zigbee2mqtt
	z.logger.Info("enabling permit join for 60 seconds to allow device pairing")
	if err := z.PermitJoin(ctx, 60); err != nil {
		z.logger.Warn("failed to enable permit join", slog.String("error", err.Error()))
	}

	// Start device join monitoring and interview triggering (ZNP only for now)
	var joinEvents <-chan znp.DeviceJoinEvent
	if znpController, ok := z.dongle.(*znp.Controller); ok {
		joinEvents = znpController.DeviceJoinEvents()
		z.logger.Info("device join monitoring enabled for interviews")
	}

	messageCount := 0
	for {
		select {
		case <-ctx.Done():
			z.logger.Info("context cancelled, stopping message loop")
			return nil

		case joinEvent, ok := <-joinEvents:
			if !ok {
				// Join events channel closed
				joinEvents = nil // Disable this case
			} else {
				// Device joined - trigger interview
				z.logger.Info("device joined, starting interview",
					slog.String("network_address", fmt.Sprintf("0x%04x", joinEvent.NetworkAddress)),
					slog.String("ieee_address", fmt.Sprintf("0x%016x", joinEvent.IEEEAddress)),
				)
				go z.handleDeviceJoin(ctx, joinEvent)
			}

		case msg, ok := <-messages:
			if !ok {
				z.logger.Warn("message channel closed")
				return fmt.Errorf("message channel closed unexpectedly")
			}

			messageCount++
			z.logger.Debug("received Zigbee message",
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
				z.logger.Info("received Zigbee message",
					slog.Int("message_count", messageCount),
					slog.String("source", fmt.Sprintf("%04x", msg.Source.Short)),
					slog.String("cluster_id", fmt.Sprintf("0x%04x", msg.ClusterID)),
					slog.String("data", fmt.Sprintf("%x", msg.Data)),
				)
			}

			// Forward message to router if router client is available
			if z.routeClient != nil {
				if err := z.forwardMessageToRouter(ctx, msg); err != nil {
					z.logger.Warn("failed to forward message to router", slog.String("error", err.Error()))
				}
			}
		}
	}
}

func (z *ZigbeeCoordinator) stopping(_ error) error {
	z.logger.Info("stopping Zigbee coordinator")

	// Stop permit join timer
	z.permitJoinMux.Lock()
	if z.permitJoinTimer != nil {
		z.permitJoinTimer.Stop()
		z.permitJoinTimer = nil
	}
	z.permitJoinMux.Unlock()

	// Disable permit join on shutdown
	if z.dongle != nil {
		_ = z.dongle.PermitJoining(context.Background(), false)
		if err := z.dongle.Close(); err != nil {
			z.logger.Warn("error closing dongle", slog.String("error", err.Error()))
			return err
		}
	}
	return nil
}

// formNetworkAndSave forms a network with the given parameters and saves the state to disk.
func (z *ZigbeeCoordinator) formNetworkAndSave(ctx context.Context, params zigbeedongle.NetworkParameters) error {
	// Form the network
	if err := z.dongle.FormNetwork(ctx, params); err != nil {
		return fmt.Errorf("forming network: %w", err)
	}

	// Save state to disk for persistence across device swaps
	if z.cfg.StateFile != "" {
		if err := zigbeedongle.SaveNetworkState(z.cfg.StateFile, params); err != nil {
			z.logger.Warn("failed to save network state", slog.String("error", err.Error()))
			// Don't fail the operation if we can't save state
		} else {
			z.logger.Info("network state saved to disk", slog.String("state_file", z.cfg.StateFile))
		}
	}

	return nil
}

// PermitJoin enables or disables device joining on the network for a specified duration.
// Duration is in seconds (0-254). A duration of 0 disables joining.
// A timer automatically disables joining after the duration expires (like zigbee-herdsman).
func (z *ZigbeeCoordinator) PermitJoin(ctx context.Context, durationSeconds int) error {
	z.permitJoinMux.Lock()
	defer z.permitJoinMux.Unlock()

	// Clear any existing timer
	if z.permitJoinTimer != nil {
		z.permitJoinTimer.Stop()
		z.permitJoinTimer = nil
	}
	z.permitJoinEnd = nil

	if durationSeconds > 0 {
		// Validate duration (max 254 seconds, like zigbee-herdsman)
		if durationSeconds > 254 {
			return fmt.Errorf("cannot permit join for more than 254 seconds, got %d", durationSeconds)
		}

		// Enable joining via dongle
		if err := z.dongle.PermitJoining(ctx, true); err != nil {
			return fmt.Errorf("enabling permit join: %w", err)
		}

		z.logger.Info("permit join enabled", slog.Int("duration_seconds", durationSeconds))

		// Set up timer to automatically disable after duration
		endTime := time.Now().Add(time.Duration(durationSeconds) * time.Second)
		z.permitJoinEnd = &endTime
		z.permitJoinTimer = time.AfterFunc(time.Duration(durationSeconds)*time.Second, func() {
			z.permitJoinMux.Lock()
			defer z.permitJoinMux.Unlock()

			if err := z.dongle.PermitJoining(context.Background(), false); err != nil {
				z.logger.Warn("failed to automatically disable permit join", slog.String("error", err.Error()))
			} else {
				z.logger.Info("permit join automatically disabled after duration expired")
			}

			z.permitJoinTimer = nil
			z.permitJoinEnd = nil
		})
	} else {
		// Disable joining
		if err := z.dongle.PermitJoining(ctx, false); err != nil {
			return fmt.Errorf("disabling permit join: %w", err)
		}

		z.logger.Info("permit join disabled")
	}

	return nil
}

// GetPermitJoin returns whether joining is currently permitted and when it will expire.
func (z *ZigbeeCoordinator) GetPermitJoin() (bool, *time.Time) {
	z.permitJoinMux.Lock()
	defer z.permitJoinMux.Unlock()

	return z.permitJoinTimer != nil, z.permitJoinEnd
}

// forwardMessageToRouter forwards a Zigbee message to the router service.
// The path format is: "zigbee/{device_id}" where device_id is the source network address.
// The message payload is JSON-encoded with the Zigbee message details.
func (z *ZigbeeCoordinator) forwardMessageToRouter(ctx context.Context, msg types.IncomingMessage) error {
	if z.routeClient == nil {
		return nil // Router client not configured, skip forwarding
	}

	// Create path: zigbee/{device_id} where device_id is the source network address
	deviceID := fmt.Sprintf("%04x", msg.Source.Short)
	path := fmt.Sprintf("zigbee/%s", deviceID)

	// Encode message as JSON for router
	// TODO: Consider using protobuf instead of JSON for better type safety
	messageJSON := map[string]interface{}{
		"source": map[string]interface{}{
			"mode":  msg.Source.Mode.String(),
			"short": fmt.Sprintf("%04x", msg.Source.Short),
		},
		"source_endpoint":      int(msg.SourceEndpoint),
		"destination_endpoint": int(msg.DestinationEndpoint),
		"cluster_id":           fmt.Sprintf("0x%04x", msg.ClusterID),
		"link_quality":         int(msg.LinkQuality),
		"data":                 fmt.Sprintf("%x", msg.Data),
	}

	jsonBytes, err := json.Marshal(messageJSON)
	if err != nil {
		return fmt.Errorf("marshaling message to JSON: %w", err)
	}

	// Send to router
	req := &iotv1proto.SendRequest{
		Path:    path,
		Message: jsonBytes,
	}

	_, err = z.routeClient.Send(ctx, req)
	if err != nil {
		return fmt.Errorf("sending to router: %w", err)
	}

	z.logger.Debug("forwarded message to router",
		slog.String("path", path),
		slog.String("source", deviceID),
	)

	return nil
}

// handleDeviceJoin performs an interview when a device joins the network.
// It deduplicates concurrent join events to prevent multiple interviews of the same device.
func (z *ZigbeeCoordinator) handleDeviceJoin(ctx context.Context, joinEvent znp.DeviceJoinEvent) {
	// Only handle ZNP devices for now
	znpController, ok := z.dongle.(*znp.Controller)
	if !ok {
		z.logger.Debug("device join event received but dongle is not ZNP, skipping interview")
		return
	}

	// Check if we're already interviewing this device (deduplicate concurrent join events)
	z.interviewingMux.Lock()
	if z.interviewing[joinEvent.NetworkAddress] {
		z.interviewingMux.Unlock()
		z.logger.Debug("device interview already in progress, skipping duplicate join event",
			slog.String("network_address", fmt.Sprintf("0x%04x", joinEvent.NetworkAddress)),
			slog.String("ieee_address", fmt.Sprintf("0x%016x", joinEvent.IEEEAddress)),
		)
		return
	}
	// Mark as interviewing
	z.interviewing[joinEvent.NetworkAddress] = true
	z.interviewingMux.Unlock()

	// Ensure we clear the interviewing flag when done
	defer func() {
		z.interviewingMux.Lock()
		delete(z.interviewing, joinEvent.NetworkAddress)
		z.interviewingMux.Unlock()
	}()

	z.logger.Info("starting device interview",
		slog.String("network_address", fmt.Sprintf("0x%04x", joinEvent.NetworkAddress)),
		slog.String("ieee_address", fmt.Sprintf("0x%016x", joinEvent.IEEEAddress)),
	)

	// Wait after device join before starting interview
	// End devices (especially battery-powered) need time to complete key authorization
	// and become ready to respond to ZDO requests. zigbee-herdsman waits 5 seconds for
	// end devices. We use a shorter delay (2 seconds) as a compromise, but may need
	// to increase for some devices.
	// Status 0x80 (INVALID_REQTYPE) often indicates the device isn't ready yet.
	z.logger.Debug("waiting for device to complete key authorization before interview",
		slog.String("network_address", fmt.Sprintf("0x%04x", joinEvent.NetworkAddress)),
		slog.Duration("delay", 2*time.Second),
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
		// Continue with interview
	}

	z.logger.Debug("device join delay complete, starting interview queries",
		slog.String("network_address", fmt.Sprintf("0x%04x", joinEvent.NetworkAddress)),
	)

	// Perform interview
	interviewInfo, err := znpController.InterviewDevice(ctx, joinEvent.NetworkAddress)
	if err != nil {
		z.logger.Error("device interview failed",
			slog.String("network_address", fmt.Sprintf("0x%04x", joinEvent.NetworkAddress)),
			slog.String("ieee_address", fmt.Sprintf("0x%016x", joinEvent.IEEEAddress)),
			slog.String("error", err.Error()),
		)

		// Send interview failure to router
		z.sendInterviewResult(ctx, joinEvent.IEEEAddress, joinEvent.NetworkAddress, nil, err)
		return
	}

	z.logger.Info("device interview completed successfully",
		slog.String("network_address", fmt.Sprintf("0x%04x", joinEvent.NetworkAddress)),
		slog.String("ieee_address", fmt.Sprintf("0x%016x", joinEvent.IEEEAddress)),
		slog.String("device_type", interviewInfo.DeviceType),
		slog.Int("endpoint_count", len(interviewInfo.Endpoints)),
	)

	// Send interview results to router
	z.sendInterviewResult(ctx, joinEvent.IEEEAddress, joinEvent.NetworkAddress, interviewInfo, nil)
}

// sendInterviewResult sends interview results to the router.
func (z *ZigbeeCoordinator) sendInterviewResult(ctx context.Context, ieeeAddr uint64, nwkAddr uint16, info *znp.DeviceInterviewInfo, interviewErr error) {
	if z.routeClient == nil {
		return // Router client not configured, skip
	}

	// Create path: zigbee/{ieee_address}/interview
	ieeeStr := fmt.Sprintf("%016x", ieeeAddr)
	path := fmt.Sprintf("zigbee/%s/interview", ieeeStr)

	// Build interview result message
	result := map[string]interface{}{
		"ieee_address":    fmt.Sprintf("0x%s", ieeeStr),
		"network_address": nwkAddr,
	}

	if interviewErr != nil {
		result["interview_state"] = "failed"
		result["error"] = interviewErr.Error()
	} else if info != nil {
		result["interview_state"] = "successful"
		result["device_type"] = info.DeviceType
		result["manufacturer_id"] = info.ManufacturerID

		if info.Capabilities != nil {
			result["capabilities"] = map[string]interface{}{
				"alternate_pan_coordinator": info.Capabilities.AlternatePanCoordinator,
				"receiver_on_when_idle":     info.Capabilities.ReceiverOnWhenIdle,
				"security_capability":       info.Capabilities.SecurityCapability,
			}
		}

		// Convert endpoints
		endpoints := make([]map[string]interface{}, 0, len(info.Endpoints))
		for _, ep := range info.Endpoints {
			epMap := map[string]interface{}{
				"id":              ep.ID,
				"profile_id":      ep.ProfileID,
				"device_id":       ep.DeviceID,
				"device_version":  ep.DeviceVersion,
				"input_clusters":  ep.InputClusters,
				"output_clusters": ep.OutputClusters,
			}
			endpoints = append(endpoints, epMap)
		}
		result["endpoints"] = endpoints
	} else {
		result["interview_state"] = "pending"
	}

	// Encode as JSON
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		z.logger.Error("failed to marshal interview result", slog.String("error", err.Error()))
		return
	}

	// Send to router
	req := &iotv1proto.SendRequest{
		Path:    path,
		Message: jsonBytes,
	}

	_, err = z.routeClient.Send(ctx, req)
	if err != nil {
		z.logger.Warn("failed to send interview result to router",
			slog.String("path", path),
			slog.String("error", err.Error()),
		)
	} else {
		z.logger.Info("sent interview result to router",
			slog.String("path", path),
			slog.String("ieee_address", fmt.Sprintf("0x%s", ieeeStr)),
		)
	}
}

func formatMessage(message, prefix string) string {
	if prefix == "" {
		return message
	}
	return fmt.Sprintf("%s-%s", prefix, message)
}
