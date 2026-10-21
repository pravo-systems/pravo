package plugin

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"pravo.dev/pravo/internal/schema"
	pb "pravo.dev/pravo/internal/tfplugin/tfplugin5"
)

type v5 struct {
	c     pb.ProviderClient
	close func()
}

func (p *v5) ProtocolVersion() int { return 5 }
func (p *v5) Close()               { p.close() }

func caps5() *pb.ClientCapabilities {
	return &pb.ClientCapabilities{DeferralAllowed: deferralAllowed, WriteOnlyAttributesAllowed: writeOnlyAttributesAllowed}
}

func diags5(in []*pb.Diagnostic) Diagnostics {
	var out Diagnostics
	for _, d := range in {
		sev := "warning"
		if d.Severity == pb.Diagnostic_ERROR {
			sev = "error"
		}
		out = append(out, Diagnostic{Severity: sev, Summary: d.Summary, Detail: d.Detail, Path: path5(d.Attribute)})
	}
	return out
}

func path5(p *pb.AttributePath) *tftypes.AttributePath {
	if p == nil {
		return nil
	}
	var steps []tftypes.AttributePathStep
	for _, s := range p.Steps {
		switch sel := s.Selector.(type) {
		case *pb.AttributePath_Step_AttributeName:
			steps = append(steps, tftypes.AttributeName(sel.AttributeName))
		case *pb.AttributePath_Step_ElementKeyString:
			steps = append(steps, tftypes.ElementKeyString(sel.ElementKeyString))
		case *pb.AttributePath_Step_ElementKeyInt:
			steps = append(steps, tftypes.ElementKeyInt(sel.ElementKeyInt))
		}
	}
	return tftypes.NewAttributePathWithSteps(steps)
}

func dv5(v tftypes.Value) (*pb.DynamicValue, error) {
	b, err := marshal(v)
	if err != nil {
		return nil, err
	}
	return &pb.DynamicValue{Msgpack: b}, nil
}

func fromDV5(d *pb.DynamicValue, t tftypes.Type) (tftypes.Value, error) {
	if d == nil {
		return tftypes.NewValue(t, nil), nil
	}
	return unmarshal(d.Msgpack, d.Json, t)
}

func nesting5(n pb.Schema_NestedBlock_NestingMode) schema.NestingMode {
	switch n {
	case pb.Schema_NestedBlock_SINGLE:
		return schema.NestingSingle
	case pb.Schema_NestedBlock_LIST:
		return schema.NestingList
	case pb.Schema_NestedBlock_SET:
		return schema.NestingSet
	case pb.Schema_NestedBlock_MAP:
		return schema.NestingMap
	case pb.Schema_NestedBlock_GROUP:
		return schema.NestingGroup
	}
	return schema.NestingInvalid
}

func attrs5(in []*pb.Schema_Attribute) []*schema.Attribute {
	out := make([]*schema.Attribute, 0, len(in))
	for _, a := range in {
		sa := &schema.Attribute{
			Name: a.Name, Description: a.Description,
			Required: a.Required, Optional: a.Optional, Computed: a.Computed,
			Sensitive: a.Sensitive, Deprecated: a.Deprecated, WriteOnly: a.WriteOnly,
		}
		if len(a.Type) > 0 {
			sa.TypeJSON = json.RawMessage(a.Type)
		}
		out = append(out, sa)
	}
	return out
}

func block5(b *pb.Schema_Block) *schema.Block {
	if b == nil {
		return &schema.Block{}
	}
	out := &schema.Block{Attributes: attrs5(b.Attributes), Description: b.Description, Deprecated: b.Deprecated}
	for _, nb := range b.BlockTypes {
		out.BlockTypes = append(out.BlockTypes, &schema.NestedBlock{
			TypeName: nb.TypeName, Block: block5(nb.Block), Nesting: nesting5(nb.Nesting),
			MinItems: nb.MinItems, MaxItems: nb.MaxItems,
		})
	}
	return out
}

func schema5(s *pb.Schema) *schema.Schema {
	if s == nil {
		return nil
	}
	return &schema.Schema{Version: s.Version, Block: block5(s.Block)}
}

func (p *v5) GetSchema(ctx context.Context) (*schema.ProviderSchema, Diagnostics, error) {
	resp, err := p.c.GetProviderSchema(ctx, &pb.GetProviderSchema_Request{})
	if err != nil {
		return nil, nil, err
	}
	ps := &schema.ProviderSchema{
		Provider: schema5(resp.Provider), ProviderMeta: schema5(resp.ProviderMeta),
		Resources: map[string]*schema.Schema{}, DataSources: map[string]*schema.Schema{},
	}
	if ps.Provider == nil {
		ps.Provider = &schema.Schema{Block: &schema.Block{}}
	}
	for k, v := range resp.ResourceSchemas {
		ps.Resources[k] = schema5(v)
	}
	for k, v := range resp.DataSourceSchemas {
		ps.DataSources[k] = schema5(v)
	}
	ps.Ephemerals = map[string]*schema.Schema{}
	for k, v := range resp.EphemeralResourceSchemas {
		ps.Ephemerals[k] = schema5(v)
	}
	ps.Functions = functions5(resp.Functions)
	return ps, diags5(resp.Diagnostics), nil
}

func (p *v5) ValidateProviderConfig(ctx context.Context, cfg tftypes.Value) (Diagnostics, error) {
	d, err := dv5(cfg)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.ValidateProviderConfig(ctx, &pb.PrepareProviderConfig_Request{Config: d})
	if err != nil {
		return nil, err
	}
	return diags5(resp.Diagnostics), nil
}

func (p *v5) Configure(ctx context.Context, cfg tftypes.Value) (Diagnostics, error) {
	d, err := dv5(cfg)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.ConfigureProvider(ctx, &pb.Configure_Request{TerraformVersion: CoreVersion, Config: d, ClientCapabilities: caps5()})
	if err != nil {
		return nil, err
	}
	return diags5(resp.Diagnostics), nil
}

func (p *v5) ValidateResourceConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	d, err := dv5(cfg)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.ValidateResourceConfig(ctx, &pb.ValidateResourceTypeConfig_Request{TypeName: typ, Config: d, ClientCapabilities: caps5()})
	if err != nil {
		return nil, err
	}
	return diags5(resp.Diagnostics), nil
}

func (p *v5) ValidateDataSourceConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	d, err := dv5(cfg)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.ValidateDataSourceConfig(ctx, &pb.ValidateDataSourceConfig_Request{TypeName: typ, Config: d})
	if err != nil {
		return nil, err
	}
	return diags5(resp.Diagnostics), nil
}

func (p *v5) UpgradeResourceState(ctx context.Context, typ string, version int64, jsonState []byte, want tftypes.Type) (tftypes.Value, Diagnostics, error) {
	resp, err := p.c.UpgradeResourceState(ctx, &pb.UpgradeResourceState_Request{
		TypeName: typ, Version: version, RawState: &pb.RawState{Json: jsonState},
	})
	if err != nil {
		return tftypes.Value{}, nil, err
	}
	diags := diags5(resp.Diagnostics)
	if diags.HasErrors() {
		return tftypes.Value{}, diags, nil
	}
	v, err := fromDV5(resp.UpgradedState, want)
	return v, diags, err
}

func (p *v5) ReadResource(ctx context.Context, typ string, state tftypes.Value, private []byte) (tftypes.Value, []byte, Diagnostics, error) {
	d, err := dv5(state)
	if err != nil {
		return tftypes.Value{}, nil, nil, err
	}
	resp, err := p.c.ReadResource(ctx, &pb.ReadResource_Request{TypeName: typ, CurrentState: d, Private: private, ClientCapabilities: caps5()})
	if err != nil {
		return tftypes.Value{}, nil, nil, err
	}
	diags := diags5(resp.Diagnostics)
	if diags.HasErrors() {
		return tftypes.Value{}, nil, diags, nil
	}
	v, err := fromDV5(resp.NewState, state.Type())
	return v, resp.Private, diags, err
}

func (p *v5) PlanResourceChange(ctx context.Context, typ string, prior, proposed, config tftypes.Value, priorPrivate []byte) (*PlanResult, Diagnostics, error) {
	pd, err := dv5(prior)
	if err != nil {
		return nil, nil, err
	}
	nd, err := dv5(proposed)
	if err != nil {
		return nil, nil, err
	}
	cd, err := dv5(config)
	if err != nil {
		return nil, nil, err
	}
	resp, err := p.c.PlanResourceChange(ctx, &pb.PlanResourceChange_Request{
		TypeName: typ, PriorState: pd, ProposedNewState: nd, Config: cd, PriorPrivate: priorPrivate,
		ClientCapabilities: caps5(),
	})
	if err != nil {
		return nil, nil, err
	}
	diags := diags5(resp.Diagnostics)
	if diags.HasErrors() {
		return nil, diags, nil
	}
	planned, err := fromDV5(resp.PlannedState, prior.Type())
	if err != nil {
		return nil, diags, err
	}
	res := &PlanResult{PlannedState: planned, PlannedPrivate: resp.PlannedPrivate, LegacyTypeSystem: resp.LegacyTypeSystem}
	for _, rp := range resp.RequiresReplace {
		res.RequiresReplace = append(res.RequiresReplace, path5(rp))
	}
	return res, diags, nil
}

func (p *v5) ApplyResourceChange(ctx context.Context, typ string, prior, planned, config tftypes.Value, plannedPrivate []byte) (*ApplyResult, Diagnostics, error) {
	pd, err := dv5(prior)
	if err != nil {
		return nil, nil, err
	}
	nd, err := dv5(planned)
	if err != nil {
		return nil, nil, err
	}
	cd, err := dv5(config)
	if err != nil {
		return nil, nil, err
	}
	resp, err := p.c.ApplyResourceChange(ctx, &pb.ApplyResourceChange_Request{
		TypeName: typ, PriorState: pd, PlannedState: nd, Config: cd, PlannedPrivate: plannedPrivate,
	})
	if err != nil {
		return nil, nil, err
	}
	diags := diags5(resp.Diagnostics)
	var newState tftypes.Value
	if resp.NewState != nil {
		newState, err = fromDV5(resp.NewState, prior.Type())
		if err != nil {
			return nil, diags, err
		}
	} else {
		newState = tftypes.NewValue(prior.Type(), nil)
	}
	return &ApplyResult{NewState: newState, Private: resp.Private, LegacyTypeSystem: resp.LegacyTypeSystem}, diags, nil
}

func (p *v5) ImportResourceState(ctx context.Context, typ, id string, types map[string]tftypes.Type) ([]ImportedResource, Diagnostics, error) {
	resp, err := p.c.ImportResourceState(ctx, &pb.ImportResourceState_Request{TypeName: typ, Id: id, ClientCapabilities: caps5()})
	if err != nil {
		return nil, nil, err
	}
	diags := diags5(resp.Diagnostics)
	var out []ImportedResource
	for _, r := range resp.ImportedResources {
		t, ok := types[r.TypeName]
		if !ok {
			continue
		}
		v, err := fromDV5(r.State, t)
		if err != nil {
			return nil, diags, err
		}
		out = append(out, ImportedResource{TypeName: r.TypeName, State: v, Private: r.Private})
	}
	return out, diags, nil
}

func (p *v5) ReadDataSource(ctx context.Context, typ string, cfg tftypes.Value) (tftypes.Value, Diagnostics, error) {
	d, err := dv5(cfg)
	if err != nil {
		return tftypes.Value{}, nil, err
	}
	resp, err := p.c.ReadDataSource(ctx, &pb.ReadDataSource_Request{TypeName: typ, Config: d, ClientCapabilities: caps5()})
	if err != nil {
		return tftypes.Value{}, nil, err
	}
	diags := diags5(resp.Diagnostics)
	if diags.HasErrors() {
		return tftypes.Value{}, diags, nil
	}
	v, err := fromDV5(resp.State, cfg.Type())
	return v, diags, err
}

func (p *v5) ValidateEphemeralConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	d, err := dv5(cfg)
	if err != nil {
		return nil, err
	}
	resp, err := p.c.ValidateEphemeralResourceConfig(ctx, &pb.ValidateEphemeralResourceConfig_Request{TypeName: typ, Config: d})
	if err != nil {
		return nil, err
	}
	return diags5(resp.Diagnostics), nil
}

func (p *v5) OpenEphemeral(ctx context.Context, typ string, cfg tftypes.Value) (*EphemeralResult, Diagnostics, error) {
	d, err := dv5(cfg)
	if err != nil {
		return nil, nil, err
	}
	resp, err := p.c.OpenEphemeralResource(ctx, &pb.OpenEphemeralResource_Request{TypeName: typ, Config: d, ClientCapabilities: caps5()})
	if err != nil {
		return nil, nil, err
	}
	diags := diags5(resp.Diagnostics)
	if diags.HasErrors() {
		return nil, diags, nil
	}
	res := &EphemeralResult{Private: resp.Private, Deferred: resp.Deferred != nil}
	if resp.RenewAt != nil {
		t := resp.RenewAt.AsTime()
		res.RenewAt = &t
	}
	res.Result, err = fromDV5(resp.Result, cfg.Type())
	return res, diags, err
}

func (p *v5) RenewEphemeral(ctx context.Context, typ string, private []byte) (*EphemeralResult, Diagnostics, error) {
	resp, err := p.c.RenewEphemeralResource(ctx, &pb.RenewEphemeralResource_Request{TypeName: typ, Private: private})
	if err != nil {
		return nil, nil, err
	}
	diags := diags5(resp.Diagnostics)
	if diags.HasErrors() {
		return nil, diags, nil
	}
	res := &EphemeralResult{Private: resp.Private}
	if resp.RenewAt != nil {
		t := resp.RenewAt.AsTime()
		res.RenewAt = &t
	}
	return res, diags, nil
}

func (p *v5) CloseEphemeral(ctx context.Context, typ string, private []byte) (Diagnostics, error) {
	resp, err := p.c.CloseEphemeralResource(ctx, &pb.CloseEphemeralResource_Request{TypeName: typ, Private: private})
	if err != nil {
		return nil, err
	}
	return diags5(resp.Diagnostics), nil
}

func (p *v5) CallFunction(ctx context.Context, name string, args []tftypes.Value, argTypes []tftypes.Type, ret tftypes.Type) (tftypes.Value, *FunctionError, error) {
	req := &pb.CallFunction_Request{Name: name}
	for i, a := range args {
		// Encode against the declared parameter type, so that a dynamic
		// parameter receives the type-carrying encoding it expects.
		b, err := a.MarshalMsgPack(argTypes[i])
		if err != nil {
			return tftypes.Value{}, nil, err
		}
		req.Arguments = append(req.Arguments, &pb.DynamicValue{Msgpack: b})
	}
	resp, err := p.c.CallFunction(ctx, req)
	if err != nil {
		return tftypes.Value{}, nil, err
	}
	if resp.Error != nil {
		return tftypes.Value{}, &FunctionError{Text: resp.Error.Text, Argument: resp.Error.FunctionArgument}, nil
	}
	v, err := fromDV5(resp.Result, ret)
	return v, nil, err
}

func functions5(in map[string]*pb.Function) map[string]*schema.Function {
	out := map[string]*schema.Function{}
	param := func(p *pb.Function_Parameter) *schema.Parameter {
		if p == nil {
			return nil
		}
		return &schema.Parameter{Name: p.Name, TypeJSON: json.RawMessage(p.Type), AllowNull: p.AllowNullValue, AllowUnknown: p.AllowUnknownValues, Description: p.Description}
	}
	for name, f := range in {
		sf := &schema.Function{Summary: f.Summary, Description: f.Description, Deprecated: f.DeprecationMessage != "", Variadic: param(f.VariadicParameter)}
		for _, p := range f.Parameters {
			sf.Parameters = append(sf.Parameters, param(p))
		}
		if f.Return != nil {
			sf.ReturnJSON = json.RawMessage(f.Return.Type)
		}
		out[name] = sf
	}
	return out
}

func (p *v5) Stop(ctx context.Context) error {
	_, err := p.c.StopProvider(ctx, &pb.Stop_Request{})
	return err
}
