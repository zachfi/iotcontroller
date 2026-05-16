package zigbeecoordinator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
)

// Test_encodeReadAttributesFrame pins the on-wire byte layout for the
// ZCL ReadAttributes (0x00) frame used by the interview rescue path.
// The bytes here are what gets put into AfDataRequest.Data and sent
// over the air, so any drift is observable on a packet capture; pin
// it so a refactor of the encoder doesn't silently re-order fields.
func Test_encodeReadAttributesFrame(t *testing.T) {
	got := encodeReadAttributesFrame(0x42, []uint16{
		0x0004, 0x0005, 0x0007,
	})
	want := []byte{
		0x00,       // FCF: global / client-to-server / no DDR / not manuf-specific
		0x42,       // txn seq
		0x00,       // command id: ReadAttributes
		0x04, 0x00, // attr 0x0004 (manufacturerName), little-endian
		0x05, 0x00, // attr 0x0005 (modelIdentifier)
		0x07, 0x00, // attr 0x0007 (powerSource)
	}
	require.Equal(t, want, got)
}

func Test_encodeReadAttributesFrame_EmptyAttrList(t *testing.T) {
	// Degenerate but well-defined: no attrs means just the 3-byte
	// header. The device will respond with an empty
	// ReadAttributesResponse — used by the test below to assert
	// the parser handles "no records" gracefully.
	got := encodeReadAttributesFrame(0x01, nil)
	require.Equal(t, []byte{0x00, 0x01, 0x00}, got)
}

// makeBasicReadResponse constructs a synthetic ZclMessage with a
// GlobalReadAttributesResponse for the three Basic-cluster attributes,
// matching what the parser would produce from a real ReadAttributesResponse
// on the wire. Each (status, manuf, model, powerSrc) parameter shapes
// the corresponding record; pass status != SUCCESS to simulate
// UNSUPPORTED_ATTRIBUTE.
func makeBasicReadResponse(nwk uint16, txnSeq uint8, mfStatus zclv1proto.ZclStatus, manufacturer string, modelStatus zclv1proto.ZclStatus, model string, psStatus zclv1proto.ZclStatus, powerSource uint8) *zclv1proto.ZclMessage {
	rec := &zclv1proto.GlobalReadAttributesResponse{
		Records: []*zclv1proto.AttributeResponseRecord{
			{
				AttributeId: basicAttrManufacturerName,
				Status:      mfStatus,
				Value: &zclv1proto.AttributeValue{
					DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_STRING_CHARACTER8,
					Value:    &zclv1proto.AttributeValue_StringValue{StringValue: manufacturer},
				},
			},
			{
				AttributeId: basicAttrModelIdentifier,
				Status:      modelStatus,
				Value: &zclv1proto.AttributeValue{
					DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_STRING_CHARACTER8,
					Value:    &zclv1proto.AttributeValue_StringValue{StringValue: model},
				},
			},
			{
				AttributeId: basicAttrPowerSource,
				Status:      psStatus,
				Value: &zclv1proto.AttributeValue{
					DataType: zclv1proto.ZclDataType_ZCL_DATA_TYPE_ENUM8,
					Value:    &zclv1proto.AttributeValue_Uint8Value{Uint8Value: uint32(powerSource)},
				},
			},
		},
	}
	return &zclv1proto.ZclMessage{
		SourceNetwork: uint32(nwk),
		ClusterId:     basicClusterID,
		Frame: &zclv1proto.ZclFrame{
			TransactionSequence: uint32(txnSeq),
			Command: &zclv1proto.ZclCommand{
				Command: &zclv1proto.ZclCommand_GlobalReadAttributesResponse{
					GlobalReadAttributesResponse: rec,
				},
			},
		},
	}
}

// Test_readBasicClusterAttributes_HappyPath: the canonical case — device
// answers all three attributes with SUCCESS, we receive the strings and
// power source value. Pins the field mapping from response records to
// the BasicClusterAttributes struct.
func Test_readBasicClusterAttributes_HappyPath(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	dongle := &sendCapturingDongle{z: z}
	z.dongle = dongle

	// The reader will pick a txn seq from nextZclTxnSequence; we can't
	// pre-construct the response without knowing the seq. Use a callback
	// on the dongle Send to capture the seq and then publish a matching
	// response. The tap's key triple includes txn seq, so an off-by-one
	// here would land in the channel-full drop branch (verified
	// separately).
	const nwk = uint16(0xf88e)
	dongle.respondWith = nil // Not using the simple-response path
	dongle.sendErr = nil

	// Wrap Send with a captor that publishes a response based on the
	// actual txn seq we put on the wire.
	origDongle := z.dongle
	z.dongle = &dynamicResponseDongle{
		inner: origDongle,
		respond: func(out []byte) *zclv1proto.ZclMessage {
			// frame[1] is the txn seq we encoded
			require.GreaterOrEqual(t, len(out), 3)
			return makeBasicReadResponse(nwk, out[1],
				zclv1proto.ZclStatus_ZCL_STATUS_SUCCESS, "SONOFF",
				zclv1proto.ZclStatus_ZCL_STATUS_SUCCESS, "SNZB-02",
				zclv1proto.ZclStatus_ZCL_STATUS_SUCCESS, PowerSourceBattery,
			)
		},
		coordinator: z,
	}

	out, err := z.readBasicClusterAttributes(context.Background(), nwk, 1, 2*time.Second)
	require.NoError(t, err)
	require.Equal(t, "SONOFF", out.Manufacturer)
	require.Equal(t, "SNZB-02", out.Model)
	require.Equal(t, PowerSourceBattery, out.PowerSource)
}

// Test_readBasicClusterAttributes_PartialResponse: a real-world Tuya
// quirk — the device returns SUCCESS for some attributes and
// UNSUPPORTED_ATTRIBUTE for others. The reader must return whatever it
// got rather than failing the whole call. This is the "partial is
// better than nothing" contract.
func Test_readBasicClusterAttributes_PartialResponse(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	const nwk = uint16(0xabcd)

	z.dongle = &dynamicResponseDongle{
		coordinator: z,
		respond: func(out []byte) *zclv1proto.ZclMessage {
			require.GreaterOrEqual(t, len(out), 3)
			return makeBasicReadResponse(nwk, out[1],
				zclv1proto.ZclStatus_ZCL_STATUS_SUCCESS, "_TZ3000_xyz",
				zclv1proto.ZclStatus_ZCL_STATUS_UNSUPPORTED_ATTRIBUTE, "", // model missing
				zclv1proto.ZclStatus_ZCL_STATUS_SUCCESS, PowerSourceBattery,
			)
		},
	}

	out, err := z.readBasicClusterAttributes(context.Background(), nwk, 1, 2*time.Second)
	require.NoError(t, err)
	require.Equal(t, "_TZ3000_xyz", out.Manufacturer)
	require.Equal(t, "", out.Model, "missing attribute leaves field at zero value")
	require.Equal(t, PowerSourceBattery, out.PowerSource)
}

// Test_readBasicClusterAttributes_NoRecordsAtAll: response frame
// successfully arrived but carries an empty record set. Treat as
// "we got nothing useful" — return zero-valued struct, no error.
func Test_readBasicClusterAttributes_EmptyRecords(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	const nwk = uint16(0x1234)

	z.dongle = &dynamicResponseDongle{
		coordinator: z,
		respond: func(out []byte) *zclv1proto.ZclMessage {
			return &zclv1proto.ZclMessage{
				SourceNetwork: uint32(nwk),
				ClusterId:     basicClusterID,
				Frame: &zclv1proto.ZclFrame{
					TransactionSequence: uint32(out[1]),
					Command: &zclv1proto.ZclCommand{
						Command: &zclv1proto.ZclCommand_GlobalReadAttributesResponse{
							GlobalReadAttributesResponse: &zclv1proto.GlobalReadAttributesResponse{
								Records: nil,
							},
						},
					},
				},
			}
		},
	}

	out, err := z.readBasicClusterAttributes(context.Background(), nwk, 1, 2*time.Second)
	require.NoError(t, err)
	require.Equal(t, "", out.Manufacturer)
	require.Equal(t, "", out.Model)
	require.Equal(t, uint8(0), out.PowerSource)
}

// Test_readBasicClusterAttributes_Timeout: no response arrives. The
// reader surfaces the timeout error from sendZclAndAwait verbatim so
// the caller can decide whether to retry.
func Test_readBasicClusterAttributes_Timeout(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	z.dongle = &sendCapturingDongle{z: z} // never publishes a response

	_, err := z.readBasicClusterAttributes(context.Background(), 0xdead, 1, 50*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Basic cluster read failed")
}

// Test_readBasicClusterAttributes_WrongCommandInResponse: a malformed
// response (right txn seq but missing the GlobalReadAttributesResponse
// command) is reported as an error rather than silently returning an
// empty struct — empty-records vs. wrong-command are different signals
// and the caller may want to distinguish.
func Test_readBasicClusterAttributes_WrongCommandInResponse(t *testing.T) {
	z := newResponseFilterCoordinator(t)
	const nwk = uint16(0x1111)

	z.dongle = &dynamicResponseDongle{
		coordinator: z,
		respond: func(out []byte) *zclv1proto.ZclMessage {
			// Frame arrives with the right txn seq but command is a
			// DefaultResponse instead of a ReadAttributesResponse.
			return &zclv1proto.ZclMessage{
				SourceNetwork: uint32(nwk),
				ClusterId:     basicClusterID,
				Frame: &zclv1proto.ZclFrame{
					TransactionSequence: uint32(out[1]),
					Command: &zclv1proto.ZclCommand{
						Command: &zclv1proto.ZclCommand_GlobalDefaultResponse{},
					},
				},
			}
		},
	}

	_, err := z.readBasicClusterAttributes(context.Background(), nwk, 1, 2*time.Second)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no ReadAttributesResponse")
}
