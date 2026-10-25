package plugin

import (
	"context"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	pb5 "github.com/pravo-systems/pravo/internal/tfplugin/tfplugin5"
	pb6 "github.com/pravo-systems/pravo/internal/tfplugin/tfplugin6"
)

// The stubs below stand in for a provider built on the plugin framework: they
// record every request, and refuse a non-null write-only attribute unless the
// request announces that the client handles one.

const woRefused = "WriteOnly Attribute Not Allowed"

var woType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
	"name":      tftypes.String,
	"secret_wo": tftypes.String,
}}

func woConfig(secret *string) tftypes.Value {
	s := tftypes.NewValue(tftypes.String, nil)
	if secret != nil {
		s = tftypes.NewValue(tftypes.String, *secret)
	}
	return tftypes.NewValue(woType, map[string]tftypes.Value{
		"name":      tftypes.NewValue(tftypes.String, "x"),
		"secret_wo": s,
	})
}

// hasWriteOnly reports whether the msgpack configuration holds a non-null
// secret_wo.
func hasWriteOnly(t *testing.T, msgpack []byte) bool {
	t.Helper()
	v, err := tftypes.ValueFromMsgPack(msgpack, woType)
	if err != nil {
		t.Fatalf("stub cannot decode the configuration: %v", err)
	}
	var m map[string]tftypes.Value
	if err := v.As(&m); err != nil {
		t.Fatal(err)
	}
	return !m["secret_wo"].IsNull()
}

type stub6 struct {
	pb6.ProviderClient
	t    *testing.T
	seen []proto.Message
}

func (s *stub6) refuse(caps *pb6.ClientCapabilities, cfg *pb6.DynamicValue) []*pb6.Diagnostic {
	if cfg != nil && hasWriteOnly(s.t, cfg.Msgpack) && !caps.GetWriteOnlyAttributesAllowed() {
		return []*pb6.Diagnostic{{Severity: pb6.Diagnostic_ERROR, Summary: woRefused}}
	}
	return nil
}

func (s *stub6) ConfigureProvider(_ context.Context, in *pb6.ConfigureProvider_Request, _ ...grpc.CallOption) (*pb6.ConfigureProvider_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.ConfigureProvider_Response{}, nil
}

func (s *stub6) ValidateResourceConfig(_ context.Context, in *pb6.ValidateResourceConfig_Request, _ ...grpc.CallOption) (*pb6.ValidateResourceConfig_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.ValidateResourceConfig_Response{Diagnostics: s.refuse(in.ClientCapabilities, in.Config)}, nil
}

func (s *stub6) ReadResource(_ context.Context, in *pb6.ReadResource_Request, _ ...grpc.CallOption) (*pb6.ReadResource_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.ReadResource_Response{}, nil
}

func (s *stub6) PlanResourceChange(_ context.Context, in *pb6.PlanResourceChange_Request, _ ...grpc.CallOption) (*pb6.PlanResourceChange_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.PlanResourceChange_Response{Diagnostics: s.refuse(in.ClientCapabilities, in.Config)}, nil
}

func (s *stub6) ApplyResourceChange(_ context.Context, in *pb6.ApplyResourceChange_Request, _ ...grpc.CallOption) (*pb6.ApplyResourceChange_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.ApplyResourceChange_Response{}, nil
}

func (s *stub6) ImportResourceState(_ context.Context, in *pb6.ImportResourceState_Request, _ ...grpc.CallOption) (*pb6.ImportResourceState_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.ImportResourceState_Response{}, nil
}

func (s *stub6) ReadDataSource(_ context.Context, in *pb6.ReadDataSource_Request, _ ...grpc.CallOption) (*pb6.ReadDataSource_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.ReadDataSource_Response{}, nil
}

func (s *stub6) ValidateDataSourceConfig(_ context.Context, in *pb6.ValidateDataResourceConfig_Request, _ ...grpc.CallOption) (*pb6.ValidateDataResourceConfig_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.ValidateDataResourceConfig_Response{}, nil
}

func (s *stub6) ValidateEphemeralResourceConfig(_ context.Context, in *pb6.ValidateEphemeralResourceConfig_Request, _ ...grpc.CallOption) (*pb6.ValidateEphemeralResourceConfig_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.ValidateEphemeralResourceConfig_Response{}, nil
}

func (s *stub6) OpenEphemeralResource(_ context.Context, in *pb6.OpenEphemeralResource_Request, _ ...grpc.CallOption) (*pb6.OpenEphemeralResource_Response, error) {
	s.seen = append(s.seen, in)
	return &pb6.OpenEphemeralResource_Response{}, nil
}

type stub5 struct {
	pb5.ProviderClient
	t    *testing.T
	seen []proto.Message
}

func (s *stub5) refuse(caps *pb5.ClientCapabilities, cfg *pb5.DynamicValue) []*pb5.Diagnostic {
	if cfg != nil && hasWriteOnly(s.t, cfg.Msgpack) && !caps.GetWriteOnlyAttributesAllowed() {
		return []*pb5.Diagnostic{{Severity: pb5.Diagnostic_ERROR, Summary: woRefused}}
	}
	return nil
}

func (s *stub5) ConfigureProvider(_ context.Context, in *pb5.Configure_Request, _ ...grpc.CallOption) (*pb5.Configure_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.Configure_Response{}, nil
}

func (s *stub5) ValidateResourceConfig(_ context.Context, in *pb5.ValidateResourceTypeConfig_Request, _ ...grpc.CallOption) (*pb5.ValidateResourceTypeConfig_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.ValidateResourceTypeConfig_Response{Diagnostics: s.refuse(in.ClientCapabilities, in.Config)}, nil
}

func (s *stub5) ReadResource(_ context.Context, in *pb5.ReadResource_Request, _ ...grpc.CallOption) (*pb5.ReadResource_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.ReadResource_Response{}, nil
}

func (s *stub5) PlanResourceChange(_ context.Context, in *pb5.PlanResourceChange_Request, _ ...grpc.CallOption) (*pb5.PlanResourceChange_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.PlanResourceChange_Response{Diagnostics: s.refuse(in.ClientCapabilities, in.Config)}, nil
}

func (s *stub5) ApplyResourceChange(_ context.Context, in *pb5.ApplyResourceChange_Request, _ ...grpc.CallOption) (*pb5.ApplyResourceChange_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.ApplyResourceChange_Response{}, nil
}

func (s *stub5) ImportResourceState(_ context.Context, in *pb5.ImportResourceState_Request, _ ...grpc.CallOption) (*pb5.ImportResourceState_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.ImportResourceState_Response{}, nil
}

func (s *stub5) ReadDataSource(_ context.Context, in *pb5.ReadDataSource_Request, _ ...grpc.CallOption) (*pb5.ReadDataSource_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.ReadDataSource_Response{}, nil
}

func (s *stub5) ValidateDataSourceConfig(_ context.Context, in *pb5.ValidateDataSourceConfig_Request, _ ...grpc.CallOption) (*pb5.ValidateDataSourceConfig_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.ValidateDataSourceConfig_Response{}, nil
}

func (s *stub5) ValidateEphemeralResourceConfig(_ context.Context, in *pb5.ValidateEphemeralResourceConfig_Request, _ ...grpc.CallOption) (*pb5.ValidateEphemeralResourceConfig_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.ValidateEphemeralResourceConfig_Response{}, nil
}

func (s *stub5) OpenEphemeralResource(_ context.Context, in *pb5.OpenEphemeralResource_Request, _ ...grpc.CallOption) (*pb5.OpenEphemeralResource_Response, error) {
	s.seen = append(s.seen, in)
	return &pb5.OpenEphemeralResource_Response{}, nil
}

// drive sends every request that pravo makes with a configuration, and checks
// that a write-only attribute in a managed resource is accepted.
func drive(t *testing.T, p Provider) {
	t.Helper()
	ctx := context.Background()
	secret := "-----BEGIN PRIVATE KEY-----"
	cfg := woConfig(&secret)
	null := tftypes.NewValue(woType, nil)

	var diags Diagnostics
	var err error
	check := func(what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if diags.HasErrors() {
			t.Errorf("%s: %v", what, diags)
		}
	}

	diags, err = p.Configure(ctx, cfg)
	check("Configure")
	diags, err = p.ValidateResourceConfig(ctx, "t", cfg)
	check("ValidateResourceConfig")
	_, _, diags, err = p.ReadResource(ctx, "t", null, nil)
	check("ReadResource")
	// The proposed new state of a write-only attribute is whatever the
	// configuration holds; the provider nulls it in the plan.
	_, diags, err = p.PlanResourceChange(ctx, "t", null, cfg, cfg, nil)
	check("PlanResourceChange")
	_, diags, err = p.ApplyResourceChange(ctx, "t", null, null, cfg, nil)
	check("ApplyResourceChange")
	_, diags, err = p.ImportResourceState(ctx, "t", ImportTarget{ID: "id"}, map[string]tftypes.Type{"t": woType})
	check("ImportResourceState")
	diags, err = p.ValidateDataSourceConfig(ctx, "d", cfg)
	check("ValidateDataSourceConfig")
	_, diags, err = p.ReadDataSource(ctx, "d", cfg)
	check("ReadDataSource")
	diags, err = p.ValidateEphemeralConfig(ctx, "e", cfg)
	check("ValidateEphemeralConfig")
	_, diags, err = p.OpenEphemeral(ctx, "e", cfg)
	check("OpenEphemeral")
}

// wantCaps lists the requests of each protocol that carry ClientCapabilities
// and that pravo sends.
var wantCaps = map[int][]string{
	5: {
		"tfplugin5.Configure.Request", "tfplugin5.ValidateResourceTypeConfig.Request", "tfplugin5.ReadResource.Request",
		"tfplugin5.PlanResourceChange.Request", "tfplugin5.ImportResourceState.Request", "tfplugin5.ReadDataSource.Request",
		"tfplugin5.OpenEphemeralResource.Request",
	},
	6: {
		"tfplugin6.ConfigureProvider.Request", "tfplugin6.ValidateResourceConfig.Request", "tfplugin6.ReadResource.Request",
		"tfplugin6.PlanResourceChange.Request", "tfplugin6.ImportResourceState.Request", "tfplugin6.ReadDataSource.Request",
		"tfplugin6.OpenEphemeralResource.Request",
	},
}

// checkCaps asserts that every recorded request with a client_capabilities
// field announces write-only attributes and does not allow deferral, and that
// the requests in want were all seen.
func checkCaps(t *testing.T, seen []proto.Message, want []string) {
	t.Helper()
	got := map[string]bool{}
	for _, m := range seen {
		r := m.ProtoReflect()
		fd := r.Descriptor().Fields().ByName("client_capabilities")
		if fd == nil {
			continue
		}
		name := string(r.Descriptor().FullName())
		got[name] = true
		if !r.Has(fd) {
			t.Errorf("%s carries no client capabilities", name)
			continue
		}
		c := r.Get(fd).Message()
		if !c.Get(c.Descriptor().Fields().ByName("write_only_attributes_allowed")).Bool() {
			t.Errorf("%s does not announce write-only attributes", name)
		}
		if c.Get(c.Descriptor().Fields().ByName("deferral_allowed")).Bool() {
			t.Errorf("%s allows deferral, which pravo does not handle", name)
		}
	}
	var names []string
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	w := append([]string(nil), want...)
	sort.Strings(w)
	if len(names) != len(w) {
		t.Fatalf("requests with client capabilities:\n got %v\nwant %v", names, w)
	}
	for i := range w {
		if names[i] != w[i] {
			t.Fatalf("requests with client capabilities:\n got %v\nwant %v", names, w)
		}
	}
}

func TestWriteOnlyAttributesAllowedV6(t *testing.T) {
	s := &stub6{t: t}
	drive(t, &v6{c: s})
	checkCaps(t, s.seen, wantCaps[6])
}

func TestWriteOnlyAttributesAllowedV5(t *testing.T) {
	s := &stub5{t: t}
	drive(t, &v5{c: s})
	checkCaps(t, s.seen, wantCaps[5])
}

// A request that does not announce the capability is refused by the stub, so
// the tests above would fail without it.
func TestStubRefusesWithoutCapability(t *testing.T) {
	secret := "x"
	cfg, err := woConfig(&secret).MarshalMsgPack(woType)
	if err != nil {
		t.Fatal(err)
	}
	s6 := &stub6{t: t}
	r6, _ := s6.PlanResourceChange(context.Background(), &pb6.PlanResourceChange_Request{Config: &pb6.DynamicValue{Msgpack: cfg}})
	if len(r6.Diagnostics) != 1 || r6.Diagnostics[0].Summary != woRefused {
		t.Errorf("v6 stub: %v", r6.Diagnostics)
	}
	s5 := &stub5{t: t}
	r5, _ := s5.PlanResourceChange(context.Background(), &pb5.PlanResourceChange_Request{Config: &pb5.DynamicValue{Msgpack: cfg}})
	if len(r5.Diagnostics) != 1 || r5.Diagnostics[0].Summary != woRefused {
		t.Errorf("v5 stub: %v", r5.Diagnostics)
	}
}
