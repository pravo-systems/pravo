package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/convert"
)

// recordedProvider is a provider that answers every call with a fixed value.
// It embeds the interface, so a call that a test does not expect panics.
type recordedProvider struct {
	Provider
}

var thingType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
	"id":       tftypes.String,
	"name":     tftypes.String,
	"password": tftypes.String,
}}

func thingValue(id, name, password string) tftypes.Value {
	return tftypes.NewValue(thingType, map[string]tftypes.Value{
		"id":       tftypes.NewValue(tftypes.String, id),
		"name":     tftypes.NewValue(tftypes.String, name),
		"password": tftypes.NewValue(tftypes.String, password),
	})
}

var providerConfigType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
	"token": tftypes.String,
}}

func providerConfig(token string) tftypes.Value {
	return tftypes.NewValue(providerConfigType, map[string]tftypes.Value{
		"token": tftypes.NewValue(tftypes.String, token),
	})
}

func (recordedProvider) ProtocolVersion() int { return 6 }
func (recordedProvider) Configure(context.Context, tftypes.Value) (Diagnostics, error) {
	return nil, nil
}
func (recordedProvider) ValidateProviderConfig(context.Context, tftypes.Value) (Diagnostics, error) {
	return nil, nil
}
func (recordedProvider) ValidateResourceConfig(context.Context, string, tftypes.Value) (Diagnostics, error) {
	return nil, nil
}
func (recordedProvider) UpgradeResourceState(_ context.Context, _ string, _ int64, js []byte, want tftypes.Type) (tftypes.Value, Diagnostics, error) {
	v, err := tftypes.ValueFromJSON(js, want)
	return v, nil, err
}

// withoutPassword is the state a provider returns for a configuration: the
// write-only attribute is null in it.
func withoutPassword(v tftypes.Value) tftypes.Value {
	var m map[string]tftypes.Value
	if err := v.As(&m); err != nil {
		return v
	}
	out := make(map[string]tftypes.Value, len(m))
	for k, x := range m {
		out[k] = x
	}
	out["password"] = tftypes.NewValue(tftypes.String, nil)
	return tftypes.NewValue(v.Type(), out)
}

func (recordedProvider) PlanResourceChange(_ context.Context, _ string, _, proposed, _ tftypes.Value, _ []byte) (*PlanResult, Diagnostics, error) {
	return &PlanResult{PlannedState: withoutPassword(proposed)}, nil, nil
}
func (recordedProvider) ApplyResourceChange(_ context.Context, _ string, _, planned, _ tftypes.Value, _ []byte) (*ApplyResult, Diagnostics, error) {
	return &ApplyResult{NewState: withoutPassword(planned), Private: []byte("private-token")}, nil, nil
}
func (recordedProvider) ReadResource(_ context.Context, _ string, state tftypes.Value, private []byte) (tftypes.Value, []byte, Diagnostics, error) {
	return state, private, nil, nil
}
func (recordedProvider) Close() {}

const (
	secretToken    = "tok-0123456789-credential"
	secretWriteOnl = "wo-9876543210-secret"
	secretPrior    = "prior-5555-secret"
)

// exercise makes the calls that carry request values and returns the answers
// that a replay has to give again.
func exercise(t *testing.T, p Provider) (planned, applied tftypes.Value) {
	t.Helper()
	ctx := context.Background()
	if _, err := p.ValidateProviderConfig(ctx, providerConfig(secretToken)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Configure(ctx, providerConfig(secretToken)); err != nil {
		t.Fatal(err)
	}
	prior := thingValue("id-1", "web", secretPrior)
	config := thingValue("", "web", secretWriteOnl)
	if _, err := p.ValidateResourceConfig(ctx, "thing", config); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.UpgradeResourceState(ctx, "thing", 0, []byte(`{"id":"id-1","name":"web","password":"`+secretPrior+`"}`), thingType); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := p.ReadResource(ctx, "thing", prior, []byte("private-read")); err != nil {
		t.Fatal(err)
	}
	res, _, err := p.PlanResourceChange(ctx, "thing", prior, config, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	app, _, err := p.ApplyResourceChange(ctx, "thing", prior, config, config, []byte("private-plan"))
	if err != nil {
		t.Fatal(err)
	}
	return res.PlannedState, app.NewState
}

// walk lists every file and directory below dir, with its mode.
func walk(t *testing.T, dir string) (dirs, files map[string]fs.FileMode) {
	t.Helper()
	dirs, files = map[string]fs.FileMode{}, map[string]fs.FileMode{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs[path] = info.Mode().Perm()
		} else {
			files[path] = info.Mode().Perm()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return dirs, files
}

// A recording is as sensitive as state: only its owner reads it.
func TestRecordingIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	root := filepath.Join(t.TempDir(), "rec")
	dir := filepath.Join(root, "thing")
	r, err := NewRecorder(recordedProvider{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, r)
	dirs, files := walk(t, root)
	if len(files) < 5 {
		t.Fatalf("recorded %d files, want at least 5", len(files))
	}
	for d, mode := range dirs {
		if mode != 0o700 {
			t.Errorf("directory %s has mode %v, want 0700", d, mode)
		}
	}
	for f, mode := range files {
		if mode != 0o600 {
			t.Errorf("file %s has mode %v, want 0600", f, mode)
		}
		if strings.HasPrefix(filepath.Base(f), ".record-") {
			t.Errorf("temporary file %s was left behind", f)
		}
	}
}

// A recording made into a directory and over files that exist from before is
// private too. The directory above the recording is not touched.
func TestRecordingTightensWhatItOwns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	root := filepath.Join(t.TempDir(), "rec")
	dir := filepath.Join(root, "thing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := NewRecorder(recordedProvider{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, r)
	// Record again over the files that exist, which a loose mode would keep.
	_, files := walk(t, dir)
	for f := range files {
		if err := os.Chmod(f, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exercise(t, r)
	if st, _ := os.Stat(root); st.Mode().Perm() != 0o755 {
		t.Errorf("the directory above the recording has mode %v, want it left at 0755", st.Mode().Perm())
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("the recording directory has mode %v, want 0700", st.Mode().Perm())
	}
	_, files = walk(t, dir)
	for f, mode := range files {
		if mode != 0o600 {
			t.Errorf("file %s has mode %v after a second recording, want 0600", f, mode)
		}
	}
}

// The values a provider received are not in the recording: not the provider
// configuration with its credentials, not a write-only attribute, not the
// state that came with a request.
func TestRecordingKeepsNoRequestValues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec", "thing")
	r, err := NewRecorder(recordedProvider{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, r)
	_, files := walk(t, dir)
	sawConfigure := false
	for f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var rec struct {
			Method  string                     `json:"method"`
			Request map[string]json.RawMessage `json:"request"`
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for k := range rec.Request {
			if k != "name" && k != "type" && k != "version" {
				t.Errorf("%s: the recorded request has the field %q, which may hold a value", filepath.Base(f), k)
			}
		}
		if rec.Method == "Configure" || rec.Method == "ValidateProviderConfig" {
			sawConfigure = true
		}
		for _, secret := range []string{secretToken, secretWriteOnl} {
			packedConfig, _ := convert.Pack(providerConfig(secret))
			if strings.Contains(string(data), secret) || strings.Contains(string(data), base64.StdEncoding.EncodeToString(packedConfig)) {
				t.Errorf("%s holds the value %q that a request carried", filepath.Base(f), secret)
			}
			packedThing, _ := convert.Pack(thingValue("", "web", secret))
			if strings.Contains(string(data), base64.StdEncoding.EncodeToString(packedThing)) {
				t.Errorf("%s holds the request that carried %q", filepath.Base(f), secret)
			}
		}
		if strings.Contains(string(data), "private-read") || strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte("private-plan"))) {
			t.Errorf("%s holds private data of a request", filepath.Base(f))
		}
	}
	if !sawConfigure {
		t.Error("the provider configuration calls were not recorded")
	}
}

// What is recorded replays: the responses come back for the same requests.
func TestRecordingReplays(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec", "thing")
	r, err := NewRecorder(recordedProvider{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	planned, applied := exercise(t, r)

	rp, err := NewReplayer(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotPlanned, gotApplied := exercise(t, rp)
	if !gotPlanned.Equal(planned) {
		t.Errorf("replayed plan = %s, want %s", gotPlanned, planned)
	}
	if !gotApplied.Equal(applied) {
		t.Errorf("replayed apply = %s, want %s", gotApplied, applied)
	}
	// A request that was not recorded is refused.
	_, _, err = rp.PlanResourceChange(context.Background(), "thing", thingValue("", "other", "x"), thingValue("", "other", "x"), thingValue("", "other", "x"), nil)
	if err == nil || !strings.Contains(err.Error(), ErrNoRecording.Error()) {
		t.Errorf("an unrecorded request: error = %v", err)
	}
}

// A recording that holds the full requests, as an earlier recording does,
// still replays: replay reads the response and never the request.
func TestReplayIgnoresTheRecordedRequest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec", "thing")
	r, err := NewRecorder(recordedProvider{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	planned, _ := exercise(t, r)
	_, files := walk(t, dir)
	for f := range files {
		data, _ := os.ReadFile(f)
		var rec map[string]any
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		rec["request"] = map[string]any{"v0": "AAAA", "state": "full", "private": "AAAA", "type": "thing"}
		out, _ := json.Marshal(rec)
		if err := os.WriteFile(f, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rp, err := NewReplayer(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := exercise(t, rp)
	if !got.Equal(planned) {
		t.Errorf("replayed plan = %s, want %s", got, planned)
	}
}

// legacyProvider answers as a provider of the legacy SDK does.
type legacyProvider struct{ recordedProvider }

func (legacyProvider) ApplyResourceChange(ctx context.Context, typ string, prior, planned, config tftypes.Value, private []byte) (*ApplyResult, Diagnostics, error) {
	res, d, err := recordedProvider{}.ApplyResourceChange(ctx, typ, prior, planned, config, private)
	if res != nil {
		res.LegacyTypeSystem = true
	}
	return res, d, err
}

// A replayed apply says, as the live one did, that the provider is of the
// legacy SDK, so the engine skips the same check.
func TestRecordingReplaysLegacyApply(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec", "thing")
	r, err := NewRecorder(legacyProvider{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, r)
	rp, err := NewReplayer(dir)
	if err != nil {
		t.Fatal(err)
	}
	config := thingValue("", "web", secretWriteOnl)
	app, _, err := rp.ApplyResourceChange(context.Background(), "thing", thingValue("id-1", "web", secretPrior), config, config, []byte("private-plan"))
	if err != nil {
		t.Fatal(err)
	}
	if !app.LegacyTypeSystem {
		t.Error("a replayed apply lost the legacy type system flag")
	}
}

// TestUnpackedFailsOnACorruptRecording: a recorded value that does not decode is an error, as it
// is for a saved plan, and not a null that the replay goes on with.
func TestUnpackedFailsOnACorruptRecording(t *testing.T) {
	obj := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String}}
	if v, err := unpacked(nil, obj); err != nil || !v.IsNull() {
		t.Errorf("an empty record = %v, %v; want a null of the type", v, err)
	}
	if _, err := unpacked([]byte("\xff\xfe not msgpack"), obj); err == nil {
		t.Error("a corrupt record unpacked without an error")
	}
	b, err := convert.Pack(tftypes.NewValue(obj, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, "x")}))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := unpacked(b, obj); err != nil || v.IsNull() {
		t.Errorf("a good record = %v, %v", v, err)
	}
}

// allSet fails the test for a field of a result that a fixture leaves at its zero value, so
// that a field added to a result has to be added to the fixture, and so to the record.
func allSet(t *testing.T, name string, v any) {
	t.Helper()
	rv := reflect.ValueOf(v).Elem()
	for i := range rv.NumField() {
		if rv.Field(i).IsZero() {
			t.Errorf("%s.%s is zero in the fixture: set it, and record and replay it", name, rv.Type().Field(i).Name)
		}
	}
}

// fieldsProvider returns results with every field set.
type fieldsProvider struct{ recordedProvider }

var renewAt = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func (fieldsProvider) PlanResourceChange(context.Context, string, tftypes.Value, tftypes.Value, tftypes.Value, []byte) (*PlanResult, Diagnostics, error) {
	return &PlanResult{
		PlannedState: thingValue("id-1", "web", ""),
		RequiresReplace: []*tftypes.AttributePath{
			tftypes.NewAttributePath().WithAttributeName("ebs").WithElementKeyInt(0).WithAttributeName("size"),
			tftypes.NewAttributePath().WithAttributeName("tags").WithElementKeyString("env"),
		},
		PlannedPrivate:   []byte("plan-private"),
		LegacyTypeSystem: true,
	}, nil, nil
}

func (fieldsProvider) ApplyResourceChange(context.Context, string, tftypes.Value, tftypes.Value, tftypes.Value, []byte) (*ApplyResult, Diagnostics, error) {
	return &ApplyResult{NewState: thingValue("id-1", "web", ""), Private: []byte("apply-private"), LegacyTypeSystem: true}, nil, nil
}

func (fieldsProvider) OpenEphemeral(context.Context, string, tftypes.Value) (*EphemeralResult, Diagnostics, error) {
	at := renewAt
	return &EphemeralResult{Result: thingValue("id-1", "web", ""), Private: []byte("eph-private"), RenewAt: &at, Deferred: true}, nil, nil
}

// TestEveryResultFieldSurvivesARecording records results with every field set and replays
// them: a field that a record leaves out comes back empty, as the legacy flag of an apply and
// the deferral of an ephemeral resource once did.
func TestEveryResultFieldSurvivesARecording(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "rec", "thing")
	r, err := NewRecorder(fieldsProvider{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := thingValue("", "web", "x")
	plan, _, _ := r.PlanResourceChange(ctx, "thing", cfg, cfg, cfg, nil)
	app, _, _ := r.ApplyResourceChange(ctx, "thing", cfg, cfg, cfg, nil)
	eph, _, _ := r.OpenEphemeral(ctx, "thing", cfg)
	allSet(t, "PlanResult", plan)
	allSet(t, "ApplyResult", app)
	allSet(t, "EphemeralResult", eph)

	rp, err := NewReplayer(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotPlan, _, err := rp.PlanResourceChange(ctx, "thing", cfg, cfg, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotApp, _, err := rp.ApplyResourceChange(ctx, "thing", cfg, cfg, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotEph, _, err := rp.OpenEphemeral(ctx, "thing", cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]any{"plan": {plan, gotPlan}, "apply": {app, gotApp}, "ephemeral": {eph, gotEph}} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("a replayed %s differs from the recorded one:\n live   %+v\n replay %+v", name, pair[0], pair[1])
		}
	}
}

// A recording made before the paths were kept holds the first attribute name of each path;
// it still replays, as that name.
func TestReplayReadsTheReplacePathsOfAnOldRecording(t *testing.T) {
	rec := &record{Values: map[string][]byte{}, Extra: []byte(`{"requires_replace":["ami"]}`)}
	res, err := planOf(rec, thingType)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RequiresReplace) != 1 || res.RequiresReplace[0].String() != `AttributeName("ami")` {
		t.Errorf("paths = %v", res.RequiresReplace)
	}
}
