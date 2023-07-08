// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        (unknown)
// source: iot/v1/iot.proto

package iotv1

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type UpdateDeviceResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *UpdateDeviceResponse) Reset() {
	*x = UpdateDeviceResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_iot_v1_iot_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *UpdateDeviceResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateDeviceResponse) ProtoMessage() {}

func (x *UpdateDeviceResponse) ProtoReflect() protoreflect.Message {
	mi := &file_iot_v1_iot_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateDeviceResponse.ProtoReflect.Descriptor instead.
func (*UpdateDeviceResponse) Descriptor() ([]byte, []int) {
	return file_iot_v1_iot_proto_rawDescGZIP(), []int{0}
}

// <discovery_prefix>/<component>/[<node_id>/]<object_id>/config
type DeviceDiscovery struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	DiscoveryPrefix string   `protobuf:"bytes,1,opt,name=discovery_prefix,json=discoveryPrefix,proto3" json:"discovery_prefix,omitempty"`
	Component       string   `protobuf:"bytes,2,opt,name=component,proto3" json:"component,omitempty"`
	NodeId          string   `protobuf:"bytes,3,opt,name=node_id,json=nodeId,proto3" json:"node_id,omitempty"`
	ObjectId        string   `protobuf:"bytes,4,opt,name=object_id,json=objectId,proto3" json:"object_id,omitempty"`
	Endpoints       []string `protobuf:"bytes,5,rep,name=endpoints,proto3" json:"endpoints,omitempty"`
	Message         []byte   `protobuf:"bytes,6,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *DeviceDiscovery) Reset() {
	*x = DeviceDiscovery{}
	if protoimpl.UnsafeEnabled {
		mi := &file_iot_v1_iot_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DeviceDiscovery) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeviceDiscovery) ProtoMessage() {}

func (x *DeviceDiscovery) ProtoReflect() protoreflect.Message {
	mi := &file_iot_v1_iot_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeviceDiscovery.ProtoReflect.Descriptor instead.
func (*DeviceDiscovery) Descriptor() ([]byte, []int) {
	return file_iot_v1_iot_proto_rawDescGZIP(), []int{1}
}

func (x *DeviceDiscovery) GetDiscoveryPrefix() string {
	if x != nil {
		return x.DiscoveryPrefix
	}
	return ""
}

func (x *DeviceDiscovery) GetComponent() string {
	if x != nil {
		return x.Component
	}
	return ""
}

func (x *DeviceDiscovery) GetNodeId() string {
	if x != nil {
		return x.NodeId
	}
	return ""
}

func (x *DeviceDiscovery) GetObjectId() string {
	if x != nil {
		return x.ObjectId
	}
	return ""
}

func (x *DeviceDiscovery) GetEndpoints() []string {
	if x != nil {
		return x.Endpoints
	}
	return nil
}

func (x *DeviceDiscovery) GetMessage() []byte {
	if x != nil {
		return x.Message
	}
	return nil
}

type Action struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Event  string `protobuf:"bytes,1,opt,name=event,proto3" json:"event,omitempty"`
	Device string `protobuf:"bytes,2,opt,name=device,proto3" json:"device,omitempty"`
	Zone   string `protobuf:"bytes,3,opt,name=zone,proto3" json:"zone,omitempty"`
}

func (x *Action) Reset() {
	*x = Action{}
	if protoimpl.UnsafeEnabled {
		mi := &file_iot_v1_iot_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Action) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Action) ProtoMessage() {}

func (x *Action) ProtoReflect() protoreflect.Message {
	mi := &file_iot_v1_iot_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Action.ProtoReflect.Descriptor instead.
func (*Action) Descriptor() ([]byte, []int) {
	return file_iot_v1_iot_proto_rawDescGZIP(), []int{2}
}

func (x *Action) GetEvent() string {
	if x != nil {
		return x.Event
	}
	return ""
}

func (x *Action) GetDevice() string {
	if x != nil {
		return x.Device
	}
	return ""
}

func (x *Action) GetZone() string {
	if x != nil {
		return x.Zone
	}
	return ""
}

type UpdateDeviceRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Device string `protobuf:"bytes,1,opt,name=device,proto3" json:"device,omitempty"`
}

func (x *UpdateDeviceRequest) Reset() {
	*x = UpdateDeviceRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_iot_v1_iot_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *UpdateDeviceRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateDeviceRequest) ProtoMessage() {}

func (x *UpdateDeviceRequest) ProtoReflect() protoreflect.Message {
	mi := &file_iot_v1_iot_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateDeviceRequest.ProtoReflect.Descriptor instead.
func (*UpdateDeviceRequest) Descriptor() ([]byte, []int) {
	return file_iot_v1_iot_proto_rawDescGZIP(), []int{3}
}

func (x *UpdateDeviceRequest) GetDevice() string {
	if x != nil {
		return x.Device
	}
	return ""
}

var File_iot_v1_iot_proto protoreflect.FileDescriptor

var file_iot_v1_iot_proto_rawDesc = []byte{
	0x0a, 0x10, 0x69, 0x6f, 0x74, 0x2f, 0x76, 0x31, 0x2f, 0x69, 0x6f, 0x74, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x12, 0x06, 0x69, 0x6f, 0x74, 0x2e, 0x76, 0x31, 0x22, 0x16, 0x0a, 0x14, 0x55, 0x70,
	0x64, 0x61, 0x74, 0x65, 0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0xc8, 0x01, 0x0a, 0x0f, 0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x44, 0x69, 0x73,
	0x63, 0x6f, 0x76, 0x65, 0x72, 0x79, 0x12, 0x29, 0x0a, 0x10, 0x64, 0x69, 0x73, 0x63, 0x6f, 0x76,
	0x65, 0x72, 0x79, 0x5f, 0x70, 0x72, 0x65, 0x66, 0x69, 0x78, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x0f, 0x64, 0x69, 0x73, 0x63, 0x6f, 0x76, 0x65, 0x72, 0x79, 0x50, 0x72, 0x65, 0x66, 0x69,
	0x78, 0x12, 0x1c, 0x0a, 0x09, 0x63, 0x6f, 0x6d, 0x70, 0x6f, 0x6e, 0x65, 0x6e, 0x74, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x63, 0x6f, 0x6d, 0x70, 0x6f, 0x6e, 0x65, 0x6e, 0x74, 0x12,
	0x17, 0x0a, 0x07, 0x6e, 0x6f, 0x64, 0x65, 0x5f, 0x69, 0x64, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x06, 0x6e, 0x6f, 0x64, 0x65, 0x49, 0x64, 0x12, 0x1b, 0x0a, 0x09, 0x6f, 0x62, 0x6a, 0x65,
	0x63, 0x74, 0x5f, 0x69, 0x64, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x6f, 0x62, 0x6a,
	0x65, 0x63, 0x74, 0x49, 0x64, 0x12, 0x1c, 0x0a, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69, 0x6e,
	0x74, 0x73, 0x18, 0x05, 0x20, 0x03, 0x28, 0x09, 0x52, 0x09, 0x65, 0x6e, 0x64, 0x70, 0x6f, 0x69,
	0x6e, 0x74, 0x73, 0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x06,
	0x20, 0x01, 0x28, 0x0c, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x4a, 0x0a,
	0x06, 0x41, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x14, 0x0a, 0x05, 0x65, 0x76, 0x65, 0x6e, 0x74,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x12, 0x16, 0x0a,
	0x06, 0x64, 0x65, 0x76, 0x69, 0x63, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x64,
	0x65, 0x76, 0x69, 0x63, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x7a, 0x6f, 0x6e, 0x65, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x04, 0x7a, 0x6f, 0x6e, 0x65, 0x22, 0x2d, 0x0a, 0x13, 0x55, 0x70, 0x64,
	0x61, 0x74, 0x65, 0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x12, 0x16, 0x0a, 0x06, 0x64, 0x65, 0x76, 0x69, 0x63, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x06, 0x64, 0x65, 0x76, 0x69, 0x63, 0x65, 0x32, 0x57, 0x0a, 0x0a, 0x49, 0x4f, 0x54, 0x53,
	0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x49, 0x0a, 0x0c, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65,
	0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x12, 0x1b, 0x2e, 0x69, 0x6f, 0x74, 0x2e, 0x76, 0x31, 0x2e,
	0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x52, 0x65, 0x71, 0x75,
	0x65, 0x73, 0x74, 0x1a, 0x1c, 0x2e, 0x69, 0x6f, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x55, 0x70, 0x64,
	0x61, 0x74, 0x65, 0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x42, 0x83, 0x01, 0x0a, 0x0a, 0x63, 0x6f, 0x6d, 0x2e, 0x69, 0x6f, 0x74, 0x2e, 0x76, 0x31,
	0x42, 0x08, 0x49, 0x6f, 0x74, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x32, 0x67, 0x69,
	0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x7a, 0x61, 0x63, 0x68, 0x66, 0x69, 0x2f,
	0x69, 0x6f, 0x74, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x6c, 0x65, 0x72, 0x2f, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2f, 0x69, 0x6f, 0x74, 0x2f, 0x76, 0x31, 0x3b, 0x69, 0x6f, 0x74, 0x76, 0x31,
	0xa2, 0x02, 0x03, 0x49, 0x58, 0x58, 0xaa, 0x02, 0x06, 0x49, 0x6f, 0x74, 0x2e, 0x56, 0x31, 0xca,
	0x02, 0x06, 0x49, 0x6f, 0x74, 0x5c, 0x56, 0x31, 0xe2, 0x02, 0x12, 0x49, 0x6f, 0x74, 0x5c, 0x56,
	0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x07,
	0x49, 0x6f, 0x74, 0x3a, 0x3a, 0x56, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_iot_v1_iot_proto_rawDescOnce sync.Once
	file_iot_v1_iot_proto_rawDescData = file_iot_v1_iot_proto_rawDesc
)

func file_iot_v1_iot_proto_rawDescGZIP() []byte {
	file_iot_v1_iot_proto_rawDescOnce.Do(func() {
		file_iot_v1_iot_proto_rawDescData = protoimpl.X.CompressGZIP(file_iot_v1_iot_proto_rawDescData)
	})
	return file_iot_v1_iot_proto_rawDescData
}

var file_iot_v1_iot_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_iot_v1_iot_proto_goTypes = []interface{}{
	(*UpdateDeviceResponse)(nil), // 0: iot.v1.UpdateDeviceResponse
	(*DeviceDiscovery)(nil),      // 1: iot.v1.DeviceDiscovery
	(*Action)(nil),               // 2: iot.v1.Action
	(*UpdateDeviceRequest)(nil),  // 3: iot.v1.UpdateDeviceRequest
}
var file_iot_v1_iot_proto_depIdxs = []int32{
	3, // 0: iot.v1.IOTService.UpdateDevice:input_type -> iot.v1.UpdateDeviceRequest
	0, // 1: iot.v1.IOTService.UpdateDevice:output_type -> iot.v1.UpdateDeviceResponse
	1, // [1:2] is the sub-list for method output_type
	0, // [0:1] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_iot_v1_iot_proto_init() }
func file_iot_v1_iot_proto_init() {
	if File_iot_v1_iot_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_iot_v1_iot_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*UpdateDeviceResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_iot_v1_iot_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DeviceDiscovery); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_iot_v1_iot_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Action); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_iot_v1_iot_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*UpdateDeviceRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_iot_v1_iot_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_iot_v1_iot_proto_goTypes,
		DependencyIndexes: file_iot_v1_iot_proto_depIdxs,
		MessageInfos:      file_iot_v1_iot_proto_msgTypes,
	}.Build()
	File_iot_v1_iot_proto = out.File
	file_iot_v1_iot_proto_rawDesc = nil
	file_iot_v1_iot_proto_goTypes = nil
	file_iot_v1_iot_proto_depIdxs = nil
}
