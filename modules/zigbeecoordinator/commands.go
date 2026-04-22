package zigbeecoordinator

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	zigbeedongle "github.com/zachfi/iotcontroller/pkg/zigbee-dongle"
	iotv1proto "github.com/zachfi/iotcontroller/proto/iot/v1"
)

// ZCL frame control byte for local (cluster-specific) commands, client-to-server, no mfg specific.
const zclFrameControlLocalClientToServer = byte(0x01)

// SendCommand implements ZigbeeCommandService.SendCommand.
// It translates high-level commands into ZCL frames and transmits via the dongle.
func (z *ZigbeeCoordinator) SendCommand(ctx context.Context, req *iotv1proto.SendCommandRequest) (*iotv1proto.SendCommandResponse, error) {
	ieee := req.GetIeeeAddress()
	if ieee == "" {
		return &iotv1proto.SendCommandResponse{Error: "ieee_address required"}, nil
	}

	// Resolve IEEE address to network (short) address
	nwk, ok := z.lookupNWK(ieee)
	if !ok {
		return &iotv1proto.SendCommandResponse{
			Error: fmt.Sprintf("device %q not found in NWK map (not yet joined or interviewed)", ieee),
		}, nil
	}

	endpoint := uint8(req.GetEndpoint())
	if endpoint == 0 {
		endpoint = 1 // default endpoint
	}

	// Build ZCL frame
	var clusterID uint16
	var zclData []byte
	seq := uint8(atomic.AddUint32(&z.cmdSequence, 1))

	switch cmd := req.GetCommand().(type) {
	case *iotv1proto.SendCommandRequest_On:
		clusterID = 0x0006
		zclData = []byte{zclFrameControlLocalClientToServer, seq, 0x01}

	case *iotv1proto.SendCommandRequest_Off:
		clusterID = 0x0006
		zclData = []byte{zclFrameControlLocalClientToServer, seq, 0x00}

	case *iotv1proto.SendCommandRequest_Toggle:
		clusterID = 0x0006
		zclData = []byte{zclFrameControlLocalClientToServer, seq, 0x02}

	case *iotv1proto.SendCommandRequest_SetBrightness:
		clusterID = 0x0008
		tt := uint16(cmd.SetBrightness.GetTransitionTime())
		if tt == 0 {
			tt = 8 // 0.8s default
		}
		// MoveToLevelWithOnOff (0x04): level (uint8) + transition_time (uint16 LE)
		payload := make([]byte, 3)
		payload[0] = uint8(cmd.SetBrightness.GetLevel())
		binary.LittleEndian.PutUint16(payload[1:], tt)
		zclData = append([]byte{zclFrameControlLocalClientToServer, seq, 0x04}, payload...)

	case *iotv1proto.SendCommandRequest_SetColorTemp:
		clusterID = 0x0300
		tt := uint16(cmd.SetColorTemp.GetTransitionTime())
		if tt == 0 {
			tt = 8 // 0.8s default
		}
		// MoveToColorTemp (0x0A): color_temp_mired (uint16 LE) + transition_time (uint16 LE)
		payload := make([]byte, 4)
		binary.LittleEndian.PutUint16(payload[0:], uint16(cmd.SetColorTemp.GetColorTemperatureMired()))
		binary.LittleEndian.PutUint16(payload[2:], tt)
		zclData = append([]byte{zclFrameControlLocalClientToServer, seq, 0x0A}, payload...)

	default:
		return &iotv1proto.SendCommandResponse{Error: "no command specified"}, nil
	}

	msg := zigbeedongle.OutgoingMessage{
		Destination: zigbeedongle.Address{
			Mode:  zigbeedongle.AddressModeNWK,
			Short: nwk,
		},
		SourceEndpoint:      1,
		DestinationEndpoint: endpoint,
		ClusterID:           clusterID,
		Radius:              30,
		Data:                zclData,
	}

	if err := z.dongle.Send(ctx, msg); err != nil {
		z.logger.Error("failed to send Zigbee command",
			slog.String("ieee", ieee),
			slog.String("cluster", fmt.Sprintf("0x%04x", clusterID)),
			slog.String("error", err.Error()),
		)
		return &iotv1proto.SendCommandResponse{Error: err.Error()}, nil
	}

	z.logger.Debug("sent Zigbee command",
		slog.String("ieee", ieee),
		slog.String("nwk", fmt.Sprintf("0x%04x", nwk)),
		slog.String("cluster", fmt.Sprintf("0x%04x", clusterID)),
	)

	return &iotv1proto.SendCommandResponse{Success: true}, nil
}

// lookupNWK resolves an IEEE address to a network address.
// The ieee string may or may not have a "0x" prefix.
func (z *ZigbeeCoordinator) lookupNWK(ieee string) (uint16, bool) {
	normalized := strings.ToLower(strings.TrimPrefix(ieee, "0x"))

	z.nwkToIEEEMux.RLock()
	nwk, ok := z.ieeeToNWK[normalized]
	z.nwkToIEEEMux.RUnlock()

	return nwk, ok
}
