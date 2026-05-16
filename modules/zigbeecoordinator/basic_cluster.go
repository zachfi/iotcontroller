package zigbeecoordinator

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	zclv1proto "github.com/zachfi/iotcontroller/proto/zcl/v1"
	zigbeev1proto "github.com/zachfi/iotcontroller/proto/zigbee/v1"
)

// ZCL Basic cluster (0x0000) attribute identifiers we read during
// interview rescue. Defined in ZCL spec §3.2 ("Basic" cluster).
const (
	basicClusterID = 0x0000

	basicAttrManufacturerName = 0x0004 // CharacterString, ZCL type 0x42
	basicAttrModelIdentifier  = 0x0005 // CharacterString, ZCL type 0x42
	basicAttrPowerSource      = 0x0007 // Enumeration8,   ZCL type 0x30

	// ZCL global command ids relevant here.
	zclCommandReadAttributes = 0x00 // Frame command id, NOT a cluster command
)

// PowerSource enum values per ZCL §3.2.2.2.8. Only the common ones —
// other values exist (battery backup, etc.) but the interview path
// today only needs to record what we got; downstream UI maps further.
const (
	PowerSourceUnknown          uint8 = 0x00
	PowerSourceMainsSinglePhase uint8 = 0x01
	PowerSourceMainsThreePhase  uint8 = 0x02
	PowerSourceBattery          uint8 = 0x03
	PowerSourceDCSource         uint8 = 0x04
)

// mapZclPowerSourceToProto translates a ZCL power-source byte to the
// zigbee proto enum. Values not listed map to UNSPECIFIED (which the
// router's InterviewRoute treats as "don't update the Device CR field"
// — preserving prior knowledge from a successful earlier interview).
func mapZclPowerSourceToProto(zclByte uint8) zigbeev1proto.PowerSource {
	switch zclByte {
	case PowerSourceMainsSinglePhase, PowerSourceMainsThreePhase:
		return zigbeev1proto.PowerSource_POWER_SOURCE_MAINS
	case PowerSourceBattery:
		return zigbeev1proto.PowerSource_POWER_SOURCE_BATTERY
	case PowerSourceDCSource:
		return zigbeev1proto.PowerSource_POWER_SOURCE_DC
	case PowerSourceUnknown:
		return zigbeev1proto.PowerSource_POWER_SOURCE_UNKNOWN
	default:
		return zigbeev1proto.PowerSource_POWER_SOURCE_UNSPECIFIED
	}
}

// BasicClusterAttributes carries the human-readable identification
// information we can read from a ZCL device that supports the Basic
// cluster (which is mandatory for all ZCL clusters per the spec, but
// individual attributes may return UNSUPPORTED_ATTRIBUTE).
//
// Fields are zero-valued when the device returned a non-SUCCESS status
// for that specific attribute or when the response didn't include the
// attribute at all — the caller can distinguish "didn't ask" from
// "asked, device declined" only by comparing the returned value to its
// zero. For now no caller in this codebase needs that distinction, so
// the API stays compact.
type BasicClusterAttributes struct {
	Manufacturer string // attribute 0x0004
	Model        string // attribute 0x0005
	PowerSource  uint8  // attribute 0x0007
}

// readBasicClusterAttributes performs a ZCL ReadAttributes against
// cluster 0x0000 on the given device endpoint, requesting manufacturer
// (0x0004), model (0x0005), and power source (0x0007) in a single
// transaction. Returns whatever the device chose to send back; missing
// or UNSUPPORTED_ATTRIBUTE responses leave the corresponding field at
// its zero value.
//
// Used by the interview rescue path when ZDO node descriptor failed —
// or as an enrichment alongside successful ZDO. Either way it's the
// only path that populates human-readable manufacturer / model strings
// (ZDO carries only a 16-bit manufacturer code).
//
// `timeout` should be generous (≥5s) for battery-powered end devices
// that poll their parent on a slow schedule. The dongle does not retry
// internally; if the timeout fires the caller decides whether to call
// again (the next sendZclAndAwait takes a fresh txn seq, so retries
// don't collide with stale responses on the tap).
func (z *ZigbeeCoordinator) readBasicClusterAttributes(
	ctx context.Context,
	nwk uint16,
	endpoint uint8,
	timeout time.Duration,
) (*BasicClusterAttributes, error) {
	txnSeq := z.nextZclTxnSequence()

	frame := encodeReadAttributesFrame(txnSeq, []uint16{
		basicAttrManufacturerName,
		basicAttrModelIdentifier,
		basicAttrPowerSource,
	})

	resp, err := z.sendZclAndAwait(ctx, nwk, endpoint, basicClusterID, txnSeq, frame, timeout)
	if err != nil {
		return nil, fmt.Errorf("Basic cluster read failed: %w", err)
	}

	out := &BasicClusterAttributes{}
	rec := resp.GetFrame().GetCommand().GetGlobalReadAttributesResponse()
	if rec == nil {
		return nil, fmt.Errorf("Basic cluster response had no ReadAttributesResponse payload (cluster=0x%04x, txnSeq=%d)", basicClusterID, txnSeq)
	}

	for _, r := range rec.GetRecords() {
		if r.GetStatus() != zclv1proto.ZclStatus_ZCL_STATUS_SUCCESS {
			// UNSUPPORTED_ATTRIBUTE (0x86) is the common case for
			// non-standard firmware — log at debug and leave the
			// field at its zero value. We don't fail the whole read
			// just because one attribute is missing; partial info
			// is strictly better than no info.
			z.logger.Debug("Basic cluster attribute returned non-success status",
				slog.String("attribute", fmt.Sprintf("0x%04x", r.GetAttributeId())),
				slog.String("status", r.GetStatus().String()),
			)
			continue
		}

		switch r.GetAttributeId() {
		case basicAttrManufacturerName:
			out.Manufacturer = r.GetValue().GetStringValue()
		case basicAttrModelIdentifier:
			out.Model = r.GetValue().GetStringValue()
		case basicAttrPowerSource:
			// Enum8 lands in the Uint8Value slot per the parser's
			// type-coverage layout (see pkg/zcl/parser.go).
			out.PowerSource = uint8(r.GetValue().GetUint8Value())
		}
	}

	return out, nil
}

// encodeReadAttributesFrame builds a ZCL global ReadAttributes (cmd 0x00)
// frame for the given attribute ids.
//
// Frame layout, per ZCL §2.3:
//
//	+------+---------+--------+----------------+
//	| FCF  | TxnSeq  | CmdID  | AttributeIDs   |
//	| 1    | 1       | 1      | 2 * N (LE)     |
//	+------+---------+--------+----------------+
//
// FCF = 0x00: frame type = global, direction = client→server, NOT
// manufacturer-specific, disable-default-response = 0. We leave DDR=0
// so the device knows we're listening for the response — though most
// devices will send the ReadAttributesResponse regardless because
// command 0x01 (the response) is not subject to default-response
// suppression.
//
// AttributeIDs are little-endian (Zigbee on-wire byte order).
func encodeReadAttributesFrame(txnSeq uint8, attrIDs []uint16) []byte {
	buf := make([]byte, 0, 3+2*len(attrIDs))
	buf = append(buf, 0x00)                     // FCF
	buf = append(buf, txnSeq)                   // Transaction sequence
	buf = append(buf, zclCommandReadAttributes) // Command id

	for _, id := range attrIDs {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], id)
		buf = append(buf, b[:]...)
	}
	return buf
}
