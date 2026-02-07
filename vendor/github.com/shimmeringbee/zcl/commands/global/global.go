package global

import "github.com/shimmeringbee/zcl"

const (
	ReadAttributesID                     zcl.CommandIdentifier = 0x00
	ReadAttributesResponseID             zcl.CommandIdentifier = 0x01
	WriteAttributesID                    zcl.CommandIdentifier = 0x02
	WriteAttributesUndividedID           zcl.CommandIdentifier = 0x03
	WriteAttributesResponseID            zcl.CommandIdentifier = 0x04
	WriteAttributesNoResponseID          zcl.CommandIdentifier = 0x05
	ConfigureReportingID                 zcl.CommandIdentifier = 0x06
	ConfigureReportingResponseID         zcl.CommandIdentifier = 0x07
	ReadReportingConfigurationID         zcl.CommandIdentifier = 0x08
	ReadReportingConfigurationResponseID zcl.CommandIdentifier = 0x09
	ReportAttributesID                   zcl.CommandIdentifier = 0x0a
	DefaultResponseID                    zcl.CommandIdentifier = 0x0b
	DiscoverAttributesID                 zcl.CommandIdentifier = 0x0c
	DiscoverAttributesResponseID         zcl.CommandIdentifier = 0x0d
	ReadAttributesStructuredID           zcl.CommandIdentifier = 0x0e
	WriteAttributesStructuredID          zcl.CommandIdentifier = 0x0f
	WriteAttributesStructuredResponseID  zcl.CommandIdentifier = 0x10
	DiscoverCommandsReceivedID           zcl.CommandIdentifier = 0x11
	DiscoverCommandsReceivedResponseID   zcl.CommandIdentifier = 0x12
	DiscoverCommandsGeneratedID          zcl.CommandIdentifier = 0x13
	DiscoverCommandsGeneratedResponseID  zcl.CommandIdentifier = 0x14
	DiscoverAttributesExtendedID         zcl.CommandIdentifier = 0x15
	DiscoverAttributesExtendedResponseID zcl.CommandIdentifier = 0x16
)

type ReadAttributes struct {
	Identifier []zcl.AttributeID
}

type ReadAttributeResponseRecord struct {
	Identifier    zcl.AttributeID
	Status        uint8
	DataTypeValue *zcl.AttributeDataTypeValue `bcincludeif:"Status==0"`
}

type ReadAttributesResponse struct {
	Records []ReadAttributeResponseRecord
}

type WriteAttributes struct {
	Records []WriteAttributesRecord
}

type WriteAttributesRecord struct {
	Identifier    zcl.AttributeID
	DataTypeValue *zcl.AttributeDataTypeValue
}

type WriteAttributesResponseRecord struct {
	Status     uint8
	Identifier zcl.AttributeID
}

type WriteAttributesResponse struct {
	Records []WriteAttributesResponseRecord
}

type WriteAttributesUndivided WriteAttributes

type WriteAttributesNoResponse WriteAttributes

type ConfigureReportingRecord struct {
	Direction        uint8
	Identifier       zcl.AttributeID
	DataType         zcl.AttributeDataType   `bcincludeif:"Direction==0"`
	MinimumInterval  uint16                  `bcincludeif:"Direction==0"`
	MaximumInterval  uint16                  `bcincludeif:"Direction==0"`
	ReportableChange *zcl.AttributeDataValue `bcincludeif:"Direction==0"`
	Timeout          uint16                  `bcincludeif:"Direction==1"`
}

type ConfigureReporting struct {
	Records []ConfigureReportingRecord
}

type ConfigureReportingResponseRecord struct {
	Status     uint8
	Direction  uint8
	Identifier zcl.AttributeID
}

type ConfigureReportingResponse struct {
	Records []ConfigureReportingResponseRecord
}

type ReadReportingConfigurationRecord struct {
	Direction  uint8
	Identifier zcl.AttributeID
}

type ReadReportingConfiguration struct {
	Records []ReadReportingConfigurationRecord
}

type ReadReportingConfigurationResponseRecord struct {
	Status           uint8
	Direction        uint8
	Identifier       zcl.AttributeID
	DataType         zcl.AttributeDataType   `bcincludeif:"Direction==0"`
	MinimumInterval  uint16                  `bcincludeif:"Direction==0"`
	MaximumInterval  uint16                  `bcincludeif:"Direction==0"`
	ReportableChange *zcl.AttributeDataValue `bcincludeif:"Direction==0"`
	Timeout          uint16                  `bcincludeif:"Direction==1"`
}

type ReadReportingConfigurationResponse struct {
	Records []ReadReportingConfigurationResponseRecord
}

type ReportAttributesRecord struct {
	Identifier    zcl.AttributeID
	DataTypeValue *zcl.AttributeDataTypeValue
}

type ReportAttributes struct {
	Records []ReportAttributesRecord
}

type DefaultResponse struct {
	CommandIdentifier uint8
	Status            uint8
}

type DiscoverAttributes struct {
	StartAttributeIdentifier  uint16
	MaximumNumberOfAttributes uint8
}

type DiscoverAttributesResponseRecord struct {
	Identifier zcl.AttributeID
	DataType   zcl.AttributeDataType
}

type DiscoverAttributesResponse struct {
	DiscoveryComplete bool
	Records           []DiscoverAttributesResponseRecord
}

type ReadAttributesStructuredRecord struct {
	Identifier zcl.AttributeID
	Selector   Selector
}

type ReadAttributesStructured struct {
	Records []ReadAttributesStructuredRecord
}

type Selector struct {
	BagSetOperation uint8    `bcfieldwidth:"4"`
	Index           []uint16 `bcsliceprefix:"4"`
}

type WriteAttributesStructuredRecord struct {
	Identifier    zcl.AttributeID
	Selector      Selector
	DataTypeValue *zcl.AttributeDataTypeValue
}

type WriteAttributesStructured struct {
	Records []WriteAttributesStructuredRecord
}

type WriteAttributesStructuredResponseRecord struct {
	Status     uint8
	Identifier zcl.AttributeID
	Selector   Selector
}

type WriteAttributesStructuredResponse struct {
	Records []WriteAttributesStructuredResponseRecord
}

type DiscoverCommandsReceived struct {
	StartCommandIdentifier  uint8
	MaximumNumberOfCommands uint8
}

type DiscoverCommandsReceivedResponse struct {
	DiscoveryComplete bool
	CommandIdentifier []uint8
}

type DiscoverCommandsGenerated struct {
	StartCommandIdentifier  uint8
	MaximumNumberOfCommands uint8
}

type DiscoverCommandsGeneratedResponse struct {
	DiscoveryComplete bool
	CommandIdentifier []uint8
}

type DiscoverAttributesExtended struct {
	StartAttributeIdentifier  uint16
	MaximumNumberOfAttributes uint8
}

type DiscoverAttributesExtendedResponseRecord struct {
	Identifier    zcl.AttributeID
	DataType      zcl.AttributeDataType
	AccessControl uint8
}

type DiscoverAttributesExtendedResponse struct {
	DiscoveryComplete bool
	Records           []DiscoverAttributesExtendedResponseRecord
}
