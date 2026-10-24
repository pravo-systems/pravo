package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// Recorder wraps a Provider and records every response under a directory,
// or replays recorded responses without a live provider. A recording makes
// a specification testable in CI, deterministically and without
// credentials: PRAVO_RECORD=<dir> records, PRAVO_REPLAY=<dir> replays.
//
// Requests are keyed by method and a digest of their arguments; responses
// are stored as JSON with provider values as msgpack.
//
// A response holds the attributes of the objects the provider returns and the
// results of ephemeral resources, so a recording is as sensitive as state: its
// directories are created with mode 0700 and its files with mode 0600. The
// request values are not stored. Provider configuration, which carries the
// credentials, and the write-only attributes of a resource sit in the requests;
// replay finds a response by its key and never reads the request.
type Recorder struct {
	inner  Provider // nil in replay mode
	dir    string
	replay bool
	mu     sync.Mutex
}

// ErrNoRecording is returned in replay mode for a request that was not recorded.
var ErrNoRecording = errors.New("no recording for this request")

// Modes of what a recording writes. Directories that already exist keep their
// mode, except dir itself, which holds nothing but the recording of one
// provider.
const (
	recordingDirMode  = 0o700
	recordingFileMode = 0o600
)

// NewRecorder wraps p to record into dir.
func NewRecorder(p Provider, dir string) (*Recorder, error) {
	if err := os.MkdirAll(dir, recordingDirMode); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, recordingDirMode); err != nil {
		return nil, err
	}
	return &Recorder{inner: p, dir: dir}, nil
}

// NewReplayer serves recorded responses from dir and never starts a provider.
func NewReplayer(dir string) (*Recorder, error) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("replay directory %s: %v", dir, err)
	}
	return &Recorder{dir: dir, replay: true}, nil
}

type record struct {
	Method   string            `json:"method"`
	Request  json.RawMessage   `json:"request"`
	Values   map[string][]byte `json:"values,omitempty"` // msgpack-encoded tftypes values of the response
	Bytes    map[string][]byte `json:"bytes,omitempty"`  // opaque byte fields (private data)
	Diags    Diagnostics       `json:"diagnostics,omitempty"`
	Error    string            `json:"error,omitempty"`
	Extra    json.RawMessage   `json:"extra,omitempty"` // method-specific JSON (schema, replace paths, timestamps)
	Protocol int               `json:"protocol"`
}

// recordedFields names the plain fields of a request that a recording keeps.
// Every other field carries a value, a state or private data of the provider.
var recordedFields = []string{"name", "type", "version"}

// key returns the name under which the response to a request is stored and
// the part of the request that the recording keeps: the method's plain fields,
// without the values.
func (r *Recorder) key(method string, req map[string]any) (string, json.RawMessage) {
	b, _ := json.Marshal(req)
	sum := sha256.Sum256(append([]byte(method+"\x00"), b...))
	kept := map[string]any{}
	for _, f := range recordedFields {
		if v, ok := req[f]; ok {
			kept[f] = v
		}
	}
	rb, _ := json.Marshal(kept)
	return method + "-" + hex.EncodeToString(sum[:8]), rb
}

func (r *Recorder) path(key string) string { return filepath.Join(r.dir, key+".json") }

func (r *Recorder) load(key string) (*record, error) {
	data, err := os.ReadFile(r.path(key))
	if err != nil {
		return nil, err
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *Recorder) save(key string, rec *record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := json.MarshalIndent(rec, "", " ")
	if err == nil {
		_ = writePrivate(r.path(key), data)
	}
}

// writePrivate writes a file that only its owner can read. The data goes to a
// new file that replaces the old one, so that a recording made before has no
// effect on the mode.
func writePrivate(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, recordingFileMode)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func packed(v tftypes.Value) []byte {
	if v.Type() == nil {
		return nil
	}
	b, _ := v.MarshalMsgPack(v.Type())
	return b
}

func unpacked(b []byte, t tftypes.Type) tftypes.Value {
	if len(b) == 0 {
		return tftypes.NewValue(t, nil)
	}
	v, err := tftypes.ValueFromMsgPack(b, t)
	if err != nil {
		return tftypes.NewValue(t, nil)
	}
	return v
}

// reqOf builds a stable request document from msgpack-encoded values and
// plain fields.
func reqOf(fields map[string]any, values ...tftypes.Value) map[string]any {
	out := map[string]any{}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = fields[k]
	}
	for i, v := range values {
		out[fmt.Sprintf("v%d", i)] = packed(v)
	}
	return out
}

func (r *Recorder) replayErr(method string) error {
	return fmt.Errorf("%s: %w (replaying from %s)", method, ErrNoRecording, r.dir)
}

func (r *Recorder) ProtocolVersion() int {
	if r.inner != nil {
		return r.inner.ProtocolVersion()
	}
	return 0
}

func (r *Recorder) Close() {
	if r.inner != nil {
		r.inner.Close()
	}
}

func (r *Recorder) Stop(ctx context.Context) error {
	if r.inner != nil {
		return r.inner.Stop(ctx)
	}
	return nil
}

func (r *Recorder) GetSchema(ctx context.Context) (*schema.ProviderSchema, Diagnostics, error) {
	key, req := r.key("GetSchema", reqOf(nil))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return nil, nil, r.replayErr("GetSchema")
		}
		ps, err := schema.Unmarshal(rec.Extra)
		return ps, rec.Diags, err
	}
	ps, diags, err := r.inner.GetSchema(ctx)
	if err == nil {
		extra, _ := ps.Marshal()
		r.save(key, &record{Method: "GetSchema", Request: req, Diags: diags, Extra: extra, Protocol: r.inner.ProtocolVersion()})
	}
	return ps, diags, err
}

func (r *Recorder) diagsOnly(ctx context.Context, method string, fields map[string]any, cfg tftypes.Value, call func() (Diagnostics, error)) (Diagnostics, error) {
	key, req := r.key(method, reqOf(fields, cfg))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return nil, nil // validation and configuration are considered to have passed
		}
		return rec.Diags, nil
	}
	diags, err := call()
	if err == nil {
		r.save(key, &record{Method: method, Request: req, Diags: diags})
	}
	return diags, err
}

func (r *Recorder) ValidateProviderConfig(ctx context.Context, cfg tftypes.Value) (Diagnostics, error) {
	return r.diagsOnly(ctx, "ValidateProviderConfig", nil, cfg, func() (Diagnostics, error) { return r.inner.ValidateProviderConfig(ctx, cfg) })
}

func (r *Recorder) Configure(ctx context.Context, cfg tftypes.Value) (Diagnostics, error) {
	return r.diagsOnly(ctx, "Configure", nil, cfg, func() (Diagnostics, error) { return r.inner.Configure(ctx, cfg) })
}

func (r *Recorder) ValidateResourceConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	return r.diagsOnly(ctx, "ValidateResourceConfig", map[string]any{"type": typ}, cfg, func() (Diagnostics, error) { return r.inner.ValidateResourceConfig(ctx, typ, cfg) })
}

func (r *Recorder) ValidateDataSourceConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	return r.diagsOnly(ctx, "ValidateDataSourceConfig", map[string]any{"type": typ}, cfg, func() (Diagnostics, error) { return r.inner.ValidateDataSourceConfig(ctx, typ, cfg) })
}

func (r *Recorder) ValidateEphemeralConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	return r.diagsOnly(ctx, "ValidateEphemeralConfig", map[string]any{"type": typ}, cfg, func() (Diagnostics, error) { return r.inner.ValidateEphemeralConfig(ctx, typ, cfg) })
}

func (r *Recorder) UpgradeResourceState(ctx context.Context, typ string, version int64, jsonState []byte, want tftypes.Type) (tftypes.Value, Diagnostics, error) {
	key, req := r.key("UpgradeResourceState", reqOf(map[string]any{"type": typ, "version": version, "state": json.RawMessage(jsonState)}))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			// Without a recording, the JSON state is already in the current schema.
			v, err := tftypes.ValueFromJSON(jsonState, want)
			return v, nil, err
		}
		return unpacked(rec.Values["state"], want), rec.Diags, nil
	}
	v, diags, err := r.inner.UpgradeResourceState(ctx, typ, version, jsonState, want)
	if err == nil {
		r.save(key, &record{Method: "UpgradeResourceState", Request: req, Values: map[string][]byte{"state": packed(v)}, Diags: diags})
	}
	return v, diags, err
}

func (r *Recorder) ReadResource(ctx context.Context, typ string, state tftypes.Value, private []byte) (tftypes.Value, []byte, Diagnostics, error) {
	key, req := r.key("ReadResource", reqOf(map[string]any{"type": typ, "private": private}, state))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			// Unrecorded reads return the state unchanged: no drift.
			return state, private, nil, nil
		}
		return unpacked(rec.Values["state"], state.Type()), rec.Bytes["private"], rec.Diags, nil
	}
	v, p, diags, err := r.inner.ReadResource(ctx, typ, state, private)
	if err == nil {
		r.save(key, &record{Method: "ReadResource", Request: req, Values: map[string][]byte{"state": packed(v)}, Bytes: map[string][]byte{"private": p}, Diags: diags})
	}
	return v, p, diags, err
}

type planExtra struct {
	RequiresReplace []string `json:"requires_replace,omitempty"`
	Legacy          bool     `json:"legacy,omitempty"`
}

func (r *Recorder) PlanResourceChange(ctx context.Context, typ string, prior, proposed, config tftypes.Value, priorPrivate []byte) (*PlanResult, Diagnostics, error) {
	key, req := r.key("PlanResourceChange", reqOf(map[string]any{"type": typ, "private": priorPrivate}, prior, proposed, config))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return nil, nil, r.replayErr("PlanResourceChange " + typ)
		}
		var extra planExtra
		_ = json.Unmarshal(rec.Extra, &extra)
		res := &PlanResult{PlannedState: unpacked(rec.Values["planned"], prior.Type()), PlannedPrivate: rec.Bytes["private"], LegacyTypeSystem: extra.Legacy}
		for _, n := range extra.RequiresReplace {
			res.RequiresReplace = append(res.RequiresReplace, tftypes.NewAttributePath().WithAttributeName(n))
		}
		return res, rec.Diags, nil
	}
	res, diags, err := r.inner.PlanResourceChange(ctx, typ, prior, proposed, config, priorPrivate)
	if err == nil && res != nil {
		var extra planExtra
		extra.Legacy = res.LegacyTypeSystem
		for _, p := range res.RequiresReplace {
			if steps := p.Steps(); len(steps) > 0 {
				if n, ok := steps[0].(tftypes.AttributeName); ok {
					extra.RequiresReplace = append(extra.RequiresReplace, string(n))
				}
			}
		}
		eb, _ := json.Marshal(extra)
		r.save(key, &record{Method: "PlanResourceChange", Request: req, Values: map[string][]byte{"planned": packed(res.PlannedState)}, Bytes: map[string][]byte{"private": res.PlannedPrivate}, Diags: diags, Extra: eb})
	}
	return res, diags, err
}

func (r *Recorder) ApplyResourceChange(ctx context.Context, typ string, prior, planned, config tftypes.Value, plannedPrivate []byte) (*ApplyResult, Diagnostics, error) {
	key, req := r.key("ApplyResourceChange", reqOf(map[string]any{"type": typ, "private": plannedPrivate}, prior, planned, config))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return nil, nil, r.replayErr("ApplyResourceChange " + typ)
		}
		return &ApplyResult{NewState: unpacked(rec.Values["state"], prior.Type()), Private: rec.Bytes["private"]}, rec.Diags, nil
	}
	res, diags, err := r.inner.ApplyResourceChange(ctx, typ, prior, planned, config, plannedPrivate)
	if err == nil && res != nil {
		r.save(key, &record{Method: "ApplyResourceChange", Request: req, Values: map[string][]byte{"state": packed(res.NewState)}, Bytes: map[string][]byte{"private": res.Private}, Diags: diags})
	}
	return res, diags, err
}

func (r *Recorder) ImportResourceState(ctx context.Context, typ string, target ImportTarget, types map[string]tftypes.Type) ([]ImportedResource, Diagnostics, error) {
	if r.replay {
		return nil, nil, r.replayErr("ImportResourceState")
	}
	return r.inner.ImportResourceState(ctx, typ, target, types)
}

func (r *Recorder) ReadDataSource(ctx context.Context, typ string, cfg tftypes.Value) (tftypes.Value, Diagnostics, error) {
	key, req := r.key("ReadDataSource", reqOf(map[string]any{"type": typ}, cfg))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return tftypes.Value{}, nil, r.replayErr("ReadDataSource " + typ)
		}
		return unpacked(rec.Values["state"], cfg.Type()), rec.Diags, nil
	}
	v, diags, err := r.inner.ReadDataSource(ctx, typ, cfg)
	if err == nil {
		r.save(key, &record{Method: "ReadDataSource", Request: req, Values: map[string][]byte{"state": packed(v)}, Diags: diags})
	}
	return v, diags, err
}

func (r *Recorder) OpenEphemeral(ctx context.Context, typ string, cfg tftypes.Value) (*EphemeralResult, Diagnostics, error) {
	key, req := r.key("OpenEphemeral", reqOf(map[string]any{"type": typ}, cfg))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return nil, nil, r.replayErr("OpenEphemeral " + typ)
		}
		return &EphemeralResult{Result: unpacked(rec.Values["result"], cfg.Type()), Private: rec.Bytes["private"]}, rec.Diags, nil
	}
	res, diags, err := r.inner.OpenEphemeral(ctx, typ, cfg)
	if err == nil && res != nil {
		r.save(key, &record{Method: "OpenEphemeral", Request: req, Values: map[string][]byte{"result": packed(res.Result)}, Bytes: map[string][]byte{"private": res.Private}, Diags: diags})
	}
	return res, diags, err
}

func (r *Recorder) ListResource(ctx context.Context, typ string, cfg tftypes.Value, limit int64) ([]ListedResource, Diagnostics, error) {
	key, req := r.key("ListResource", reqOf(map[string]any{"type": typ, "limit": limit}, cfg))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return nil, nil, r.replayErr("ListResource " + typ)
		}
		var out []ListedResource
		if len(rec.Extra) > 0 {
			if err := json.Unmarshal(rec.Extra, &out); err != nil {
				return nil, nil, err
			}
		}
		for i := range out {
			// JSON has one kind of number: a whole one is an integer, as MessagePack gives it.
			if id, ok := integral(out[i].Identity).(map[string]any); ok {
				out[i].Identity = id
			}
		}
		return out, rec.Diags, nil
	}
	out, diags, err := r.inner.ListResource(ctx, typ, cfg, limit)
	if err == nil {
		extra, _ := json.Marshal(out)
		r.save(key, &record{Method: "ListResource", Request: req, Diags: diags, Extra: extra})
	}
	return out, diags, err
}

func (r *Recorder) RenewEphemeral(ctx context.Context, typ string, private []byte) (*EphemeralResult, Diagnostics, error) {
	if r.replay {
		return &EphemeralResult{Private: private}, nil, nil
	}
	return r.inner.RenewEphemeral(ctx, typ, private)
}

func (r *Recorder) CloseEphemeral(ctx context.Context, typ string, private []byte) (Diagnostics, error) {
	if r.replay {
		return nil, nil
	}
	return r.inner.CloseEphemeral(ctx, typ, private)
}

func (r *Recorder) CallFunction(ctx context.Context, name string, args []tftypes.Value, argTypes []tftypes.Type, ret tftypes.Type) (tftypes.Value, *FunctionError, error) {
	key, req := r.key("CallFunction", reqOf(map[string]any{"name": name}, args...))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return tftypes.Value{}, nil, r.replayErr("CallFunction " + name)
		}
		if rec.Error != "" {
			return tftypes.Value{}, &FunctionError{Text: rec.Error}, nil
		}
		return unpacked(rec.Values["result"], ret), nil, nil
	}
	v, ferr, err := r.inner.CallFunction(ctx, name, args, argTypes, ret)
	if err == nil {
		rec := &record{Method: "CallFunction", Request: req, Values: map[string][]byte{"result": packed(v)}}
		if ferr != nil {
			rec.Error = ferr.Text
		}
		r.save(key, rec)
	}
	return v, ferr, err
}

// integral turns the numbers of a decoded JSON value that have no fraction into int64.
func integral(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = integral(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = integral(e)
		}
		return out
	}
	return v
}

// Wrap applies PRAVO_RECORD / PRAVO_REPLAY to a freshly started provider
// (or, in replay mode, to no provider at all). name selects the
// subdirectory, typically the provider's local name.
func Wrap(p Provider, name string) (Provider, error) {
	var out Provider
	switch {
	case os.Getenv("PRAVO_REPLAY") != "":
		if p != nil {
			p.Close()
		}
		r, err := NewReplayer(filepath.Join(os.Getenv("PRAVO_REPLAY"), name))
		if err != nil {
			return nil, err
		}
		out = r
	case os.Getenv("PRAVO_RECORD") != "":
		r, err := NewRecorder(p, filepath.Join(os.Getenv("PRAVO_RECORD"), name))
		if err != nil {
			return nil, err
		}
		out = r
	default:
		out = p
	}
	if telemetry.Enabled() {
		out = NewTraced(out, name)
	}
	return out, nil
}

// Replaying reports whether PRAVO_REPLAY is set, in which case no provider
// binary is needed or started.
func Replaying() bool { return os.Getenv("PRAVO_REPLAY") != "" }
