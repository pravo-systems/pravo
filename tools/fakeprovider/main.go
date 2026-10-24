// Command fakeprovider is a provider for trying pravo discoveries without a
// cloud. It speaks the plugin protocol, version 6, as a provider binary does, and
// has one resource type, demo_server, that is also a list resource type, so a
// specification can declare a discovery of it:
//
//	go build -o terraform-provider-demo ./tools/fakeprovider
//
//	import demo "pravo.dev/providers/demo/demo"
//
//	$demo: demo.#Provider & {$path: "./terraform-provider-demo"}
//	servers: $demo.#Discovery.demo_server & {region: "eu"}
//
// The objects that exist are kept in the directory that FAKEPROVIDER_DIR names, in
// catalog.json, a list of {"id", "name", "region"}. Applying a demo_server adds
// an object to it. The file "fail", when it exists, makes a list fail with its
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
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region"`
}

// provider implements the RPCs that pravo calls. The others are nil.
type provider struct {
	tfprotov6.ProviderServer

	dir string
	mu  sync.Mutex
	mem []object
}

var (
	serverType   = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String, "name": tftypes.String, "size": tftypes.String, "secret": tftypes.String}}
	listType     = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"region": tftypes.String}}
	identityType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String, "region": tftypes.String}}
)

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
		}}}},
		ListResourceSchemas: map[string]*tfprotov6.Schema{"demo_server": {Block: &tfprotov6.SchemaBlock{Attributes: []*tfprotov6.SchemaAttribute{
			{Name: "region", Type: tftypes.String, Optional: true, Description: "Only the servers of this region."},
		}}}},
	}, nil
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
	v, err := req.RawState.UnmarshalWithOpts(serverType, tfprotov6.UnmarshalOpts{})
	if err != nil {
		return nil, err
	}
	dv, err := tfprotov6.NewDynamicValue(serverType, v)
	return &tfprotov6.UpgradeResourceStateResponse{UpgradedState: &dv}, err
}

func (p *provider) ReadResource(_ context.Context, req *tfprotov6.ReadResourceRequest) (*tfprotov6.ReadResourceResponse, error) {
	return &tfprotov6.ReadResourceResponse{NewState: req.CurrentState, Private: req.Private}, nil
}

func (p *provider) PlanResourceChange(_ context.Context, req *tfprotov6.PlanResourceChangeRequest) (*tfprotov6.PlanResourceChangeResponse, error) {
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
	_ = m["name"].As(&name)
	if !m["id"].IsKnown() || m["id"].IsNull() {
		m["id"] = tftypes.NewValue(tftypes.String, "srv-"+name)
	}
	var id string
	_ = m["id"].As(&id)
	p.mu.Lock()
	if objs := p.catalog(); !slices.ContainsFunc(objs, func(o object) bool { return o.ID == id }) {
		p.setCatalog(append(objs, object{ID: id, Name: name, Region: "eu"}))
	}
	p.mu.Unlock()
	dv, err := tfprotov6.NewDynamicValue(serverType, tftypes.NewValue(serverType, m))
	return &tfprotov6.ApplyResourceChangeResponse{NewState: &dv}, err
}

func (p *provider) ImportResourceState(_ context.Context, req *tfprotov6.ImportResourceStateRequest) (*tfprotov6.ImportResourceStateResponse, error) {
	p.mu.Lock()
	objs := p.catalog()
	p.mu.Unlock()
	for _, o := range objs {
		if o.ID != req.ID {
			continue
		}
		st := tftypes.NewValue(serverType, map[string]tftypes.Value{
			"id": tftypes.NewValue(tftypes.String, o.ID), "name": tftypes.NewValue(tftypes.String, o.Name),
			"size": tftypes.NewValue(tftypes.String, "large"), "secret": tftypes.NewValue(tftypes.String, "hunter2"),
		})
		dv, err := tfprotov6.NewDynamicValue(serverType, st)
		return &tfprotov6.ImportResourceStateResponse{ImportedResources: []*tfprotov6.ImportedResource{{TypeName: req.TypeName, State: &dv}}}, err
	}
	return &tfprotov6.ImportResourceStateResponse{Diagnostics: []*tfprotov6.Diagnostic{{
		Severity: tfprotov6.DiagnosticSeverityError, Summary: "no such server " + req.ID,
	}}}, nil
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
			if region != "" && o.Region != region {
				continue
			}
			id := tftypes.NewValue(identityType, map[string]tftypes.Value{
				"id": tftypes.NewValue(tftypes.String, o.ID), "region": tftypes.NewValue(tftypes.String, o.Region),
			})
			dv, _ := tfprotov6.NewDynamicValue(identityType, id)
			if !yield(tfprotov6.ListResourceResult{DisplayName: o.Name, Identity: &tfprotov6.ResourceIdentityData{IdentityData: &dv}}) {
				return
			}
			if n++; req.Limit > 0 && n >= req.Limit {
				return
			}
		}
	}}, nil
}
