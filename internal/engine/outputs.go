package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"cuelang.org/go/cue"
	cuejson "cuelang.org/go/encoding/json"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// sensitiveKey marks a recorded output that listings must not show.
const sensitiveKey = "$sensitive"

// WrapOutput prepares an output value for the state: a sensitive one is
// recorded as {"$sensitive": value}.
func WrapOutput(value json.RawMessage, sensitive bool) json.RawMessage {
	if !sensitive {
		return value
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]json.RawMessage{sensitiveKey: value})
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// indentJSON encodes v as indented JSON. The text of a value is written as it
// is: <, > and & are not written as \u003c, \u003e and \u0026.
func indentJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnwrapOutput returns the value of a recorded output and whether it is
// sensitive.
func UnwrapOutput(raw json.RawMessage) (json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil && len(m) == 1 {
		if v, ok := m[sensitiveKey]; ok {
			return v, true
		}
	}
	return raw, false
}

// outputsAfterDestroy returns the outputs to record when a destroy has ended,
// given the outputs that are recorded. A destroy that removes everything the
// plan covers records none. A part of a saved destroy plan leaves resources
// behind: an output whose value reads none of the resources the destroy removed
// still evaluates from what is left and stays as it is recorded; an output that
// reads one of them is removed.
func (e *Engine) outputsAfterDestroy(plan *Plan, recorded map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if !plan.Partial {
		return nil, nil
	}
	gone := map[string]bool{}
	for _, ch := range plan.Changes {
		if ch.Action == ActionDelete {
			gone[ch.Addr] = true
		}
	}
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	kept := map[string]json.RawMessage{}
	reads := e.outputReadsLocked()
	for _, o := range e.Root.Outputs {
		raw, ok := recorded[o.Name]
		if !ok {
			continue
		}
		if !slices.ContainsFunc(reads[o.Name], func(addr string) bool { return gone[addr] }) {
			kept[o.Name] = raw
		}
	}
	return kept, nil
}

// outputJSON encodes a concrete output value with the keys of every struct in
// sorted order. A struct built by a comprehension over resources lists its
// fields in the order the resources finished, which differs from run to run.
func outputJSON(v cue.Value) ([]byte, error) {
	b, err := convert.Default(v).MarshalJSON()
	if err != nil {
		return nil, err
	}
	return sortedJSON(b)
}

// sortedJSON re-encodes a JSON document through Go maps, which encode their
// keys in order. Numbers keep the text they have.
func sortedJSON(b []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(x); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// canonicalOutput returns a concrete output value with its struct keys in
// sorted order, so that a plan prints it as the apply records it. A value that
// is not concrete yet is returned as it is.
func canonicalOutput(v cue.Value) cue.Value {
	if !convert.IsConcrete(v) {
		return v
	}
	b, err := outputJSON(v)
	if err != nil {
		return v
	}
	expr, err := cuejson.Extract("", b)
	if err != nil {
		return v
	}
	if c := v.Context().BuildExpr(expr); c.Err() == nil {
		return c
	}
	return v
}

// builtinTask runs a task that pravo implements itself.
func (e *Engine) builtinTask(ctx context.Context, r *config.Resource, v cue.Value) error {
	if r.Type == "remote_outputs" {
		return e.remoteOutputsTask(ctx, r)
	}
	return e.envTask(r, v)
}

// notReaderOfOutputs is the error for the outputs of a state that is sealed to
// readers that do not include this process. It wraps state.ErrNotReader, so that
// the command exits with the code of that error.
func notReaderOfOutputs(name string, id *protect.Identity, idErr error) error {
	who := "no identity was found"
	switch {
	case idErr != nil && !errors.Is(idErr, os.ErrNotExist):
		who = "the identity could not be used: " + idErr.Error()
	case id != nil:
		who = "the identity " + id.Name + " is not one of them"
	}
	return fmt.Errorf("the outputs of %s are sealed to other readers, and %s; ask one of its readers to add yours to .pravo/access/readers.txt of that state: %w", name, who, state.ErrNotReader)
}

// openRemoteOutputs reads the outputs of the state at b. If that state is sealed,
// they are read with the identity of this process, which is looked for only then.
// A state that is sealed to other readers is an error that wraps state.ErrNotReader.
func (e *Engine) openRemoteOutputs(ctx context.Context, b state.Backend) (map[string]json.RawMessage, error) {
	raw, err := state.OpenOutputs(ctx, b, nil)
	if !errors.Is(err, state.ErrNotReader) {
		return raw, err
	}
	var id *protect.Identity
	var idErr error
	if e.Opts.Identity != nil {
		// A meta.json that cannot be read leaves the choice without the readers; the
		// open that follows says why it failed.
		readers, _ := state.OutputsReaders(ctx, b)
		if id, idErr = e.Opts.Identity(readers); idErr != nil {
			id = nil
		}
	}
	if id != nil {
		if raw, err = state.OpenOutputs(ctx, b, id); !errors.Is(err, state.ErrNotReader) {
			return raw, err
		}
	}
	return nil, notReaderOfOutputs(b.Name(), id, idErr)
}

// RemoteBackend opens the backend that the state settings name, an S3 bucket or an
// http server. The credentials the settings leave out come from the environment:
// AWS_SESSION_TOKEN for a bucket and PRAVO_STATE_TOKEN for a server.
func RemoteBackend(ctx context.Context, st config.StateConfig) (state.Backend, error) {
	if st.Backend == "http" {
		token := st.Token
		if token == "" && st.Username == "" {
			token = os.Getenv("PRAVO_STATE_TOKEN")
		}
		return state.NewHTTPBackend(state.HTTPConfig{URL: st.URL, Token: token, Username: st.Username, Password: st.Password})
	}
	return state.NewS3Backend(ctx, state.S3Config{
		Bucket: st.Bucket, Prefix: st.Prefix, Region: st.Region, Endpoint: st.Endpoint,
		PathStyle: st.PathStyle, Profile: st.Profile, AccessKey: st.AccessKey, SecretKey: st.SecretKey,
		SessionToken: os.Getenv("AWS_SESSION_TOKEN"),
	})
}

// remoteOutputsTask reads the outputs another root has recorded and fills
// them into the configuration. Outputs that root marked sensitive are left
// out. If that root's state is sealed, they are read with the identity of this
// process, which has to be one of its readers.
func (e *Engine) remoteOutputsTask(ctx context.Context, r *config.Resource) error {
	var b state.Backend
	if r.Remote.State.Remote() {
		var err error
		if b, err = RemoteBackend(ctx, r.Remote.State); err != nil {
			return fmt.Errorf("%s: %w", r.Address(), err)
		}
	} else {
		b = &state.LocalBackend{Dir: r.Remote.Dir}
	}
	plog.Debug(ctx, "reading outputs of another root", "addr", r.Address(), "state", b.Name())
	raw, err := e.openRemoteOutputs(ctx, b)
	if err != nil {
		return fmt.Errorf("%s: %w", r.Address(), err)
	}
	outputs := map[string]any{}
	for name, rec := range raw {
		value, sensitive := UnwrapOutput(rec)
		if sensitive {
			continue
		}
		var g any
		if err := json.Unmarshal(value, &g); err != nil {
			return fmt.Errorf("%s: output %s of %s: %w", r.Address(), name, b.Name(), err)
		}
		outputs[name] = g
	}
	return e.fill(r, map[string]any{"outputs": outputs})
}

// envTask reads the environment variable of a pravo.#Env and fills it into
// the configuration. It runs on every plan and apply and records nothing.
func (e *Engine) envTask(r *config.Resource, v cue.Value) error {
	e.cueMu.Lock()
	name, err := v.LookupPath(cue.ParsePath("name")).String()
	def, derr := v.LookupPath(cue.ParsePath("default")).String()
	e.cueMu.Unlock()
	if err != nil {
		return nil // the name depends on something not known yet
	}
	val, ok := os.LookupEnv(name)
	plog.Debug(context.Background(), "environment variable", "addr", r.Address(), "name", name, "set", ok)
	if !ok {
		if derr != nil {
			if r.Type == "secret_env" {
				return fmt.Errorf("%s: the environment variable %s is not set", r.Address(), name)
			}
			return fmt.Errorf("%s: the environment variable %s is not set, and there is no default", r.Address(), name)
		}
		val = def
	}
	return e.fill(r, map[string]any{"value": val})
}
