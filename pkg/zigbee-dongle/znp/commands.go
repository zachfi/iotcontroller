package znp

import (
	"fmt"
)

// ProfileID represents a Zigbee application profile identifier.
// This is a local type to avoid import cycles. Convert to zigbeedongle.ProfileID when needed.
type ProfileID uint16

const (
	ProfileDevice                       ProfileID = 0x0000
	ProfileIndustrialPlantMonitoring    ProfileID = 0x0101
	ProfileHomeAutomation               ProfileID = 0x0104
	ProfileCommercialBuildingAutomation ProfileID = 0x0105
	ProfileTelecomApplications          ProfileID = 0x0107
	ProfilePersonalHomeAndHospitalCare  ProfileID = 0x0108
	ProfileAdvancedMeteringInitialtive  ProfileID = 0x0109
)

type DeviceState uint8

const (
	DeviceStateInitializedNotStarted   DeviceState = 0x00 // Initialized - not started automatically
	DeviceStateInitializedNotConnected DeviceState = 0x01 // Initialized - not connected to anything
	DeviceStateDiscovering             DeviceState = 0x02 // Discovering PANs to join
	DeviceStateJoining                 DeviceState = 0x03 // Joining a PAN
	DeviceStateRejoining               DeviceState = 0x04 // Rejoining a PAN, only for end devices
	DeviceStateJoinedNotAuthenticated  DeviceState = 0x05 // Joined but not yet authenticated by trust center
	DeviceStateEndDevice               DeviceState = 0x06 // Started as device after authentication
	DeviceStateRouter                  DeviceState = 0x07 // Device joined, authenticated and is a router
	DeviceStateCoordinatorStarting     DeviceState = 0x08 // Starting as ZigBee Coordinator
	DeviceStateCoordinator             DeviceState = 0x09 // Started as ZigBee Coordinator
	DeviceStateOrphan                  DeviceState = 0x0A // Device has lost information about its parent
)

var DeviceStateNames = []string{
	"InitializedNotStarted",
	"InitializedNotConnected",
	"Discovering",
	"Joining",
	"Rejoining",
	"JoinedNotAuthenticated",
	"EndDevice",
	"Router",
	"CoordinatorStarting",
	"Coordinator",
	"Orphan",
}

func (state DeviceState) String() string {
	if int(state) < len(DeviceStateNames) {
		return fmt.Sprintf("%s(%d)", DeviceStateNames[state], uint8(state))
	}
	return fmt.Sprintf("%d", uint8(state))
}

/* FRAME_SUBSYSTEM_SYS */

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_SYS, 0x02, SysVersionRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_SYS, 0x02, SysVersionResponse{})
}

type SysVersionRequest struct{}

type SysVersionResponse struct {
	TransportRev uint8
	Product      uint8
	MajorRel     uint8
	MinorRel     uint8
	MaintRel     uint8
	Revision     uint32
}

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_SYS, 0x08, SysOsalNvReadRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_SYS, 0x08, SysOsalNvReadResponse{})
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_SYS, 0x09, SysOsalNvWriteRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_SYS, 0x09, SysOsalNvWriteResponse{})
}

type SysOsalNvReadRequest struct {
	ID     uint16
	Offset uint8
}

type SysOsalNvReadResponse struct {
	Status byte
	Value  []byte
}

type SysOsalNvWriteRequest struct {
	ID     uint16
	Offset uint8
	Value  []byte
}

type SysOsalNvWriteResponse struct {
	Status byte
}

func init() {
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_SYS, 0x80, SysResetInd{})
}

const (
	ResetReasonPowerUp  = 0x00
	ResetReasonExternal = 0x01
	ResetReasonWatchDog = 0x02
)

type SysResetInd struct {
	Reason       uint8
	TransportRev uint8
	Product      uint8
	MajorRel     uint8
	MinorRel     uint8
	MaintRel     uint8
}

/* FRAME_SUBSYSTEM_MAC */

/* FRAME_SUBSYSTEM_NWK */

/* FRAME_SUBSYSTEM_AF */

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_AF, 0x00, AfRegisterRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_AF, 0x00, AfRegisterResponse{})
}

const (
	LatencyReqNoLatency   = 0x00
	LatencyReqFastBeacons = 0x01
	LatencyReqSlowBeacons = 0x02
)

type AfRegisterRequest struct {
	Endpoint       uint8
	AppProfID      ProfileID
	AppDeviceID    uint16
	AddDevVer      uint8
	LatencyReq     uint8
	AppInClusters  []uint16
	AppOutClusters []uint16
}

type AfRegisterResponse struct {
	Status byte
}

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_AF, 0x01, AfDataRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_AF, 0x01, AfDataResponse{})
}

type AfDataRequest struct {
	DstAddr        uint16
	DstEndpoint    uint8
	SrcEndpoint    uint8
	ClusterID      uint16
	TransSeqNumber uint8
	Options        uint8
	Radius         uint8
	Data           []byte
}

type AfDataResponse struct {
	Status byte
}

func init() {
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_AF, 0x80, AfDataConfirm{})
}

type AfDataConfirm struct {
	Status         byte
	Endpoint       uint8
	TransSeqNumber uint8
}

func init() {
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_AF, 0x81, AfIncomingMsg{})
}

type AfIncomingMsg struct {
	GroupID        uint16
	ClusterID      uint16
	SrcAddr        uint16
	SrcEndpoint    uint8
	DstEndpoint    uint8
	WasBroadcast   uint8
	LinkQuality    uint8
	SecureUse      uint8
	TimeStamp      uint32
	TransSeqNumber uint8
	Data           []byte
}

/* FRAME_SUBSYSTEM_ZDO*/

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_ZDO, 0x05, ZdoActiveEPRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_ZDO, 0x05, ZdoActiveEPResponse{})
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0x85, ZdoActiveEP{})
}

type ZdoActiveEPRequest struct {
	DstAddr           uint16
	NWKAddrOfInterest uint16
}

type ZdoActiveEPResponse struct {
	Status byte
}

type ZdoActiveEP struct {
	SrcAddr   uint16
	Status    byte
	NWKAddr   uint16
	ActiveEPs []uint8
}

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_ZDO, 0x36, ZdoMgmtPermitJoinRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_ZDO, 0x36, ZdoMgmtPermitJoinResponse{})
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0xb6, ZdoMgmtPermitJoin{})
}

type ZdoMgmtPermitJoinRequest struct {
	AddrMode       byte
	DstAddr        uint16
	Duration       byte
	TCSignificance byte
}

type ZdoMgmtPermitJoinResponse struct {
	Status byte
}

type ZdoMgmtPermitJoin struct {
	SrcAddr uint16
	Status  byte
}

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_ZDO, 0x40, ZdoStartupFromAppRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_ZDO, 0x40, ZdoStartupFromAppResponse{})
}

type ZdoStartupFromAppRequest struct {
	StartDelay uint16
}

type ZdoStartupFromAppResponse struct {
	Status byte
}

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_ZDO, 0x50, ZdoExtNwkInfoRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_ZDO, 0x50, ZdoExtNwkInfoResponse{})
}

type ZdoExtNwkInfoRequest struct{}

type ZdoExtNwkInfoResponse struct {
	ShortAddress          uint16
	DeviceState           uint8 // Device state (not used, but present in response)
	PanID                 uint16
	ParentAddress         uint16
	ExtendedPanID         uint64
	ExtendedParentAddress uint64
	Channel               uint8 // Channel is uint8 (11-26), not uint16
}

func init() {
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0xc0, ZdoStateChangeInd{})
}

type ZdoStateChangeInd struct {
	State DeviceState
}

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_ZDO, 0x02, ZdoNodeDescriptorRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_ZDO, 0x02, ZdoNodeDescriptorResponse{})
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0x82, ZdoNodeDescriptor{})
}

// ZdoNodeDescriptorRequest requests the node descriptor from a device.
// ZNP Command ID: 0x02 (ZDO subsystem)
type ZdoNodeDescriptorRequest struct {
	DstAddr uint16 // Network address of target device
}

// ZdoNodeDescriptorResponse is the synchronous response to NodeDescriptorRequest.
type ZdoNodeDescriptorResponse struct {
	Status byte // Status code (0x00 = SUCCESS)
}

// ZdoNodeDescriptor is the asynchronous response containing the node descriptor.
// ZNP protocol format: [srcaddr:2] [status:1] [nwkaddr:2] [logicaltype:1] [complexdesc:1] [userdesc:1] [reserved:1]
//
//	[apsflags:1] [freqband:1] [maccapflags:1] [manufacturercode:2] [maxbuffersize:1]
//	[maxintransfersize:2] [servermask:2] [maxouttransfersize:2] [descriptorcap:1]
//
// Note: ZNP sends this as a flat byte array, not a nested struct. We parse it manually in interview.go.
type ZdoNodeDescriptor struct {
	SrcAddr            uint16
	Status             byte
	NWKAddr            uint16
	LogicalType        byte   // 0x00=Coordinator, 0x01=Router, 0x02=EndDevice
	ComplexDescriptor  byte   // Complex descriptor available (0x00=no, 0x01=yes)
	UserDescriptor     byte   // User descriptor available (0x00=no, 0x01=yes)
	Reserved           byte   // Reserved field
	APSFlags           byte   // APS flags
	FrequencyBand      byte   // Frequency band
	MACCapabilityFlags byte   // MAC capability flags (bit flags)
	ManufacturerCode   uint16 // Manufacturer code
	MaxBufferSize      byte   // Maximum buffer size
	MaxInTransferSize  uint16 // Maximum incoming transfer size
	ServerMask         uint16 // Server mask (bit flags for server capabilities)
	MaxOutTransferSize uint16 // Maximum outgoing transfer size
	DescriptorCap      byte   // Descriptor capability field
}

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_ZDO, 0x04, ZdoSimpleDescriptorRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_ZDO, 0x04, ZdoSimpleDescriptorResponse{})
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0x84, ZdoSimpleDescriptor{})
}

// ZdoSimpleDescriptorRequest requests the simple descriptor for a specific endpoint.
// ZNP Command ID: 0x04 (ZDO subsystem)
type ZdoSimpleDescriptorRequest struct {
	DstAddr           uint16 // Network address of target device
	NWKAddrOfInterest uint16 // Usually same as DstAddr
	Endpoint          uint8  // Endpoint to query (1-240)
}

// ZdoSimpleDescriptorResponse is the synchronous response to SimpleDescriptorRequest.
type ZdoSimpleDescriptorResponse struct {
	Status byte // Status code (0x00 = SUCCESS)
}

// ZdoSimpleDescriptor is the asynchronous response containing the simple descriptor.
// ZNP protocol format: [srcaddr:2] [status:1] [nwkaddr:2] [len:1] [endpoint:1] [profileid:2] [deviceid:2]
//
//	[deviceversion:1] [numinclusters:1] [inclusterlist:2*numinclusters] [numoutclusters:1] [outclusterlist:2*numoutclusters]
//
// Note: ZNP sends this as a flat byte array with variable-length cluster lists. We parse it manually in interview.go.
// The 'len' field indicates the length of the simple descriptor data (excluding srcaddr, status, nwkaddr, len itself).
type ZdoSimpleDescriptor struct {
	SrcAddr        uint16
	Status         byte
	NWKAddr        uint16
	Length         uint8    // Length of simple descriptor data (excluding header fields)
	Endpoint       uint8    // Endpoint ID
	AppProfileID   uint16   // Application profile ID
	AppDeviceID    uint16   // Application device ID
	AppDeviceVer   uint8    // Application device version
	Reserved       uint8    // Reserved field (not always present, depends on Z-Stack version)
	NumInClusters  uint8    // Number of input clusters
	InClusters     []uint16 // Input cluster IDs
	NumOutClusters uint8    // Number of output clusters
	OutClusters    []uint16 // Output cluster IDs
}

func init() {
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0xc1, ZdoEndDeviceAnnceInd{})
}

const (
	CapabilitiesAlternatePanCoordinator = 1 << 0
	CapabilitiesRouter                  = 1 << 1
	CapabilitiesMainPowered             = 1 << 2
	CapabilitiesReceiverOnWhenIdle      = 1 << 3
	CapabilitiesSecurityCapability      = 1 << 6
)

type ZdoEndDeviceAnnceInd struct {
	SrcAddr      uint16
	NwkAddr      uint16
	IEEEAddr     uint64
	Capabilities byte
}

func init() {
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0xca, ZdoTcDevInd{})
}

// ZdoSrcRtgInd informs the host device about the receipt of a source route to a given device.
type ZdoSrcRtgInd struct {
	DstAddr   uint16
	RelayList []uint16
}

func init() {
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0xc4, ZdoSrcRtgInd{})
}

// ZdoTcDevInd is a Trust Center Device Indication.
type ZdoTcDevInd struct {
	SrcNwkAddr    uint16
	SrcIEEEAddr   uint64
	ParentNwkAddr uint16
}

func init() {
	registerCommand(FRAME_TYPE_AREQ, FRAME_SUBSYSTEM_ZDO, 0xcb, ZdoPermitJoinInd{})
}

type ZdoPermitJoinInd struct {
	Duration byte
}

/* FRAME_SUBSYSTEM_SAPI */

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_SAPI, 0x04, ZbReadConfigurationRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_SAPI, 0x04, ZBReadConfigurationResponse{})
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_SAPI, 0x05, ZbWriteConfigurationRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_SAPI, 0x05, ZbWriteConfigurationResponse{})
}

type ZbReadConfigurationRequest struct {
	ConfigID uint8
}

type ZBReadConfigurationResponse struct {
	Status   byte
	ConfigID uint8
	Value    []byte
}

type ZbWriteConfigurationRequest struct {
	ConfigID uint8
	Value    []byte
}

type ZbWriteConfigurationResponse struct {
	Status byte
}

/* FRAME_SUBSYSTEM_UTIL */

func init() {
	registerCommand(FRAME_TYPE_SREQ, FRAME_SUBSYSTEM_UTIL, 0x00, UtilGetDeviceInfoRequest{})
	registerCommand(FRAME_TYPE_SRSP, FRAME_SUBSYSTEM_UTIL, 0x00, UtilGetDeviceInfoResponse{})
}

type UtilGetDeviceInfoRequest struct{}

type UtilGetDeviceInfoResponse struct {
	Status       byte
	IEEEAddr     uint64
	ShortAddr    uint16
	DeviceType   uint8
	DeviceState  DeviceState
	AssocDevices []uint16
}

/* FRAME_SUBSYSTEM_DEBUG */

/* FRAME_SUBSYSTEM_APP */
