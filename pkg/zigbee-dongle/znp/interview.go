package znp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// InterviewDevice performs a device interview to discover device capabilities.
// Follows the zigbee-herdsman interview process:
//
//  1. Get Node Descriptor (device type, manufacturer, capabilities)
//  2. Get Active Endpoints
//  3. For each endpoint: Get Simple Descriptor (clusters)
//
// Each step is best-effort: a failure in one step does NOT abort the
// remaining steps, because non-compliant firmware (most notoriously Tuya
// and some Sonoff devices) commonly rejects ZDO node-descriptor with
// status 0x80 (INVALID_REQTYPE) but still answers active-endpoints and
// simple-descriptor queries. The 2026-05-15 closet SNZB-02 pairing is
// the canonical case: node descriptor failed all 6 retries, yet the
// device immediately broadcast attribute reports on the same channel.
// Under the previous always-error behavior the entire interview was
// scrapped, the Device CR landed empty, and the operator had to
// hand-patch ieee_address / network_address / type. With best-effort
// the same join now collects whatever it can in one pass; what remains
// missing can be filled by a re-interview annotation (see Bug 3) or by
// future ZCL Basic cluster fallback (cluster 0x0000 attribute reads).
//
// Returns (info, nil) on partial or full success. Returns (nil, err)
// only when every step failed in a way the caller would prefer not to
// see a half-empty struct for. info.Endpoints == nil && ManufacturerID
// == 0 is the conventional "we learned nothing" signal — the caller
// can promote that to INTERVIEW_STATE_FAILED.
func (c *Controller) InterviewDevice(ctx context.Context, networkAddress uint16) (*types.DeviceInterviewInfo, error) {
	info := &types.DeviceInterviewInfo{
		NetworkAddress: networkAddress,
	}

	// Step 1: Get Node Descriptor (with retries — best effort).
	// Failure modes captured in zigbeeLog at warn level so the operator
	// can correlate the partial result with the failed step.
	//
	// Per-attempt budget capped at 5s (not the 40s state-change default):
	// 6 silent attempts at 40s would burn ~4 minutes before the
	// best-effort code reached the active-endpoints / Basic-cluster
	// steps. At 5s × 6 the worst-case is ~30s, which fits comfortably
	// inside the typical sleepy-device awake window and leaves budget
	// for the Basic-cluster read on join.
	const nodeDescAttemptTimeout = 5 * time.Second
	var nodeDescResp ZdoNodeDescriptor
	gotNodeDesc := false
	for attempt := 0; attempt < 6; attempt++ {
		handler := c.port.RegisterOneOffHandlerWithTimeout(ZdoNodeDescriptor{}, nodeDescAttemptTimeout)
		defer handler.fail() // Clean up if we exit early

		_, err := c.port.WriteCommandTimeout(ZdoNodeDescriptorRequest{
			DstAddr: networkAddress,
		}, 5*time.Second)
		if err != nil {
			if c.settings.LogErrors {
				c.zigbeeLog.Warn("node descriptor request failed", slog.Int("attempt", attempt+1), slog.Int("max", 6), slog.Any("error", err))
			}
			continue
		}

		response, err := handler.Receive()
		if err != nil {
			if c.settings.LogErrors {
				c.zigbeeLog.Warn("node descriptor response timeout", slog.Int("attempt", attempt+1), slog.Int("max", 6), slog.Any("error", err))
			}
			continue
		}

		resp := response.(ZdoNodeDescriptor)
		if resp.Status != 0 {
			// 0x80 INVALID_REQTYPE is the common "device isn't ready
			// yet OR doesn't support this query" code from Tuya
			// firmware — wait longer between retries.
			retryDelay := 500 * time.Millisecond
			if resp.Status == 0x80 {
				retryDelay = 2 * time.Second
				if c.settings.LogErrors {
					c.zigbeeLog.Warn("node descriptor status error (INVALID_REQTYPE, waiting longer)", slog.Int("attempt", attempt+1), slog.Int("max", 6), slog.Any("status", resp.Status))
				}
			} else if c.settings.LogErrors {
				c.zigbeeLog.Warn("node descriptor status error", slog.Int("attempt", attempt+1), slog.Int("max", 6), slog.Any("status", resp.Status))
			}
			if attempt < 5 {
				time.Sleep(retryDelay)
			}
			continue
		}

		nodeDescResp = resp
		gotNodeDesc = true

		info.ManufacturerID = uint32(nodeDescResp.ManufacturerCode)
		info.Capabilities = &types.DeviceCapabilities{
			AlternatePanCoordinator: (nodeDescResp.MACCapabilityFlags & (1 << 0)) != 0,
			ReceiverOnWhenIdle:      (nodeDescResp.MACCapabilityFlags & (1 << 3)) != 0,
			SecurityCapability:      (nodeDescResp.MACCapabilityFlags & (1 << 6)) != 0,
		}

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

		break
	}

	if !gotNodeDesc && c.settings.LogErrors {
		// Don't bail — keep going for active endpoints. Some devices
		// answer EP queries even when they refuse node descriptor.
		c.zigbeeLog.Warn("node descriptor unavailable; continuing with active-endpoints query",
			slog.String("network_address", fmt.Sprintf("0x%04x", networkAddress)))
	}

	// Step 2: Get Active Endpoints (with retries — best effort).
	// Same 5s per-attempt cap rationale as Step 1.
	const activeEPAttemptTimeout = 5 * time.Second
	var activeEndpoints []uint8
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		handler := c.port.RegisterOneOffHandlerWithTimeout(ZdoActiveEP{}, activeEPAttemptTimeout)
		defer handler.fail()

		_, err := c.port.WriteCommandTimeout(ZdoActiveEPRequest{
			DstAddr:           networkAddress,
			NWKAddrOfInterest: networkAddress,
		}, 5*time.Second)
		if err != nil {
			if c.settings.LogErrors {
				c.zigbeeLog.Warn("active endpoints request failed", slog.Int("attempt", attempt+1), slog.Int("max", 2), slog.Any("error", err))
			}
			continue
		}

		response, err := handler.Receive()
		if err != nil {
			if c.settings.LogErrors {
				c.zigbeeLog.Warn("active endpoints response timeout", slog.Int("attempt", attempt+1), slog.Int("max", 2), slog.Any("error", err))
			}
			continue
		}

		activeEPResp := response.(ZdoActiveEP)
		if activeEPResp.Status != 0 {
			if c.settings.LogErrors {
				c.zigbeeLog.Warn("active endpoints status error", slog.Int("attempt", attempt+1), slog.Int("max", 2), slog.Any("status", activeEPResp.Status))
			}
			continue
		}

		activeEndpoints = activeEPResp.ActiveEPs
		break
	}

	if len(activeEndpoints) == 0 {
		// Nothing more we can do; return whatever was collected so far
		// (possibly only ManufacturerID from a successful node desc).
		if c.settings.LogErrors {
			c.zigbeeLog.Warn("no active endpoints discovered; returning partial interview",
				slog.String("network_address", fmt.Sprintf("0x%04x", networkAddress)),
				slog.Bool("got_node_desc", gotNodeDesc))
		}
		return info, nil
	}

	// Step 3: Get Simple Descriptor for each endpoint.
	// Single attempt per endpoint at the same 5s receive budget as the
	// earlier steps; an endpoint that misses its slot is logged and the
	// next endpoint still gets a fair shot, which is the best-effort
	// contract this function documents.
	const simpleDescAttemptTimeout = 5 * time.Second
	info.Endpoints = make([]types.EndpointInfo, 0, len(activeEndpoints))
	for _, endpointID := range activeEndpoints {
		handler := c.port.RegisterOneOffHandlerWithTimeout(ZdoSimpleDescriptor{}, simpleDescAttemptTimeout)
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
		info.Endpoints = append(info.Endpoints, types.EndpointInfo{
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

// Type aliases for backward compatibility within the znp package.
// The canonical definitions are in pkg/zigbee-dongle/types.
type (
	DeviceInterviewInfo = types.DeviceInterviewInfo
	DeviceCapabilities  = types.DeviceCapabilities
	EndpointInfo        = types.EndpointInfo
)
