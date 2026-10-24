package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pravo.state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestListedOf(t *testing.T) {
	l := ListedOf(map[string]any{"id": "i-1", "region": "eu", "account_id": int64(7)}, "web")
	if l.ID != "i-1" || l.ImportID != "i-1" || l.Name != "web" || l.Key != "account_id=7,id=i-1,region=eu" {
		t.Errorf("%+v", l)
	}
	one := ListedOf(map[string]any{"bucket": "logs"}, "")
	if one.ID != "logs" || one.ImportID != "logs" {
		t.Errorf("a one-attribute identity is the id: %+v", one)
	}
	many := ListedOf(map[string]any{"bucket": "logs", "region": "eu"}, "")
	if many.ID != "bucket=logs,region=eu" || many.ImportID != "" {
		t.Errorf("an identity of several attributes without id has no import id: %+v", many)
	}
	nested := ListedOf(map[string]any{"tags": []any{"a", int64(2)}}, "")
	if nested.ImportID != "" || nested.Key != `tags=["a",2]` {
		t.Errorf("%+v", nested)
	}
	if empty := ListedOf(nil, "n"); empty.Key != "" || empty.ID != "" || empty.ImportID != "" {
		t.Errorf("%+v", empty)
	}
}

func TestDiscoveryRunRoundTrip(t *testing.T) {
	s, dir := openTemp(t)
	if run, err := s.Discovery("vms"); run != nil || err != nil {
		t.Fatalf("a discovery that was not run: %v %v", run, err)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	run := &DiscoveryRun{Name: "web.vms", Type: "aws_instance", Provider: "registry.opentofu.org/hashicorp/aws", ProviderKey: "aws", Digest: "abc",
		Scope: map[string]any{"region": "eu"}, Started: now, Finished: now.Add(time.Second), Actor: "marcin", Status: RunComplete,
		Items: []Listed{ListedOf(map[string]any{"id": "i-1"}, "one")}}
	if err := s.PutDiscovery(run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "discovery", "runs", "web.vms.json")); err != nil {
		t.Fatalf("the object is not where the layout says: %v", err)
	}
	got, err := s.Discovery("web.vms")
	if err != nil || got == nil {
		t.Fatal(got, err)
	}
	if got.Digest != "abc" || got.V != DiscoveryFormat || len(got.Items) != 1 || got.Items[0].ID != "i-1" || !got.Finished.Equal(run.Finished) || got.Scope["region"] != "eu" {
		t.Errorf("%+v", got)
	}
	// The next run replaces it.
	run.Digest = "def"
	run.Items = nil
	if err := s.PutDiscovery(run); err != nil {
		t.Fatal(err)
	}
	all, problems, err := s.Discoveries()
	if err != nil || len(problems) != 0 || len(all) != 1 || all["web.vms"].Digest != "def" || all["web.vms"].Items == nil {
		t.Fatalf("%+v %v %v", all, problems, err)
	}
	if err := s.DeleteDiscovery("web.vms"); err != nil {
		t.Fatal(err)
	}
	if run, _ := s.Discovery("web.vms"); run != nil {
		t.Error("a deleted run is still there")
	}
	if err := s.PutDiscovery(&DiscoveryRun{}); err == nil {
		t.Error("a run without a name was stored")
	}
	big := &DiscoveryRun{Name: "x", Items: make([]Listed, MaxListed+1)}
	if err := s.PutDiscovery(big); err == nil {
		t.Error("a run over the bound was stored")
	}
}

func TestDiscoveriesSkipWhatCannotBeRead(t *testing.T) {
	s, dir := openTemp(t)
	if err := s.PutDiscovery(&DiscoveryRun{Name: "good", Status: RunComplete}); err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(dir, "discovery", "runs")
	os.WriteFile(filepath.Join(runs, "bad.json"), []byte("{nope"), 0o600)
	os.WriteFile(filepath.Join(runs, "moved.json"), []byte(`{"v":1,"name":"elsewhere"}`), 0o600)
	os.WriteFile(filepath.Join(runs, "newer.json"), []byte(`{"v":99,"name":"newer"}`), 0o600)
	os.WriteFile(filepath.Join(runs, "note.txt"), []byte("not json"), 0o600)
	all, problems, err := s.Discoveries()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all["good"] == nil {
		t.Errorf("the objects that can be read: %v", all)
	}
	if len(problems) != 3 {
		t.Errorf("problems: %v", problems)
	}
	for _, p := range problems {
		if !strings.HasPrefix(p.Key, "discovery/runs/") || p.Error() == "" {
			t.Errorf("%v", p)
		}
	}
}

func TestCopyCarriesTheDiscoveryObjects(t *testing.T) {
	src, srcDir := openTemp(t)
	if err := src.PutDiscovery(&DiscoveryRun{Name: "vms", Status: RunComplete, Items: []Listed{ListedOf(map[string]any{"id": "i-1"}, "")}}); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(t.TempDir(), "other.state")
	n, err := Copy(context.Background(), &LocalBackend{Dir: srcDir}, mustLocal(t, dstDir))
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("copied %d objects", n)
	}
	dst, err := Open(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if run, _ := dst.Discovery("vms"); run == nil || len(run.Items) != 1 {
		t.Errorf("the run was not copied: %+v", run)
	}
	// A state that holds only discovery objects is not empty.
	if empty, err := holdsNothing(context.Background(), &LocalBackend{Dir: dstDir}); err != nil || empty {
		t.Errorf("holdsNothing = %v, %v", empty, err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal()
	}
}

func mustLocal(t *testing.T, dir string) *LocalBackend {
	t.Helper()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
