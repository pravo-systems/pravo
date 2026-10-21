package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pravo.dev/pravo/internal/state"
)

// importRoot makes a root with an empty specification and a copy of a state
// file that OpenTofu 1.13.1 wrote (internal/tfstate/testdata).
func importRoot(t *testing.T, fixture string) string {
	t.Helper()
	dir := specDir(t, map[string]string{"main.cue": "package t\n"})
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "tfstate", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestImportTfstateOfAModuleWithForEach(t *testing.T) {
	dir := importRoot(t, "platform.tfstate")
	out, code := runPravo(t, dir, "state", "import-tfstate", "terraform.tfstate")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"Imported 7 resources and 2 outputs from terraform.tfstate into ",
		"Renamed instances (these are the addresses now in the state; point each at its place in the specification with $from):",
		`  module.service["api"].random_pet.host -> random_pet.service_api_host`,
		`  module.service["worker"].local_file.config -> local_file.service_worker_config`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	list, _ := runPravo(t, dir, "state", "list")
	if !strings.Contains(list, "random_password.admin\n") || strings.Count(list, "\n") != 7 {
		t.Errorf("state list:\n%s", list)
	}

	// The dependency the state recorded without the key of the module instance
	// is on the pet of the same instance.
	graph, _ := runPravo(t, dir, "state", "graph")
	want := "local_file.service_api_config -> random_pet.service_api_host\n" +
		"local_file.service_web_config -> random_pet.service_web_host\n" +
		"local_file.service_worker_config -> random_pet.service_worker_host\n"
	if graph != want {
		t.Errorf("state graph:\n%s\nwant:\n%s", graph, want)
	}
	deps, _ := runPravo(t, dir, "state", "dependents", "random_pet.service_web_host")
	if deps != "local_file.service_web_config\n" {
		t.Errorf("dependents:\n%s", deps)
	}

	// An imported record has no provider version yet. The header says so
	// without a bare "v".
	show, _ := runPravo(t, dir, "state", "show", "random_pet.service_api_host")
	first := strings.SplitN(show, "\n", 2)[0]
	if first != "// random_pet.service_api_host (provider registry.opentofu.org/hashicorp/random as random, schema version 0, revision 1)" {
		t.Errorf("state show header: %s", first)
	}
	if !strings.Contains(show, "// needed by local_file.service_api_config\n") {
		t.Errorf("state show:\n%s", show)
	}

	// The sensitive output is listed as such and printed on request.
	listing, _ := runPravo(t, dir, "output")
	if !strings.Contains(listing, "admin_password: (sensitive)\n") {
		t.Errorf("output:\n%s", listing)
	}
}

func TestImportTfstateWarnsAboutTaintAndDeposedObjects(t *testing.T) {
	dir := importRoot(t, "taint.tfstate")
	out, code := runPravo(t, dir, "state", "import-tfstate", "terraform.tfstate")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"Imported 4 resources and 0 outputs from terraform.tfstate into ",
		"Warning: local_file.web[0]: deposed object 2477c3ca skipped",
		"Warning: local_file.web[1]: deposed object f180dada skipped",
		"Warning: null_resource.check: tainted in the source state; pravo has no taint, the object is recorded as healthy",
		"Warning: random_pet.server: tainted in the source state",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The converted record is the current object, not the deposed one.
	show, _ := runPravo(t, dir, "state", "show", "local_file.web_0")
	if !strings.Contains(show, `"out/web0-2.txt"`) || strings.Contains(show, "web0-1") {
		t.Errorf("state show:\n%s", show)
	}
}

func TestImportTfstateRefusesOtherFormats(t *testing.T) {
	dir := importRoot(t, "platform.tfstate")
	for _, version := range []string{"3", "5"} {
		path := filepath.Join(dir, "v"+version+".tfstate")
		if err := os.WriteFile(path, []byte(`{"version": `+version+`, "modules": []}`), 0o644); err != nil {
			t.Fatal(err)
		}
		out, code := runPravo(t, dir, "state", "import-tfstate", path)
		want := "unsupported state format version " + version + ": pravo reads format version 4, which OpenTofu and Terraform 0.12 or newer write\n"
		if code != 1 || !strings.HasSuffix(out, want) || strings.Contains(out, "(or legacy") {
			t.Errorf("version %s: exit %d:\n%s", version, code, out)
		}
	}
	if list, _ := runPravo(t, dir, "state", "list"); list != "" {
		t.Errorf("a refused import left records:\n%s", list)
	}
}

func TestImportTfstateReplacesOnlyWithForce(t *testing.T) {
	dir := importRoot(t, "platform.tfstate")
	if out, code := runPravo(t, dir, "state", "import-tfstate", "terraform.tfstate"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	out, code := runPravo(t, dir, "state", "import-tfstate", "terraform.tfstate")
	if code != 1 || !strings.Contains(out, "already holds 7 resources and 2 outputs; use -force to replace them") {
		t.Errorf("second import: exit %d:\n%s", code, out)
	}
	if out, code := runPravo(t, dir, "state", "import-tfstate", "terraform.tfstate", "--force"); code != 0 {
		t.Errorf("forced import: exit %d:\n%s", code, out)
	}
}

// The locks of a command that was killed are listed as stale, "pravo state log"
// shows its transaction as interrupted and not as running for ever, and "pravo
// state unlock --stale" releases them and nothing else.
func TestStateShowsAndReleasesTheLocksOfAKilledCommand(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	killed := st.Begin("apply", "pravo apply")
	running := st.Begin("apply", "pravo apply")
	for _, tx := range []*state.Txn{killed, running} {
		if err := tx.Start(); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour).UTC()
	for _, addr := range []string{"a", "b"} {
		l := state.Lock{Addr: addr, PID: 999999, Host: host, Command: "pravo apply", Txn: killed.ID(), Nonce: "dead", Since: old, RenewedAt: old, Kind: "lock"}
		data, _ := json.Marshal(l)
		if err := st.Backend().Put(context.Background(), "locks/"+addr+".lock", data); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Lock("c", "pravo apply", running.ID()); err != nil {
		t.Fatal(err)
	}

	text, code := runPravo(t, dir, "state", "locks")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], ": pravo apply [stale]") || !strings.HasSuffix(lines[1], ": pravo apply [stale]") || strings.Contains(lines[2], "[stale]") || !strings.HasPrefix(lines[2], "c  locked by") {
		t.Errorf("state locks:\n%s", text)
	}
	text, _ = runPravo(t, dir, "state", "locks", "--json")
	var listed []state.Lock
	if err := json.Unmarshal([]byte(text), &listed); err != nil || len(listed) != 3 || !listed[0].Stale || !listed[1].Stale || listed[2].Stale {
		t.Errorf("state locks --json: %v\n%s", err, text)
	}

	text, _ = runPravo(t, dir, "state", "log")
	if !strings.Contains(text, killed.ID()+"  apply    interrupted ") || !strings.Contains(text, running.ID()+"  apply    running ") {
		t.Errorf("state log:\n%s", text)
	}
	text, _ = runPravo(t, dir, "state", "log", "--json")
	var txns []state.Transaction
	if err := json.Unmarshal([]byte(text), &txns); err != nil || len(txns) != 2 {
		t.Fatalf("state log --json: %v\n%s", err, text)
	}
	for _, tx := range txns {
		want := map[string]string{killed.ID(): "interrupted", running.ID(): "running"}[tx.ID]
		if tx.Status != want {
			t.Errorf("transaction %s is %q, want %q", tx.ID, tx.Status, want)
		}
	}

	if text, code := runPravo(t, dir, "state", "unlock"); code == 0 || !strings.Contains(text, "--stale") {
		t.Errorf("state unlock without an address: exit %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "state", "unlock", "--stale", "a"); code == 0 || !strings.Contains(text, "takes no address") {
		t.Errorf("state unlock --stale a: exit %d:\n%s", code, text)
	}
	text, code = runPravo(t, dir, "state", "unlock", "--stale")
	if code != 0 || text != "Unlocked a\nUnlocked b\n" {
		t.Errorf("state unlock --stale: exit %d:\n%s", code, text)
	}
	text, _ = runPravo(t, dir, "state", "locks")
	if lines := strings.Split(strings.TrimSpace(text), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], "c  locked by") {
		t.Errorf("state locks after the release:\n%s", text)
	}
	if text, code := runPravo(t, dir, "state", "unlock", "--stale"); code != 0 || text != "No stale locks.\n" {
		t.Errorf("state unlock --stale again: exit %d:\n%s", code, text)
	}
	// With its locks gone, the transaction is still shown as interrupted.
	if text, _ := runPravo(t, dir, "state", "log"); !strings.Contains(text, killed.ID()+"  apply    interrupted ") {
		t.Errorf("state log after the release:\n%s", text)
	}
}

// "pravo state mv" writes the records that depend on the node it renames, so it
// takes their locks: a dependent that another command holds stops it, and
// nothing is renamed.
func TestStateMvLocksTheDependents(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*state.Resource{
		{Addr: "base", Mode: "managed", Type: "t", Name: "base", Attributes: []byte(`{}`)},
		{Addr: "d1", Mode: "managed", Type: "t", Name: "d1", Attributes: []byte(`{}`), Dependencies: []string{"base"}},
		{Addr: "d2", Mode: "managed", Type: "t", Name: "d2", Attributes: []byte(`{}`), Dependencies: []string{"base"}},
	} {
		if err := st.Set(r, "setup"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Lock("d2", "pravo apply", "other"); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, dir, "state", "mv", "base", "moved")
	if code == 0 || !strings.Contains(text, "d2 depends on base") || !strings.Contains(text, "d2 is locked by pravo apply") {
		t.Fatalf("state mv with a dependent held: exit %d:\n%s", code, text)
	}
	if list, _ := runPravo(t, dir, "state", "list"); list != "base\nd1\nd2\n" {
		t.Errorf("the refused move changed the state:\n%s", list)
	}
	if locks, _ := runPravo(t, dir, "state", "locks"); strings.Count(locks, "\n") != 1 || !strings.HasPrefix(locks, "d2  locked by") {
		t.Errorf("locks left:\n%s", locks)
	}

	if err := st.Unlock("d2"); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, dir, "state", "mv", "base", "moved")
	if code != 0 || text != "Moved base to moved\n" {
		t.Fatalf("state mv: exit %d:\n%s", code, text)
	}
	if graph, _ := runPravo(t, dir, "state", "graph"); graph != "d1 -> moved\nd2 -> moved\n" {
		t.Errorf("state graph:\n%s", graph)
	}
	if locks, _ := runPravo(t, dir, "state", "locks"); !strings.Contains(locks, "No locks") {
		t.Errorf("locks left after the move:\n%s", locks)
	}
}
