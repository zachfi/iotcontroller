package zcl

import (
	"fmt"
	"github.com/shimmeringbee/bytecodec"
	"github.com/shimmeringbee/bytecodec/bitbuffer"
	"github.com/shimmeringbee/zigbee"
	"math"
)

func unmarshalZCLType(bb *bitbuffer.BitBuffer, dt AttributeDataType, ctx bytecodec.Context) (interface{}, error) {
	switch dt {
	case TypeNull:
		return nil, nil
	case TypeData8:
		return unmarshalData(bb, 1)
	case TypeData16:
		return unmarshalData(bb, 2)
	case TypeData24:
		return unmarshalData(bb, 3)
	case TypeData32:
		return unmarshalData(bb, 4)
	case TypeData40:
		return unmarshalData(bb, 5)
	case TypeData48:
		return unmarshalData(bb, 6)
	case TypeData56:
		return unmarshalData(bb, 7)
	case TypeData64:
		return unmarshalData(bb, 8)
	case TypeBoolean:
		return unmarshalBoolean(bb)
	case TypeBitmap8:
		return unmarshalUint(bb, 8)
	case TypeBitmap16:
		return unmarshalUint(bb, 16)
	case TypeBitmap24:
		return unmarshalUint(bb, 24)
	case TypeBitmap32:
		return unmarshalUint(bb, 32)
	case TypeBitmap40:
		return unmarshalUint(bb, 40)
	case TypeBitmap48:
		return unmarshalUint(bb, 48)
	case TypeBitmap56:
		return unmarshalUint(bb, 56)
	case TypeBitmap64:
		return unmarshalUint(bb, 64)
	case TypeUnsignedInt8:
		return unmarshalUint(bb, 8)
	case TypeUnsignedInt16:
		return unmarshalUint(bb, 16)
	case TypeUnsignedInt24:
		return unmarshalUint(bb, 24)
	case TypeUnsignedInt32:
		return unmarshalUint(bb, 32)
	case TypeUnsignedInt40:
		return unmarshalUint(bb, 40)
	case TypeUnsignedInt48:
		return unmarshalUint(bb, 48)
	case TypeUnsignedInt56:
		return unmarshalUint(bb, 56)
	case TypeUnsignedInt64:
		return unmarshalUint(bb, 64)
	case TypeSignedInt8:
		return unmarshalInt(bb, 8)
	case TypeSignedInt16:
		return unmarshalInt(bb, 16)
	case TypeSignedInt24:
		return unmarshalInt(bb, 24)
	case TypeSignedInt32:
		return unmarshalInt(bb, 32)
	case TypeSignedInt40:
		return unmarshalInt(bb, 40)
	case TypeSignedInt48:
		return unmarshalInt(bb, 48)
	case TypeSignedInt56:
		return unmarshalInt(bb, 56)
	case TypeSignedInt64:
		return unmarshalInt(bb, 64)
	case TypeEnum8:
		val, err := unmarshalUint(bb, 8)

		if err == nil {
			val = uint8(val.(uint64))
		}

		return val, err
	case TypeEnum16:
		val, err := unmarshalUint(bb, 16)

		if err == nil {
			val = uint16(val.(uint64))
		}

		return val, err
	case TypeStringOctet8:
		return unmarshalString(bb, 8)
	case TypeStringOctet16:
		return unmarshalString(bb, 16)
	case TypeStringCharacter8:
		return unmarshalString(bb, 8)
	case TypeStringCharacter16:
		return unmarshalString(bb, 16)
	case TypeTimeOfDay:
		return unmarshalTimeOfDay(bb)
	case TypeDate:
		return unmarshalDate(bb)
	case TypeUTCTime:
		return unmarshalUTCTime(bb)
	case TypeClusterID:
		return unmarshalClusterID(bb)
	case TypeAttributeID:
		return unmarshalAttributeID(bb)
	case TypeIEEEAddress:
		return unmarshalIEEEAddress(bb)
	case TypeSecurityKey128:
		return unmarshalSecurityKey(bb)
	case TypeBACnetOID:
		return unmarshalBACnetOID(bb)
	case TypeStructure:
		return unmarshalStructure(bb, ctx)
	case TypeArray, TypeSet, TypeBag:
		return unmarshalSlice(bb, ctx)
	case TypeFloatSingle:
		return unmarshalFloatSingle(bb)
	case TypeFloatDouble:
		return unmarshalFloatDouble(bb)
	default:
		return nil, fmt.Errorf("unsupported ZCL type to unmarshal: %d", dt)
	}
}

func unmarshalData(bb *bitbuffer.BitBuffer, size int) (interface{}, error) {
	data := make([]byte, size)

	for i := size - 1; i >= 0; i-- {
		if b, err := bb.ReadByte(); err != nil {
			return nil, err
		} else {
			data[i] = b
		}
	}

	return data, nil
}

func unmarshalBoolean(bb *bitbuffer.BitBuffer) (interface{}, error) {
	if data, err := bb.ReadByte(); err != nil {
		return nil, err
	} else {
		return data != 0x00, nil
	}
}

func unmarshalUint(bb *bitbuffer.BitBuffer, bitsize int) (interface{}, error) {
	v, err := bb.ReadUint(bitbuffer.LittleEndian, bitsize)

	if err != nil {
		return nil, err
	}

	return v, nil
}

func unmarshalInt(bb *bitbuffer.BitBuffer, bitsize int) (interface{}, error) {
	v, err := bb.ReadInt(bitbuffer.LittleEndian, bitsize)

	if err != nil {
		return nil, err
	}

	return v, nil
}

func unmarshalString(bb *bitbuffer.BitBuffer, bitsize int) (interface{}, error) {
	if data, err := bb.ReadStringLengthPrefixed(bitbuffer.LittleEndian, bitsize); err != nil {
		return nil, err
	} else {
		return data, nil
	}
}

func unmarshalStringRune(bb *bitbuffer.BitBuffer, bitsize int) (interface{}, error) {
	runeCount, err := bb.ReadUint(bitbuffer.LittleEndian, bitsize)

	if err != nil {
		return nil, err
	}

	var data []byte

	for i := 0; i < int(runeCount); i++ {
		b, err := bb.ReadByte()

		if err != nil {
			return nil, err
		}

		data = append(data, b)

		if b > 0x80 {
			moreBytes := 0

			if b < 0b1110000 {
				moreBytes = 1
			} else if b >= 0b1110000 && b < 0b11110000 {
				moreBytes = 2
			} else {
				moreBytes = 3
			}

			for i := 0; i < moreBytes; i++ {
				if b, err := bb.ReadByte(); err != nil {
					return nil, err
				} else {
					data = append(data, b)
				}
			}
		}
	}

	return string(data), nil
}

func unmarshalTimeOfDay(bb *bitbuffer.BitBuffer) (interface{}, error) {
	tod := TimeOfDay{}

	if err := bytecodec.UnmarshalFromBitBuffer(bb, &tod); err != nil {
		return nil, err
	}

	return tod, nil
}

func unmarshalDate(bb *bitbuffer.BitBuffer) (interface{}, error) {
	date := Date{}

	if err := bytecodec.UnmarshalFromBitBuffer(bb, &date); err != nil {
		return nil, err
	}

	return date, nil
}

func unmarshalUTCTime(bb *bitbuffer.BitBuffer) (interface{}, error) {
	v, err := bb.ReadInt(bitbuffer.LittleEndian, 32)

	if err != nil {
		return nil, err
	}

	return UTCTime(v), nil
}

func unmarshalClusterID(bb *bitbuffer.BitBuffer) (interface{}, error) {
	v, err := bb.ReadInt(bitbuffer.LittleEndian, 16)

	if err != nil {
		return nil, err
	}

	return zigbee.ClusterID(v), nil
}

func unmarshalAttributeID(bb *bitbuffer.BitBuffer) (interface{}, error) {
	v, err := bb.ReadInt(bitbuffer.LittleEndian, 16)

	if err != nil {
		return nil, err
	}

	return AttributeID(v), nil
}

func unmarshalIEEEAddress(bb *bitbuffer.BitBuffer) (interface{}, error) {
	v, err := bb.ReadInt(bitbuffer.LittleEndian, 64)

	if err != nil {
		return nil, err
	}

	return zigbee.IEEEAddress(v), nil
}

func unmarshalSecurityKey(bb *bitbuffer.BitBuffer) (interface{}, error) {
	networkKey := zigbee.NetworkKey{}

	if err := bytecodec.UnmarshalFromBitBuffer(bb, &networkKey); err != nil {
		return nil, err
	}

	return networkKey, nil
}

func unmarshalBACnetOID(bb *bitbuffer.BitBuffer) (interface{}, error) {
	v, err := bb.ReadInt(bitbuffer.LittleEndian, 32)

	if err != nil {
		return nil, err
	}

	return BACnetOID(v), nil
}

func unmarshalStructure(bb *bitbuffer.BitBuffer, ctx bytecodec.Context) (interface{}, error) {
	itemCount, err := bb.ReadUint(bitbuffer.LittleEndian, 16)

	if err != nil {
		return nil, err
	}

	values := []AttributeDataTypeValue{}

	for i := 0; i < int(itemCount); i++ {
		val := AttributeDataTypeValue{}

		if err := val.Unmarshal(bb, ctx); err != nil {
			return nil, err
		}

		values = append(values, val)
	}

	return values, nil
}

func unmarshalSlice(bb *bitbuffer.BitBuffer, ctx bytecodec.Context) (interface{}, error) {
	rawType, err := bb.ReadUint(bitbuffer.LittleEndian, 8)

	if err != nil {
		return nil, err
	}

	itemCount, err := bb.ReadUint(bitbuffer.LittleEndian, 16)

	if err != nil {
		return nil, err
	}

	itemType := AttributeDataType(rawType)

	value := AttributeSlice{
		DataType: itemType,
		Values:   []interface{}{},
	}

	for i := 0; i < int(itemCount); i++ {
		if val, err := unmarshalZCLType(bb, itemType, ctx); err != nil {
			return nil, err
		} else {
			value.Values = append(value.Values, val)
		}
	}

	return value, nil
}

func unmarshalFloatSingle(bb *bitbuffer.BitBuffer) (interface{}, error) {
	if bits, err := bb.ReadUint(bitbuffer.LittleEndian, 32); err != nil {
		return nil, err
	} else {
		return math.Float32frombits(uint32(bits)), nil
	}
}

func unmarshalFloatDouble(bb *bitbuffer.BitBuffer) (interface{}, error) {
	if bits, err := bb.ReadUint(bitbuffer.LittleEndian, 64); err != nil {
		return nil, err
	} else {
		return math.Float64frombits(bits), nil
	}
}
