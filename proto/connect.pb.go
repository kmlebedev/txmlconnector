// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.25.0
// 	protoc        v3.14.0
// source: connect.proto

package transaqConnector

import (
	context "context"
	reflect "reflect"
	sync "sync"

	proto "github.com/golang/protobuf/proto"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// This is a compile-time assertion that a sufficiently up-to-date version
// of the legacy proto package is being used.
const _ = proto.ProtoPackageIsVersion4

type DataRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *DataRequest) Reset() {
	*x = DataRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_connect_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DataRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DataRequest) ProtoMessage() {}

func (x *DataRequest) ProtoReflect() protoreflect.Message {
	mi := &file_connect_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DataRequest.ProtoReflect.Descriptor instead.
func (*DataRequest) Descriptor() ([]byte, []int) {
	return file_connect_proto_rawDescGZIP(), []int{0}
}

type DataResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Message string `protobuf:"bytes,1,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *DataResponse) Reset() {
	*x = DataResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_connect_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DataResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DataResponse) ProtoMessage() {}

func (x *DataResponse) ProtoReflect() protoreflect.Message {
	mi := &file_connect_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DataResponse.ProtoReflect.Descriptor instead.
func (*DataResponse) Descriptor() ([]byte, []int) {
	return file_connect_proto_rawDescGZIP(), []int{1}
}

func (x *DataResponse) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

type SendCommandRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Message string `protobuf:"bytes,1,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *SendCommandRequest) Reset() {
	*x = SendCommandRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_connect_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SendCommandRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SendCommandRequest) ProtoMessage() {}

func (x *SendCommandRequest) ProtoReflect() protoreflect.Message {
	mi := &file_connect_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SendCommandRequest.ProtoReflect.Descriptor instead.
func (*SendCommandRequest) Descriptor() ([]byte, []int) {
	return file_connect_proto_rawDescGZIP(), []int{2}
}

func (x *SendCommandRequest) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

type SendCommandResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Message string `protobuf:"bytes,1,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *SendCommandResponse) Reset() {
	*x = SendCommandResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_connect_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SendCommandResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SendCommandResponse) ProtoMessage() {}

func (x *SendCommandResponse) ProtoReflect() protoreflect.Message {
	mi := &file_connect_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SendCommandResponse.ProtoReflect.Descriptor instead.
func (*SendCommandResponse) Descriptor() ([]byte, []int) {
	return file_connect_proto_rawDescGZIP(), []int{3}
}

func (x *SendCommandResponse) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

var File_connect_proto protoreflect.FileDescriptor

var file_connect_proto_rawDesc = []byte{
	0x0a, 0x0d, 0x63, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12,
	0x10, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x71, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x6f,
	0x72, 0x22, 0x0d, 0x0a, 0x0b, 0x44, 0x61, 0x74, 0x61, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x22, 0x28, 0x0a, 0x0c, 0x44, 0x61, 0x74, 0x61, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x2e, 0x0a, 0x12, 0x53, 0x65,
	0x6e, 0x64, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x2f, 0x0a, 0x13, 0x53, 0x65,
	0x6e, 0x64, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x32, 0xc6, 0x01, 0x0a, 0x0e,
	0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x56,
	0x0a, 0x11, 0x46, 0x65, 0x74, 0x63, 0x68, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x44,
	0x61, 0x74, 0x61, 0x12, 0x1d, 0x2e, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x71, 0x43, 0x6f, 0x6e,
	0x6e, 0x65, 0x63, 0x74, 0x6f, 0x72, 0x2e, 0x44, 0x61, 0x74, 0x61, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x1a, 0x1e, 0x2e, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x71, 0x43, 0x6f, 0x6e, 0x6e,
	0x65, 0x63, 0x74, 0x6f, 0x72, 0x2e, 0x44, 0x61, 0x74, 0x61, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x00, 0x30, 0x01, 0x12, 0x5c, 0x0a, 0x0b, 0x53, 0x65, 0x6e, 0x64, 0x43, 0x6f,
	0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x12, 0x24, 0x2e, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x71, 0x43,
	0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x6f, 0x72, 0x2e, 0x53, 0x65, 0x6e, 0x64, 0x43, 0x6f, 0x6d,
	0x6d, 0x61, 0x6e, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x25, 0x2e, 0x74, 0x72,
	0x61, 0x6e, 0x73, 0x61, 0x71, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x6f, 0x72, 0x2e, 0x53,
	0x65, 0x6e, 0x64, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x00, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_connect_proto_rawDescOnce sync.Once
	file_connect_proto_rawDescData = file_connect_proto_rawDesc
)

func file_connect_proto_rawDescGZIP() []byte {
	file_connect_proto_rawDescOnce.Do(func() {
		file_connect_proto_rawDescData = protoimpl.X.CompressGZIP(file_connect_proto_rawDescData)
	})
	return file_connect_proto_rawDescData
}

var file_connect_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_connect_proto_goTypes = []interface{}{
	(*DataRequest)(nil),         // 0: transaqConnector.DataRequest
	(*DataResponse)(nil),        // 1: transaqConnector.DataResponse
	(*SendCommandRequest)(nil),  // 2: transaqConnector.SendCommandRequest
	(*SendCommandResponse)(nil), // 3: transaqConnector.SendCommandResponse
}
var file_connect_proto_depIdxs = []int32{
	0, // 0: transaqConnector.ConnectService.FetchResponseData:input_type -> transaqConnector.DataRequest
	2, // 1: transaqConnector.ConnectService.SendCommand:input_type -> transaqConnector.SendCommandRequest
	1, // 2: transaqConnector.ConnectService.FetchResponseData:output_type -> transaqConnector.DataResponse
	3, // 3: transaqConnector.ConnectService.SendCommand:output_type -> transaqConnector.SendCommandResponse
	2, // [2:4] is the sub-list for method output_type
	0, // [0:2] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_connect_proto_init() }
func file_connect_proto_init() {
	if File_connect_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_connect_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DataRequest); i {
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
		file_connect_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DataResponse); i {
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
		file_connect_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SendCommandRequest); i {
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
		file_connect_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SendCommandResponse); i {
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
			RawDescriptor: file_connect_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_connect_proto_goTypes,
		DependencyIndexes: file_connect_proto_depIdxs,
		MessageInfos:      file_connect_proto_msgTypes,
	}.Build()
	File_connect_proto = out.File
	file_connect_proto_rawDesc = nil
	file_connect_proto_goTypes = nil
	file_connect_proto_depIdxs = nil
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConnInterface

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion6

// ConnectServiceClient is the client API for ConnectService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type ConnectServiceClient interface {
	FetchResponseData(ctx context.Context, in *DataRequest, opts ...grpc.CallOption) (ConnectService_FetchResponseDataClient, error)
	SendCommand(ctx context.Context, in *SendCommandRequest, opts ...grpc.CallOption) (*SendCommandResponse, error)
}

type connectServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewConnectServiceClient(cc grpc.ClientConnInterface) ConnectServiceClient {
	return &connectServiceClient{cc}
}

func (c *connectServiceClient) FetchResponseData(ctx context.Context, in *DataRequest, opts ...grpc.CallOption) (ConnectService_FetchResponseDataClient, error) {
	stream, err := c.cc.NewStream(ctx, &_ConnectService_serviceDesc.Streams[0], "/transaqConnector.ConnectService/FetchResponseData", opts...)
	if err != nil {
		return nil, err
	}
	x := &connectServiceFetchResponseDataClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type ConnectService_FetchResponseDataClient interface {
	Recv() (*DataResponse, error)
	grpc.ClientStream
}

type connectServiceFetchResponseDataClient struct {
	grpc.ClientStream
}

func (x *connectServiceFetchResponseDataClient) Recv() (*DataResponse, error) {
	m := new(DataResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *connectServiceClient) SendCommand(ctx context.Context, in *SendCommandRequest, opts ...grpc.CallOption) (*SendCommandResponse, error) {
	out := new(SendCommandResponse)
	err := c.cc.Invoke(ctx, "/transaqConnector.ConnectService/SendCommand", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ConnectServiceServer is the server API for ConnectService service.
type ConnectServiceServer interface {
	FetchResponseData(*DataRequest, ConnectService_FetchResponseDataServer) error
	SendCommand(context.Context, *SendCommandRequest) (*SendCommandResponse, error)
}

// UnimplementedConnectServiceServer can be embedded to have forward compatible implementations.
type UnimplementedConnectServiceServer struct {
}

func (*UnimplementedConnectServiceServer) FetchResponseData(*DataRequest, ConnectService_FetchResponseDataServer) error {
	return status.Errorf(codes.Unimplemented, "method FetchResponseData not implemented")
}
func (*UnimplementedConnectServiceServer) SendCommand(context.Context, *SendCommandRequest) (*SendCommandResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SendCommand not implemented")
}

func RegisterConnectServiceServer(s *grpc.Server, srv ConnectServiceServer) {
	s.RegisterService(&_ConnectService_serviceDesc, srv)
}

func _ConnectService_FetchResponseData_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(DataRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(ConnectServiceServer).FetchResponseData(m, &connectServiceFetchResponseDataServer{stream})
}

type ConnectService_FetchResponseDataServer interface {
	Send(*DataResponse) error
	grpc.ServerStream
}

type connectServiceFetchResponseDataServer struct {
	grpc.ServerStream
}

func (x *connectServiceFetchResponseDataServer) Send(m *DataResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _ConnectService_SendCommand_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SendCommandRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ConnectServiceServer).SendCommand(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/transaqConnector.ConnectService/SendCommand",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ConnectServiceServer).SendCommand(ctx, req.(*SendCommandRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _ConnectService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "transaqConnector.ConnectService",
	HandlerType: (*ConnectServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SendCommand",
			Handler:    _ConnectService_SendCommand_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "FetchResponseData",
			Handler:       _ConnectService_FetchResponseData_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "connect.proto",
}
