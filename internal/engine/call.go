package engine

import (
	"context"
	"fmt"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"pravo.dev/pravo/internal/config"
	"pravo.dev/pravo/internal/convert"
	"pravo.dev/pravo/internal/schema"
)

// ActionCall marks a provider-defined function call evaluated for the run.
const ActionCall Action = "call"

func (e *Engine) functionSchema(r *config.Resource) (*schema.Function, error) {
	ps, ok := e.Schemas[r.LocalProvider()]
	if !ok {
		return nil, fmt.Errorf("%s: no schema for provider %s", r.Address(), r.LocalProvider())
	}
	f := ps.Functions[r.Type]
	if f == nil {
		return nil, fmt.Errorf("%s: provider %s has no function %q", r.Address(), r.LocalProvider(), r.Type)
	}
	return f, nil
}

// callTask evaluates call.<provider>.<function>.<name>: converts args, calls
// the function when they are known, and returns the Go value to fill into
// the task (its result).
func (e *Engine) callTask(ctx context.Context, r *config.Resource, v cue.Value, strict bool) (map[string]any, error) {
	f, err := e.functionSchema(r)
	if err != nil {
		return nil, err
	}
	addr := r.Address()
	ch := &Change{Addr: addr, Mode: r.Mode, Type: r.Type, Name: r.Name, ProviderKey: r.ProviderKey, Action: ActionCall, Deps: e.deps[addr]}

	e.cueMu.Lock()
	argsV := v.LookupPath(cue.MakePath(cue.Str("args")))
	var elems []cue.Value
	if argsV.Exists() {
		if it, err := convert.Default(argsV).List(); err == nil {
			for it.Next() {
				elems = append(elems, it.Value())
			}
		} else {
			e.cueMu.Unlock()
			return nil, fmt.Errorf("%s: args must be a list: %w", addr, err)
		}
	}
	conv := &convert.Converter{Strict: strict, Self: r.Path, Resolve: e.resolver(), IsEphemeral: e.isEphemeral}
	if ref, ok := conv.EphemeralRef(argsV); ok {
		e.cueMu.Unlock()
		return nil, fmt.Errorf("%s: args reference %s; function results may be recorded in state, so ephemeral values cannot be passed to a function", addr, ref)
	}
	args := make([]tftypes.Value, 0, len(elems))
	argTypes := make([]tftypes.Type, 0, len(elems))
	for i, el := range elems {
		var t tftypes.Type
		switch {
		case i < len(f.Parameters):
			t = f.Parameters[i].Type()
		case f.Variadic != nil:
			t = f.Variadic.Type()
		default:
			e.cueMu.Unlock()
			return nil, fmt.Errorf("%s: %d arguments given, %s takes %d", addr, len(elems), r.Type, len(f.Parameters))
		}
		args = append(args, conv.Value(fmt.Sprintf("%s.args[%d]", addr, i), t, el))
		argTypes = append(argTypes, t)
	}
	e.cueMu.Unlock()
	if err := conv.Err(); err != nil {
		return nil, err
	}
	if len(elems) < len(f.Parameters) {
		return nil, fmt.Errorf("%s: %d arguments given, %s needs %d", addr, len(elems), r.Type, len(f.Parameters))
	}
	for i, a := range args {
		if i < len(f.Parameters) && a.IsNull() && !f.Parameters[i].AllowNull {
			return nil, fmt.Errorf("%s: argument %d (%s) must not be null", addr, i, f.Parameters[i].Name)
		}
	}
	if conv.Unknown {
		// Unknown inputs: the result is known after apply.
		ch.Deferred = true
		e.record(ch)
		return nil, nil
	}
	prov, err := e.Providers.Unconfigured(ctx, r.ProviderKey)
	if err != nil {
		return nil, err
	}
	res, ferr, err := prov.CallFunction(ctx, r.Type, args, argTypes, f.ReturnType())
	if err != nil {
		return nil, fmt.Errorf("%s: calling %s: %w", addr, r.Type, err)
	}
	if ferr != nil {
		return nil, fmt.Errorf("%s: %s: %w", addr, r.Type, ferr)
	}
	g, err := convert.ToGo(res)
	if err != nil {
		return nil, fmt.Errorf("%s: result of %s: %w", addr, r.Type, err)
	}
	ch.Planned = res
	e.record(ch)
	e.count(func(st *Stats) { st.Called++ })
	return map[string]any{"result": g}, nil
}
