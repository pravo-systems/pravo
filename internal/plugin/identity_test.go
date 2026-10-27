package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"google.golang.org/grpc"

	pb6 "github.com/pravo-systems/pravo/internal/tfplugin/tfplugin6"
)

var bucketIdentity = &IdentitySchema{Attributes: []IdentityAttribute{
	{Name: "account_id", Type: tftypes.String, OptionalForImport: true},
	{Name: "bucket", Type: tftypes.String, RequiredForImport: true},
	{Name: "region", Type: tftypes.String, OptionalForImport: true},
	{Name: "versions", Type: tftypes.List{ElementType: tftypes.Number}, OptionalForImport: true},
}}

func TestIdentityEncode(t *testing.T) {
	v, err := bucketIdentity.encode("aws_s3_bucket", map[string]any{"bucket": "logs", "region": "eu", "versions": []any{int64(1), float64(2)}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]tftypes.Value
	if err := v.As(&m); err != nil {
		t.Fatal(err)
	}
	var bucket string
	if err := m["bucket"].As(&bucket); err != nil || bucket != "logs" {
		t.Errorf("bucket = %q (%v)", bucket, err)
	}
	if !m["account_id"].IsNull() {
		t.Errorf("an attribute that is left out is null: %v", m["account_id"])
	}
	if _, err := v.MarshalMsgPack(bucketIdentity.Type()); err != nil {
		t.Errorf("the value does not marshal as its type: %v", err)
	}

	for _, c := range []struct {
		name     string
		identity map[string]any
		want     string
	}{
		{"an attribute the type lacks", map[string]any{"bucket": "x", "arn": "y"}, `no identity attribute "arn"; its identity is made of account_id, bucket, region, versions`},
		{"a required attribute missing", map[string]any{"region": "eu"}, `needs "bucket"`},
		{"a required attribute null", map[string]any{"bucket": nil}, `needs "bucket"`},
		{"a value of the wrong type", map[string]any{"bucket": []any{"x"}}, "does not fit its schema"},
	} {
		if _, err := bucketIdentity.encode("aws_s3_bucket", c.identity); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestImportTargetLabel(t *testing.T) {
	if got := (ImportTarget{ID: "i-1"}).Label(); got != "i-1" {
		t.Error(got)
	}
	got := ImportTarget{Identity: map[string]any{"region": "eu", "bucket": "logs", "n": int64(3), "l": []any{"a"}}}.Label()
	if got != `bucket=logs,l=["a"],n=3,region=eu` {
		t.Error(got)
	}
}

// identityStub is a provider of protocol 6 that has an identity for one type.
type identityStub struct {
	pb6.ProviderClient
	schemaCalls int
	schemaErr   error
	imports     []*pb6.ImportResourceState_Request
}

func (s *identityStub) GetResourceIdentitySchemas(context.Context, *pb6.GetResourceIdentitySchemas_Request, ...grpc.CallOption) (*pb6.GetResourceIdentitySchemas_Response, error) {
	s.schemaCalls++
	if s.schemaErr != nil {
		return nil, s.schemaErr
	}
	return &pb6.GetResourceIdentitySchemas_Response{IdentitySchemas: map[string]*pb6.ResourceIdentitySchema{
		"aws_s3_bucket": {IdentityAttributes: []*pb6.ResourceIdentitySchema_IdentityAttribute{
			{Name: "account_id", Type: []byte(`"string"`), OptionalForImport: true},
			{Name: "bucket", Type: []byte(`"string"`), RequiredForImport: true},
			{Name: "region", Type: []byte(`"string"`), OptionalForImport: true},
		}},
	}}, nil
}

func (s *identityStub) ImportResourceState(_ context.Context, in *pb6.ImportResourceState_Request, _ ...grpc.CallOption) (*pb6.ImportResourceState_Response, error) {
	s.imports = append(s.imports, in)
	return &pb6.ImportResourceState_Response{}, nil
}

func TestImportByIdentityAsksForTheSchemaOnceAndSendsTheIdentity(t *testing.T) {
	stub := &identityStub{}
	p := &v6{c: stub, close: func() {}}
	types := map[string]tftypes.Type{}
	for range 2 {
		if _, _, err := p.ImportResourceState(context.Background(), "aws_s3_bucket", ImportTarget{Identity: map[string]any{"bucket": "logs", "region": "eu-north-1"}}, types); err != nil {
			t.Fatal(err)
		}
	}
	if stub.schemaCalls != 1 {
		t.Errorf("the identity schemas were asked for %d times", stub.schemaCalls)
	}
	req := stub.imports[0]
	if req.Id != "" || req.Identity == nil || req.Identity.IdentityData == nil {
		t.Fatalf("the request: %+v", req)
	}
	got, err := tftypes.ValueFromMsgPack(req.Identity.IdentityData.Msgpack, tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"account_id": tftypes.String, "bucket": tftypes.String, "region": tftypes.String,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]tftypes.Value
	_ = got.As(&m)
	var bucket, region string
	_ = m["bucket"].As(&bucket)
	_ = m["region"].As(&region)
	if bucket != "logs" || region != "eu-north-1" || !m["account_id"].IsNull() {
		t.Errorf("identity sent: %v", got)
	}
}

func TestImportByIdDoesNotAskForIdentities(t *testing.T) {
	stub := &identityStub{}
	p := &v6{c: stub, close: func() {}}
	if _, _, err := p.ImportResourceState(context.Background(), "aws_s3_bucket", ImportTarget{ID: "logs"}, nil); err != nil {
		t.Fatal(err)
	}
	if stub.schemaCalls != 0 || stub.imports[0].Id != "logs" || stub.imports[0].Identity != nil {
		t.Errorf("%d schema calls, request %+v", stub.schemaCalls, stub.imports[0])
	}
}

func TestImportByIdentityErrorsAreSaidInPlainWords(t *testing.T) {
	ctx := context.Background()
	types := map[string]tftypes.Type{}
	// A provider that does not have the call.
	p := &v6{c: &identityStub{schemaErr: errors.New("rpc error: code = Unimplemented")}, close: func() {}}
	if _, _, err := p.ImportResourceState(ctx, "aws_s3_bucket", ImportTarget{Identity: map[string]any{"bucket": "x"}}, types); err == nil || !strings.Contains(err.Error(), "does not give resource identities") {
		t.Errorf("a provider without identities: %v", err)
	}
	// A type that has no identity.
	p = &v6{c: &identityStub{}, close: func() {}}
	if _, _, err := p.ImportResourceState(ctx, "aws_nothing", ImportTarget{Identity: map[string]any{"bucket": "x"}}, types); err == nil || !strings.Contains(err.Error(), "declares no identity for aws_nothing") {
		t.Errorf("a type without an identity: %v", err)
	}
	// An attribute the identity lacks never reaches the provider.
	stub := &identityStub{}
	p = &v6{c: stub, close: func() {}}
	if _, _, err := p.ImportResourceState(ctx, "aws_s3_bucket", ImportTarget{Identity: map[string]any{"name": "x"}}, types); err == nil || !strings.Contains(err.Error(), `no identity attribute "name"`) || len(stub.imports) != 0 {
		t.Errorf("a wrong attribute: %v (%d imports)", err, len(stub.imports))
	}
}
