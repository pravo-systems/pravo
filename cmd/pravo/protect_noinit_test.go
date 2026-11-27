package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The protect commands read only $settings, so they run in a checkout whose
// specification imports provider packages that pravo init has not generated yet.
func TestProtectRunsBeforeInit(t *testing.T) {
	rt := &runContext{}
	requireSealing(t)
	ids := isolateIdentities(t, rt)
	dir := t.TempDir()
	spec := `package main

import "pravo.dev/providers/random"

$settings: state: path: ".pravo/state"
$providers: random: {source: "registry.opentofu.org/hashicorp/random", version: "3.6.3"}

pet: random.#Pet & {length: 2}
`
	if err := os.WriteFile(filepath.Join(dir, "main.cue"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ada := person(t, ids, "ada")
	writeReaders(t, filepath.Join(dir, ".pravo", "access"), ada)
	// A plain load stops here: there is no module and no generated package.
	if text, code := runPravo(t, rt, dir, "validate"); code == 0 || !strings.Contains(text, "pravo init") {
		t.Fatalf("validate: want a refusal that names pravo init, got exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "protect", "check", "--allow-local"); code != 0 || !strings.Contains(text, "Store") {
		t.Fatalf("protect check: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "protect", "status"); code != 0 || !strings.Contains(text, "Readers") {
		t.Fatalf("protect status: exit %d\n%s", code, text)
	}
	text, code := runPravo(t, rt, dir, "--identity", "ada", "protect", "init", "--encrypt", "--yes", "--allow-local")
	if code != 0 {
		t.Fatalf("protect init: exit %d\n%s", code, text)
	}
	if strings.Contains(text, "pravo init") || strings.Contains(text, "provider schema") {
		t.Errorf("protect init asks for pravo init:\n%s", text)
	}
	if text, code := runPravo(t, rt, dir, "--identity", "ada", "protect", "status"); code != 0 || !strings.Contains(text, "on") {
		t.Fatalf("protect status after the seal: exit %d\n%s", code, text)
	}
}

// A $settings that cannot be read on its own falls back to the whole load, which
// names the problem.
func TestProtectSaysWhatIsWrongWithTheSettings(t *testing.T) {
	rt := &runContext{}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.cue"), []byte("package main\n\n$settings: state: backend: \"nonsense\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, rt, dir, "protect", "status")
	if code == 0 || !strings.Contains(text, "backend") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}
