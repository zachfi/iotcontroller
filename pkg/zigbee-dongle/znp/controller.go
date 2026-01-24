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
		port:     port,
		settings: &settings,
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

	fmt.Printf("[zigbee] sending magic byte (0xEF) to skip bootloader...\n")
	err := c.port.WriteMagicByteForBootloader()
	if err != nil {
		return nil, fmt.Errorf("writing magic byte for bootloader: %w", err)
	}
	fmt.Printf("[zigbee] magic byte sent, waiting for device to process...\n")

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

	response, err := c.port.WriteCommand(UtilGetDeviceInfoRequest{})
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

	return output, nil
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

	return &types.NetworkInfo{
		ShortAddress:          info.ShortAddress,
		PanID:                 info.PanID,
		ParentAddress:         info.ParentAddress,
		ExtendedPanID:         info.ExtendedPanID,
		ExtendedParentAddress: info.ExtendedParentAddress,
		Channel:               info.Channel,
		State:                 deviceInfo.DeviceState.String(),
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
