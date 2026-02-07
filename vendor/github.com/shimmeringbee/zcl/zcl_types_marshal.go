package zcl

import (
	"errors"
	"fmt"
	"github.com/shimmeringbee/bytecodec"
	"github.com/shimmeringbee/bytecodec/bitbuffer"
	"github.com/shimmeringbee/zigbee"
	"math"
)

func marshalZCLType(bb *bitbuffer.BitBuffer, ctx bytecodec.Context, dt AttributeDataType, v interface{}) error {
	switch dt {
	case TypeNull:
		return nil
	case TypeData8:
		return marshalData(bb, v, 1)
	case TypeData16:
		return marshalData(bb, v, 2)
	case TypeData24:
		return marshalData(bb, v, 3)
	case TypeData32:
		return marshalData(bb, v, 4)
	case TypeData40:
		return marshalData(bb, v, 5)
	case TypeData48:
		return marshalData(bb, v, 6)
	case TypeData56:
		return marshalData(bb, v, 7)
	case TypeData64:
		return marshalData(bb, v, 8)
	case TypeBoolean:
		return marshalBoolean(bb, v)
	case TypeBitmap8:
		return marshalUint(bb, v, 8)
	case TypeBitmap16:
		return marshalUint(bb, v, 16)
	case TypeBitmap24:
		return marshalUint(bb, v, 24)
	case TypeBitmap32:
		return marshalUint(bb, v, 32)
	case TypeBitmap40:
		return marshalUint(bb, v, 40)
	case TypeBitmap48:
		return marshalUint(bb, v, 48)
	case TypeBitmap56:
		return marshalUint(bb, v, 56)
	case TypeBitmap64:
		return marshalUint(bb, v, 64)
	case TypeUnsignedInt8:
		return marshalUint(bb, v, 8)
	case TypeUnsignedInt16:
		return marshalUint(bb, v, 16)
	case TypeUnsignedInt24:
		return marshalUint(bb, v, 24)
	case TypeUnsignedInt32:
		return marshalUint(bb, v, 32)
	case TypeUnsignedInt40:
		return marshalUint(bb, v, 40)
	case TypeUnsignedInt48:
		return marshalUint(bb, v, 48)
	case TypeUnsignedInt56:
		return marshalUint(bb, v, 56)
	case TypeUnsignedInt64:
		return marshalUint(bb, v, 64)
	case TypeSignedInt8:
		return marshalInt(bb, v, 8)
	case TypeSignedInt16:
		return marshalInt(bb, v, 16)
	case TypeSignedInt24:
		return marshalInt(bb, v, 24)
	case TypeSignedInt32:
		return marshalInt(bb, v, 32)
	case TypeSignedInt40:
		return marshalInt(bb, v, 40)
	case TypeSignedInt48:
		return marshalInt(bb, v, 48)
	case TypeSignedInt56:
		return marshalInt(bb, v, 56)
	case TypeSignedInt64:
		return marshalInt(bb, v, 64)
	case TypeEnum8:
		return marshalUint(bb, v, 8)
	case TypeEnum16:
		return marshalUint(bb, v, 16)
	case TypeStringOctet8:
		return marshalString(bb, v, 8)
	case TypeStringOctet16:
		return marshalString(bb, v, 16)
	case TypeStringCharacter8:
		return marshalString(bb, v, 8)
	case TypeStringCharacter16:
		return marshalString(bb, v, 16)
	case TypeTimeOfDay:
		return marshalTimeOfDay(bb, v)
	case TypeDate:
		return marshalDate(bb, v)
	case TypeUTCTime:
		return marshalUTCTime(bb, v)
	case TypeClusterID:
		return marshalClusterID(bb, v)
	case TypeAttributeID:
		return marshalAttributeID(bb, v)
	case TypeIEEEAddress:
		return marshalIEEEAddress(bb, v)
	case TypeSecurityKey128:
		return marshalSecurityKey(bb, v)
	case TypeBACnetOID:
		return marshalBACnetOID(bb, v)
	case TypeStructure:
		return marshalStructure(bb, ctx, v)
	case TypeArray, TypeSet, TypeBag:
		return marshalSlice(bb, ctx, v)
	case TypeFloatSingle:
		return marshalFloatSingle(bb, v)
	case TypeFloatDouble:
		return marshalFloatDouble(bb, v)
	default:
		return fmt.Errorf("unsupported ZCL type to marshal: %d", dt)
	}
}

func marshalData(bb *bitbuffer.BitBuffer, v interface{}, size int) error {
	data, ok := v.([]byte)

	if !ok {
		return errors.New("could not cast value")
	}

	if len(data) != size {
		return fmt.Errorf("data array provided does not match output size")
	}

	for i := size - 1; i >= 0; i-- {
		if err := bb.WriteByte(data[i]); err != nil {
			return err
		}
	}

	return nil
}

func marshalBoolean(bb *bitbuffer.BitBuffer, v interface{}) error {
	data, ok := v.(bool)

	if !ok {
		return errors.New("could not cast value")
	}

	if data {
		return bb.WriteByte(0x01)
	} else {
		return bb.WriteByte(0x00)
	}
}

func marshalUint(bb *bitbuffer.BitBuffer, v interface{}, bitsize int) error {
	switch v := v.(type) {
	case uint:
		return bb.WriteUint(uint64(v), bitbuffer.LittleEndian, bitsize)
	case uint8:
		return bb.WriteUint(uint64(v), bitbuffer.LittleEndian, bitsize)
	case uint16:
		return bb.WriteUint(uint64(v), bitbuffer.LittleEndian, bitsize)
	case uint32:
		return bb.WriteUint(uint64(v), bitbuffer.LittleEndian, bitsize)
	case uint64:
		return bb.WriteUint(v, bitbuffer.LittleEndian, bitsize)
	}

	return errors.New("marshalling uint to ZCL type received unsupported value")
}

func marshalInt(bb *bitbuffer.BitBuffer, v interface{}, bitsize int) error {
	switch v := v.(type) {
	case int:
		return bb.WriteInt(int64(v), bitbuffer.LittleEndian, bitsize)
	case int8:
		return bb.WriteInt(int64(v), bitbuffer.LittleEndian, bitsize)
	case int16:
		return bb.WriteInt(int64(v), bitbuffer.LittleEndian, bitsize)
	case int32:
		return bb.WriteInt(int64(v), bitbuffer.LittleEndian, bitsize)
	case int64:
		return bb.WriteInt(v, bitbuffer.LittleEndian, bitsize)
	}

	return errors.New("marshalling int to ZCL type received unsupported value")
}

func marshalString(bb *bitbuffer.BitBuffer, v interface{}, bitsize int) error {
	data, ok := v.(string)

	if !ok {
		return errors.New("could not cast value")
	}

	return bb.WriteStringLengthPrefixed(data, bitbuffer.LittleEndian, bitsize)
}

func marshalStringRune(bb *bitbuffer.BitBuffer, v interface{}, bitsize int) error {
	data, ok := v.(string)

	if !ok {
		return errors.New("could not cast value")
	}

	if err := bb.WriteUint(uint64(len([]rune(data))), bitbuffer.LittleEndian, bitsize); err != nil {
		return err
	}

	for i := 0; i < len(data); i++ {
		if err := bb.WriteByte(data[i]); err != nil {
			return err
		}
	}

	return nil
}

func marshalTimeOfDay(bb *bitbuffer.BitBuffer, v interface{}) error {
	tod, ok := v.(TimeOfDay)

	if !ok {
		return errors.New("could not cast value")
	}

	return bytecodec.MarshalToBitBuffer(bb, &tod)
}

func marshalDate(bb *bitbuffer.BitBuffer, v interface{}) error {
	date, ok := v.(Date)

	if !ok {
		return errors.New("could not cast value")
	}

	return bytecodec.MarshalToBitBuffer(bb, &date)
}

func marshalUTCTime(bb *bitbuffer.BitBuffer, v interface{}) error {
	utcTime, ok := v.(UTCTime)

	if !ok {
		return errors.New("could not cast value")
	}

	return bb.WriteUint(uint64(utcTime), bitbuffer.LittleEndian, 32)
}

func marshalClusterID(bb *bitbuffer.BitBuffer, v interface{}) error {
	clusterID, ok := v.(zigbee.ClusterID)

	if !ok {
		return errors.New("could not cast value")
	}

	return bb.WriteUint(uint64(clusterID), bitbuffer.LittleEndian, 16)
}

func marshalAttributeID(bb *bitbuffer.BitBuffer, v interface{}) error {
	attributeID, ok := v.(AttributeID)

	if !ok {
		return errors.New("could not cast value")
	}

	return bb.WriteUint(uint64(attributeID), bitbuffer.LittleEndian, 16)
}

func marshalIEEEAddress(bb *bitbuffer.BitBuffer, v interface{}) error {
	ieeeAddress, ok := v.(zigbee.IEEEAddress)

	if !ok {
		return errors.New("could not cast value")
	}

	return bb.WriteUint(uint64(ieeeAddress), bitbuffer.LittleEndian, 64)
}

func marshalSecurityKey(bb *bitbuffer.BitBuffer, v interface{}) error {
	networkKey, ok := v.(zigbee.NetworkKey)

	if !ok {
		return errors.New("could not cast value")
	}

	return bytecodec.MarshalToBitBuffer(bb, &networkKey)
}

func marshalBACnetOID(bb *bitbuffer.BitBuffer, v interface{}) error {
	oid, ok := v.(BACnetOID)

	if !ok {
		return errors.New("could not cast value")
	}

	return bb.WriteUint(uint64(oid), bitbuffer.LittleEndian, 32)
}

func marshalStructure(bb *bitbuffer.BitBuffer, ctx bytecodec.Context, v interface{}) error {
	values, ok := v.([]AttributeDataTypeValue)

	if !ok {
		return errors.New("could not cast value")
	}

	if err := bb.WriteUint(uint64(len(values)), bitbuffer.LittleEndian, 16); err != nil {
		return err
	}

	for _, val := range values {
		if err := val.Marshal(bb, ctx); err != nil {
			return err
		}
	}

	return nil
}

func marshalSlice(bb *bitbuffer.BitBuffer, ctx bytecodec.Context, v interface{}) error {
	slice, ok := v.(AttributeSlice)

	if !ok {
		return errors.New("could not cast value")
	}

	if err := bb.WriteByte(byte(slice.DataType)); err != nil {
		return err
	}

	if err := bb.WriteUint(uint64(len(slice.Values)), bitbuffer.LittleEndian, 16); err != nil {
		return err
	}

	for i := 0; i < len(slice.Values); i++ {
		if err := marshalZCLType(bb, ctx, slice.DataType, slice.Values[i]); err != nil {
			return err
		}
	}

	return nil
}

func marshalFloatSingle(bb *bitbuffer.BitBuffer, v interface{}) error {
	value, ok := v.(float32)

	if !ok {
		return errors.New("could not cast value")
	}

	bits := math.Float32bits(value)

	return bb.WriteUint(uint64(bits), bitbuffer.LittleEndian, 32)
}

func marshalFloatDouble(bb *bitbuffer.BitBuffer, v interface{}) error {
	value, ok := v.(float64)

	if !ok {
		return errors.New("could not cast value")
	}

	bits := math.Float64bits(value)

	return bb.WriteUint(bits, bitbuffer.LittleEndian, 64)
}
