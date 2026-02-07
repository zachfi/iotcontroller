package znp

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

type Controller struct {
	port     *Port
	settings *Settings
	sequence uint32

	// Device join events channel (initialized in NewController, remains open until controller is closed)
	joinEvents chan DeviceJoinEvent
}

type Settings struct {
	Port               string
	BaudRate           int  // Default: 115200
	DisableFlowControl bool // Disable RTS/CTS flow control
	LogCommands        bool
	LogErrors          bool
}

func NewController(settings Settings) (*Controller, error) {
	callbacks := Callbacks{
		OnReadError: func(err error) ErrorHandling {
			if err == ErrInvalidFrame || err == ErrGarbage {
				if settings.LogErrors {
					// TODO: use proper logger
					fmt.Printf("[zigbee] %v\n", err)
				}
				return ErrorHandlingContinue
			}
			return ErrorHandlingPanic
		},

		OnParseError: func(err error, frame Frame) ErrorHandling {
			if err == ErrCommandInvalidFrame {
				if settings.LogErrors {
					fmt.Printf("[zigbee] invalid serial frame\n")
				}
				return ErrorHandlingContinue
			}
			if err == ErrCommandUnknownFrameHeader {
				if settings.LogErrors {
					fmt.Printf("[zigbee] unknown serial frame: %v\n", frame)
				}
				return ErrorHandlingContinue
			}
			return ErrorHandlingPanic
		},
	}

	if settings.LogCommands {
		callbacks.BeforeWrite = func(command interface{}) {
			fmt.Printf("--> %T%+v\n", command, command)
		}
		callbacks.AfterRead = func(command interface{}) {
			fmt.Printf("<-- %T%+v\n", command, command)
		}
	}

	port, err := NewPort(settings.Port, settings.BaudRate, settings.DisableFlowControl, callbacks)
	if err != nil {
		return nil, err
	}

	return &Controller{
		port:       port,
		settings:   &settings,
		joinEvents: make(chan DeviceJoinEvent, 10), // Initialize join events channel
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
	fmt.Printf("[zigbee] starting controller on port %s...\n", c.settings.Port)
	fmt.Printf("[zigbee] DEBUG: About to perform hardware reset...\n")

	// Perform hardware reset first (similar to zigbee-herdsman and EZSP)
	// This ensures the device is in a known state before initialization
	fmt.Printf("[zigbee] performing hardware reset (DTR toggle)...\n")
	if err := c.port.ResetDevice(); err != nil {
		// Reset failure is not fatal - device might not support it
		fmt.Printf("[zigbee] Warning: hardware reset failed (continuing): %v\n", err)
	} else {
		fmt.Printf("[zigbee] hardware reset completed successfully\n")
	}

	// Give device time to stabilize after reset
	// Z-Stack 3.x devices may need more time to boot
	fmt.Printf("[zigbee] waiting for device to stabilize after reset...\n")
	time.Sleep(500 * time.Millisecond) // Increased from 100ms to 500ms for Z-Stack 3.x

	// Note: When RTS/CTS flow control is enabled, the kernel should automatically
	// manage RTS based on buffer availability. Manual assertion may conflict with
	// the kernel's automatic management. However, some devices may need RTS asserted
	// initially. If you're not receiving data with flow control enabled, try:
	// 1. Disabling flow control (if device doesn't support it)
	// 2. Checking RTS/CTS wiring
	// 3. Verifying device firmware supports RTS/CTS flow control

	fmt.Printf("[zigbee] sending magic byte (0xEF) to skip bootloader...\n")
	err := c.port.WriteMagicByteForBootloader()
	if err != nil {
		return nil, fmt.Errorf("writing magic byte for bootloader: %w", err)
	}
	fmt.Printf("[zigbee] magic byte sent, proceeding immediately (device may send SYS_RESET_IND asynchronously)...\n")

	// Note: The device MAY send SYS_RESET_IND after the magic byte, but we don't wait for it.
	// The continuous read loop will handle it if it arrives. We proceed immediately like the vendor code.
	// Z-Stack 3.x may need time to process the magic byte and exit bootloader
	time.Sleep(500 * time.Millisecond)

	// This command is used to test communication with the device.
	// The response may be slow if the device has to finish booting,
	// therefore we use a custom timeout value.
	// Z-Stack 3.x might need even longer, so we use 15 seconds
	fmt.Printf("[zigbee] sending SysVersionRequest (timeout: 15s)...\n")
	_, err = c.port.WriteCommandTimeout(SysVersionRequest{}, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("getting system version: %w", err)
	}

	// Give device a moment after SysVersionRequest before sending UtilGetDeviceInfoRequest
	// Some devices may need time to process the first command
	time.Sleep(200 * time.Millisecond)

	fmt.Printf("[zigbee] sending UtilGetDeviceInfoRequest (timeout: 10s)...\n")
	// Use longer timeout - some devices are slow to respond to UtilGetDeviceInfoRequest
	response, err := c.port.WriteCommandTimeout(UtilGetDeviceInfoRequest{}, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("getting device info: %w", err)
	}

	fmt.Printf("[zigbee] device info received, checking state...\n")

	deviceInfo := response.(UtilGetDeviceInfoResponse)
	fmt.Printf("[zigbee] Device state: %v (Coordinator=%v)\n", deviceInfo.DeviceState, DeviceStateCoordinator)
	if deviceInfo.DeviceState != DeviceStateCoordinator {
		fmt.Printf("[zigbee] Device not in coordinator state, starting coordinator...\n")
		// Register handler BEFORE sending the command
		handler := c.port.RegisterOneOffHandler(ZdoStateChangeInd{})
		fmt.Printf("[zigbee] Handler registered for ZdoStateChangeInd\n")

		_, err = c.port.WriteCommand(ZdoStartupFromAppRequest{StartDelay: 100})
		if err != nil {
			return nil, fmt.Errorf("sending startup from app: %w", err)
		}
		fmt.Printf("[zigbee] ZdoStartupFromAppRequest sent, waiting for ZdoStateChangeInd (timeout: 30s)...\n")

		// Use a longer timeout for state change - Z-Stack 3.x might need more time
		stateChange, err := handler.Receive()
		if err != nil {
			return nil, fmt.Errorf("waiting for state change: %w", err)
		}
		fmt.Printf("[zigbee] Received state change indication: %+v\n", stateChange)
	} else {
		fmt.Printf("[zigbee] Device already in coordinator state, skipping startup\n")
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
				fmt.Printf("[zigbee] Device joined: NWK=0x%04x, IEEE=0x%016x\n", announce.NwkAddr, announce.IEEEAddr)
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
				fmt.Printf("[zigbee] Trust Center device: NWK=0x%04x, IEEE=0x%016x, Parent=0x%04x\n",
					tcDev.SrcNwkAddr, tcDev.SrcIEEEAddr, tcDev.ParentNwkAddr)
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

// FormNetwork creates a new Zigbee network with the specified parameters.
// For Z-Stack, this requires setting NV memory parameters before calling ZdoStartupFromApp.
// This is not yet implemented - Z-Stack network formation is more complex than Ember.
func (c *Controller) FormNetwork(ctx context.Context, params types.NetworkParameters) error {
	// TODO: Implement Z-Stack network formation
	// This requires:
	// 1. Setting ZCD_NV_PANID in NV memory
	// 2. Setting ZCD_NV_EXTENDED_PANID in NV memory
	// 3. Setting ZCD_NV_CHANLIST in NV memory
	// 4. Setting ZCD_NV_PRECFGKEY_ENABLE and ZCD_NV_PRECFGKEY in NV memory
	// 5. Calling ZdoStartupFromApp which will form the network
	return fmt.Errorf("ZNP FormNetwork not yet implemented - Z-Stack requires NV memory configuration")
}

// Close closes the controller and releases resources.
func (c *Controller) Close() error {
	return c.port.Close()
}
