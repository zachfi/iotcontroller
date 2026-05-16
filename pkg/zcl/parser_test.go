package zcl

import (
	"testing"

	"github.com/shimmeringbee/zcl"
	"github.com/stretchr/testify/require"
	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

// Test_convertAttributeValue pins the dynamic-type contract between
// shimmeringbee's attribute unmarshaller and our proto converter. The
// regression that motivated the table: on 2026-05-15 a Sonoff SNZB-02
// joined natively and emitted cluster=0x0402 (Temperature Measurement)
// reports with data type 0x29 (TypeSignedInt16). The converter then
// returned "unsupported data type: 41" because:
//
//  1. There was no case for TypeSignedInt16, and
//  2. The existing TypeUnsignedInt* cases asserted dtv.Value.(uint),
//     but shimmeringbee unmarshalUint returns uint64 (the raw value
//     from bitbuffer.ReadUint), so even unsigned ints had been
//     silently failing — they just never got exercised because every
//     other paired device was routed through z2m.
//
// Each row picks a representative type from each family and verifies
// the resulting oneof slot + DataType enum. Width-bucket boundaries
// (8/16/24/32 → fixed slot, 40/48/56/64 → 64-bit slot) get explicit
// coverage so a future split or merge of those buckets doesn't quietly
// drop a width.
func Test_convertAttributeValue(t *testing.T) {
	p := &Parser{}

	cases := []struct {
		name           string
		dt             zcl.AttributeDataType
		value          interface{}
		wantSlot       string // name of the AttributeValue oneof slot
		wantDataType   zclv1proto.ZclDataType
		wantIntValue   int64  // for signed cases (uses Int*Value)
		wantUintValue  uint64 // for unsigned/bitmap/enum (uses Uint*Value)
		wantBoolValue  bool
		wantFloatValue float64
		wantString     string
	}{
		{
			name:          "boolean true",
			dt:            zcl.TypeBoolean,
			value:         true,
			wantSlot:      "bool",
			wantDataType:  zclv1proto.ZclDataType_ZCL_DATA_TYPE_BOOLEAN,
			wantBoolValue: true,
		},
		// Regression: temperature report. Data bytes 0x280a (little-endian
		// int16 = 2600) → 26.00°C. unmarshalInt returns int64.
		{
			name:         "SignedInt16 (temperature) — 2600",
			dt:           zcl.TypeSignedInt16,
			value:        int64(2600),
			wantSlot:     "int16",
			wantDataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT16,
			wantIntValue: 2600,
		},
		{
			name:         "SignedInt16 negative",
			dt:           zcl.TypeSignedInt16,
			value:        int64(-1234),
			wantSlot:     "int16",
			wantDataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT16,
			wantIntValue: -1234,
		},
		{
			name:         "SignedInt8",
			dt:           zcl.TypeSignedInt8,
			value:        int64(-42),
			wantSlot:     "int8",
			wantDataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT8,
			wantIntValue: -42,
		},
		{
			name:         "SignedInt32 — boundary into 32-bit slot",
			dt:           zcl.TypeSignedInt32,
			value:        int64(-2_000_000_000),
			wantSlot:     "int32",
			wantDataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT32,
			wantIntValue: -2_000_000_000,
		},
		{
			name:         "SignedInt64 — widens into 64-bit slot",
			dt:           zcl.TypeSignedInt64,
			value:        int64(-9_000_000_000_000_000_000),
			wantSlot:     "int64",
			wantDataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_INT64,
			wantIntValue: -9_000_000_000_000_000_000,
		},
		// Bug fix: the existing UInt16 case used to assert .(uint) and
		// always missed; this row would have failed before the fix.
		{
			name:          "UnsignedInt16 (humidity-style) — 5523",
			dt:            zcl.TypeUnsignedInt16,
			value:         uint64(5523),
			wantSlot:      "uint16",
			wantDataType:  zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT16,
			wantUintValue: 5523,
		},
		{
			name:          "UnsignedInt8",
			dt:            zcl.TypeUnsignedInt8,
			value:         uint64(200),
			wantSlot:      "uint8",
			wantDataType:  zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT8,
			wantUintValue: 200,
		},
		{
			name:          "UnsignedInt32",
			dt:            zcl.TypeUnsignedInt32,
			value:         uint64(1_234_567_890),
			wantSlot:      "uint32",
			wantDataType:  zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT32,
			wantUintValue: 1_234_567_890,
		},
		{
			name:          "UnsignedInt64 — widens into 64-bit slot",
			dt:            zcl.TypeUnsignedInt64,
			value:         uint64(18_000_000_000_000_000_000),
			wantSlot:      "uint64",
			wantDataType:  zclv1proto.ZclDataType_ZCL_DATA_TYPE_UINT64,
			wantUintValue: 18_000_000_000_000_000_000,
		},
		// Enum8 (power source on Basic cluster attr 0x0007) shares the
		// uint8 proto slot but reports ENUM8 as its semantic type.
		// Note: shimmeringbee's unmarshal layer down-converts Enum8 to
		// Go uint8 (not uint64), unlike UnsignedInt8 / Bitmap8 which
		// stay as uint64. The parser asserts against the right type
		// for each — pin that here so a refactor that merges the
		// Enum8 case back into the unsigned-int case (because they
		// "look the same") breaks loudly. The 2026-05-15 SNZB-02
		// power-source = 0 bug was exactly this assertion failure
		// silently dropping into the data-bytes fallback.
		{
			name:          "Enum8 (power source: battery = 0x03)",
			dt:            zcl.TypeEnum8,
			value:         uint8(0x03),
			wantSlot:      "uint8",
			wantDataType:  zclv1proto.ZclDataType_ZCL_DATA_TYPE_ENUM8,
			wantUintValue: 3,
		},
		{
			name:          "Enum16",
			dt:            zcl.TypeEnum16,
			value:         uint16(0x1234),
			wantSlot:      "uint16",
			wantDataType:  zclv1proto.ZclDataType_ZCL_DATA_TYPE_ENUM16,
			wantUintValue: 0x1234,
		},
		{
			name:          "Bitmap16",
			dt:            zcl.TypeBitmap16,
			value:         uint64(0xABCD),
			wantSlot:      "uint16",
			wantDataType:  zclv1proto.ZclDataType_ZCL_DATA_TYPE_BITMAP16,
			wantUintValue: 0xABCD,
		},
		{
			name:           "FloatSingle",
			dt:             zcl.TypeFloatSingle,
			value:          float32(3.14),
			wantSlot:       "float32",
			wantDataType:   zclv1proto.ZclDataType_ZCL_DATA_TYPE_FLOAT_SINGLE,
			wantFloatValue: float64(float32(3.14)),
		},
		{
			name:           "FloatDouble",
			dt:             zcl.TypeFloatDouble,
			value:          float64(2.718281828),
			wantSlot:       "float64",
			wantDataType:   zclv1proto.ZclDataType_ZCL_DATA_TYPE_FLOAT_DOUBLE,
			wantFloatValue: 2.718281828,
		},
		{
			name:         "StringCharacter8 (Basic cluster manufacturer / model)",
			dt:           zcl.TypeStringCharacter8,
			value:        "SONOFF",
			wantSlot:     "string",
			wantDataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_STRING_CHARACTER8,
			wantString:   "SONOFF",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.convertAttributeValue(&zcl.AttributeDataTypeValue{
				DataType: tc.dt,
				Value:    tc.value,
			})
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, tc.wantDataType, got.DataType, "DataType enum")

			switch tc.wantSlot {
			case "bool":
				v, ok := got.Value.(*zclv1proto.AttributeValue_BoolValue)
				require.True(t, ok, "expected BoolValue slot, got %T", got.Value)
				require.Equal(t, tc.wantBoolValue, v.BoolValue)
			case "int8":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Int8Value)
				require.True(t, ok, "expected Int8Value slot, got %T", got.Value)
				require.Equal(t, tc.wantIntValue, int64(v.Int8Value))
			case "int16":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Int16Value)
				require.True(t, ok, "expected Int16Value slot, got %T", got.Value)
				require.Equal(t, tc.wantIntValue, int64(v.Int16Value))
			case "int32":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Int32Value)
				require.True(t, ok, "expected Int32Value slot, got %T", got.Value)
				require.Equal(t, tc.wantIntValue, int64(v.Int32Value))
			case "int64":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Int64Value)
				require.True(t, ok, "expected Int64Value slot, got %T", got.Value)
				require.Equal(t, tc.wantIntValue, v.Int64Value)
			case "uint8":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Uint8Value)
				require.True(t, ok, "expected Uint8Value slot, got %T", got.Value)
				require.Equal(t, tc.wantUintValue, uint64(v.Uint8Value))
			case "uint16":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Uint16Value)
				require.True(t, ok, "expected Uint16Value slot, got %T", got.Value)
				require.Equal(t, tc.wantUintValue, uint64(v.Uint16Value))
			case "uint32":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Uint32Value)
				require.True(t, ok, "expected Uint32Value slot, got %T", got.Value)
				require.Equal(t, tc.wantUintValue, uint64(v.Uint32Value))
			case "uint64":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Uint64Value)
				require.True(t, ok, "expected Uint64Value slot, got %T", got.Value)
				require.Equal(t, tc.wantUintValue, v.Uint64Value)
			case "float32":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Float32Value)
				require.True(t, ok, "expected Float32Value slot, got %T", got.Value)
				require.InDelta(t, tc.wantFloatValue, float64(v.Float32Value), 1e-6)
			case "float64":
				v, ok := got.Value.(*zclv1proto.AttributeValue_Float64Value)
				require.True(t, ok, "expected Float64Value slot, got %T", got.Value)
				require.InDelta(t, tc.wantFloatValue, v.Float64Value, 1e-12)
			case "string":
				v, ok := got.Value.(*zclv1proto.AttributeValue_StringValue)
				require.True(t, ok, "expected StringValue slot, got %T", got.Value)
				require.Equal(t, tc.wantString, v.StringValue)
			default:
				t.Fatalf("unhandled wantSlot %q", tc.wantSlot)
			}
		})
	}
}

// Test_convertAttributeValue_nil verifies the early-return on a nil
// input — a defensive contract worth keeping pinned.
func Test_convertAttributeValue_nil(t *testing.T) {
	p := &Parser{}
	_, err := p.convertAttributeValue(nil)
	require.Error(t, err)
}
