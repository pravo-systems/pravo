// Code hand-written to match tfplugin6.proto. It mirrors what protoc-gen-go-grpc
// would emit for the subset of Provider RPCs that pravo uses.
package tfplugin6

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"
)

// ProviderClient is the client API for the tfplugin6 Provider service.
type ProviderClient interface {
	GetProviderSchema(ctx context.Context, in *GetProviderSchema_Request, opts ...grpc.CallOption) (*GetProviderSchema_Response, error)
	ValidateProviderConfig(ctx context.Context, in *ValidateProviderConfig_Request, opts ...grpc.CallOption) (*ValidateProviderConfig_Response, error)
	ValidateResourceConfig(ctx context.Context, in *ValidateResourceConfig_Request, opts ...grpc.CallOption) (*ValidateResourceConfig_Response, error)
	ValidateDataSourceConfig(ctx context.Context, in *ValidateDataResourceConfig_Request, opts ...grpc.CallOption) (*ValidateDataResourceConfig_Response, error)
	UpgradeResourceState(ctx context.Context, in *UpgradeResourceState_Request, opts ...grpc.CallOption) (*UpgradeResourceState_Response, error)
	ConfigureProvider(ctx context.Context, in *ConfigureProvider_Request, opts ...grpc.CallOption) (*ConfigureProvider_Response, error)
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
	// ListResource calls the server-streaming RPC. fn receives each event in the
	// order the provider sent it; when fn returns false the stream is cancelled and
	// ListResource returns nil.
	ListResource(ctx context.Context, in *ListResource_Request, fn func(*ListResource_Event) bool, opts ...grpc.CallOption) error
	StopProvider(ctx context.Context, in *StopProvider_Request, opts ...grpc.CallOption) (*StopProvider_Response, error)
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
	return invoke[GetProviderSchema_Response](ctx, c.cc, "/tfplugin6.Provider/GetProviderSchema", in, opts)
}
func (c *providerClient) ValidateProviderConfig(ctx context.Context, in *ValidateProviderConfig_Request, opts ...grpc.CallOption) (*ValidateProviderConfig_Response, error) {
	return invoke[ValidateProviderConfig_Response](ctx, c.cc, "/tfplugin6.Provider/ValidateProviderConfig", in, opts)
}
func (c *providerClient) ValidateResourceConfig(ctx context.Context, in *ValidateResourceConfig_Request, opts ...grpc.CallOption) (*ValidateResourceConfig_Response, error) {
	return invoke[ValidateResourceConfig_Response](ctx, c.cc, "/tfplugin6.Provider/ValidateResourceConfig", in, opts)
}
func (c *providerClient) ValidateDataSourceConfig(ctx context.Context, in *ValidateDataResourceConfig_Request, opts ...grpc.CallOption) (*ValidateDataResourceConfig_Response, error) {
	return invoke[ValidateDataResourceConfig_Response](ctx, c.cc, "/tfplugin6.Provider/ValidateDataResourceConfig", in, opts)
}
func (c *providerClient) UpgradeResourceState(ctx context.Context, in *UpgradeResourceState_Request, opts ...grpc.CallOption) (*UpgradeResourceState_Response, error) {
	return invoke[UpgradeResourceState_Response](ctx, c.cc, "/tfplugin6.Provider/UpgradeResourceState", in, opts)
}
func (c *providerClient) ConfigureProvider(ctx context.Context, in *ConfigureProvider_Request, opts ...grpc.CallOption) (*ConfigureProvider_Response, error) {
	return invoke[ConfigureProvider_Response](ctx, c.cc, "/tfplugin6.Provider/ConfigureProvider", in, opts)
}
func (c *providerClient) ReadResource(ctx context.Context, in *ReadResource_Request, opts ...grpc.CallOption) (*ReadResource_Response, error) {
	return invoke[ReadResource_Response](ctx, c.cc, "/tfplugin6.Provider/ReadResource", in, opts)
}
func (c *providerClient) PlanResourceChange(ctx context.Context, in *PlanResourceChange_Request, opts ...grpc.CallOption) (*PlanResourceChange_Response, error) {
	return invoke[PlanResourceChange_Response](ctx, c.cc, "/tfplugin6.Provider/PlanResourceChange", in, opts)
}
func (c *providerClient) ApplyResourceChange(ctx context.Context, in *ApplyResourceChange_Request, opts ...grpc.CallOption) (*ApplyResourceChange_Response, error) {
	return invoke[ApplyResourceChange_Response](ctx, c.cc, "/tfplugin6.Provider/ApplyResourceChange", in, opts)
}
func (c *providerClient) ImportResourceState(ctx context.Context, in *ImportResourceState_Request, opts ...grpc.CallOption) (*ImportResourceState_Response, error) {
	return invoke[ImportResourceState_Response](ctx, c.cc, "/tfplugin6.Provider/ImportResourceState", in, opts)
}
func (c *providerClient) ReadDataSource(ctx context.Context, in *ReadDataSource_Request, opts ...grpc.CallOption) (*ReadDataSource_Response, error) {
	return invoke[ReadDataSource_Response](ctx, c.cc, "/tfplugin6.Provider/ReadDataSource", in, opts)
}
func (c *providerClient) ValidateEphemeralResourceConfig(ctx context.Context, in *ValidateEphemeralResourceConfig_Request, opts ...grpc.CallOption) (*ValidateEphemeralResourceConfig_Response, error) {
	return invoke[ValidateEphemeralResourceConfig_Response](ctx, c.cc, "/tfplugin6.Provider/ValidateEphemeralResourceConfig", in, opts)
}
func (c *providerClient) OpenEphemeralResource(ctx context.Context, in *OpenEphemeralResource_Request, opts ...grpc.CallOption) (*OpenEphemeralResource_Response, error) {
	return invoke[OpenEphemeralResource_Response](ctx, c.cc, "/tfplugin6.Provider/OpenEphemeralResource", in, opts)
}
func (c *providerClient) RenewEphemeralResource(ctx context.Context, in *RenewEphemeralResource_Request, opts ...grpc.CallOption) (*RenewEphemeralResource_Response, error) {
	return invoke[RenewEphemeralResource_Response](ctx, c.cc, "/tfplugin6.Provider/RenewEphemeralResource", in, opts)
}
func (c *providerClient) CloseEphemeralResource(ctx context.Context, in *CloseEphemeralResource_Request, opts ...grpc.CallOption) (*CloseEphemeralResource_Response, error) {
	return invoke[CloseEphemeralResource_Response](ctx, c.cc, "/tfplugin6.Provider/CloseEphemeralResource", in, opts)
}
func (c *providerClient) CallFunction(ctx context.Context, in *CallFunction_Request, opts ...grpc.CallOption) (*CallFunction_Response, error) {
	return invoke[CallFunction_Response](ctx, c.cc, "/tfplugin6.Provider/CallFunction", in, opts)
}
func (c *providerClient) StopProvider(ctx context.Context, in *StopProvider_Request, opts ...grpc.CallOption) (*StopProvider_Response, error) {
	return invoke[StopProvider_Response](ctx, c.cc, "/tfplugin6.Provider/StopProvider", in, opts)
}

func (c *providerClient) ListResource(ctx context.Context, in *ListResource_Request, fn func(*ListResource_Event) bool, opts ...grpc.CallOption) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := c.cc.NewStream(ctx, &grpc.StreamDesc{StreamName: "ListResource", ServerStreams: true}, "/tfplugin6.Provider/ListResource", opts...)
	if err != nil {
		return err
	}
	if err := st.SendMsg(in); err != nil {
		return err
	}
	if err := st.CloseSend(); err != nil {
		return err
	}
	for {
		ev := new(ListResource_Event)
		if err := st.RecvMsg(ev); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if !fn(ev) {
			return nil
		}
	}
}
