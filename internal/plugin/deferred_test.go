package plugin

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"google.golang.org/grpc"

	pb5 "github.com/pravo-systems/pravo/internal/tfplugin/tfplugin5"
	pb6 "github.com/pravo-systems/pravo/internal/tfplugin/tfplugin6"
)

// defer5 and defer6 answer every plan and read that they cannot answer yet.
type defer5 struct{ pb5.ProviderClient }

func (defer5) PlanResourceChange(context.Context, *pb5.PlanResourceChange_Request, ...grpc.CallOption) (*pb5.PlanResourceChange_Response, error) {
	return &pb5.PlanResourceChange_Response{Deferred: &pb5.Deferred{Reason: pb5.Deferred_PROVIDER_CONFIG_UNKNOWN}}, nil
}

func (defer5) ReadDataSource(context.Context, *pb5.ReadDataSource_Request, ...grpc.CallOption) (*pb5.ReadDataSource_Response, error) {
	return &pb5.ReadDataSource_Response{Deferred: &pb5.Deferred{Reason: pb5.Deferred_ABSENT_PREREQ}}, nil
}

type defer6 struct{ pb6.ProviderClient }

func (defer6) PlanResourceChange(context.Context, *pb6.PlanResourceChange_Request, ...grpc.CallOption) (*pb6.PlanResourceChange_Response, error) {
	return &pb6.PlanResourceChange_Response{Deferred: &pb6.Deferred{Reason: pb6.Deferred_RESOURCE_CONFIG_UNKNOWN}}, nil
}

func (defer6) ReadResource(context.Context, *pb6.ReadResource_Request, ...grpc.CallOption) (*pb6.ReadResource_Response, error) {
	return &pb6.ReadResource_Response{Deferred: &pb6.Deferred{}}, nil
}

func TestDeferredAnswersBecomeADeferredDiagnostic(t *testing.T) {
	ctx := context.Background()
	typ := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"name": tftypes.String}}
	null := tftypes.NewValue(typ, nil)
	obj := tftypes.NewValue(typ, map[string]tftypes.Value{"name": tftypes.NewValue(tftypes.String, "x")})
	check := func(name, want string, d Diagnostics) {
		t.Helper()
		if why, ok := d.Deferred(); !ok || why != want {
			t.Errorf("%s: deferred %q %v, want %q", name, why, ok, want)
		}
	}
	_, d, err := (&v5{c: defer5{}}).PlanResourceChange(ctx, "t", null, obj, obj, nil)
	if err != nil {
		t.Fatal(err)
	}
	check("v5 plan", DeferredProviderConfigUnknown, d)
	_, d, err = (&v5{c: defer5{}}).ReadDataSource(ctx, "t", obj)
	if err != nil {
		t.Fatal(err)
	}
	check("v5 read data", DeferredAbsentPrereq, d)
	_, d, err = (&v6{c: defer6{}}).PlanResourceChange(ctx, "t", null, obj, obj, nil)
	if err != nil {
		t.Fatal(err)
	}
	check("v6 plan", DeferredResourceConfigUnknown, d)
	_, _, d, err = (&v6{c: defer6{}}).ReadResource(ctx, "t", obj, nil)
	if err != nil {
		t.Fatal(err)
	}
	check("v6 read", DeferredUnknownReason, d)
}
