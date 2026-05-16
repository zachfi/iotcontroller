package zcl

import (
	"fmt"
	"log/slog"

	"github.com/shimmeringbee/bytecodec"
	"github.com/shimmeringbee/bytecodec/bitbuffer"
	"github.com/shimmeringbee/zcl"
	"github.com/shimmeringbee/zcl/commands/global"
	"github.com/shimmeringbee/zcl/commands/local/onoff"
	"github.com/shimmeringbee/zigbee"
	"github.com/zachfi/iotcontroller/pkg/zcl/colorcontrol"
	"github.com/zachfi/iotcontroller/pkg/zcl/levelcontrol"
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
	levelcontrol.Register(registry)
	colorcontrol.Register(registry)

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
		FrameType:              p.convertFrameType(zclMsg.FrameType),
		Direction:              p.convertDirection(zclMsg.Direction),
		ManufacturerSpecific:   zclMsg.Manufacturer > 0,
		DisableDefaultResponse: false, // TODO: Extract from frame control byte
	}

	// Build frame
	frame := &zclv1proto.ZclFrame{
		FrameControl:        frameControl,
		TransactionSequence: uint32(zclMsg.TransactionSequence),
		ManufacturerCode:    uint32(zclMsg.Manufacturer),
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
		case 0x0008: // genLevelControl
			switch c := zclMsg.Command.(type) {
			case *levelcontrol.MoveToLevel:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolMoveToLevel{
					GenLevelcontrolMoveToLevel: &zclv1proto.GenLevelControlMoveToLevel{
						Level:          uint32(c.Level),
						TransitionTime: uint32(c.TransitionTime),
					},
				}
			case *levelcontrol.Move:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolMove{
					GenLevelcontrolMove: &zclv1proto.GenLevelControlMove{
						MoveMode: p.convertMoveMode(c.MoveMode),
						Rate:     uint32(c.Rate),
					},
				}
			case *levelcontrol.Step:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolStep{
					GenLevelcontrolStep: &zclv1proto.GenLevelControlStep{
						StepMode:       p.convertStepMode(c.StepMode),
						StepSize:       uint32(c.StepSize),
						TransitionTime: uint32(c.TransitionTime),
					},
				}
			case *levelcontrol.Stop:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolStop{
					GenLevelcontrolStop: &zclv1proto.GenLevelControlStop{},
				}
			case *levelcontrol.MoveToLevelWithOnOff:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolMoveToLevel{
					GenLevelcontrolMoveToLevel: &zclv1proto.GenLevelControlMoveToLevel{
						Level:          uint32(c.Level),
						TransitionTime: uint32(c.TransitionTime),
					},
				}
			case *levelcontrol.MoveWithOnOff:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolMove{
					GenLevelcontrolMove: &zclv1proto.GenLevelControlMove{
						MoveMode: p.convertMoveMode(c.MoveMode),
						Rate:     uint32(c.Rate),
					},
				}
			case *levelcontrol.StepWithOnOff:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolStep{
					GenLevelcontrolStep: &zclv1proto.GenLevelControlStep{
						StepMode:       p.convertStepMode(c.StepMode),
						StepSize:       uint32(c.StepSize),
						TransitionTime: uint32(c.TransitionTime),
					},
				}
			case *levelcontrol.StopWithOnOff:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolStop{
					GenLevelcontrolStop: &zclv1proto.GenLevelControlStop{},
				}
			default:
				return nil, fmt.Errorf("unsupported genLevelControl command: %T", c)
			}
		case 0x0300: // genColorControl
			switch c := zclMsg.Command.(type) {
			case *colorcontrol.MoveToColorTemp:
				cmd.Command = &zclv1proto.ZclCommand_GenColorcontrolMoveToColorTemp{
					GenColorcontrolMoveToColorTemp: &zclv1proto.GenColorControlMoveToColorTemp{
						ColorTemperature: uint32(c.ColorTemperature),
						TransitionTime:   uint32(c.TransitionTime),
					},
				}
			case *colorcontrol.MoveColorTemp:
				cmd.Command = &zclv1proto.ZclCommand_GenColorcontrolMoveColorTemp{
					GenColorcontrolMoveColorTemp: &zclv1proto.GenColorControlMoveColorTemp{
						MoveMode:          p.convertMoveMode(c.MoveMode),
						Rate:              uint32(c.Rate),
						ColorTempMinMired: uint32(c.ColorTempMinMired),
						ColorTempMaxMired: uint32(c.ColorTempMaxMired),
					},
				}
			case *colorcontrol.StepColorTemp:
				cmd.Command = &zclv1proto.ZclCommand_GenColorcontrolStepColorTemp{
					GenColorcontrolStepColorTemp: &zclv1proto.GenColorControlStepColorTemp{
						StepMode:          p.convertStepMode(c.StepMode),
						StepSize:          uint32(c.StepSize),
						TransitionTime:    uint32(c.TransitionTime),
						ColorTempMinMired: uint32(c.ColorTempMinMired),
						ColorTempMaxMired: uint32(c.ColorTempMaxMired),
					},
				}
			case *colorcontrol.StopMoveStep:
				cmd.Command = &zclv1proto.ZclCommand_GenLevelcontrolStop{
					GenLevelcontrolStop: &zclv1proto.GenLevelControlStop{},
				}
			default:
				return nil, fmt.Errorf("unsupported genColorControl command: %T", c)
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
//
// Type assertion notes: shimmeringbee unmarshals every signed integer
// (TypeSignedInt8..TypeSignedInt64) into a Go `int64` and every unsigned
// integer / enum / bitmap into a Go `uint64` — see
// vendor/github.com/shimmeringbee/zcl/zcl_types_unmarshal.go where
// unmarshalInt/unmarshalUint return the bitbuffer's int64/uint64 boxed
// into interface{}. Asserting `dtv.Value.(uint)` (the earlier shape of
// this code) is therefore a type-identity bug: it always fails on
// amd64 even though `uint` and `uint64` are the same width, because
// type assertions check dynamic type, not size. The closet SNZB-02
// pairing on 2026-05-15 was the first native-zigbee device to emit
// attribute reports we'd actually try to parse — the bug had been
// dormant because z2m-routed devices never reach this code.
//
// Type coverage: all primitive ZCL attribute types from the 0x10–0x4F
// range. We map smaller types (8/16/24/32-bit) to the matching
// fixed-size proto slot and widen to 64-bit slots for 40/48/56/64-bit
// values. ZCL semantically-distinct families that share width with
// integers (enum, bitmap) reuse the unsigned-int proto slots; their
// semantic type is preserved in AttributeValue.DataType.
func (p *Parser) convertAttributeValue(dtv *zcl.AttributeDataTypeValue) (*zclv1proto.AttributeValue, error) {
	if dtv == nil {
		return nil, fmt.Errorf("nil AttributeDataTypeValue")
	}

	attrValue := &zclv1proto.AttributeValue{
		DataType: p.convertDataType(dtv.DataType),
	}

	switch dtv.DataType {
	case zcl.TypeBoolean:
		if v, ok := dtv.Value.(bool); ok {
			attrValue.Value = &zclv1proto.AttributeValue_BoolValue{BoolValue: v}
			return attrValue, nil
		}

	// Unsigned ints and bitmaps unmarshal as uint64 (bb.ReadUint's
	// native type). Bucket by width into the right proto slot.
	case zcl.TypeUnsignedInt8, zcl.TypeBitmap8:
		if v, ok := dtv.Value.(uint64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint8Value{Uint8Value: uint32(v)}
			return attrValue, nil
		}
	case zcl.TypeUnsignedInt16, zcl.TypeBitmap16:
		if v, ok := dtv.Value.(uint64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint16Value{Uint16Value: uint32(v)}
			return attrValue, nil
		}
	case zcl.TypeUnsignedInt24, zcl.TypeBitmap24,
		zcl.TypeUnsignedInt32, zcl.TypeBitmap32:
		if v, ok := dtv.Value.(uint64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint32Value{Uint32Value: uint32(v)}
			return attrValue, nil
		}
	case zcl.TypeUnsignedInt40, zcl.TypeBitmap40,
		zcl.TypeUnsignedInt48, zcl.TypeBitmap48,
		zcl.TypeUnsignedInt56, zcl.TypeBitmap56,
		zcl.TypeUnsignedInt64, zcl.TypeBitmap64:
		if v, ok := dtv.Value.(uint64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint64Value{Uint64Value: v}
			return attrValue, nil
		}

	// Enum8 / Enum16 are special: shimmeringbee's unmarshal layer
	// down-converts them to uint8 / uint16 respectively before
	// returning (zcl_types_unmarshal.go: `val = uint8(val.(uint64))`).
	// Asserting against uint64 here would fall through to the
	// data-bytes fallback, which lands the value in DataValue instead
	// of Uint8Value and presents as 0 to downstream readers (Sonoff
	// SNZB-02WD power-source 2026-05-15 bug). Use the native types.
	case zcl.TypeEnum8:
		if v, ok := dtv.Value.(uint8); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint8Value{Uint8Value: uint32(v)}
			return attrValue, nil
		}
	case zcl.TypeEnum16:
		if v, ok := dtv.Value.(uint16); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Uint16Value{Uint16Value: uint32(v)}
			return attrValue, nil
		}

	// Signed ints unmarshal as int64 — needed for temperature (0x29).
	case zcl.TypeSignedInt8:
		if v, ok := dtv.Value.(int64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Int8Value{Int8Value: int32(v)}
			return attrValue, nil
		}
	case zcl.TypeSignedInt16:
		if v, ok := dtv.Value.(int64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Int16Value{Int16Value: int32(v)}
			return attrValue, nil
		}
	case zcl.TypeSignedInt24, zcl.TypeSignedInt32:
		if v, ok := dtv.Value.(int64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Int32Value{Int32Value: int32(v)}
			return attrValue, nil
		}
	case zcl.TypeSignedInt40, zcl.TypeSignedInt48,
		zcl.TypeSignedInt56, zcl.TypeSignedInt64:
		if v, ok := dtv.Value.(int64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Int64Value{Int64Value: v}
			return attrValue, nil
		}

	case zcl.TypeFloatSingle:
		if v, ok := dtv.Value.(float32); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Float32Value{Float32Value: v}
			return attrValue, nil
		}
	case zcl.TypeFloatDouble:
		if v, ok := dtv.Value.(float64); ok {
			attrValue.Value = &zclv1proto.AttributeValue_Float64Value{Float64Value: v}
			return attrValue, nil
		}

	case zcl.TypeStringCharacter8:
		if v, ok := dtv.Value.(string); ok {
			attrValue.Value = &zclv1proto.AttributeValue_StringValue{StringValue: v}
			return attrValue, nil
		}
	case zcl.TypeStringOctet8:
		if v, ok := dtv.Value.([]byte); ok {
			attrValue.Value = &zclv1proto.AttributeValue_OctetStringValue{OctetStringValue: v}
			return attrValue, nil
		}
	}

	// Either an unknown type or a recognized type whose Go value didn't
	// have the shape we expected. Fall through to a raw-bytes
	// serialization so the caller at least gets opaque value-as-data;
	// upstream can decide whether to honor it or drop the report.
	bb := bitbuffer.NewBitBuffer()
	if err := bytecodec.MarshalToBitBuffer(bb, dtv.Value); err == nil {
		attrValue.Value = &zclv1proto.AttributeValue_DataValue{DataValue: bb.Bytes()}
		return attrValue, nil
	}
	return nil, fmt.Errorf("unsupported data type: 0x%02x (Go value type %T)", uint8(dtv.DataType), dtv.Value)
}

// convertMoveMode converts a ZCL move mode byte (0x00=up, 0x01=down) to proto ZclMoveMode.
func (p *Parser) convertMoveMode(mode uint8) zclv1proto.ZclMoveMode {
	switch mode {
	case 0x00:
		return zclv1proto.ZclMoveMode_ZCL_MOVE_MODE_UP
	case 0x01:
		return zclv1proto.ZclMoveMode_ZCL_MOVE_MODE_DOWN
	default:
		return zclv1proto.ZclMoveMode_ZCL_MOVE_MODE_UNSPECIFIED
	}
}

// convertStepMode converts a ZCL step mode byte (0x00=up, 0x01=down) to proto ZclStepMode.
func (p *Parser) convertStepMode(mode uint8) zclv1proto.ZclStepMode {
	switch mode {
	case 0x00:
		return zclv1proto.ZclStepMode_ZCL_STEP_MODE_UP
	case 0x01:
		return zclv1proto.ZclStepMode_ZCL_STEP_MODE_DOWN
	default:
		return zclv1proto.ZclStepMode_ZCL_STEP_MODE_UNSPECIFIED
	}
}

// convertDataType converts shimmeringbee AttributeDataType to proto
// ZclDataType. Kept aligned with convertAttributeValue's type coverage;
// any type returnable by the value converter must round-trip through
// the data-type enum so downstream code can interpret the oneof slot
// correctly.
func (p *Parser) convertDataType(dt zcl.AttributeDataType) zclv1proto.ZclDataType {
	switch dt {
	case zcl.TypeBoolean:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_BOOLEAN

	case zcl.TypeUnsignedInt8:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT8
	case zcl.TypeUnsignedInt16:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT16
	case zcl.TypeUnsignedInt24:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT24
	case zcl.TypeUnsignedInt32:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT32
	case zcl.TypeUnsignedInt40:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT40
	case zcl.TypeUnsignedInt48:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT48
	case zcl.TypeUnsignedInt56:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT56
	case zcl.TypeUnsignedInt64:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT64

	case zcl.TypeSignedInt8:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT8
	case zcl.TypeSignedInt16:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT16
	case zcl.TypeSignedInt24:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT24
	case zcl.TypeSignedInt32:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT32
	case zcl.TypeSignedInt40:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT40
	case zcl.TypeSignedInt48:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT48
	case zcl.TypeSignedInt56:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT56
	case zcl.TypeSignedInt64:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT64

	case zcl.TypeEnum8:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_ENUM8
	case zcl.TypeEnum16:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_ENUM16

	case zcl.TypeBitmap8:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_BITMAP8
	case zcl.TypeBitmap16:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_BITMAP16
	case zcl.TypeBitmap24:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_BITMAP24
	case zcl.TypeBitmap32:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_BITMAP32

	case zcl.TypeFloatSingle:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_FLOAT_SINGLE
	case zcl.TypeFloatDouble:
		return zclv1proto.ZclDataType_ZCL_DATA_TYPE_FLOAT_DOUBLE

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
		0x0003: "genIdentify",
		0x0004: "genGroups",
		0x0005: "genScenes",
		0x0006: "genOnOff",
		0x0008: "genLevelControl",
		0x0019: "genOta",
		0x0020: "genPollCtrl",
		0x0300: "genColorControl",
		0x0400: "msIlluminanceMeasurement",
		0x0401: "msIlluminanceLevelSensing",
		0x0402: "msTemperatureMeasurement",
		0x0403: "msPressureMeasurement",
		0x0404: "msFlowMeasurement",
		0x0405: "msRelativeHumidity",
		0x0406: "msOccupancySensing",
		0x0500: "ssIasZone",
	}

	if name, ok := clusterNames[clusterID]; ok {
		return name
	}
	return fmt.Sprintf("cluster_0x%04x", clusterID)
}
