package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
)

func cacheFixture() *schema.ProviderSchema {
	str := json.RawMessage(`"string"`)
	block := func(name string) *schema.Schema {
		return &schema.Schema{Version: 1, Block: &schema.Block{Attributes: []*schema.Attribute{{Name: name, TypeJSON: str, Optional: true}}}}
	}
	return &schema.ProviderSchema{
		Provider:    block("region"),
		Resources:   map[string]*schema.Schema{"x_alpha": block("name"), "x_beta": block("size")},
		DataSources: map[string]*schema.Schema{}, Ephemerals: map[string]*schema.Schema{}, Functions: map[string]*schema.Function{},
	}
}

func cachedProviders(t *testing.T, reg *registry.Client, src registry.Source) *Providers {
	t.Helper()
	m := NewProviders(&config.Root{Providers: map[string]*config.ProviderReq{}}, reg)
	m.Resolved["x"] = &ResolvedProvider{Req: &config.ProviderReq{LocalName: "x", Source: src, Protocol: config.ProtocolTfplugin}, Version: "1.0.0", Protocol: config.ProtocolTfplugin}
	return m
}

// The schema cache that an earlier pravo wrote is JSON. It is read once and kept
// as a pack, which a run reads by type.
func TestSchemaCacheOfAnEarlierPravoBecomesAPack(t *testing.T) {
	t.Setenv("PRAVO_RECORD", "")
	t.Setenv("PRAVO_REPLAY", "")
	reg := &registry.Client{Home: t.TempDir()}
	src := registry.Source{Host: registry.DefaultHost, Namespace: "hashicorp", Type: "x"}
	data, err := cacheFixture().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	jsonPath, packPath := reg.SchemaCachePath(src, "1.0.0"), reg.SchemaPackPath(src, "1.0.0")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	ps, err := cachedProviders(t, reg, src).Schema(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if ps.Resource("x_alpha") == nil {
		t.Error("the JSON cache is not read")
	}
	if _, err := os.Stat(packPath); err != nil {
		t.Fatalf("the pack is not written: %v", err)
	}

	// The next run reads the pack, and a type is decoded when it is asked for.
	ps, err = cachedProviders(t, reg, src).Schema(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Resources) != 0 {
		t.Errorf("a type was decoded before it was asked for: %d", len(ps.Resources))
	}
	if ps.Resource("x_beta") == nil || len(ps.Resources) != 1 {
		t.Errorf("x_beta = %v, decoded %d", ps.Resource("x_beta"), len(ps.Resources))
	}

	// A pack that cannot be read is not an error: the JSON is read and the pack is written again.
	if err := os.WriteFile(packPath, []byte("not a pack"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ps, err = cachedProviders(t, reg, src).Schema(context.Background(), "x"); err != nil || ps.Resource("x_alpha") == nil {
		t.Fatalf("a damaged pack: %v", err)
	}
	if again, err := schema.OpenPack(packPath); err != nil || again.Resource("x_alpha") == nil {
		t.Errorf("the pack is not written again: %v", err)
	}
}

// A provider from a path is started every run, since its binary can change
// without its version changing. Its schema is still cached as "dev", so that
// the viewer, which never starts a provider, can read it.
func TestADevProviderIsAskedEveryRunAndItsSchemaIsCachedForTheViewer(t *testing.T) {
	t.Setenv("PRAVO_RECORD", "")
	t.Setenv("PRAVO_REPLAY", "")
	reg := &registry.Client{Home: t.TempDir()}
	src := registry.Source{Host: registry.DefaultHost, Namespace: "demo", Type: "demo"}
	fake := &fakeProvider{}
	providers := func() *Providers {
		m := NewProviders(&config.Root{Providers: map[string]*config.ProviderReq{}}, reg)
		req := &config.ProviderReq{LocalName: "demo", Source: src, Path: "./terraform-provider-demo", Protocol: config.ProtocolTfplugin}
		m.Resolved["demo"] = &ResolvedProvider{Req: req, Version: "dev", Path: req.Path, Protocol: config.ProtocolTfplugin}
		m.launch = func(string, *ResolvedProvider) (plugin.Provider, error) {
			fake.count("start")
			return fake, nil
		}
		return m
	}
	for run := 1; run <= 2; run++ {
		ps, err := providers().Schema(context.Background(), "demo")
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if ps.Resource("fake_thing") == nil {
			t.Fatalf("run %d: the schema of the provider is not the one it reported", run)
		}
		if got := fake.calls["start"]; got != run {
			t.Errorf("run %d: the provider was started %d times, so a run read the cache", run, got)
		}
	}
	packPath := reg.SchemaPackPath(src, "dev")
	cached, err := schema.OpenPack(packPath)
	if err != nil {
		t.Fatalf("the schema is not cached for the viewer: %v", err)
	}
	if cached.Resource("fake_thing") == nil {
		t.Error("the cached schema is not the one the provider reported")
	}
}
