package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
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
			if packed := packed(providerConfig(secret)); strings.Contains(string(data), secret) || strings.Contains(string(data), base64.StdEncoding.EncodeToString(packed)) {
				t.Errorf("%s holds the value %q that a request carried", filepath.Base(f), secret)
			}
			if packed := packed(thingValue("", "web", secret)); strings.Contains(string(data), base64.StdEncoding.EncodeToString(packed)) {
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
