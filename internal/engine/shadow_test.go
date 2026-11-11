package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// shadowRecord is a record of a converted Terraform state: a fake_thing.
func shadowRecord(name, id, content, pin string, deps ...string) ShadowInput {
	attrs := map[string]any{"id": id, "name": name, "content": content, "pin": nil, "secret_wo": nil, "secret_wo_version": nil}
	if pin != "" {
		attrs["pin"] = pin
	}
	b, _ := json.Marshal(attrs)
	return ShadowInput{
		Record:  &state.Resource{Mode: "managed", Type: "fake_thing", Name: name, Provider: "example.test/x/fake", ProviderKey: "fake", Attributes: b, Dependencies: deps},
		Name:    name,
		Address: "fake_thing." + name,
	}
}

func shadowOf(t *testing.T, h *harness, opts ShadowOptions, in ...ShadowInput) *ShadowResult {
	t.Helper()
	e := h.engine(spec{cue: ""}, Options{})
	res, err := e.Shadow(context.Background(), in, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestShadowWritesReferencesAndKeepsSecretsOut(t *testing.T) {
	h := newHarness(t)
	res := shadowOf(t, h, ShadowOptions{Refresh: true, Repair: true},
		shadowRecord("base", "id-1", "hello", "hunter2"),
		shadowRecord("user", "id-2", "id-1", "", "fake_thing.base"),
	)
	src := res.Source
	if strings.Contains(src, "hunter2") {
		t.Errorf("a sensitive value reached the source:\n%s", src)
	}
	if !strings.Contains(src, "content: base.id") {
		t.Errorf("the value that names the dependency is a reference:\n%s", src)
	}
	if strings.Contains(src, "id:") && strings.Contains(src, `"id-1"`) && !strings.Contains(src, "base.id") {
		t.Errorf("the computed id is written:\n%s", src)
	}
	r := res.Report
	if r.Resources != 2 || r.Edges.State != 1 || r.Edges.Reference != 1 || r.Edges.Lost != 0 {
		t.Errorf("the dependency is counted once, as a reference: %+v", r.Edges)
	}
	if r.FinalClean != 2 || r.FinalInvalid != 0 || r.FinalChanges != 0 {
		t.Errorf("the provider is asked about every resource and plans nothing: %+v", r)
	}
	if len(res.Records) != 2 || res.Records[1].Dependencies[0] != res.Records[0].Addr {
		t.Errorf("a record names the address of its dependency in the specification: %+v", res.Records)
	}
}

func TestShadowGuardRefusesChangesUntilTheHandOver(t *testing.T) {
	h := newHarness(t)
	one := func(content string) spec {
		return spec{cue: "a: " + thing("a", "content: \""+content+"\"\n"), resources: []string{"a"}}
	}
	h.apply(one("v1"))
	lock := NewShadowLock(ShadowSource{Place: "-"}, []byte(`{"serial": 3, "lineage": "l"}`))
	if err := lock.Write(h.dir); err != nil {
		t.Fatal(err)
	}
	run := func(content string) (*ApplyResult, error) {
		e := h.engine(one(content), Options{})
		plan, err := e.Plan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return e.Apply(context.Background(), plan)
	}
	// Nothing changes: the apply is allowed.
	if _, err := run("v1"); err != nil {
		t.Fatalf("an apply of nothing was refused: %v", err)
	}
	_, err := run("v2")
	var coded *CodedError
	if err == nil || !errors.As(err, &coded) || coded.Code != ExitRefused || !strings.Contains(err.Error(), "Terraform still owns its objects") {
		t.Fatalf("an apply that changes an object = %v, want a refusal", err)
	}
	if got := h.state.Get("a"); !strings.Contains(string(got.Attributes), "v1") {
		t.Errorf("the refused apply changed the record: %s", got.Attributes)
	}
	lock.Owner = "pravo"
	if err := lock.Write(h.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := run("v2"); err != nil {
		t.Fatalf("an apply after the hand-over: %v", err)
	}
}

func TestShadowLockNoticesAStateThatMoved(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "terraform.tfstate")
	write := func(serial int) []byte {
		b := []byte(fmt.Sprintf(`{"version": 4, "serial": %d, "lineage": "l", "resources": []}`, serial))
		if err := os.WriteFile(file, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return b
	}
	lock := NewShadowLock(ShadowSource{Place: file}, write(3))
	if m := lock.Moved(context.Background()); m != "" {
		t.Errorf("the same file moved: %s", m)
	}
	write(4)
	if m := lock.Moved(context.Background()); !strings.Contains(m, "serial 3 then, 4 now") {
		t.Errorf("a new serial is not noticed: %q", m)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if m := lock.Moved(context.Background()); m != "" {
		t.Errorf("a state that cannot be read from here is not a move: %q", m)
	}
	if got := NewShadowLock(ShadowSource{Place: "-"}, []byte(`{}`)); got.Source != "" || got.Moved(context.Background()) != "" {
		t.Errorf("a state from standard input has no place: %+v", got)
	}
	s3 := NewShadowLock(ShadowSource{Place: "s3://b/k", Region: "eu-north-1", Endpoint: "http://x"}, []byte(`{}`))
	if got := s3.SourceOf(); got.Place != "s3://b/k" || got.Region != "eu-north-1" || got.Endpoint != "http://x" {
		t.Errorf("an s3 source is read again with what it was read with: %+v", got)
	}
}

func TestShadowSource(t *testing.T) {
	ctx := context.Background()
	if got, err := (ShadowSource{Place: "-", Stdin: strings.NewReader("{}")}).Read(ctx); err != nil || string(got) != "{}" {
		t.Errorf("standard input: %q, %v", got, err)
	}
	if _, err := (ShadowSource{Place: "-"}).Read(ctx); err == nil {
		t.Error("no standard input was not an error")
	}
	for _, bad := range []string{"s3://", "s3://bucket", "s3://bucket/"} {
		if _, err := (ShadowSource{Place: bad}).Read(ctx); err == nil || !strings.Contains(err.Error(), "s3://bucket/key") {
			t.Errorf("%q = %v, want the form named", bad, err)
		}
	}
	if n := (ShadowSource{Place: "/a/b/terraform.tfstate"}).Name(); n != "terraform.tfstate" {
		t.Errorf("name = %q", n)
	}
	if n := (ShadowSource{Place: "s3://b/k/x.tfstate"}).Name(); n != "s3://b/k/x.tfstate" {
		t.Errorf("name = %q", n)
	}
}
