package engine

import (
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

func str(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }
func nullStr() tftypes.Value     { return tftypes.NewValue(tftypes.String, nil) }

func TestProposedNew(t *testing.T) {
	blk := &schema.Block{Attributes: []*schema.Attribute{
		{Name: "id", TypeJSON: json.RawMessage(`"string"`), Computed: true},
		{Name: "name", TypeJSON: json.RawMessage(`"string"`), Required: true},
		{Name: "size", TypeJSON: json.RawMessage(`"number"`), Optional: true, Computed: true},
	}}
	ty := blk.Type()
	prior := tftypes.NewValue(ty, map[string]tftypes.Value{
		"id": str("abc"), "name": str("old"), "size": tftypes.NewValue(tftypes.Number, big.NewFloat(3)),
	})
	cfg := tftypes.NewValue(ty, map[string]tftypes.Value{
		"id": nullStr(), "name": str("new"), "size": tftypes.NewValue(tftypes.Number, nil),
	})
	got := ProposedNew(blk, prior, cfg)
	var m map[string]tftypes.Value
	if err := got.As(&m); err != nil {
		t.Fatal(err)
	}
	if !m["id"].Equal(str("abc")) {
		t.Errorf("computed id should inherit prior, got %s", m["id"])
	}
	if !m["name"].Equal(str("new")) {
		t.Errorf("name should come from config, got %s", m["name"])
	}
	var f big.Float
	if err := m["size"].As(&f); err != nil || f.Cmp(big.NewFloat(3)) != 0 {
		t.Errorf("optional+computed size should inherit prior, got %s", m["size"])
	}
	if !ProposedNew(blk, tftypes.NewValue(ty, nil), cfg).Equal(cfg) {
		t.Error("null prior should yield config")
	}
	ign := applyIgnoreChanges(blk, prior, cfg, []string{"name"})
	_ = ign.As(&m)
	if !m["name"].Equal(str("old")) {
		t.Errorf("ignore_changes should keep prior name, got %s", m["name"])
	}
}

func TestOrderForDestroy(t *testing.T) {
	changes := []*Change{
		{Addr: "a", Action: ActionDelete},
		{Addr: "b", Action: ActionDelete, Deps: []string{"a"}},
		{Addr: "c", Action: ActionDelete, Deps: []string{"b"}},
		{Addr: "d", Action: ActionDelete, Deps: []string{"a"}},
	}
	got := orderForDestroy(changes)
	pos := map[string]int{}
	for i, c := range got {
		pos[c.Addr] = i
	}
	if pos["c"] >= pos["b"] || pos["b"] >= pos["a"] || pos["d"] >= pos["a"] {
		t.Errorf("bad destroy order: %v", Addresses(got))
	}
}

func TestLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l := &Lock{Providers: map[string]*LockEntry{
		"registry.opentofu.org/hashicorp/random": {Version: "3.9.1", Constraints: "~> 3.6", Hashes: map[string]string{"darwin_arm64": "sha256:abc"}},
		"registry.opentofu.org/hashicorp/local":  {Version: "2.9.1"},
	}}
	if err := l.Write(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, LockFileName))
	t.Logf("\n%s", data)
	l2, err := ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := l2.Providers["registry.opentofu.org/hashicorp/random"]
	if e == nil || e.Version != "3.9.1" || e.Constraints != "~> 3.6" || len(e.Hashes) != 1 {
		t.Errorf("lock entry = %+v", e)
	}
	if l2.Providers["registry.opentofu.org/hashicorp/local"].Version != "2.9.1" {
		t.Error("local entry lost")
	}
}

func TestLockModules(t *testing.T) {
	dir := t.TempDir()
	l := &Lock{Providers: map[string]*LockEntry{}, Modules: map[string]*ModuleEntry{}}
	if err := l.UpdateModules([]ModuleUse{{Path: "github.com/a/m@v1", Version: "v1.0.0", Hash: "sha256:aa"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := l.Write(dir); err != nil {
		t.Fatal(err)
	}
	l2, err := ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := l2.Modules["github.com/a/m@v1"]; e == nil || e.Version != "v1.0.0" || e.Hash != "sha256:aa" {
		t.Fatalf("module entry lost: %+v", e)
	}
	// Same version, different content: refused, unless relocking.
	err = l2.UpdateModules([]ModuleUse{{Path: "github.com/a/m@v1", Version: "v1.0.0", Hash: "sha256:bb"}}, false)
	var changed *ErrModuleChanged
	if !errors.As(err, &changed) {
		t.Fatalf("expected ErrModuleChanged, got %v", err)
	}
	if err := l2.UpdateModules([]ModuleUse{{Path: "github.com/a/m@v1", Version: "v1.0.0", Hash: "sha256:bb"}}, true); err != nil {
		t.Errorf("relock must accept changed content: %v", err)
	}
	// A new version replaces the entry without complaint.
	if err := l2.UpdateModules([]ModuleUse{{Path: "github.com/a/m@v1", Version: "v1.1.0", Hash: "sha256:cc"}}, false); err != nil {
		t.Errorf("a version bump is a deliberate change: %v", err)
	}
	// Modules no longer used are dropped.
	_ = l2.UpdateModules(nil, false)
	if len(l2.Modules) != 0 {
		t.Errorf("unused modules should be dropped: %v", l2.Modules)
	}
}
