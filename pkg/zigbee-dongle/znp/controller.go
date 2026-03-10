package znp

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// Formation/startup timeouts. zigbee-herdsman uses 40s for startupFromApp and 30s for reset
// (see zigbee-herdsman src/adapter/z-stack/znp/znp.ts: timeouts.SREQ=6s, reset=30s, startupFromApp=40s).
const (
	startupVersionTimeout     = 30 * time.Second // SysVersionRequest after reset (herdsman default 10s; we allow more for slow boot)
	startupDeviceInfoTimeout  = 15 * time.Second // UtilGetDeviceInfoRequest
	startupFromAppTimeout     = 40 * time.Second // ZdoStartupFromAppRequest (matches zigbee-herdsman)
	startupStateChangeTimeout = 40 * time.Second // ZdoStateChangeInd after startup (same as startupFromApp)
	postResetDelay            = 2 * time.Second  // after hardware reset before first command
	postMagicByteDelay        = 1 * time.Second  // after magic byte (herdsman uses 1s)
)

// Controller implements the Zigbee dongle interface using the ZNP (Z-Stack Network Processor) protocol.
// Logs use "layer" attribute: "znp" = ZNP protocol (serial, framing, commands), "zigbee" = dongle/coordinator (startup, joins, interview).
type Controller struct {
	port     *Port
	settings *Settings
	sequence uint32

	znpLog    *slog.Logger // layer=znp: protocol, serial, commands
	zigbeeLog *slog.Logger // layer=zigbee: startup, device state, joins, interview

	joinEvents chan DeviceJoinEvent
}

type Settings struct {
	Port               string
	BaudRate           int  // Default: 115200
	DisableFlowControl bool // Disable RTS/CTS flow control
	SkipHardwareReset  bool // If true, skip DTR reset before magic byte (old/znp does not reset)
	LogCommands        bool
	LogErrors          bool
	Logger             *slog.Logger // If nil, slog.Default() is used; logs get layer=znp or layer=zigbee
}

func NewController(settings Settings) (*Controller, error) {
	logger := settings.Logger
	if logger == nil {
		logger = slog.Default()
	}
	znpLog := logger.With("layer", "znp")
	zigbeeLog := logger.With("layer", "zigbee")

	callbacks := Callbacks{
		OnReadError: func(err error) ErrorHandling {
			if err == ErrInvalidFrame || err == ErrGarbage {
				if settings.LogErrors {
					zigbeeLog.Warn("read error", slog.Any("error", err))
				}
				return ErrorHandlingContinue
			}
			return ErrorHandlingPanic
		},

		OnParseError: func(err error, frame Frame) ErrorHandling {
			if err == ErrCommandInvalidFrame {
				if settings.LogErrors {
					zigbeeLog.Warn("invalid serial frame")
				}
				return ErrorHandlingContinue
			}
			if err == ErrCommandUnknownFrameHeader {
				if settings.LogErrors {
					zigbeeLog.Warn("unknown serial frame", slog.Any("frame", frame))
				}
				return ErrorHandlingContinue
			}
			return ErrorHandlingPanic
		},
	}

	if settings.LogCommands {
		callbacks.BeforeWrite = func(command interface{}) {
			znpLog.Debug("command send", slog.Any("command", command))
		}
		callbacks.AfterRead = func(command interface{}) {
			znpLog.Debug("command recv", slog.Any("command", command))
		}
	}

	port, err := NewPort(settings.Port, settings.BaudRate, settings.DisableFlowControl, callbacks, znpLog)
	if err != nil {
		return nil, err
	}

	return &Controller{
		port:       port,
		settings:   &settings,
		znpLog:     znpLog,
		zigbeeLog:  zigbeeLog,
		joinEvents: make(chan DeviceJoinEvent, 10),
	}, nil
}

// HealthCheck performs a simple communication test with the device.
// It sends a SysVersionRequest and waits for a response.
// Returns an error if communication fails.
func (c *Controller) HealthCheck(ctx context.Context) error {
	// Use a short timeout for health check
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.port.WriteCommandTimeout(SysVersionRequest{}, 5*time.Second)
		done <- err
	}()

	select {
	case <-healthCtx.Done():
		return fmt.Errorf("health check timeout: %w", healthCtx.Err())
	case err := <-done:
		if err != nil {
			return fmt.Errorf("health check failed: %w", err)
		}
		return nil
	}
}

// DeviceJoinEvent represents a device joining the network.
type DeviceJoinEvent struct {
	NetworkAddress uint16
	IEEEAddress    uint64
}

// Start initializes the controller and returns a channel of incoming messages.
func (c *Controller) Start(ctx context.Context) (<-chan types.IncomingMessage, error) {
	c.zigbeeLog.Info("starting controller", slog.String("port", c.settings.Port))

	if !c.settings.SkipHardwareReset {
		c.zigbeeLog.Debug("about to perform hardware reset")
		if err := c.port.ResetDevice(); err != nil {
			c.zigbeeLog.Warn("hardware reset failed (continuing)", slog.Any("error", err))
		} else {
			c.zigbeeLog.Debug("hardware reset completed successfully")
		}
		c.zigbeeLog.Debug("waiting for device to stabilize after reset...")
		time.Sleep(postResetDelay)
	} else {
		c.zigbeeLog.Debug("skipping hardware reset (skip-hardware-reset)")
	}

	// zigbee-herdsman skipBootloader: try SYS ping (250ms) first; if fail, magic byte + 1s + ping again (old/zigbee-herdsman src/adapter/z-stack/znp/znp.ts).
	// No unconditional magic byte — only send it when ping fails (device may already be in ZNP app mode).
	var err error
	gotVersion := false
	if _, err = c.port.WriteCommandTimeout(SysPingRequest{}, 250*time.Millisecond); err != nil {
		c.zigbeeLog.Debug("SYS ping failed (device may be in bootloader), sending magic byte", slog.Any("error", err))
		if err := c.port.WriteMagicByteForBootloader(); err != nil {
			return nil, fmt.Errorf("writing magic byte for bootloader: %w", err)
		}
		c.zigbeeLog.Debug("magic byte sent, waiting 1s (herdsman skipBootloader)", slog.Duration("delay", postMagicByteDelay))
		time.Sleep(postMagicByteDelay)
		if _, err = c.port.WriteCommandTimeout(SysPingRequest{}, 250*time.Millisecond); err != nil {
			c.zigbeeLog.Debug("SYS ping still failed after magic byte, trying version with long timeout", slog.Any("error", err))
			_, err = c.port.WriteCommandTimeout(SysVersionRequest{}, startupVersionTimeout)
			if err != nil {
				return nil, fmt.Errorf("getting system version: %w", err)
			}
			gotVersion = true
		}
	} else {
		c.zigbeeLog.Debug("SYS ping succeeded (device already in ZNP app mode)")
	}

	if !gotVersion {
		c.zigbeeLog.Debug("sending SysVersionRequest", slog.Duration("timeout", startupVersionTimeout))
		if _, err = c.port.WriteCommandTimeout(SysVersionRequest{}, startupVersionTimeout); err != nil {
			return nil, fmt.Errorf("getting system version: %w", err)
		}
	}

	// Give device a moment after SysVersionRequest before sending UtilGetDeviceInfoRequest
	// Some devices may need time to process the first command
	time.Sleep(200 * time.Millisecond)

	c.zigbeeLog.Debug("sending UtilGetDeviceInfoRequest", slog.Duration("timeout", startupDeviceInfoTimeout))
	response, err := c.port.WriteCommandTimeout(UtilGetDeviceInfoRequest{}, startupDeviceInfoTimeout)
	if err != nil {
		return nil, fmt.Errorf("getting device info: %w", err)
	}

	c.zigbeeLog.Debug("device info received, checking state")

	deviceInfo := response.(UtilGetDeviceInfoResponse)
	c.zigbeeLog.Debug("device state", slog.Any("state", deviceInfo.DeviceState), slog.Bool("is_coordinator", deviceInfo.DeviceState == DeviceStateCoordinator))
	if deviceInfo.DeviceState != DeviceStateCoordinator {
		c.zigbeeLog.Info("device not in coordinator state, starting coordinator")
		handler := c.port.RegisterOneOffHandler(ZdoStateChangeInd{})
		c.zigbeeLog.Debug("handler registered for ZdoStateChangeInd")

		_, err = c.port.WriteCommandTimeout(ZdoStartupFromAppRequest{StartDelay: 100}, startupFromAppTimeout)
		if err != nil {
			return nil, fmt.Errorf("sending startup from app: %w", err)
		}
		c.zigbeeLog.Debug("ZDO startup from app request sent, waiting for ZdoStateChangeInd", slog.Duration("timeout", startupStateChangeTimeout))

		stateChange, err := handler.Receive()
		if err != nil {
			return nil, fmt.Errorf("waiting for state change: %w", err)
		}
		c.zigbeeLog.Debug("received state change indication", slog.Any("state_change", stateChange))
	} else {
		c.zigbeeLog.Debug("device already in coordinator state, skipping startup")
	}

	// Activate endpoints to receive incoming messages.
	endpoints := []struct {
		Endpoint  uint8
		AppProfID ProfileID
	}{
		{1, ProfileHomeAutomation},
		{2, ProfileIndustrialPlantMonitoring},
		{3, ProfileCommercialBuildingAutomation},
		{4, ProfileTelecomApplications},
		{5, ProfilePersonalHomeAndHospitalCare},
		{6, ProfileAdvancedMeteringInitialtive},
		{8, ProfileHomeAutomation},
	}

	for _, endpoint := range endpoints {
		_, err = c.port.WriteCommand(AfRegisterRequest{
			Endpoint:    endpoint.Endpoint,
			AppProfID:   endpoint.AppProfID,
			AppDeviceID: 0x0005,
			LatencyReq:  LatencyReqNoLatency,
		})
		if err != nil {
			return nil, fmt.Errorf("sending register endpoint: %w", err)
		}
	}

	handler := c.port.RegisterPermanentHandler(AfIncomingMsg{})
	output := make(chan types.IncomingMessage)

	// Ensure join events channel is initialized (should already be initialized in NewController)
	if c.joinEvents == nil {
		c.joinEvents = make(chan DeviceJoinEvent, 10) // Buffered channel for join events
	}

	// Register handlers for device join events (for interview triggering)
	// These are ZDO async messages, not AF messages, so they need separate handlers
	deviceJoinHandler := c.port.RegisterPermanentHandler(ZdoEndDeviceAnnceInd{})
	tcDeviceHandler := c.port.RegisterPermanentHandler(ZdoTcDevInd{})

	go func() {
		defer close(output)
		for {
			cmd, err := handler.Receive()
			if err != nil {
				// Handler was closed or timed out
				break
			}

			message := cmd.(AfIncomingMsg)
			output <- types.IncomingMessage{
				Source: types.Address{
					Mode:  types.AddressModeNWK,
					Short: message.SrcAddr,
				},
				SourceEndpoint:      message.SrcEndpoint,
				DestinationEndpoint: message.DstEndpoint,
				ClusterID:           message.ClusterID,
				LinkQuality:         message.LinkQuality,
				Data:                message.Data,
			}
		}
	}()

	// Handle device join events and send to joinEvents channel
	go func() {
		for {
			cmd, err := deviceJoinHandler.Receive()
			if err != nil {
				break
			}
			announce := cmd.(ZdoEndDeviceAnnceInd)
			if c.settings.LogCommands {
				c.zigbeeLog.Info("device joined", slog.String("nwk", fmt.Sprintf("0x%04x", announce.NwkAddr)), slog.String("ieee", fmt.Sprintf("0x%016x", announce.IEEEAddr)))
			}
			// Send join event (non-blocking)
			if c.joinEvents != nil {
				select {
				case c.joinEvents <- DeviceJoinEvent{
					NetworkAddress: announce.NwkAddr,
					IEEEAddress:    announce.IEEEAddr,
				}:
				default:
					// Channel full, skip (shouldn't happen with buffered channel)
				}
			}
		}
	}()

	// Handle Trust Center device events (also indicate device joins)
	go func() {
		for {
			cmd, err := tcDeviceHandler.Receive()
			if err != nil {
				break
			}
			tcDev := cmd.(ZdoTcDevInd)
			if c.settings.LogCommands {
				c.zigbeeLog.Info("trust center device", slog.String("nwk", fmt.Sprintf("0x%04x", tcDev.SrcNwkAddr)), slog.String("ieee", fmt.Sprintf("0x%016x", tcDev.SrcIEEEAddr)), slog.String("parent", fmt.Sprintf("0x%04x", tcDev.ParentNwkAddr)))
			}
			// Trust Center device events also indicate a device joined
			// Send join event (non-blocking)
			if c.joinEvents != nil {
				select {
				case c.joinEvents <- DeviceJoinEvent{
					NetworkAddress: tcDev.SrcNwkAddr,
					IEEEAddress:    tcDev.SrcIEEEAddr,
				}:
				default:
					// Channel full, skip
				}
			}
		}
	}()

	return output, nil
}

// DeviceJoinEvents returns a channel of device join events.
// This channel will receive events when devices join the network.
// The channel is created when Start() is called and remains open until the controller is closed.
func (c *Controller) DeviceJoinEvents() <-chan DeviceJoinEvent {
	return c.joinEvents
}

// Send sends a message to a device on the Zigbee network.
func (c *Controller) Send(ctx context.Context, msg types.OutgoingMessage) error {
	if mode := msg.Destination.Mode; mode != types.AddressModeNWK && mode != types.AddressModeCombined {
		return fmt.Errorf("address mode not supported: %v", mode)
	}

	sequence := atomic.AddUint32(&c.sequence, 1)

	_, err := c.port.WriteCommand(AfDataRequest{
		DstAddr:        msg.Destination.Short,
		DstEndpoint:    msg.DestinationEndpoint,
		SrcEndpoint:    msg.SourceEndpoint,
		ClusterID:      msg.ClusterID,
		TransSeqNumber: uint8(sequence),
		Options:        0,
		Radius:         msg.Radius,
		Data:           msg.Data,
	})

	return err
}

// PermitJoining enables or disables device joining on the network.
func (c *Controller) PermitJoining(ctx context.Context, enabled bool) error {
	permitJoinRequest := ZdoMgmtPermitJoinRequest{
		AddrMode: 0x0f,
		DstAddr:  0xfffc, // broadcast
	}
	if enabled {
		permitJoinRequest.Duration = 0xff // turn on indefinitely
	}
	_, err := c.port.WriteCommand(permitJoinRequest)
	if err != nil {
		return fmt.Errorf("sending permit join: %w", err)
	}

	return nil
}

// GetNetworkInfo returns information about the current network state.
func (c *Controller) GetNetworkInfo(ctx context.Context) (*types.NetworkInfo, error) {
	response, err := c.port.WriteCommand(ZdoExtNwkInfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("getting network info: %w", err)
	}

	info := response.(ZdoExtNwkInfoResponse)

	deviceInfoResp, err := c.port.WriteCommand(UtilGetDeviceInfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("getting device info: %w", err)
	}
	deviceInfo := deviceInfoResp.(UtilGetDeviceInfoResponse)

	// Map ZNP DeviceState to NetworkState enum
	var networkState types.NetworkState
	switch deviceInfo.DeviceState {
	case DeviceStateCoordinator, DeviceStateRouter, DeviceStateEndDevice:
		// Device is joined to a network
		networkState = types.NetworkStateUp
	case DeviceStateInitializedNotStarted:
		// Device initialized but not started (not joined)
		networkState = types.NetworkStateNotJoined
	default:
		// Unknown state
		networkState = types.NetworkStateUnknown
	}

	return &types.NetworkInfo{
		ShortAddress:          info.ShortAddress,
		PanID:                 info.PanID,
		ParentAddress:         info.ParentAddress,
		ExtendedPanID:         info.ExtendedPanID,
		ExtendedParentAddress: info.ExtendedParentAddress,
		Channel:               uint16(info.Channel), // Convert uint8 to uint16 for NetworkInfo
		State:                 networkState,
	}, nil
}

// Z-Stack NV item IDs (ZCD_NV_*). Values may vary by Z-Stack version; these are for Z-Stack 2.x/3.x.
const (
	nvStartupOption   uint16 = 0x0003 // ZCD_NV_STARTUP_OPTION: bit 1 = CLEAR_STATE
	nvPANID           uint16 = 0x0083 // ZCD_NV_PANID, 2 bytes
	nvExtendedPANID   uint16 = 0x002D // ZCD_NV_EXTENDED_PANID, 8 bytes (Z-Stack 2.x)
	nvChannelList     uint16 = 0x0084 // ZCD_NV_CHANLIST, 4 bytes (bitmask)
	nvPrecfgKeyEnable uint16 = 0x0063 // ZCD_NV_PRECFGKEY_ENABLE, 1 byte
	nvPrecfgKey       uint16 = 0x0062 // ZCD_NV_PRECFGKEY, 16 bytes
)

// FormNetwork creates a new Zigbee network by writing NV parameters and resetting the device.
// After reset, the device will use the new PAN ID, extended PAN ID, channel, and network key.
// This implementation writes NV via SYS_OSAL_NV_WRITE then triggers a software reset and
// re-runs the startup sequence (magic byte, ZdoStartupFromApp).
func (c *Controller) FormNetwork(ctx context.Context, params types.NetworkParameters) error {
	// 1. Set CLEAR_STATE so the device clears previous network state on next boot.
	if err := c.nvWrite(nvStartupOption, 0, []byte{0x02}); err != nil {
		return fmt.Errorf("writing startup option: %w", err)
	}
	// 2. PAN ID (2 bytes, little endian)
	panIDBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(panIDBytes, params.PanID)
	if err := c.nvWrite(nvPANID, 0, panIDBytes); err != nil {
		return fmt.Errorf("writing PAN ID: %w", err)
	}
	// 3. Extended PAN ID (8 bytes, little endian)
	extPanIDBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(extPanIDBytes, params.ExtendedPanID)
	if err := c.nvWrite(nvExtendedPANID, 0, extPanIDBytes); err != nil {
		return fmt.Errorf("writing extended PAN ID: %w", err)
	}
	// 4. Channel list (4 bytes): bitmask, bit 0 = channel 11, bit 1 = channel 12, ...
	if params.Channel < 11 || params.Channel > 26 {
		return fmt.Errorf("channel must be 11-26, got %d", params.Channel)
	}
	chanMask := uint32(1 << (params.Channel - 11))
	chanListBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(chanListBytes, chanMask)
	if err := c.nvWrite(nvChannelList, 0, chanListBytes); err != nil {
		return fmt.Errorf("writing channel list: %w", err)
	}
	// 5. Preconfigured key enable = 1
	if err := c.nvWrite(nvPrecfgKeyEnable, 0, []byte{1}); err != nil {
		return fmt.Errorf("writing precfg key enable: %w", err)
	}
	// 6. Preconfigured network key (16 bytes)
	if err := c.nvWrite(nvPrecfgKey, 0, params.NetworkKey[:]); err != nil {
		return fmt.Errorf("writing precfg key: %w", err)
	}

	// 7. Register for reset indication before sending reset. The device does not send
	// an SRSP for reset; it sends SysResetInd (AREQ) when it is about to reset.
	handler := c.port.RegisterOneOffHandler(SysResetInd{})
	go func() {
		_, _ = c.port.WriteCommandTimeout(SysResetRequest{Type: 0}, 2*time.Second) // SRSP may not come; we wait for SysResetInd
	}()
	_, err := handler.Receive()
	if err != nil {
		return fmt.Errorf("waiting for reset indication: %w", err)
	}

	// 8. Re-run startup: magic byte, version, device info, ZdoStartupFromApp (zigbee-herdsman uses 40s for startupFromApp)
	time.Sleep(postResetDelay)
	if err := c.port.WriteMagicByteForBootloader(); err != nil {
		return fmt.Errorf("magic byte after reset: %w", err)
	}
	time.Sleep(postMagicByteDelay)
	if _, err := c.port.WriteCommandTimeout(SysVersionRequest{}, startupVersionTimeout); err != nil {
		return fmt.Errorf("version after reset: %w", err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := c.port.WriteCommandTimeout(UtilGetDeviceInfoRequest{}, startupDeviceInfoTimeout); err != nil {
		return fmt.Errorf("device info after reset: %w", err)
	}
	stateHandler := c.port.RegisterOneOffHandler(ZdoStateChangeInd{})
	if _, err := c.port.WriteCommandTimeout(ZdoStartupFromAppRequest{StartDelay: 100}, startupFromAppTimeout); err != nil {
		return fmt.Errorf("startup from app: %w", err)
	}
	if _, err := stateHandler.Receive(); err != nil {
		return fmt.Errorf("waiting for state change: %w", err)
	}
	return nil
}

// nvWrite writes a single NV item. Offset is 0 for full-item writes.
func (c *Controller) nvWrite(id uint16, offset uint8, value []byte) error {
	resp, err := c.port.WriteCommandTimeout(SysOsalNvWriteRequest{
		ID:     id,
		Offset: offset,
		Value:  value,
	}, 2*time.Second)
	if err != nil {
		return err
	}
	r := resp.(SysOsalNvWriteResponse)
	if r.Status != 0 {
		return fmt.Errorf("NV write id=0x%04x status=0x%02x", id, r.Status)
	}
	return nil
}

// Close closes the controller and releases resources.
func (c *Controller) Close() error {
	return c.port.Close()
}
