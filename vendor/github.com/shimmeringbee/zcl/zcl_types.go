package zcl

import (
	"errors"
	"github.com/shimmeringbee/bytecodec"
	"github.com/shimmeringbee/bytecodec/bitbuffer"
)

/*
 * Zigbee Cluster List data types, as per 2.6.2 in ZCL Revision 6 (14 January 2016).
 * Downloaded From: https://zigbeealliance.org/developer_resources/zigbee-cluster-library/
 */

const (
	TypeNull AttributeDataType = 0x00

	TypeData8  AttributeDataType = 0x08
	TypeData16 AttributeDataType = 0x09
	TypeData24 AttributeDataType = 0x0a
	TypeData32 AttributeDataType = 0x0b
	TypeData40 AttributeDataType = 0x0c
	TypeData48 AttributeDataType = 0x0d
	TypeData56 AttributeDataType = 0x0e
	TypeData64 AttributeDataType = 0x0f

	TypeBoolean AttributeDataType = 0x10

	TypeBitmap8  AttributeDataType = 0x18
	TypeBitmap16 AttributeDataType = 0x19
	TypeBitmap24 AttributeDataType = 0x1a
	TypeBitmap32 AttributeDataType = 0x1b
	TypeBitmap40 AttributeDataType = 0x1c
	TypeBitmap48 AttributeDataType = 0x1d
	TypeBitmap56 AttributeDataType = 0x1e
	TypeBitmap64 AttributeDataType = 0x1f

	TypeUnsignedInt8  AttributeDataType = 0x20
	TypeUnsignedInt16 AttributeDataType = 0x21
	TypeUnsignedInt24 AttributeDataType = 0x22
	TypeUnsignedInt32 AttributeDataType = 0x23
	TypeUnsignedInt40 AttributeDataType = 0x24
	TypeUnsignedInt48 AttributeDataType = 0x25
	TypeUnsignedInt56 AttributeDataType = 0x26
	TypeUnsignedInt64 AttributeDataType = 0x27

	TypeSignedInt8  AttributeDataType = 0x28
	TypeSignedInt16 AttributeDataType = 0x29
	TypeSignedInt24 AttributeDataType = 0x2a
	TypeSignedInt32 AttributeDataType = 0x2b
	TypeSignedInt40 AttributeDataType = 0x2c
	TypeSignedInt48 AttributeDataType = 0x2d
	TypeSignedInt56 AttributeDataType = 0x2e
	TypeSignedInt64 AttributeDataType = 0x2f

	TypeEnum8  AttributeDataType = 0x30
	TypeEnum16 AttributeDataType = 0x31

	TypeFloatSemi   AttributeDataType = 0x38
	TypeFloatSingle AttributeDataType = 0x39
	TypeFloatDouble AttributeDataType = 0x3a

	TypeStringOctet8      AttributeDataType = 0x41
	TypeStringCharacter8  AttributeDataType = 0x42
	TypeStringOctet16     AttributeDataType = 0x43
	TypeStringCharacter16 AttributeDataType = 0x44

	TypeArray     AttributeDataType = 0x48
	TypeStructure AttributeDataType = 0x4c
	TypeSet       AttributeDataType = 0x50
	TypeBag       AttributeDataType = 0x51

	TypeTimeOfDay AttributeDataType = 0xe0
	TypeDate      AttributeDataType = 0xe1
	TypeUTCTime   AttributeDataType = 0xe2

	TypeClusterID   AttributeDataType = 0xe9
	TypeAttributeID AttributeDataType = 0xea
	TypeBACnetOID   AttributeDataType = 0xeb

	TypeIEEEAddress    AttributeDataType = 0xf0
	TypeSecurityKey128 AttributeDataType = 0xf1
	TypeUnknown        AttributeDataType = 0xff
)

var DiscreteTypes = map[AttributeDataType]bool{
	TypeNull: false,

	TypeData8:  true,
	TypeData16: true,
	TypeData24: true,
	TypeData32: true,
	TypeData40: true,
	TypeData48: true,
	TypeData56: true,
	TypeData64: true,

	TypeBoolean: true,

	TypeBitmap8:  true,
	TypeBitmap16: true,
	TypeBitmap24: true,
	TypeBitmap32: true,
	TypeBitmap40: true,
	TypeBitmap48: true,
	TypeBitmap56: true,
	TypeBitmap64: true,

	TypeUnsignedInt8:  false,
	TypeUnsignedInt16: false,
	TypeUnsignedInt24: false,
	TypeUnsignedInt32: false,
	TypeUnsignedInt40: false,
	TypeUnsignedInt48: false,
	TypeUnsignedInt56: false,
	TypeUnsignedInt64: false,

	TypeSignedInt8:  false,
	TypeSignedInt16: false,
	TypeSignedInt24: false,
	TypeSignedInt32: false,
	TypeSignedInt40: false,
	TypeSignedInt48: false,
	TypeSignedInt56: false,
	TypeSignedInt64: false,

	TypeEnum8:  true,
	TypeEnum16: true,

	TypeFloatSemi:   false,
	TypeFloatSingle: false,
	TypeFloatDouble: false,

	TypeStringOctet8:      true,
	TypeStringCharacter8:  true,
	TypeStringOctet16:     true,
	TypeStringCharacter16: true,

	TypeArray:     true,
	TypeStructure: true,
	TypeSet:       true,
	TypeBag:       true,

	TypeTimeOfDay: false,
	TypeDate:      false,
	TypeUTCTime:   false,

	TypeClusterID:   true,
	TypeAttributeID: true,
	TypeBACnetOID:   true,

	TypeIEEEAddress:    true,
	TypeSecurityKey128: true,
	TypeUnknown:        false,
}

type AttributeDataType byte
type AttributeID uint16

type AttributeDataValue struct {
	Value interface{}
}

func findPreviousDataType(ctx bytecodec.Context) (AttributeDataType, error) {
	structType := ctx.Root.Type()

	for i := ctx.CurrentIndex - 1; i >= 0; i-- {
		fieldType := structType.Field(i)

		if fieldType.Type.Name() == "AttributeDataType" {
			value := ctx.Root.Field(i)

			dataType := value.Interface().(AttributeDataType)

			return dataType, nil
		}
	}

	return TypeUnknown, errors.New("unable to find prior attribute data type to extrapolate type information")
}

func (a *AttributeDataValue) Marshal(bb *bitbuffer.BitBuffer, ctx bytecodec.Context) error {
	if dataType, err := findPreviousDataType(ctx); err != nil {
		return err
	} else {
		if DiscreteTypes[dataType] {
			return nil
		}

		return marshalZCLType(bb, ctx, dataType, a.Value)
	}
}

func (a *AttributeDataValue) Unmarshal(bb *bitbuffer.BitBuffer, ctx bytecodec.Context) error {
	if dataType, err := findPreviousDataType(ctx); err != nil {
		return err
	} else {
		if DiscreteTypes[dataType] {
			return nil
		}

		if value, err := unmarshalZCLType(bb, dataType, ctx); err != nil {
			return err
		} else {
			a.Value = value
		}
	}

	return nil
}

type AttributeDataTypeValue struct {
	DataType AttributeDataType
	Value    interface{}
}

func (a *AttributeDataTypeValue) Marshal(bb *bitbuffer.BitBuffer, ctx bytecodec.Context) error {
	if err := bb.WriteByte(byte(a.DataType)); err != nil {
		return err
	}

	return marshalZCLType(bb, ctx, a.DataType, a.Value)
}

func (a *AttributeDataTypeValue) Unmarshal(bb *bitbuffer.BitBuffer, ctx bytecodec.Context) error {
	if dt, err := bb.ReadByte(); err != nil {
		return err
	} else {
		a.DataType = AttributeDataType(dt)
	}

	val, err := unmarshalZCLType(bb, a.DataType, ctx)

	if err != nil {
		return err
	}

	a.Value = val

	return nil
}

type AttributeSlice struct {
	DataType AttributeDataType
	Values   []interface{}
}

type BACnetOID uint32

type TimeOfDay struct {
	Hours      uint8
	Minutes    uint8
	Seconds    uint8
	Hundredths uint8
}

type Date struct {
	Year       uint8
	Month      uint8
	DayOfMonth uint8
	DayOfWeek  uint8
}

type UTCTime uint32
