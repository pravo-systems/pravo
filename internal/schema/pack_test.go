package schema

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func packFixture() *ProviderSchema {
	str := json.RawMessage(`"string"`)
	block := func(attrs ...string) *Schema {
		b := &Block{}
		for _, a := range attrs {
			b.Attributes = append(b.Attributes, &Attribute{Name: a, TypeJSON: str, Optional: true})
		}
		return &Schema{Version: 1, Block: b}
	}
	return &ProviderSchema{
		Provider: block("region"),
		Resources: map[string]*Schema{
			"x_alpha": block("name", "arn"),
			"x_beta":  block("id"),
			"x_gamma": block("size"),
		},
		DataSources: map[string]*Schema{"x_lookup": block("key")},
		Ephemerals:  map[string]*Schema{"x_token": block("value")},
		Functions: map[string]*Function{
			"parse": {Parameters: []*Parameter{{Name: "s", TypeJSON: str}}, ReturnJSON: str, Summary: "parses"},
		},
	}
}

func writePackFile(t *testing.T, p *ProviderSchema) string {
	t.Helper()
	data, err := p.MarshalPack()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "x.pack")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPackReadsATypeWhenItIsAskedFor(t *testing.T) {
	orig := packFixture()
	p, err := OpenPack(writePackFile(t, orig))
	if err != nil {
		t.Fatal(err)
	}
	if p.Provider == nil || len(p.Provider.Block.Attributes) != 1 {
		t.Fatalf("the provider block is read when the pack is opened: %+v", p.Provider)
	}
	if len(p.Resources) != 0 {
		t.Fatalf("no type is decoded before it is asked for: %d", len(p.Resources))
	}
	if got := p.SortedResourceNames(); len(got) != 3 || got[0] != "x_alpha" || got[2] != "x_gamma" {
		t.Errorf("names = %v", got)
	}
	if len(p.Resources) != 0 {
		t.Errorf("listing the names decodes nothing: %d", len(p.Resources))
	}
	if a, b := asJSON(t, p.Resource("x_alpha")), asJSON(t, orig.Resources["x_alpha"]); a != b {
		t.Errorf("x_alpha = %s, want %s", a, b)
	}
	if len(p.Resources) != 1 {
		t.Errorf("one type is decoded: %d", len(p.Resources))
	}
	if first, again := p.Resource("x_alpha"), p.Resource("x_alpha"); first != again {
		t.Error("a type is decoded once")
	}
	if p.Resource("nope") != nil || p.DataSource("x_alpha") != nil {
		t.Error("an unknown type is nil, and so is a type of another kind")
	}
	if a, b := asJSON(t, p.DataSource("x_lookup")), asJSON(t, orig.DataSources["x_lookup"]); a != b {
		t.Errorf("x_lookup = %s", a)
	}
	if a, b := asJSON(t, p.Ephemeral("x_token")), asJSON(t, orig.Ephemerals["x_token"]); a != b {
		t.Errorf("x_token = %s", a)
	}
	if f := p.Function("parse"); f == nil || f.Summary != "parses" || f.ReturnType() == nil {
		t.Errorf("function = %+v", f)
	}
}

func TestPackMaterializeGivesTheWholeSchema(t *testing.T) {
	orig := packFixture()
	p, err := OpenPack(writePackFile(t, orig))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Materialize(); err != nil {
		t.Fatal(err)
	}
	if len(p.Resources) != 3 || len(p.DataSources) != 1 || len(p.Ephemerals) != 1 || len(p.Functions) != 1 {
		t.Errorf("types = %d %d %d %d", len(p.Resources), len(p.DataSources), len(p.Ephemerals), len(p.Functions))
	}
	if len(p.AllResources()) != 3 {
		t.Errorf("AllResources = %d", len(p.AllResources()))
	}
	// The pack of a pack is the same pack.
	a, err := orig.MarshalPack()
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.MarshalPack()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("a schema that was read from a pack is not written as the pack it came from")
	}
}

func TestPackGenerateCUEIsTheSameAsFromTheDecodedSchema(t *testing.T) {
	ps := packFixture()
	ps.Resources["x_beta"].Block.Attributes = append(ps.Resources["x_beta"].Block.Attributes,
		&Attribute{Name: "number", TypeJSON: json.RawMessage(`"bool"`), Optional: true})
	lazy, err := OpenPack(writePackFile(t, ps))
	if err != nil {
		t.Fatal(err)
	}
	opts := GenOptions{Header: "h", Source: "registry.opentofu.org/x/x"}
	if want, got := GenerateCUE(ps, "x", opts), GenerateCUE(lazy, "x", opts); !bytes.Equal(want, got) {
		t.Errorf("the CUE generated from the pack differs:\n%s\n---\n%s", want, got)
	}
	// With one type, as pravo schema asks, the builtin shadowing of the whole
	// provider still decides how the types are written.
	one := GenOptions{Resources: []string{"x_alpha"}, DataSources: []string{}, Ephemerals: []string{}, Functions: []string{}, Source: "s"}
	if want, got := GenerateCUE(ps, "x", one), GenerateCUE(lazy, "x", one); !bytes.Equal(want, got) {
		t.Errorf("one type: the CUE differs:\n%s\n---\n%s", want, got)
	}
	if !bytes.Contains(GenerateCUE(lazy, "x", one), []byte("__string")) {
		t.Error("a provider with an attribute named number is generated with the __ aliases, whichever type is asked for")
	}
}

func TestPackRefusesWhatIsNotAPack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.pack")
	for name, content := range map[string][]byte{
		"empty":     {},
		"json":      []byte(`{"cache_version":3}`),
		"truncated": append([]byte(packMagic), 0, 0, 0, 9, '{'),
		"too long":  append(append([]byte(packMagic), 0xff, 0xff, 0xff, 0xff), []byte("{}")...),
	} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenPack(path); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	if _, err := OpenPack(filepath.Join(dir, "missing.pack")); err == nil {
		t.Error("a missing file opened")
	}
}

func TestPackOfAnOlderPravoIsStale(t *testing.T) {
	head, err := json.Marshal(packIndex{CacheVersion: CacheVersion - 1})
	if err != nil {
		t.Fatal(err)
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(head)))
	path := filepath.Join(t.TempDir(), "x.pack")
	if err := os.WriteFile(path, append(append([]byte(packMagic), n[:]...), head...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPack(path); !errors.Is(err, ErrStaleCache) {
		t.Errorf("err = %v, want ErrStaleCache", err)
	}
}

// Tasks ask for types from several goroutines.
func TestPackIsSafeToReadFromManyGoroutines(t *testing.T) {
	p, err := OpenPack(writePackFile(t, packFixture()))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, n := range []string{"x_alpha", "x_beta", "x_gamma", "nope"} {
				if s := p.Resource(n); (s == nil) != (n == "nope") {
					t.Errorf("%s = %v", n, s)
				}
			}
			p.Function("parse")
			p.SortedResourceNames()
		}()
	}
	wg.Wait()
}

// On Windows a pack is opened for each read, so that a directory that holds one
// can be removed and the file can be replaced while a schema is in use. The mode
// is tested on every system.
func TestPackOpenedForEachRead(t *testing.T) {
	was := holdPack
	holdPack = false
	t.Cleanup(func() { holdPack = was })
	orig := packFixture()
	path := writePackFile(t, orig)
	p, err := OpenPack(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.lazy.f != nil {
		t.Fatal("the file is held open")
	}
	if p.Provider == nil || p.Resource("x_alpha") == nil || len(p.Resource("x_alpha").Block.Attributes) != 2 {
		t.Fatalf("a type is read from the file when it is asked for: %+v", p.Resource("x_alpha"))
	}
	if p.Function("parse") == nil {
		t.Error("a function is read from the file when it is asked for")
	}
	// The file is replaced by a pack of another size: a type that was not read yet
	// is not found, and the bytes of the other file are not taken for it.
	other := packFixture()
	other.Resources["x_delta"] = other.Resources["x_alpha"]
	data, err := other.MarshalPack()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := p.Resource("x_beta"); got != nil {
		t.Errorf("a type was read from a file that is not the one that was opened: %+v", got)
	}
	if p.Resource("x_alpha") == nil {
		t.Error("a type that was read before stays")
	}
}
