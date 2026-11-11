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
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/env"
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

// unpacked reads a recorded value. A record that holds none stands for a null of the type;
// bytes that do not decode are an error, so a replay of a corrupt recording fails where a
// saved plan does, instead of going on with a null.
func unpacked(b []byte, t tftypes.Type) (tftypes.Value, error) {
	if len(b) == 0 {
		return tftypes.NewValue(t, nil), nil
	}
	v, err := convert.Unpack(b, t)
	if err != nil {
		return tftypes.Value{}, fmt.Errorf("recorded value: %w", err)
	}
	return v, nil
}

// put saves a record with the values it holds. A value that cannot be packed makes no record,
// so that a replay says there is none instead of replaying a gap.
func (r *Recorder) put(key string, rec *record, values map[string]tftypes.Value) {
	rec.Values = map[string][]byte{}
	for k, v := range values {
		b, err := convert.Pack(v)
		if err != nil {
			return
		}
		rec.Values[k] = b
	}
	r.save(key, rec)
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
		b, _ := convert.Pack(v) // a value that does not pack leaves the key without it: a replay then finds no recording
		out[fmt.Sprintf("v%d", i)] = b
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
		v, err := unpacked(rec.Values["state"], want)
		return v, rec.Diags, err
	}
	v, diags, err := r.inner.UpgradeResourceState(ctx, typ, version, jsonState, want)
	if err == nil {
		r.put(key, &record{Method: "UpgradeResourceState", Request: req, Diags: diags}, map[string]tftypes.Value{"state": v})
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
		v, err := unpacked(rec.Values["state"], state.Type())
		return v, rec.Bytes["private"], rec.Diags, err
	}
	v, p, diags, err := r.inner.ReadResource(ctx, typ, state, private)
	if err == nil {
		r.put(key, &record{Method: "ReadResource", Request: req, Bytes: map[string][]byte{"private": p}, Diags: diags}, map[string]tftypes.Value{"state": v})
	}
	return v, p, diags, err
}

// Each result that a provider returns has one pair of functions here: one that turns it
// into the parts of a record and one that turns a record back into it. A field of a result
// that a pair leaves out is lost in replay, so the pair is the only place that lists the
// fields, and TestEveryResultFieldSurvivesARecording fails when a field is added to a result
// and not to its pair.

// planExtra holds the JSON part of the record of a plan or an apply.
type planExtra struct {
	// RequiresReplace holds the first attribute name of each path, as recordings made
	// before the paths were kept hold them.
	RequiresReplace []string `json:"requires_replace,omitempty"`
	// Paths holds the whole of each path that requires replacement.
	Paths  [][]recordedStep `json:"requires_replace_paths,omitempty"`
	Legacy bool             `json:"legacy,omitempty"`
}

// recordedStep is one step of an attribute path: an attribute name, a string key or an index.
type recordedStep struct {
	Name  *string `json:"name,omitempty"`
	Key   *string `json:"key,omitempty"`
	Index *int64  `json:"index,omitempty"`
}

func recordedPath(p *tftypes.AttributePath) []recordedStep {
	var out []recordedStep
	for _, st := range p.Steps() {
		switch s := st.(type) {
		case tftypes.AttributeName:
			n := string(s)
			out = append(out, recordedStep{Name: &n})
		case tftypes.ElementKeyString:
			k := string(s)
			out = append(out, recordedStep{Key: &k})
		case tftypes.ElementKeyInt:
			i := int64(s)
			out = append(out, recordedStep{Index: &i})
		}
	}
	return out
}

func (rs recordedStep) step() tftypes.AttributePathStep {
	switch {
	case rs.Name != nil:
		return tftypes.AttributeName(*rs.Name)
	case rs.Key != nil:
		return tftypes.ElementKeyString(*rs.Key)
	default:
		return tftypes.ElementKeyInt(*rs.Index)
	}
}

// recordPlan turns a plan result into the parts of its record.
func recordPlan(res *PlanResult) (values map[string]tftypes.Value, bytes map[string][]byte, extra []byte) {
	e := planExtra{Legacy: res.LegacyTypeSystem}
	for _, p := range res.RequiresReplace {
		if path := recordedPath(p); len(path) > 0 {
			e.Paths = append(e.Paths, path)
		}
	}
	extra, _ = json.Marshal(e)
	return map[string]tftypes.Value{"planned": res.PlannedState}, map[string][]byte{"private": res.PlannedPrivate}, extra
}

// planOf is the inverse of recordPlan; t is the type of the planned state.
func planOf(rec *record, t tftypes.Type) (*PlanResult, error) {
	var e planExtra
	_ = json.Unmarshal(rec.Extra, &e)
	planned, err := unpacked(rec.Values["planned"], t)
	if err != nil {
		return nil, err
	}
	res := &PlanResult{PlannedState: planned, PlannedPrivate: rec.Bytes["private"], LegacyTypeSystem: e.Legacy}
	if len(e.Paths) > 0 {
		for _, steps := range e.Paths {
			path := make([]tftypes.AttributePathStep, len(steps))
			for i, s := range steps {
				path[i] = s.step()
			}
			res.RequiresReplace = append(res.RequiresReplace, tftypes.NewAttributePathWithSteps(path))
		}
		return res, nil
	}
	for _, n := range e.RequiresReplace {
		res.RequiresReplace = append(res.RequiresReplace, tftypes.NewAttributePath().WithAttributeName(n))
	}
	return res, nil
}

func recordApply(res *ApplyResult) (map[string]tftypes.Value, map[string][]byte, []byte) {
	extra, _ := json.Marshal(planExtra{Legacy: res.LegacyTypeSystem})
	return map[string]tftypes.Value{"state": res.NewState}, map[string][]byte{"private": res.Private}, extra
}

func applyOf(rec *record, t tftypes.Type) (*ApplyResult, error) {
	var e planExtra
	_ = json.Unmarshal(rec.Extra, &e)
	newState, err := unpacked(rec.Values["state"], t)
	if err != nil {
		return nil, err
	}
	return &ApplyResult{NewState: newState, Private: rec.Bytes["private"], LegacyTypeSystem: e.Legacy}, nil
}

// ephemeralExtra holds the JSON part of the record of an ephemeral resource.
type ephemeralExtra struct {
	RenewAt  *time.Time `json:"renew_at,omitempty"`
	Deferred bool       `json:"deferred,omitempty"`
}

func recordEphemeral(res *EphemeralResult) (map[string]tftypes.Value, map[string][]byte, []byte) {
	extra, _ := json.Marshal(ephemeralExtra{RenewAt: res.RenewAt, Deferred: res.Deferred})
	return map[string]tftypes.Value{"result": res.Result}, map[string][]byte{"private": res.Private}, extra
}

func ephemeralOf(rec *record, t tftypes.Type) (*EphemeralResult, error) {
	var e ephemeralExtra
	_ = json.Unmarshal(rec.Extra, &e)
	result, err := unpacked(rec.Values["result"], t)
	if err != nil {
		return nil, err
	}
	return &EphemeralResult{Result: result, Private: rec.Bytes["private"], RenewAt: e.RenewAt, Deferred: e.Deferred}, nil
}

// putResult saves a record of the parts that a record function gave.
func (r *Recorder) putResult(key string, method string, req json.RawMessage, diags Diagnostics, values map[string]tftypes.Value, bytes map[string][]byte, extra []byte) {
	r.put(key, &record{Method: method, Request: req, Bytes: bytes, Diags: diags, Extra: extra}, values)
}

func (r *Recorder) PlanResourceChange(ctx context.Context, typ string, prior, proposed, config tftypes.Value, priorPrivate []byte) (*PlanResult, Diagnostics, error) {
	key, req := r.key("PlanResourceChange", reqOf(map[string]any{"type": typ, "private": priorPrivate}, prior, proposed, config))
	if r.replay {
		rec, err := r.load(key)
		if err != nil {
			return nil, nil, r.replayErr("PlanResourceChange " + typ)
		}
		res, err := planOf(rec, prior.Type())
		if err != nil {
			return nil, nil, err
		}
		return res, rec.Diags, nil
	}
	res, diags, err := r.inner.PlanResourceChange(ctx, typ, prior, proposed, config, priorPrivate)
	if err == nil && res != nil {
		v, b, x := recordPlan(res)
		r.putResult(key, "PlanResourceChange", req, diags, v, b, x)
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
		res, err := applyOf(rec, prior.Type())
		if err != nil {
			return nil, nil, err
		}
		return res, rec.Diags, nil
	}
	res, diags, err := r.inner.ApplyResourceChange(ctx, typ, prior, planned, config, plannedPrivate)
	if err == nil && res != nil {
		v, b, x := recordApply(res)
		r.putResult(key, "ApplyResourceChange", req, diags, v, b, x)
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
		v, err := unpacked(rec.Values["state"], cfg.Type())
		return v, rec.Diags, err
	}
	v, diags, err := r.inner.ReadDataSource(ctx, typ, cfg)
	if err == nil {
		r.put(key, &record{Method: "ReadDataSource", Request: req, Diags: diags}, map[string]tftypes.Value{"state": v})
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
		res, err := ephemeralOf(rec, cfg.Type())
		if err != nil {
			return nil, nil, err
		}
		return res, rec.Diags, nil
	}
	res, diags, err := r.inner.OpenEphemeral(ctx, typ, cfg)
	if err == nil && res != nil {
		v, b, x := recordEphemeral(res)
		r.putResult(key, "OpenEphemeral", req, diags, v, b, x)
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
		v, err := unpacked(rec.Values["result"], ret)
		return v, nil, err
	}
	v, ferr, err := r.inner.CallFunction(ctx, name, args, argTypes, ret)
	if err == nil {
		rec := &record{Method: "CallFunction", Request: req}
		if ferr != nil {
			rec.Error = ferr.Text
		}
		r.put(key, rec, map[string]tftypes.Value{"result": v})
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

// Recording says whether providers are recorded or replayed, and where. The
// command line fills it from PRAVO_RECORD and PRAVO_REPLAY.
type Recording struct {
	// Record is a directory to record the calls of every provider in, "" for none.
	Record string
	// Replay is a directory to replay the calls of every provider from, "" for
	// none. With it no provider binary is needed or started.
	Replay string
}

// RecordingFrom reads PRAVO_RECORD and PRAVO_REPLAY from the environment.
func RecordingFrom(e env.Env) Recording {
	return Recording{Record: e.Get("PRAVO_RECORD"), Replay: e.Get("PRAVO_REPLAY")}
}

// Replaying reports whether the providers are replayed, in which case no
// provider binary is needed or started.
func (r Recording) Replaying() bool { return r.Replay != "" }

// Wrap applies the recording to a freshly started provider (or, in replay mode,
// to no provider at all). name selects the subdirectory, typically the
// provider's local name.
func Wrap(p Provider, name string, rec Recording) (Provider, error) {
	var out Provider
	switch {
	case rec.Replay != "":
		if p != nil {
			p.Close()
		}
		r, err := NewReplayer(filepath.Join(rec.Replay, name))
		if err != nil {
			return nil, err
		}
		out = r
	case rec.Record != "":
		r, err := NewRecorder(p, filepath.Join(rec.Record, name))
		if err != nil {
			return nil, err
		}
		out = r
	default:
		out = p
	}
	if telemetry.Active() {
		out = NewTraced(out, name)
	}
	return out, nil
}
