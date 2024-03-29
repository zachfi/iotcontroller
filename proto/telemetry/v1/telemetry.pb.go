// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        (unknown)
// source: telemetry/v1/telemetry.proto

package telemetryv1

import (
	v1 "github.com/zachfi/iotcontroller/proto/iot/v1"
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

type TelemetryReportIOTDeviceRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	DeviceDiscovery *v1.DeviceDiscovery `protobuf:"bytes,1,opt,name=device_discovery,json=deviceDiscovery,proto3" json:"device_discovery,omitempty"`
}

func (x *TelemetryReportIOTDeviceRequest) Reset() {
	*x = TelemetryReportIOTDeviceRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_telemetry_v1_telemetry_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TelemetryReportIOTDeviceRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TelemetryReportIOTDeviceRequest) ProtoMessage() {}

func (x *TelemetryReportIOTDeviceRequest) ProtoReflect() protoreflect.Message {
	mi := &file_telemetry_v1_telemetry_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TelemetryReportIOTDeviceRequest.ProtoReflect.Descriptor instead.
func (*TelemetryReportIOTDeviceRequest) Descriptor() ([]byte, []int) {
	return file_telemetry_v1_telemetry_proto_rawDescGZIP(), []int{0}
}

func (x *TelemetryReportIOTDeviceRequest) GetDeviceDiscovery() *v1.DeviceDiscovery {
	if x != nil {
		return x.DeviceDiscovery
	}
	return nil
}

type TelemetryReportIOTDeviceResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *TelemetryReportIOTDeviceResponse) Reset() {
	*x = TelemetryReportIOTDeviceResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_telemetry_v1_telemetry_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TelemetryReportIOTDeviceResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TelemetryReportIOTDeviceResponse) ProtoMessage() {}

func (x *TelemetryReportIOTDeviceResponse) ProtoReflect() protoreflect.Message {
	mi := &file_telemetry_v1_telemetry_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TelemetryReportIOTDeviceResponse.ProtoReflect.Descriptor instead.
func (*TelemetryReportIOTDeviceResponse) Descriptor() ([]byte, []int) {
	return file_telemetry_v1_telemetry_proto_rawDescGZIP(), []int{1}
}

var File_telemetry_v1_telemetry_proto protoreflect.FileDescriptor

var file_telemetry_v1_telemetry_proto_rawDesc = []byte{
	0x0a, 0x1c, 0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x2f, 0x76, 0x31, 0x2f, 0x74,
	0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0c,
	0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x2e, 0x76, 0x31, 0x1a, 0x10, 0x69, 0x6f,
	0x74, 0x2f, 0x76, 0x31, 0x2f, 0x69, 0x6f, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x65,
	0x0a, 0x1f, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x52, 0x65, 0x70, 0x6f, 0x72,
	0x74, 0x49, 0x4f, 0x54, 0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x12, 0x42, 0x0a, 0x10, 0x64, 0x65, 0x76, 0x69, 0x63, 0x65, 0x5f, 0x64, 0x69, 0x73, 0x63,
	0x6f, 0x76, 0x65, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x17, 0x2e, 0x69, 0x6f,
	0x74, 0x2e, 0x76, 0x31, 0x2e, 0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x44, 0x69, 0x73, 0x63, 0x6f,
	0x76, 0x65, 0x72, 0x79, 0x52, 0x0f, 0x64, 0x65, 0x76, 0x69, 0x63, 0x65, 0x44, 0x69, 0x73, 0x63,
	0x6f, 0x76, 0x65, 0x72, 0x79, 0x22, 0x22, 0x0a, 0x20, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74,
	0x72, 0x79, 0x52, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x49, 0x4f, 0x54, 0x44, 0x65, 0x76, 0x69, 0x63,
	0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x32, 0x8f, 0x01, 0x0a, 0x10, 0x54, 0x65,
	0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x7b,
	0x0a, 0x18, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x52, 0x65, 0x70, 0x6f, 0x72,
	0x74, 0x49, 0x4f, 0x54, 0x44, 0x65, 0x76, 0x69, 0x63, 0x65, 0x12, 0x2d, 0x2e, 0x74, 0x65, 0x6c,
	0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x2e, 0x76, 0x31, 0x2e, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65,
	0x74, 0x72, 0x79, 0x52, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x49, 0x4f, 0x54, 0x44, 0x65, 0x76, 0x69,
	0x63, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x2e, 0x2e, 0x74, 0x65, 0x6c, 0x65,
	0x6d, 0x65, 0x74, 0x72, 0x79, 0x2e, 0x76, 0x31, 0x2e, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74,
	0x72, 0x79, 0x52, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x49, 0x4f, 0x54, 0x44, 0x65, 0x76, 0x69, 0x63,
	0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x28, 0x01, 0x42, 0xb3, 0x01, 0x0a, 0x10,
	0x63, 0x6f, 0x6d, 0x2e, 0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x2e, 0x76, 0x31,
	0x42, 0x0e, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x50, 0x72, 0x6f, 0x74, 0x6f,
	0x50, 0x01, 0x5a, 0x3e, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x7a,
	0x61, 0x63, 0x68, 0x66, 0x69, 0x2f, 0x69, 0x6f, 0x74, 0x63, 0x6f, 0x6e, 0x74, 0x72, 0x6f, 0x6c,
	0x6c, 0x65, 0x72, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65,
	0x74, 0x72, 0x79, 0x2f, 0x76, 0x31, 0x3b, 0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79,
	0x76, 0x31, 0xa2, 0x02, 0x03, 0x54, 0x58, 0x58, 0xaa, 0x02, 0x0c, 0x54, 0x65, 0x6c, 0x65, 0x6d,
	0x65, 0x74, 0x72, 0x79, 0x2e, 0x56, 0x31, 0xca, 0x02, 0x0c, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65,
	0x74, 0x72, 0x79, 0x5c, 0x56, 0x31, 0xe2, 0x02, 0x18, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74,
	0x72, 0x79, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74,
	0x61, 0xea, 0x02, 0x0d, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x3a, 0x3a, 0x56,
	0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_telemetry_v1_telemetry_proto_rawDescOnce sync.Once
	file_telemetry_v1_telemetry_proto_rawDescData = file_telemetry_v1_telemetry_proto_rawDesc
)

func file_telemetry_v1_telemetry_proto_rawDescGZIP() []byte {
	file_telemetry_v1_telemetry_proto_rawDescOnce.Do(func() {
		file_telemetry_v1_telemetry_proto_rawDescData = protoimpl.X.CompressGZIP(file_telemetry_v1_telemetry_proto_rawDescData)
	})
	return file_telemetry_v1_telemetry_proto_rawDescData
}

var file_telemetry_v1_telemetry_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_telemetry_v1_telemetry_proto_goTypes = []interface{}{
	(*TelemetryReportIOTDeviceRequest)(nil),  // 0: telemetry.v1.TelemetryReportIOTDeviceRequest
	(*TelemetryReportIOTDeviceResponse)(nil), // 1: telemetry.v1.TelemetryReportIOTDeviceResponse
	(*v1.DeviceDiscovery)(nil),               // 2: iot.v1.DeviceDiscovery
}
var file_telemetry_v1_telemetry_proto_depIdxs = []int32{
	2, // 0: telemetry.v1.TelemetryReportIOTDeviceRequest.device_discovery:type_name -> iot.v1.DeviceDiscovery
	0, // 1: telemetry.v1.TelemetryService.TelemetryReportIOTDevice:input_type -> telemetry.v1.TelemetryReportIOTDeviceRequest
	1, // 2: telemetry.v1.TelemetryService.TelemetryReportIOTDevice:output_type -> telemetry.v1.TelemetryReportIOTDeviceResponse
	2, // [2:3] is the sub-list for method output_type
	1, // [1:2] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_telemetry_v1_telemetry_proto_init() }
func file_telemetry_v1_telemetry_proto_init() {
	if File_telemetry_v1_telemetry_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_telemetry_v1_telemetry_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TelemetryReportIOTDeviceRequest); i {
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
		file_telemetry_v1_telemetry_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TelemetryReportIOTDeviceResponse); i {
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
			RawDescriptor: file_telemetry_v1_telemetry_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_telemetry_v1_telemetry_proto_goTypes,
		DependencyIndexes: file_telemetry_v1_telemetry_proto_depIdxs,
		MessageInfos:      file_telemetry_v1_telemetry_proto_msgTypes,
	}.Build()
	File_telemetry_v1_telemetry_proto = out.File
	file_telemetry_v1_telemetry_proto_rawDesc = nil
	file_telemetry_v1_telemetry_proto_goTypes = nil
	file_telemetry_v1_telemetry_proto_depIdxs = nil
}
