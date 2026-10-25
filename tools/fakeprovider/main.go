// Command fakeprovider is a provider for trying pravo discoveries without a
// cloud. It speaks the plugin protocol, version 6, as a provider binary does, and
// has two resource types that are also list resource types, so a specification can
// declare a discovery of them:
//
//	go build -o terraform-provider-demo ./tools/fakeprovider
//
//	import demo "pravo.dev/providers/demo/demo"
//
//	$demo: demo.#Provider & {$path: "./terraform-provider-demo"}
//	servers: $demo.#Discovery.demo_server & {region: "eu"}
//
// demo_server is named by an id, with an optional region. demo_bucket is named by
// an identity of several attributes and no id: an account_id, a bucket and a region,
// and can only be imported by that identity, as a bucket of a cloud is.
//
// The objects that exist are kept in the directory that FAKEPROVIDER_DIR names, in
// catalog.json, a list of {"id", "name", "region"} and, for a bucket, "type":
// "demo_bucket". Applying a demo_server adds an object to it. The file "fail", when it exists, makes a list fail with its
// contents, and every list adds a line to "calls". Without the variable the
// provider keeps its objects in memory, for as long as it runs.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func main() {
	err := tf6server.Serve("registry.opentofu.org/demo/demo", func() tfprotov6.ProviderServer {
		return &provider{dir: os.Getenv("FAKEPROVIDER_DIR")}
	})
	if err != nil {
		log.Fatal(err)
	}
}

type object struct {
	// Type is the resource type of the object: demo_server when it is empty.
	Type   string `json:"type,omitempty"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region"`
}

func (o object) typeName() string {
	if o.Type == "" {
		return "demo_server"
	}
	return o.Type
}

const accountID = "000000000000"

// provider implements the RPCs that pravo calls. The others are nil.
type provider struct {
	tfprotov6.ProviderServer

	dir string
	mu  sync.Mutex
	mem []object
}

var (
	serverType         = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String, "name": tftypes.String, "size": tftypes.String, "secret": tftypes.String}}
	bucketType         = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String, "bucket": tftypes.String}}
	listType           = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"region": tftypes.String}}
	identityType       = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String, "region": tftypes.String}}
	bucketIdentityType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"account_id": tftypes.String, "bucket": tftypes.String, "region": tftypes.String}}
)

// typeOf is the type of the state of a resource type, and nameAttr the attribute
// that names an object of it.
func typeOf(typ string) tftypes.Object {
	if typ == "demo_bucket" {
		return bucketType
	}
	return serverType
}

func nameAttr(typ string) string {
	if typ == "demo_bucket" {
		return "bucket"
	}
	return "name"
}

func attr(name string, required, optional, computed bool) *tfprotov6.SchemaAttribute {
	return &tfprotov6.SchemaAttribute{Name: name, Type: tftypes.String, Required: required, Optional: optional, Computed: computed}
}

func (p *provider) catalog() []object {
	if p.dir == "" {
		return slices.Clone(p.mem)
	}
	var objs []object
	if b, err := os.ReadFile(filepath.Join(p.dir, "catalog.json")); err == nil {
		_ = json.Unmarshal(b, &objs)
	}
	return objs
}

func (p *provider) setCatalog(objs []object) {
	if p.dir == "" {
		p.mem = objs
		return
	}
	b, _ := json.Marshal(objs)
	_ = os.WriteFile(filepath.Join(p.dir, "catalog.json"), b, 0o644)
}

func (p *provider) GetProviderSchema(context.Context, *tfprotov6.GetProviderSchemaRequest) (*tfprotov6.GetProviderSchemaResponse, error) {
	return &tfprotov6.GetProviderSchemaResponse{
		Provider: &tfprotov6.Schema{Block: &tfprotov6.SchemaBlock{Attributes: []*tfprotov6.SchemaAttribute{attr("token", false, true, false)}}},
		ResourceSchemas: map[string]*tfprotov6.Schema{"demo_server": {Block: &tfprotov6.SchemaBlock{Attributes: []*tfprotov6.SchemaAttribute{
			attr("id", false, false, true), attr("name", true, false, false), attr("size", false, true, false),
			{Name: "secret", Type: tftypes.String, Optional: true, Sensitive: true},
		}}}, "demo_bucket": {Block: &tfprotov6.SchemaBlock{Attributes: []*tfprotov6.SchemaAttribute{
			attr("id", false, false, true), attr("bucket", true, false, false),
		}}}},
		ListResourceSchemas: map[string]*tfprotov6.Schema{"demo_server": {Block: &tfprotov6.SchemaBlock{Attributes: []*tfprotov6.SchemaAttribute{
			{Name: "region", Type: tftypes.String, Optional: true, Description: "Only the servers of this region."},
		}}}, "demo_bucket": {Block: &tfprotov6.SchemaBlock{Attributes: []*tfprotov6.SchemaAttribute{
			{Name: "region", Type: tftypes.String, Optional: true, Description: "Only the buckets of this region."},
		}}}},
	}, nil
}

// GetResourceIdentitySchemas gives the identity of each type: what names an object
// of it for an import, and what a list gives for each object.
func (p *provider) GetResourceIdentitySchemas(context.Context, *tfprotov6.GetResourceIdentitySchemasRequest) (*tfprotov6.GetResourceIdentitySchemasResponse, error) {
	return &tfprotov6.GetResourceIdentitySchemasResponse{IdentitySchemas: map[string]*tfprotov6.ResourceIdentitySchema{
		"demo_server": {IdentityAttributes: []*tfprotov6.ResourceIdentitySchemaAttribute{
			{Name: "id", Type: tftypes.String, RequiredForImport: true},
			{Name: "region", Type: tftypes.String, OptionalForImport: true},
		}},
		"demo_bucket": {IdentityAttributes: []*tfprotov6.ResourceIdentitySchemaAttribute{
			{Name: "account_id", Type: tftypes.String, OptionalForImport: true},
			{Name: "bucket", Type: tftypes.String, RequiredForImport: true},
			{Name: "region", Type: tftypes.String, OptionalForImport: true},
		}},
	}}, nil
}

func (p *provider) ValidateProviderConfig(context.Context, *tfprotov6.ValidateProviderConfigRequest) (*tfprotov6.ValidateProviderConfigResponse, error) {
	return &tfprotov6.ValidateProviderConfigResponse{}, nil
}

func (p *provider) ConfigureProvider(context.Context, *tfprotov6.ConfigureProviderRequest) (*tfprotov6.ConfigureProviderResponse, error) {
	return &tfprotov6.ConfigureProviderResponse{}, nil
}

func (p *provider) StopProvider(context.Context, *tfprotov6.StopProviderRequest) (*tfprotov6.StopProviderResponse, error) {
	return &tfprotov6.StopProviderResponse{}, nil
}

func (p *provider) ValidateResourceConfig(context.Context, *tfprotov6.ValidateResourceConfigRequest) (*tfprotov6.ValidateResourceConfigResponse, error) {
	return &tfprotov6.ValidateResourceConfigResponse{}, nil
}

func (p *provider) UpgradeResourceState(_ context.Context, req *tfprotov6.UpgradeResourceStateRequest) (*tfprotov6.UpgradeResourceStateResponse, error) {
	v, err := req.RawState.UnmarshalWithOpts(typeOf(req.TypeName), tfprotov6.UnmarshalOpts{})
	if err != nil {
		return nil, err
	}
	dv, err := tfprotov6.NewDynamicValue(typeOf(req.TypeName), v)
	return &tfprotov6.UpgradeResourceStateResponse{UpgradedState: &dv}, err
}

func (p *provider) ReadResource(_ context.Context, req *tfprotov6.ReadResourceRequest) (*tfprotov6.ReadResourceResponse, error) {
	return &tfprotov6.ReadResourceResponse{NewState: req.CurrentState, Private: req.Private}, nil
}

func (p *provider) PlanResourceChange(_ context.Context, req *tfprotov6.PlanResourceChangeRequest) (*tfprotov6.PlanResourceChangeResponse, error) {
	serverType := typeOf(req.TypeName)
	proposed, err := req.ProposedNewState.Unmarshal(serverType)
	if err != nil {
		return nil, err
	}
	if proposed.IsNull() {
		return &tfprotov6.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}, nil
	}
	var m map[string]tftypes.Value
	if err := proposed.As(&m); err != nil {
		return nil, err
	}
	prior, _ := req.PriorState.Unmarshal(serverType)
	if prior.IsNull() {
		m["id"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	} else {
		var pm map[string]tftypes.Value
		_ = prior.As(&pm)
		m["id"] = pm["id"]
	}
	dv, err := tfprotov6.NewDynamicValue(serverType, tftypes.NewValue(serverType, m))
	return &tfprotov6.PlanResourceChangeResponse{PlannedState: &dv}, err
}

func (p *provider) ApplyResourceChange(_ context.Context, req *tfprotov6.ApplyResourceChangeRequest) (*tfprotov6.ApplyResourceChangeResponse, error) {
	serverType := typeOf(req.TypeName)
	planned, err := req.PlannedState.Unmarshal(serverType)
	if err != nil {
		return nil, err
	}
	if planned.IsNull() {
		return &tfprotov6.ApplyResourceChangeResponse{NewState: req.PlannedState}, nil
	}
	var m map[string]tftypes.Value
	if err := planned.As(&m); err != nil {
		return nil, err
	}
	var name string
	_ = m[nameAttr(req.TypeName)].As(&name)
	if !m["id"].IsKnown() || m["id"].IsNull() {
		m["id"] = tftypes.NewValue(tftypes.String, "srv-"+name)
	}
	var id string
	_ = m["id"].As(&id)
	p.mu.Lock()
	if objs := p.catalog(); !slices.ContainsFunc(objs, func(o object) bool { return o.ID == id }) {
		o := object{ID: id, Name: name, Region: "eu"}
		if req.TypeName == "demo_bucket" {
			o.Type = "demo_bucket"
		}
		p.setCatalog(append(objs, o))
	}
	p.mu.Unlock()
	dv, err := tfprotov6.NewDynamicValue(serverType, tftypes.NewValue(serverType, m))
	return &tfprotov6.ApplyResourceChangeResponse{NewState: &dv}, err
}

func (p *provider) ImportResourceState(_ context.Context, req *tfprotov6.ImportResourceStateRequest) (*tfprotov6.ImportResourceStateResponse, error) {
	fail := func(format string, args ...any) (*tfprotov6.ImportResourceStateResponse, error) {
		return &tfprotov6.ImportResourceStateResponse{Diagnostics: []*tfprotov6.Diagnostic{{
			Severity: tfprotov6.DiagnosticSeverityError, Summary: fmt.Sprintf(format, args...),
		}}}, nil
	}
	// What names the object: an id, or an identity. A bucket has no id.
	var want func(object) bool
	switch {
	case req.Identity != nil && req.Identity.IdentityData != nil:
		idType := identityType
		if req.TypeName == "demo_bucket" {
			idType = bucketIdentityType
		}
		v, err := req.Identity.IdentityData.Unmarshal(idType)
		if err != nil {
			return fail("the identity does not fit: %v", err)
		}
		var m map[string]tftypes.Value
		if err := v.As(&m); err != nil {
			return nil, err
		}
		get := func(name string) string {
			var s string
			_ = m[name].As(&s)
			return s
		}
		if req.TypeName == "demo_bucket" {
			want = func(o object) bool {
				return o.Type == "demo_bucket" && o.Name == get("bucket") && (get("region") == "" || get("region") == o.Region) && (get("account_id") == "" || get("account_id") == accountID)
			}
		} else {
			want = func(o object) bool {
				return o.Type == "" && o.ID == get("id") && (get("region") == "" || get("region") == o.Region)
			}
		}
	case req.TypeName == "demo_bucket":
		return fail("a demo_bucket has no id: import it by its identity (account_id, bucket and region)")
	default:
		want = func(o object) bool { return o.Type == "" && o.ID == req.ID }
	}
	p.mu.Lock()
	objs := p.catalog()
	p.mu.Unlock()
	for _, o := range objs {
		if !want(o) {
			continue
		}
		var st tftypes.Value
		if req.TypeName == "demo_bucket" {
			st = tftypes.NewValue(bucketType, map[string]tftypes.Value{
				"id": tftypes.NewValue(tftypes.String, o.ID), "bucket": tftypes.NewValue(tftypes.String, o.Name),
			})
		} else {
			st = tftypes.NewValue(serverType, map[string]tftypes.Value{
				"id": tftypes.NewValue(tftypes.String, o.ID), "name": tftypes.NewValue(tftypes.String, o.Name),
				"size": tftypes.NewValue(tftypes.String, "large"), "secret": tftypes.NewValue(tftypes.String, "hunter2"),
			})
		}
		dv, err := tfprotov6.NewDynamicValue(typeOf(req.TypeName), st)
		return &tfprotov6.ImportResourceStateResponse{ImportedResources: []*tfprotov6.ImportedResource{{TypeName: req.TypeName, State: &dv}}}, err
	}
	return fail("no such %s", req.TypeName)
}

func (p *provider) ValidateListResourceConfig(context.Context, *tfprotov6.ValidateListResourceConfigRequest) (*tfprotov6.ValidateListResourceConfigResponse, error) {
	return &tfprotov6.ValidateListResourceConfigResponse{}, nil
}

func (p *provider) ListResource(_ context.Context, req *tfprotov6.ListResourceRequest) (*tfprotov6.ListResourceServerStream, error) {
	p.mu.Lock()
	objs := p.catalog()
	p.mu.Unlock()
	if p.dir != "" {
		if b, err := os.ReadFile(filepath.Join(p.dir, "fail")); err == nil {
			return nil, fmt.Errorf("%s", strings.TrimSpace(string(b)))
		}
		if f, err := os.OpenFile(filepath.Join(p.dir, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, "list")
			f.Close()
		}
	}
	cfg, err := req.Config.Unmarshal(listType)
	if err != nil {
		return nil, err
	}
	var m map[string]tftypes.Value
	var region string
	if cfg.As(&m) == nil {
		_ = m["region"].As(&region)
	}
	return &tfprotov6.ListResourceServerStream{Results: func(yield func(tfprotov6.ListResourceResult) bool) {
		n := int64(0)
		for _, o := range objs {
			if o.typeName() != req.TypeName || (region != "" && o.Region != region) {
				continue
			}
			var id tftypes.Value
			idType := identityType
			if o.Type == "demo_bucket" {
				idType = bucketIdentityType
				id = tftypes.NewValue(idType, map[string]tftypes.Value{
					"account_id": tftypes.NewValue(tftypes.String, accountID), "bucket": tftypes.NewValue(tftypes.String, o.Name), "region": tftypes.NewValue(tftypes.String, o.Region),
				})
			} else {
				id = tftypes.NewValue(idType, map[string]tftypes.Value{
					"id": tftypes.NewValue(tftypes.String, o.ID), "region": tftypes.NewValue(tftypes.String, o.Region),
				})
			}
			dv, _ := tfprotov6.NewDynamicValue(idType, id)
			if !yield(tfprotov6.ListResourceResult{DisplayName: o.Name, Identity: &tfprotov6.ResourceIdentityData{IdentityData: &dv}}) {
				return
			}
			if n++; req.Limit > 0 && n >= req.Limit {
				return
			}
		}
	}}, nil
}
