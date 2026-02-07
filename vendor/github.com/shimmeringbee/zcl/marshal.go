package zcl

import (
	"errors"
	"github.com/shimmeringbee/bytecodec"
	"github.com/shimmeringbee/bytecodec/bitbuffer"
	"github.com/shimmeringbee/zigbee"
)

func (cr *CommandRegistry) Marshal(message Message) (zigbee.ApplicationMessage, error) {
	bb := bitbuffer.NewBitBuffer()

	header := Header{
		Control: Control{
			Reserved:               0,
			DisableDefaultResponse: false,
			Direction:              message.Direction,
			ManufacturerSpecific:   message.isManufacturerSpecific(),
			FrameType:              message.FrameType,
		},
		Manufacturer:        message.Manufacturer,
		TransactionSequence: message.TransactionSequence,
	}

	switch message.FrameType {
	case FrameGlobal:
		commandId, err := cr.GetGlobalCommandIdentifier(message.Command)

		if err != nil {
			return zigbee.ApplicationMessage{}, err
		}

		header.CommandIdentifier = commandId
	case FrameLocal:
		commandId, err := cr.GetLocalCommandIdentifier(message.ClusterID, message.Manufacturer, message.Direction, message.Command)

		if err != nil {
			return zigbee.ApplicationMessage{}, err
		}

		header.CommandIdentifier = commandId
	default:
		return zigbee.ApplicationMessage{}, errors.New("unknown frame type encountered")
	}

	if err := bytecodec.MarshalToBitBuffer(bb, header); err != nil {
		return zigbee.ApplicationMessage{}, err
	}

	if err := bytecodec.MarshalToBitBuffer(bb, message.Command); err != nil {
		return zigbee.ApplicationMessage{}, err
	}

	msg := zigbee.ApplicationMessage{
		ClusterID:           message.ClusterID,
		SourceEndpoint:      message.SourceEndpoint,
		DestinationEndpoint: message.DestinationEndpoint,
		Data:                bb.Bytes(),
	}

	return msg, nil
}
