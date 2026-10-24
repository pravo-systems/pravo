package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// The tests that run the command line against a provider that can list use
// tools/fakeprovider, a real provider binary: pravo starts it and speaks the
// plugin protocol to it as to any other. It is built once for the tests of the
// package. They are skipped by -short, as the other tests that build a binary are.

type demoObject struct{ ID, Name, Region string }

var (
	demoBuild  sync.Once
	demoBinary string
	demoErr    error
)

func buildDemoProvider(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a provider")
	}
	demoBuild.Do(func() {
		dir, err := os.MkdirTemp("", "pravo-fakeprovider-")
		if err != nil {
			demoErr = err
			return
		}
		name := "terraform-provider-demo"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		demoBinary = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", demoBinary, "./tools/fakeprovider")
		cmd.Dir = filepath.Join("..", "..")
		if b, err := cmd.CombinedOutput(); err != nil {
			demoErr = fmt.Errorf("building the fake provider: %v\n%s", err, b)
		}
	})
	if demoErr != nil {
		t.Fatal(demoErr)
	}
	return demoBinary
}

// demoProvider is the directory the fake provider keeps its objects in.
type demoProvider struct {
	t   *testing.T
	dir string
}

// newDemoProvider makes a provider with these objects and has the commands of the
// test start it.
func newDemoProvider(t *testing.T, objs ...demoObject) *demoProvider {
	t.Helper()
	buildDemoProvider(t)
	p := &demoProvider{t: t, dir: t.TempDir()}
	t.Setenv("FAKEPROVIDER_DIR", p.dir)
	p.add(objs...)
	return p
}

func (p *demoProvider) catalog() []map[string]string {
	b, err := os.ReadFile(filepath.Join(p.dir, "catalog.json"))
	if err != nil {
		return nil
	}
	var objs []map[string]string
	if err := json.Unmarshal(b, &objs); err != nil {
		p.t.Fatal(err)
	}
	return objs
}

func (p *demoProvider) add(objs ...demoObject) {
	p.t.Helper()
	all := p.catalog()
	for _, o := range objs {
		all = append(all, map[string]string{"id": o.ID, "name": o.Name, "region": o.Region})
	}
	b, _ := json.Marshal(all)
	if err := os.WriteFile(filepath.Join(p.dir, "catalog.json"), b, 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// addBucket adds a demo_bucket, a resource that has no id and is named by an
// identity: its account, its name and its region.
func (p *demoProvider) addBucket(name, region string) {
	p.t.Helper()
	all := append(p.catalog(), map[string]string{"type": "demo_bucket", "id": "bkt-" + name, "name": name, "region": region})
	b, _ := json.Marshal(all)
	if err := os.WriteFile(filepath.Join(p.dir, "catalog.json"), b, 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// listCalls is how many times the provider was asked to list.
func (p *demoProvider) listCalls() int {
	b, err := os.ReadFile(filepath.Join(p.dir, "calls"))
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "list")
}

// failWith makes the provider fail to list with this text; "" ends it.
func (p *demoProvider) failWith(text string) {
	p.t.Helper()
	path := filepath.Join(p.dir, "fail")
	if text == "" {
		_ = os.Remove(path)
		return
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// demoEstate writes a specification that uses the provider, followed by body, and
// initialises it.
func demoEstate(t *testing.T, body string) string {
	t.Helper()
	spec := "package estate\n\nimport demo \"pravo.dev/providers/demo/demo\"\n\n$demo: demo.#Provider & {$path: " + fmt.Sprintf("%q", demoBinary) + "}\n" + body
	dir := specDir(t, map[string]string{"main.cue": spec})
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	return dir
}

func mustWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("the output lacks %q:\n%s", w, text)
		}
	}
}

func lacks(t *testing.T, text string, nots ...string) {
	t.Helper()
	for _, w := range nots {
		if strings.Contains(text, w) {
			t.Errorf("the output has %q:\n%s", w, text)
		}
	}
}
