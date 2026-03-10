package znp

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// InterviewDevice performs a device interview to discover device capabilities.
// This follows the zigbee-herdsman interview process:
// 1. Get Node Descriptor (device type, manufacturer, capabilities)
// 2. Get Active Endpoints
// 3. For each endpoint: Get Simple Descriptor (clusters)
func (c *Controller) InterviewDevice(ctx context.Context, networkAddress uint16) (*DeviceInterviewInfo, error) {
	info := &DeviceInterviewInfo{
		NetworkAddress: networkAddress,
	}

	// Step 1: Get Node Descriptor (with retries - some devices need multiple attempts)
	var nodeDescResp ZdoNodeDescriptor
	gotNodeDesc := false
	for attempt := 0; attempt < 6; attempt++ {
		// Note: Retry delay is handled inside the loop after checking status
		// This allows us to use different delays based on error type

		// Register handler for async response BEFORE sending request
		handler := c.port.RegisterOneOffHandler(ZdoNodeDescriptor{})
		defer handler.fail() // Clean up if we exit early

		// Send Node Descriptor Request
		_, err := c.port.WriteCommandTimeout(ZdoNodeDescriptorRequest{
			DstAddr: networkAddress,
		}, 5*time.Second)

		if err != nil {
			if attempt < 5 {
				if c.settings.LogErrors {
					c.zigbeeLog.Warn("node descriptor request failed", slog.Int("attempt", attempt+1), slog.Int("max", 6), slog.Any("error", err))
				}
				continue
			}
			return nil, fmt.Errorf("node descriptor request failed after 6 attempts: %w", err)
		}

		// Wait for async response
		response, err := handler.Receive()
		if err != nil {
			if attempt < 5 {
				if c.settings.LogErrors {
					c.zigbeeLog.Warn("node descriptor response timeout", slog.Int("attempt", attempt+1), slog.Int("max", 6), slog.Any("error", err))
				}
				continue
			}
			return nil, fmt.Errorf("node descriptor response timeout after 6 attempts: %w", err)
		}

		resp := response.(ZdoNodeDescriptor)
		if resp.Status != 0 {
			// Status 0x80 (INVALID_REQTYPE) often means device isn't ready yet
			// Increase delay between retries for this specific error
			retryDelay := 500 * time.Millisecond
			if resp.Status == 0x80 && attempt < 5 {
				retryDelay = 2 * time.Second
				if c.settings.LogErrors {
					c.zigbeeLog.Warn("node descriptor status error (INVALID_REQTYPE, waiting longer)", slog.Int("attempt", attempt+1), slog.Int("max", 6), slog.Any("status", resp.Status))
				}
			} else if attempt < 5 {
				if c.settings.LogErrors {
					c.zigbeeLog.Warn("node descriptor status error", slog.Int("attempt", attempt+1), slog.Int("max", 6), slog.Any("status", resp.Status))
				}
			}
			if attempt < 5 {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("node descriptor status error: 0x%02x", resp.Status)
		}

		nodeDescResp = resp
		gotNodeDesc = true

		// Extract information from flattened node descriptor
		info.ManufacturerID = uint32(nodeDescResp.ManufacturerCode)
		info.Capabilities = &DeviceCapabilities{
			AlternatePanCoordinator: (nodeDescResp.MACCapabilityFlags & (1 << 0)) != 0,
			ReceiverOnWhenIdle:      (nodeDescResp.MACCapabilityFlags & (1 << 3)) != 0,
			SecurityCapability:      (nodeDescResp.MACCapabilityFlags & (1 << 6)) != 0,
		}

		// Determine device type from logical type
		switch nodeDescResp.LogicalType {
		case 0x00:
			info.DeviceType = "Coordinator"
		case 0x01:
			info.DeviceType = "Router"
		case 0x02:
			info.DeviceType = "EndDevice"
		default:
			info.DeviceType = "Unknown"
		}

		break // Success
	}

	if !gotNodeDesc {
		return nil, fmt.Errorf("failed to get node descriptor after 6 attempts")
	}

	// Step 2: Get Active Endpoints (with retries)
	var activeEndpoints []uint8
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		handler := c.port.RegisterOneOffHandler(ZdoActiveEP{})
		defer handler.fail()

		_, err := c.port.WriteCommandTimeout(ZdoActiveEPRequest{
			DstAddr:           networkAddress,
			NWKAddrOfInterest: networkAddress,
		}, 5*time.Second)

		if err != nil {
			if attempt < 1 {
				if c.settings.LogErrors {
					c.zigbeeLog.Warn("active endpoints request failed", slog.Int("attempt", attempt+1), slog.Int("max", 2), slog.Any("error", err))
				}
				continue
			}
			return nil, fmt.Errorf("active endpoints request failed: %w", err)
		}

		response, err := handler.Receive()
		if err != nil {
			if attempt < 1 {
				if c.settings.LogErrors {
					c.zigbeeLog.Warn("active endpoints response timeout", slog.Int("attempt", attempt+1), slog.Int("max", 2), slog.Any("error", err))
				}
				continue
			}
			return nil, fmt.Errorf("active endpoints response timeout: %w", err)
		}

		activeEPResp := response.(ZdoActiveEP)
		if activeEPResp.Status != 0 {
			if attempt < 1 {
				if c.settings.LogErrors {
					c.zigbeeLog.Warn("active endpoints status error", slog.Int("attempt", attempt+1), slog.Int("max", 2), slog.Any("status", activeEPResp.Status))
				}
				continue
			}
			return nil, fmt.Errorf("active endpoints status error: 0x%02x", activeEPResp.Status)
		}

		activeEndpoints = activeEPResp.ActiveEPs
		break // Success
	}

	if len(activeEndpoints) == 0 {
		return nil, fmt.Errorf("no active endpoints found")
	}

	// Step 3: Get Simple Descriptor for each endpoint
	info.Endpoints = make([]EndpointInfo, 0, len(activeEndpoints))
	for _, endpointID := range activeEndpoints {
		handler := c.port.RegisterOneOffHandler(ZdoSimpleDescriptor{})
		defer handler.fail()

		_, err := c.port.WriteCommandTimeout(ZdoSimpleDescriptorRequest{
			DstAddr:           networkAddress,
			NWKAddrOfInterest: networkAddress,
			Endpoint:          endpointID,
		}, 5*time.Second)

		if err != nil {
			if c.settings.LogErrors {
				c.zigbeeLog.Warn("simple descriptor request failed", slog.Uint64("endpoint", uint64(endpointID)), slog.Any("error", err))
			}
			continue
		}

		response, err := handler.Receive()
		if err != nil {
			if c.settings.LogErrors {
				c.zigbeeLog.Warn("simple descriptor response timeout", slog.Uint64("endpoint", uint64(endpointID)), slog.Any("error", err))
			}
			continue
		}

		simpleDescResp := response.(ZdoSimpleDescriptor)
		if simpleDescResp.Status != 0 {
			if c.settings.LogErrors {
				c.zigbeeLog.Warn("simple descriptor status error", slog.Uint64("endpoint", uint64(endpointID)), slog.Any("status", simpleDescResp.Status))
			}
			continue
		}

		// Extract information from flattened simple descriptor
		info.Endpoints = append(info.Endpoints, EndpointInfo{
			ID:             uint32(simpleDescResp.Endpoint),
			ProfileID:      uint32(simpleDescResp.AppProfileID),
			DeviceID:       uint32(simpleDescResp.AppDeviceID),
			DeviceVersion:  uint32(simpleDescResp.AppDeviceVer),
			InputClusters:  simpleDescResp.InClusters,
			OutputClusters: simpleDescResp.OutClusters,
		})
	}

	return info, nil
}

// DeviceInterviewInfo contains the results of a device interview.
type DeviceInterviewInfo struct {
	NetworkAddress uint16
	DeviceType     string // "Coordinator", "Router", "EndDevice", "Unknown"
	ManufacturerID uint32
	Capabilities   *DeviceCapabilities
	Endpoints      []EndpointInfo
	// Note: genBasic attributes (modelId, manufacturerName, etc.) require ZCL reads
	// which are more complex and can be added later
}

// DeviceCapabilities describes device capabilities from the node descriptor.
type DeviceCapabilities struct {
	AlternatePanCoordinator bool
	ReceiverOnWhenIdle      bool
	SecurityCapability      bool
}

// EndpointInfo describes a single endpoint and its clusters.
type EndpointInfo struct {
	ID             uint32
	ProfileID      uint32
	DeviceID       uint32
	DeviceVersion  uint32
	InputClusters  []uint16
	OutputClusters []uint16
}
