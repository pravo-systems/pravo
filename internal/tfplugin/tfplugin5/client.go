// Code hand-written to match tfplugin5.proto. It mirrors what protoc-gen-go-grpc
// would emit for the subset of Provider RPCs that pravo uses.
package tfplugin5

import (
	"context"

	"google.golang.org/grpc"
)

// ProviderClient is the client API for the tfplugin5 Provider service.
type ProviderClient interface {
	GetProviderSchema(ctx context.Context, in *GetProviderSchema_Request, opts ...grpc.CallOption) (*GetProviderSchema_Response, error)
	ValidateProviderConfig(ctx context.Context, in *PrepareProviderConfig_Request, opts ...grpc.CallOption) (*PrepareProviderConfig_Response, error)
	ValidateResourceConfig(ctx context.Context, in *ValidateResourceTypeConfig_Request, opts ...grpc.CallOption) (*ValidateResourceTypeConfig_Response, error)
	ValidateDataSourceConfig(ctx context.Context, in *ValidateDataSourceConfig_Request, opts ...grpc.CallOption) (*ValidateDataSourceConfig_Response, error)
	UpgradeResourceState(ctx context.Context, in *UpgradeResourceState_Request, opts ...grpc.CallOption) (*UpgradeResourceState_Response, error)
	ConfigureProvider(ctx context.Context, in *Configure_Request, opts ...grpc.CallOption) (*Configure_Response, error)
	ReadResource(ctx context.Context, in *ReadResource_Request, opts ...grpc.CallOption) (*ReadResource_Response, error)
	PlanResourceChange(ctx context.Context, in *PlanResourceChange_Request, opts ...grpc.CallOption) (*PlanResourceChange_Response, error)
	ApplyResourceChange(ctx context.Context, in *ApplyResourceChange_Request, opts ...grpc.CallOption) (*ApplyResourceChange_Response, error)
	ImportResourceState(ctx context.Context, in *ImportResourceState_Request, opts ...grpc.CallOption) (*ImportResourceState_Response, error)
	ReadDataSource(ctx context.Context, in *ReadDataSource_Request, opts ...grpc.CallOption) (*ReadDataSource_Response, error)
	ValidateEphemeralResourceConfig(ctx context.Context, in *ValidateEphemeralResourceConfig_Request, opts ...grpc.CallOption) (*ValidateEphemeralResourceConfig_Response, error)
	OpenEphemeralResource(ctx context.Context, in *OpenEphemeralResource_Request, opts ...grpc.CallOption) (*OpenEphemeralResource_Response, error)
	RenewEphemeralResource(ctx context.Context, in *RenewEphemeralResource_Request, opts ...grpc.CallOption) (*RenewEphemeralResource_Response, error)
	CloseEphemeralResource(ctx context.Context, in *CloseEphemeralResource_Request, opts ...grpc.CallOption) (*CloseEphemeralResource_Response, error)
	CallFunction(ctx context.Context, in *CallFunction_Request, opts ...grpc.CallOption) (*CallFunction_Response, error)
	StopProvider(ctx context.Context, in *Stop_Request, opts ...grpc.CallOption) (*Stop_Response, error)
}

type providerClient struct {
	cc grpc.ClientConnInterface
}

// NewProviderClient returns a client for the Provider service on conn.
func NewProviderClient(cc grpc.ClientConnInterface) ProviderClient {
	return &providerClient{cc}
}

func invoke[Resp any](ctx context.Context, cc grpc.ClientConnInterface, method string, in any, opts []grpc.CallOption) (*Resp, error) {
	out := new(Resp)
	if err := cc.Invoke(ctx, method, in, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *providerClient) GetProviderSchema(ctx context.Context, in *GetProviderSchema_Request, opts ...grpc.CallOption) (*GetProviderSchema_Response, error) {
	return invoke[GetProviderSchema_Response](ctx, c.cc, "/tfplugin5.Provider/GetSchema", in, opts)
}
func (c *providerClient) ValidateProviderConfig(ctx context.Context, in *PrepareProviderConfig_Request, opts ...grpc.CallOption) (*PrepareProviderConfig_Response, error) {
	return invoke[PrepareProviderConfig_Response](ctx, c.cc, "/tfplugin5.Provider/PrepareProviderConfig", in, opts)
}
func (c *providerClient) ValidateResourceConfig(ctx context.Context, in *ValidateResourceTypeConfig_Request, opts ...grpc.CallOption) (*ValidateResourceTypeConfig_Response, error) {
	return invoke[ValidateResourceTypeConfig_Response](ctx, c.cc, "/tfplugin5.Provider/ValidateResourceTypeConfig", in, opts)
}
func (c *providerClient) ValidateDataSourceConfig(ctx context.Context, in *ValidateDataSourceConfig_Request, opts ...grpc.CallOption) (*ValidateDataSourceConfig_Response, error) {
	return invoke[ValidateDataSourceConfig_Response](ctx, c.cc, "/tfplugin5.Provider/ValidateDataSourceConfig", in, opts)
}
func (c *providerClient) UpgradeResourceState(ctx context.Context, in *UpgradeResourceState_Request, opts ...grpc.CallOption) (*UpgradeResourceState_Response, error) {
	return invoke[UpgradeResourceState_Response](ctx, c.cc, "/tfplugin5.Provider/UpgradeResourceState", in, opts)
}
func (c *providerClient) ConfigureProvider(ctx context.Context, in *Configure_Request, opts ...grpc.CallOption) (*Configure_Response, error) {
	return invoke[Configure_Response](ctx, c.cc, "/tfplugin5.Provider/Configure", in, opts)
}
func (c *providerClient) ReadResource(ctx context.Context, in *ReadResource_Request, opts ...grpc.CallOption) (*ReadResource_Response, error) {
	return invoke[ReadResource_Response](ctx, c.cc, "/tfplugin5.Provider/ReadResource", in, opts)
}
func (c *providerClient) PlanResourceChange(ctx context.Context, in *PlanResourceChange_Request, opts ...grpc.CallOption) (*PlanResourceChange_Response, error) {
	return invoke[PlanResourceChange_Response](ctx, c.cc, "/tfplugin5.Provider/PlanResourceChange", in, opts)
}
func (c *providerClient) ApplyResourceChange(ctx context.Context, in *ApplyResourceChange_Request, opts ...grpc.CallOption) (*ApplyResourceChange_Response, error) {
	return invoke[ApplyResourceChange_Response](ctx, c.cc, "/tfplugin5.Provider/ApplyResourceChange", in, opts)
}
func (c *providerClient) ImportResourceState(ctx context.Context, in *ImportResourceState_Request, opts ...grpc.CallOption) (*ImportResourceState_Response, error) {
	return invoke[ImportResourceState_Response](ctx, c.cc, "/tfplugin5.Provider/ImportResourceState", in, opts)
}
func (c *providerClient) ReadDataSource(ctx context.Context, in *ReadDataSource_Request, opts ...grpc.CallOption) (*ReadDataSource_Response, error) {
	return invoke[ReadDataSource_Response](ctx, c.cc, "/tfplugin5.Provider/ReadDataSource", in, opts)
}
func (c *providerClient) ValidateEphemeralResourceConfig(ctx context.Context, in *ValidateEphemeralResourceConfig_Request, opts ...grpc.CallOption) (*ValidateEphemeralResourceConfig_Response, error) {
	return invoke[ValidateEphemeralResourceConfig_Response](ctx, c.cc, "/tfplugin5.Provider/ValidateEphemeralResourceConfig", in, opts)
}
func (c *providerClient) OpenEphemeralResource(ctx context.Context, in *OpenEphemeralResource_Request, opts ...grpc.CallOption) (*OpenEphemeralResource_Response, error) {
	return invoke[OpenEphemeralResource_Response](ctx, c.cc, "/tfplugin5.Provider/OpenEphemeralResource", in, opts)
}
func (c *providerClient) RenewEphemeralResource(ctx context.Context, in *RenewEphemeralResource_Request, opts ...grpc.CallOption) (*RenewEphemeralResource_Response, error) {
	return invoke[RenewEphemeralResource_Response](ctx, c.cc, "/tfplugin5.Provider/RenewEphemeralResource", in, opts)
}
func (c *providerClient) CloseEphemeralResource(ctx context.Context, in *CloseEphemeralResource_Request, opts ...grpc.CallOption) (*CloseEphemeralResource_Response, error) {
	return invoke[CloseEphemeralResource_Response](ctx, c.cc, "/tfplugin5.Provider/CloseEphemeralResource", in, opts)
}
func (c *providerClient) CallFunction(ctx context.Context, in *CallFunction_Request, opts ...grpc.CallOption) (*CallFunction_Response, error) {
	return invoke[CallFunction_Response](ctx, c.cc, "/tfplugin5.Provider/CallFunction", in, opts)
}
func (c *providerClient) StopProvider(ctx context.Context, in *Stop_Request, opts ...grpc.CallOption) (*Stop_Response, error) {
	return invoke[Stop_Response](ctx, c.cc, "/tfplugin5.Provider/Stop", in, opts)
}
