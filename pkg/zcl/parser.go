package zcl

import (
	"fmt"
	"log/slog"

	"github.com/shimmeringbee/bytecodec"
	"github.com/shimmeringbee/bytecodec/bitbuffer"
	"github.com/shimmeringbee/zigbee"
	"github.com/shimmeringbee/zcl"
	"github.com/shimmeringbee/zcl/commands/global"
	"github.com/shimmeringbee/zcl/commands/local/onoff"
	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

// Parser parses ZCL frames from raw bytes into proto messages.
type Parser struct {
	registry *zcl.CommandRegistry
	logger   *slog.Logger
}

// NewParser creates a new ZCL parser with registered commands.
func NewParser(logger *slog.Logger) *Parser {
	registry := zcl.NewCommandRegistry()
	
	// Register global commands
	global.Register(registry)
	
	// Register local commands
	onoff.Register(registry)
	// TODO: Add more clusters as needed
	
	return &Parser{
		registry: registry,
		logger:   logger.With("component", "zcl-parser"),
	}
}

// ParseMessage parses raw ZCL data bytes into a proto ZclMessage.
// Returns nil if parsing fails (non-ZCL message or parse error).
func (p *Parser) ParseMessage(
	sourceIEEE string,
	sourceNetwork uint32,
	sourceEndpoint uint8,
	destinationEndpoint uint8,
	clusterID uint16,
	linkQuality uint8,
	data []byte,
) (*zclv1proto.ZclMessage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	// Create ApplicationMessage for shimmeringbee/zcl
	appMsg := zigbee.ApplicationMessage{
		ClusterID:           zigbee.ClusterID(clusterID),
		SourceEndpoint:      zigbee.Endpoint(sourceEndpoint),
		DestinationEndpoint: zigbee.Endpoint(destinationEndpoint),
		Data:                data,
	}

	// Parse ZCL frame
	zclMsg, err := p.registry.Unmarshal(appMsg)
	if err != nil {
		// Not a ZCL message or parse error - return nil to indicate fallback to raw
		p.logger.Debug("Failed to parse ZCL message",
			slog.String("error", err.Error()),
			slog.String("cluster_id", fmt.Sprintf("0x%04x", clusterID)),
			slog.String("data", fmt.Sprintf("%x", data)),
		)
		return nil, fmt.Errorf("not a ZCL message: %w", err)
	}

	// Convert to proto
	protoMsg, err := p.convertToProto(zclMsg, sourceIEEE, sourceNetwork, clusterID, linkQuality)
	if err != nil {
		return nil, fmt.Errorf("converting to proto: %w", err)
	}

	return protoMsg, nil
}

// convertToProto converts a shimmeringbee/zcl Message to proto ZclMessage.
func (p *Parser) convertToProto(
	zclMsg zcl.Message,
	sourceIEEE string,
	sourceNetwork uint32,
	clusterID uint16,
	linkQuality uint8,
) (*zclv1proto.ZclMessage, error) {
	// Build frame control
	frameControl := &zclv1proto.ZclFrameControl{
		FrameType:           p.convertFrameType(zclMsg.FrameType),
		Direction:           p.convertDirection(zclMsg.Direction),
		ManufacturerSpecific: zclMsg.isManufacturerSpecific(),
		DisableDefaultResponse: false, // TODO: Extract from frame control byte
	}

	// Build frame
	frame := &zclv1proto.ZclFrame{
		FrameControl:       frameControl,
		TransactionSequence: uint32(zclMsg.TransactionSequence),
		ManufacturerCode:   uint32(zclMsg.Manufacturer),
	}

	// Convert command based on type
	command, err := p.convertCommand(zclMsg, clusterID)
	if err != nil {
		return nil, fmt.Errorf("converting command: %w", err)
	}
	frame.Command = command

	// Get cluster name
	clusterName := p.getClusterName(clusterID)

	// Build message
	msg := &zclv1proto.ZclMessage{
		SourceIeee:          sourceIEEE,
		SourceNetwork:       sourceNetwork,
		SourceEndpoint:      uint32(zclMsg.SourceEndpoint),
		DestinationEndpoint: uint32(zclMsg.DestinationEndpoint),
		ClusterId:           uint32(clusterID),
		ClusterName:         clusterName,
		Frame:               frame,
		LinkQuality:         uint32(linkQuality),
		Timestamp:           0, // TODO: Add timestamp
	}

	return msg, nil
}

// convertFrameType converts shimmeringbee FrameType to proto.
func (p *Parser) convertFrameType(ft zcl.FrameType) zclv1proto.ZclFrameType {
	switch ft {
	case zcl.FrameGlobal:
		return zclv1proto.ZclFrameType_ZCL_FRAME_TYPE_GLOBAL
	case zcl.FrameLocal:
		return zclv1proto.ZclFrameType_ZCL_FRAME_TYPE_LOCAL
	default:
		return zclv1proto.ZclFrameType_ZCL_FRAME_TYPE_UNSPECIFIED
	}
}

// convertDirection converts shimmeringbee Direction to proto.
func (p *Parser) convertDirection(d zcl.Direction) zclv1proto.ZclDirection {
	switch d {
	case zcl.ClientToServer:
		return zclv1proto.ZclDirection_ZCL_DIRECTION_CLIENT_TO_SERVER
	case zcl.ServerToClient:
		return zclv1proto.ZclDirection_ZCL_DIRECTION_SERVER_TO_CLIENT
	default:
		return zclv1proto.ZclDirection_ZCL_DIRECTION_UNSPECIFIED
	}
}

// convertCommand converts shimmeringbee command to proto ZclCommand.
func (p *Parser) convertCommand(zclMsg zcl.Message, clusterID uint16) (*zclv1proto.ZclCommand, error) {
	cmd := &zclv1proto.ZclCommand{}

	switch zclMsg.FrameType {
	case zcl.FrameGlobal:
		// Global command
		switch c := zclMsg.Command.(type) {
		case *global.ReportAttributes:
			protoReport, err := p.convertReportAttributes(c)
			if err != nil {
				return nil, err
			}
			cmd.Command = &zclv1proto.ZclCommand_GlobalReportAttributes{
				GlobalReportAttributes: protoReport,
			}
		case *global.ReadAttributes:
			protoRead, err := p.convertReadAttributes(c)
			if err != nil {
				return nil, err
			}
			cmd.Command = &zclv1proto.ZclCommand_GlobalReadAttributes{
				GlobalReadAttributes: protoRead,
			}
		case *global.ReadAttributesResponse:
			protoReadResp, err := p.convertReadAttributesResponse(c)
			if err != nil {
				return nil, err
			}
			cmd.Command = &zclv1proto.ZclCommand_GlobalReadAttributesResponse{
				GlobalReadAttributesResponse: protoReadResp,
			}
		default:
			return nil, fmt.Errorf("unsupported global command: %T", c)
		}

	case zcl.FrameLocal:
		// Local command - cluster-specific
		switch clusterID {
		case 0x0006: // genOnOff
			switch c := zclMsg.Command.(type) {
			case *onoff.Toggle:
				cmd.Command = &zclv1proto.ZclCommand_GenOnoffToggle{
					GenOnoffToggle: &zclv1proto.GenOnOffToggle{},
				}
			case *onoff.On:
				cmd.Command = &zclv1proto.ZclCommand_GenOnoffOn{
					GenOnoffOn: &zclv1proto.GenOnOffOn{},
				}
			case *onoff.Off:
				cmd.Command = &zclv1proto.ZclCommand_GenOnoffOff{
					GenOnoffOff: &zclv1proto.GenOnOffOff{},
				}
			default:
				return nil, fmt.Errorf("unsupported genOnOff command: %T", c)
			}
		default:
			return nil, fmt.Errorf("unsupported cluster: 0x%04x", clusterID)
		}

	default:
		return nil, fmt.Errorf("unknown frame type: %v", zclMsg.FrameType)
	}

	return cmd, nil
}

// convertReportAttributes converts shimmeringbee ReportAttributes to proto.
func (p *Parser) convertReportAttributes(cmd *global.ReportAttributes) (*zclv1proto.GlobalReportAttributes, error) {
	records := make([]*zclv1proto.AttributeRecord, 0, len(cmd.Records))
	
	for _, rec := range cmd.Records {
		attrValue, err := p.convertAttributeValue(rec.DataTypeValue)
		if err != nil {
			return nil, fmt.Errorf("converting attribute value: %w", err)
		}
		
		records = append(records, &zclv1proto.AttributeRecord{
			AttributeId: uint32(rec.Identifier),
			Value:       attrValue,
		})
	}
	
	return &zclv1proto.GlobalReportAttributes{
		Records: records,
	}, nil
}

// convertReadAttributes converts shimmeringbee ReadAttributes to proto.
func (p *Parser) convertReadAttributes(cmd *global.ReadAttributes) (*zclv1proto.GlobalReadAttributes, error) {
	ids := make([]uint32, 0, len(cmd.Identifier))
	for _, id := range cmd.Identifier {
		ids = append(ids, uint32(id))
	}
	
	return &zclv1proto.GlobalReadAttributes{
		AttributeIds: ids,
	}, nil
}

// convertReadAttributesResponse converts shimmeringbee ReadAttributesResponse to proto.
func (p *Parser) convertReadAttributesResponse(cmd *global.ReadAttributesResponse) (*zclv1proto.GlobalReadAttributesResponse, error) {
	records := make([]*zclv1proto.AttributeResponseRecord, 0, len(cmd.Records))
	
	for _, rec := range cmd.Records {
		record := &zclv1proto.AttributeResponseRecord{
			AttributeId: uint32(rec.Identifier),
			Status:      p.convertStatus(rec.Status),
		}
		
		if rec.Status == 0 && rec.DataTypeValue != nil {
			attrValue, err := p.convertAttributeValue(rec.DataTypeValue)
			if err != nil {
				return nil, fmt.Errorf("converting attribute value: %w", err)
			}
			record.Value = attrValue
		}
		
		records = append(records, record)
	}
	
	return &zclv1proto.GlobalReadAttributesResponse{
		Records: records,
	}, nil
}

// convertAttributeValue converts shimmeringbee AttributeDataTypeValue to proto AttributeValue.
func (p *Parser) convertAttributeValue(dtv *zcl.AttributeDataTypeValue) (*zclv1proto.AttributeValue, error) {
	if dtv == nil {
		return nil, fmt.Errorf("nil AttributeDataTypeValue")
	}
	
	attrValue := &zclv1proto.AttributeValue{
		DataType: p.convertDataType(dtv.DataType),
	}
	
	// Convert value based on data type
	switch dtv.DataType {
	case zcl.TypeBoolean:
		if v, ok := dtv.Value.(bool); ok {
			attrValue.Value = &zclv1proto.AttributeValue_BoolValue{BoolValue: v}
		}
	case zcl.TypeUnsignedInt8:
		if v, ok := dtv.Value.(uint); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint8Value{Uint8Value: uint32(v)}
		}
	case zcl.TypeUnsignedInt16:
		if v, ok := dtv.Value.(uint); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint16Value{Uint16Value: uint32(v)}
		}
	case zcl.TypeUnsignedInt32:
		if v, ok := dtv.Value.(uint); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint32Value{Uint32Value: uint32(v)}
		}
	case zcl.TypeStringCharacter8:
		if v, ok := dtv.Value.(string); ok {
			attrValue.Value = &zclv1proto.AttributeValue_StringValue{StringValue: v}
		}
	default:
		// For unknown types, try to serialize as bytes
		bb := bitbuffer.NewBitBuffer()
		if err := bytecodec.MarshalToBitBuffer(bb, dtv.Value); err == nil {
			attrValue.Value = &zclv1proto.AttributeValue_DataValue{DataValue: bb.Bytes()}
		} else {
			return nil, fmt.Errorf("unsupported data type: %v", dtv.DataType)
		}
	}
	
	return attrValue, nil
}

// convertDataType converts shimmeringbee AttributeDataType to proto ZclDataType.
func (p *Parser) convertDataType(dt zcl.AttributeDataType) zclv1proto.ZclDataType {
	// Map shimmeringbee types to proto types
	switch dt {
	case zcl.TypeBoolean:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_BOOLEAN
	case zcl.TypeUnsignedInt8:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT8
	case zcl.TypeUnsignedInt16:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT16
	case zcl.TypeUnsignedInt32:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT32
	case zcl.TypeStringCharacter8:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_STRING_CHARACTER8
	default:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UNKNOWN
	}
}

// convertStatus converts ZCL status byte to proto ZclStatus.
// Note: Proto values are shifted by +1 from ZCL values.
func (p *Parser) convertStatus(status uint8) zclv1proto.ZclStatus {
	if status == 0 {
		return zclv1proto.ZclStatus_ZCL_STATUS_SUCCESS // ZCL 0x00 = SUCCESS = proto 1
	}
	return zclv1proto.ZclStatus(status + 1) // Shift by +1
}

// getClusterName returns the cluster name for a given cluster ID.
func (p *Parser) getClusterName(clusterID uint16) string {
	clusterNames := map[uint16]string{
		0x0000: "genBasic",
		0x0001: "genPowerCfg",
		0x0006: "genOnOff",
		0x0008: "genLevelControl",
		0x0300: "genColorControl",
	}
	
	if name, ok := clusterNames[clusterID]; ok {
		return name
	}
	return fmt.Sprintf("cluster_0x%04x", clusterID)
}
