package zcl

import (
	"errors"
	"fmt"
	"github.com/shimmeringbee/bytecodec"
	"github.com/shimmeringbee/bytecodec/bitbuffer"
	"github.com/shimmeringbee/zigbee"
)

func (cr *CommandRegistry) Unmarshal(appMsg zigbee.ApplicationMessage) (Message, error) {
	header := Header{}
	var command interface{}

	bb := bitbuffer.NewBitBufferFromBytes(appMsg.Data)

	if err := bytecodec.UnmarshalFromBitBuffer(bb, &header); err != nil {
		return Message{}, err
	}

	switch header.Control.FrameType {
	case FrameGlobal:
		foundCommand, err := cr.GetGlobalCommand(header.CommandIdentifier)

		if err != nil {
			return Message{}, fmt.Errorf("unknown ZCL global command identifier received: %d", header.CommandIdentifier)
		}

		command = foundCommand
	case FrameLocal:
		foundCommand, err := cr.GetLocalCommand(appMsg.ClusterID, header.Manufacturer, header.Control.Direction, header.CommandIdentifier)

		if err != nil {
			return Message{}, fmt.Errorf("unknown ZCL local command identifier received: %d", header.CommandIdentifier)
		}

		command = foundCommand
	default:
		return Message{}, errors.New("unknown frame type encountered")
	}

	if err := bytecodec.UnmarshalFromBitBuffer(bb, command); err != nil {
		return Message{}, err
	}

	return Message{
		FrameType:           header.Control.FrameType,
		Direction:           header.Control.Direction,
		TransactionSequence: header.TransactionSequence,
		Manufacturer:        header.Manufacturer,
		ClusterID:           appMsg.ClusterID,
		SourceEndpoint:      appMsg.SourceEndpoint,
		DestinationEndpoint: appMsg.DestinationEndpoint,
		CommandIdentifier:   header.CommandIdentifier,
		Command:             command,
	}, nil
}
