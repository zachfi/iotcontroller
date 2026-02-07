package zcl

import "github.com/shimmeringbee/zigbee"

type FrameType uint8

const (
	FrameGlobal FrameType = 0x00
	FrameLocal  FrameType = 0x01
)

type Direction uint8

const (
	ClientToServer Direction = 0
	ServerToClient Direction = 1
)

type CommandIdentifier uint8

type Control struct {
	Reserved               uint8     `bcfieldwidth:"3"`
	DisableDefaultResponse bool      `bcfieldwidth:"1"`
	Direction              Direction `bcfieldwidth:"1"`
	ManufacturerSpecific   bool      `bcfieldwidth:"1"`
	FrameType              FrameType `bcfieldwidth:"2"`
}

type Header struct {
	Control             Control
	Manufacturer        zigbee.ManufacturerCode `bcincludeif:".Control.ManufacturerSpecific"`
	TransactionSequence uint8
	CommandIdentifier   CommandIdentifier
}

type Message struct {
	FrameType           FrameType
	Direction           Direction
	TransactionSequence uint8
	Manufacturer        zigbee.ManufacturerCode
	ClusterID           zigbee.ClusterID
	SourceEndpoint      zigbee.Endpoint
	DestinationEndpoint zigbee.Endpoint
	CommandIdentifier   CommandIdentifier
	Command             interface{}
}

func (z Message) isManufacturerSpecific() bool {
	return z.Manufacturer > 0
}
