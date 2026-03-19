package ember

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

// ZDP cluster IDs for device interview (standard Zigbee spec, not ZNP-specific)
const (
	zdpNodeDescriptorRequest  = uint16(0x0002)
	zdpNodeDescriptorResponse = uint16(0x8002)
	zdpActiveEPRequest        = uint16(0x0005)
	zdpActiveEPResponse       = uint16(0x8005)
	zdpSimpleDescRequest      = uint16(0x0004)
	zdpSimpleDescResponse     = uint16(0x8004)
)

// InterviewDevice performs a ZDO device interview to discover device capabilities.
// It sends ZDO requests via EZSP_SEND_UNICAST (ZDP profile 0x0000) and waits
// for responses via EZSP_INCOMING_MESSAGE_HANDLER.
func (c *Controller) InterviewDevice(ctx context.Context, networkAddress uint16) (*types.DeviceInterviewInfo, error) {
	info := &types.DeviceInterviewInfo{
		NetworkAddress: networkAddress,
	}

	// Step 1: Node Descriptor
	nodeDesc, err := c.zdoNodeDescriptor(ctx, networkAddress)
	if err != nil {
		return nil, fmt.Errorf("node descriptor: %w", err)
	}

	// NodeDescriptor byte 0 bits[2:0] = logical type
	logicalType := nodeDesc[0] & 0x07
	switch logicalType {
	case 0x00:
		info.DeviceType = "Coordinator"
	case 0x01:
		info.DeviceType = "Router"
	case 0x02:
		info.DeviceType = "EndDevice"
	default:
		info.DeviceType = "Unknown"
	}

	// NodeDescriptor byte 2 = MAC capability flags
	macCapFlags := nodeDesc[2]
	info.Capabilities = &types.DeviceCapabilities{
		AlternatePanCoordinator: (macCapFlags & (1 << 0)) != 0,
		ReceiverOnWhenIdle:      (macCapFlags & (1 << 3)) != 0,
		SecurityCapability:      (macCapFlags & (1 << 6)) != 0,
	}
	// NodeDescriptor bytes 3-4 = manufacturer code (LE)
	if len(nodeDesc) >= 5 {
		info.ManufacturerID = uint32(binary.LittleEndian.Uint16(nodeDesc[3:5]))
	}

	// Step 2: Active Endpoints
	endpoints, err := c.zdoActiveEndpoints(ctx, networkAddress)
	if err != nil {
		return nil, fmt.Errorf("active endpoints: %w", err)
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no active endpoints found")
	}

	// Step 3: Simple Descriptor for each endpoint
	info.Endpoints = make([]types.EndpointInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		epInfo, err := c.zdoSimpleDescriptor(ctx, networkAddress, ep)
		if err != nil {
			if c.settings.LogErrors {
				c.log.Warn("simple descriptor failed, skipping endpoint",
					slog.Int("endpoint", int(ep)),
					slog.String("error", err.Error()),
				)
			}
			continue
		}
		info.Endpoints = append(info.Endpoints, *epInfo)
	}

	return info, nil
}

// sendZDORequest sends a ZDP unicast via EZSP_SEND_UNICAST with ZDP profile (0x0000).
// ZDO uses endpoint 0 for both source and destination.
func (c *Controller) sendZDORequest(dst uint16, clusterID uint16, payload []byte) error {
	// EZSP_SEND_UNICAST parameters:
	//   type(1) indexOrDestination(2LE) apsFrame(11) messageTag(1) messageLength(1) message[...]
	// EmberApsFrame: profileId(2LE) clusterId(2LE) srcEp(1) dstEp(1) options(2LE) groupId(2LE) seq(1)
	params := make([]byte, 0, 16+len(payload))
	params = append(params, 0x00)                            // type = EMBER_OUTGOING_DIRECT
	params = append(params, byte(dst), byte(dst>>8))         // indexOrDestination = NWK address
	params = append(params, 0x00, 0x00)                      // profileId = 0x0000 (ZDP)
	params = append(params, byte(clusterID), byte(clusterID>>8))
	params = append(params, 0x00)       // srcEndpoint = 0
	params = append(params, 0x00)       // dstEndpoint = 0
	params = append(params, 0x00, 0x00) // options = 0
	params = append(params, 0x00, 0x00) // groupId = 0
	params = append(params, 0x00)       // APS sequence (NCP assigns)
	params = append(params, 0x00)       // messageTag
	params = append(params, uint8(len(payload)))
	params = append(params, payload...)

	resp, err := c.sendEZSPCommand(EZSP_SEND_UNICAST, params, 5*time.Second)
	if err != nil {
		return fmt.Errorf("SEND_UNICAST: %w", err)
	}
	if len(resp.Parameters) < 1 {
		return fmt.Errorf("SEND_UNICAST response too short")
	}
	if resp.Parameters[0] != 0x00 {
		return fmt.Errorf("SEND_UNICAST status: 0x%02X", resp.Parameters[0])
	}
	return nil
}

// zdoNodeDescriptor sends a Node Descriptor Request (cluster 0x0002) and returns
// the raw 13-byte NodeDescriptor field from the response (cluster 0x8002).
func (c *Controller) zdoNodeDescriptor(ctx context.Context, nwkAddr uint16) ([]byte, error) {
	const zdpTimeout = 10 * time.Second
	const maxAttempts = 3

	ch := c.registerZDOWaiter(zdpNodeDescriptorResponse)
	defer c.removeZDOWaiter(zdpNodeDescriptorResponse)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}

		// ZDP payload: seq(1) + NWKAddrOfInterest(2LE)
		payload := []byte{byte(attempt), byte(nwkAddr), byte(nwkAddr >> 8)}
		if err := c.sendZDORequest(nwkAddr, zdpNodeDescriptorRequest, payload); err != nil {
			if attempt < maxAttempts-1 {
				continue
			}
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(zdpTimeout):
			if attempt < maxAttempts-1 {
				continue
			}
			return nil, fmt.Errorf("node descriptor response timeout")
		case data := <-ch:
			// ZDP response: seq(1) status(1) NWKAddrOfInterest(2LE) NodeDescriptor(13)
			if len(data) < 4 {
				return nil, fmt.Errorf("node descriptor response too short: %d bytes", len(data))
			}
			status := data[1]
			if status != 0x00 {
				if attempt < maxAttempts-1 {
					continue
				}
				return nil, fmt.Errorf("node descriptor status: 0x%02X", status)
			}
			if len(data) < 17 {
				return nil, fmt.Errorf("node descriptor payload too short: %d bytes", len(data))
			}
			return data[4:17], nil // 13-byte NodeDescriptor
		}
	}
	return nil, fmt.Errorf("node descriptor failed after %d attempts", maxAttempts)
}

// zdoActiveEndpoints sends an Active Endpoints Request (cluster 0x0005) and returns
// the list of active endpoint IDs from the response (cluster 0x8005).
func (c *Controller) zdoActiveEndpoints(ctx context.Context, nwkAddr uint16) ([]uint8, error) {
	const zdpTimeout = 10 * time.Second

	ch := c.registerZDOWaiter(zdpActiveEPResponse)
	defer c.removeZDOWaiter(zdpActiveEPResponse)

	// ZDP payload: seq(1) + NWKAddrOfInterest(2LE)
	payload := []byte{0x00, byte(nwkAddr), byte(nwkAddr >> 8)}
	if err := c.sendZDORequest(nwkAddr, zdpActiveEPRequest, payload); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(zdpTimeout):
		return nil, fmt.Errorf("active endpoints response timeout")
	case data := <-ch:
		// ZDP response: seq(1) status(1) NWKAddrOfInterest(2LE) endpointCount(1) endpoints[...]
		if len(data) < 5 {
			return nil, fmt.Errorf("active endpoints response too short: %d bytes", len(data))
		}
		status := data[1]
		if status != 0x00 {
			return nil, fmt.Errorf("active endpoints status: 0x%02X", status)
		}
		count := int(data[4])
		if len(data) < 5+count {
			return nil, fmt.Errorf("active endpoints list truncated: expected %d, got %d bytes", 5+count, len(data))
		}
		eps := make([]uint8, count)
		copy(eps, data[5:5+count])
		return eps, nil
	}
}

// zdoSimpleDescriptor sends a Simple Descriptor Request (cluster 0x0004) and returns
// the parsed EndpointInfo from the response (cluster 0x8004).
func (c *Controller) zdoSimpleDescriptor(ctx context.Context, nwkAddr uint16, endpoint uint8) (*types.EndpointInfo, error) {
	const zdpTimeout = 10 * time.Second

	ch := c.registerZDOWaiter(zdpSimpleDescResponse)
	defer c.removeZDOWaiter(zdpSimpleDescResponse)

	// ZDP payload: seq(1) + NWKAddrOfInterest(2LE) + endpoint(1)
	payload := []byte{0x00, byte(nwkAddr), byte(nwkAddr >> 8), endpoint}
	if err := c.sendZDORequest(nwkAddr, zdpSimpleDescRequest, payload); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(zdpTimeout):
		return nil, fmt.Errorf("simple descriptor timeout for endpoint %d", endpoint)
	case data := <-ch:
		// ZDP response: seq(1) status(1) NWKAddrOfInterest(2LE) length(1) SimpleDescriptor[...]
		// SimpleDescriptor: epID(1) profileId(2LE) deviceId(2LE) deviceVer(1) inCount(1) inClusters... outCount(1) outClusters...
		if len(data) < 5 {
			return nil, fmt.Errorf("simple descriptor response too short: %d bytes", len(data))
		}
		status := data[1]
		if status != 0x00 {
			return nil, fmt.Errorf("simple descriptor status: 0x%02X", status)
		}
		// data[4] = SimpleDescriptor length; data[5+] = SimpleDescriptor body
		if len(data) < 6 {
			return nil, fmt.Errorf("simple descriptor too short")
		}
		sd := data[5:]
		if len(sd) < 8 {
			return nil, fmt.Errorf("simple descriptor body too short: %d bytes", len(sd))
		}
		epID := sd[0]
		profileID := binary.LittleEndian.Uint16(sd[1:3])
		deviceID := binary.LittleEndian.Uint16(sd[3:5])
		deviceVer := sd[5] & 0x0F

		inCount := int(sd[6])
		off := 7
		if len(sd) < off+inCount*2+1 {
			return nil, fmt.Errorf("simple descriptor input clusters truncated")
		}
		inputClusters := make([]uint16, inCount)
		for i := range inCount {
			inputClusters[i] = binary.LittleEndian.Uint16(sd[off+i*2 : off+i*2+2])
		}
		off += inCount * 2

		outCount := int(sd[off])
		off++
		if len(sd) < off+outCount*2 {
			return nil, fmt.Errorf("simple descriptor output clusters truncated")
		}
		outputClusters := make([]uint16, outCount)
		for i := range outCount {
			outputClusters[i] = binary.LittleEndian.Uint16(sd[off+i*2 : off+i*2+2])
		}

		return &types.EndpointInfo{
			ID:             uint32(epID),
			ProfileID:      uint32(profileID),
			DeviceID:       uint32(deviceID),
			DeviceVersion:  uint32(deviceVer),
			InputClusters:  inputClusters,
			OutputClusters: outputClusters,
		}, nil
	}
}
