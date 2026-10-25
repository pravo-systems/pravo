package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// exitCodeTable is the table of docs/reference/exit-codes.md as the code
// states it: the code, its name, and words that the meaning column must use.
// Change the table, this list and the scenarios below together.
var exitCodeTable = []struct {
	code     int
	name     string
	mentions []string
}{
	{0, "success", []string{"--detailed-exitcode", "drift"}},
	{1, "error", []string{"unknown flag", "stray argument", "--auto-approve", "yes"}},
	{2, "changes", []string{"--detailed-exitcode", "drift"}},
	{3, "stale", []string{"saved plan"}},
	{4, "rejected", []string{"locked", "reserved"}},
	{5, "refused", []string{"plan policy", "policy file", "lock"}},
	{6, "invalid", []string{"does not load or evaluate", "circle"}},
	{7, "untrusted", []string{"does not match", "pravo protect status"}},
}

// docTable reads the rows of the table in the exit codes reference.
func docTable(t *testing.T) (rows []struct {
	code    int
	name    string
	meaning string
}) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "exit-codes.md"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`^\| (\d+) \| ([a-z]+) \| (.*) \|$`)
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		m := row.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		rows = append(rows, struct {
			code    int
			name    string
			meaning string
		}{n, m[2], m[3]})
	}
	return rows
}

// The table in the reference, the constants of the engine and the list above
// say the same thing.
func TestExitCodeTableMatchesTheReference(t *testing.T) {
	rows := docTable(t)
	if len(rows) != len(exitCodeTable) {
		t.Fatalf("the reference lists %d codes, the test knows %d", len(rows), len(exitCodeTable))
	}
	for i, want := range exitCodeTable {
		got := rows[i]
		if got.code != want.code || got.name != want.name {
			t.Errorf("row %d of the reference is %d %s, want %d %s", i, got.code, got.name, want.code, want.name)
		}
		for _, w := range want.mentions {
			if !strings.Contains(got.meaning, w) {
				t.Errorf("the meaning of %d %s does not mention %q:\n%s", got.code, got.name, w, got.meaning)
			}
		}
	}
	for code, c := range map[int]engine.ExitCode{0: engine.ExitOK, 1: engine.ExitError, 2: engine.ExitChanges, 3: engine.ExitStale, 4: engine.ExitLocked, 5: engine.ExitRefused, 6: engine.ExitInvalid, 7: engine.ExitUntrusted} {
		if int(c) != code {
			t.Errorf("the engine constant for %d is %d", code, c)
		}
	}
}

// pendingDir is a root whose specification declares nothing and whose state
// records a data source: the plan deletes the record, which is a change that
// needs no provider.
func pendingDir(t *testing.T) (string, *state.Store) {
	t.Helper()
	dir := specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "cfg", Mode: "data", Type: "t_data", Name: "cfg", Attributes: []byte(`{}`)}, ""); err != nil {
		t.Fatal(err)
	}
	return dir, st
}

// untrustedDir is a root whose state holds one immutable record that was
// changed after it was written: the bytes of rec/<sha256> no longer hash to the
// name.
func untrustedDir(t *testing.T) string {
	t.Helper()
	dir, st := pendingDir(t)
	if _, err := st.Upgrade(context.Background(), state.UpgradeOptions{To: state.FormatImmutable}); err != nil {
		if errors.Is(err, state.ErrNotImplemented) {
			t.Skip("the immutable layout is not implemented in this tree yet")
		}
		t.Fatal(err)
	}
	recs, _ := filepath.Glob(filepath.Join(dir, "pravo.state", "rec", "*"))
	if len(recs) == 0 {
		t.Fatal("the upgrade wrote no rec/ object")
	}
	if err := os.WriteFile(recs[0], []byte(`{"addr":"cfg","attributes":{"tampered":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// stdin answers the prompt of a command that asks for confirmation.
func stdin(t *testing.T, answer string) func() {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	go func() { w.WriteString(answer); w.Close() }()
	return func() { os.Stdin = old; r.Close() }
}

type exitScenario struct {
	name string
	// run drives the command layer and returns what it printed and the exit
	// code that main would end the process with.
	run func(t *testing.T) (string, int)
	// want is a text that the output has.
	want string
}

// exitScenarios has at least one scenario for every code of the table.
func exitScenarios() map[int][]exitScenario {
	return map[int][]exitScenario{
		0: {
			{"validate", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"}), "validate")
			}, "Success!"},
			{"plan with --detailed-exitcode and no changes", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"}), "plan", "--detailed-exitcode")
			}, "No changes."},
			{"plan without --detailed-exitcode and with changes", func(t *testing.T) (string, int) {
				dir, _ := pendingDir(t)
				return runPravo(t, dir, "plan")
			}, "1 to destroy"},
			{"drift with no drift", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"}), "drift")
			}, "No drift."},
		},
		1: {
			{"a missing output", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"}), "output", "nope")
			}, `output "nope" is not recorded`},
			{"an unknown flag", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"}), "plan", "--nope")
			}, "unknown flag: --nope"},
			{"a stray argument", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"}), "plan", "stray")
			}, "takes no arguments"},
			{"apply --json with changes and no --auto-approve", func(t *testing.T) (string, int) {
				dir, _ := pendingDir(t)
				return runPravo(t, dir, "apply", "--json")
			}, "needs -auto-approve"},
			{"an answer other than yes", func(t *testing.T) (string, int) {
				dir, _ := pendingDir(t)
				defer stdin(t, "no\n")()
				return runPravo(t, dir, "apply")
			}, "apply cancelled"},
		},
		2: {
			{"plan --detailed-exitcode with changes", func(t *testing.T) (string, int) {
				dir, _ := pendingDir(t)
				return runPravo(t, dir, "plan", "--detailed-exitcode")
			}, "1 to destroy"},
		},
		3: {
			{"a saved plan after the specification changed", func(t *testing.T) (string, int) {
				dir := specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"})
				if text, code := runPravo(t, dir, "plan", "--out", "change.plan"); code != 0 {
					t.Fatalf("plan --out exited %d:\n%s", code, text)
				}
				if err := os.WriteFile(filepath.Join(dir, "main.cue"), []byte("package infra\n\nsize: 4\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return runPravo(t, dir, "apply", "change.plan")
			}, "saved plan is stale"},
		},
		4: {
			{"an apply while another command holds the node", func(t *testing.T) (string, int) {
				dir, st := pendingDir(t)
				if err := st.Lock("cfg", "pravo apply", ""); err != nil {
					t.Fatal(err)
				}
				defer st.Unlock("cfg")
				return runPravo(t, dir, "apply", "--auto-approve")
			}, "locked by"},
			{"a plan while another actor has reserved the node", func(t *testing.T) (string, int) {
				dir, _ := pendingDir(t)
				if text, code := runPravo(t, dir, "plan", "--reserve", "15m", "--actor", "ci"); code != 0 {
					t.Fatalf("plan --reserve exited %d:\n%s", code, text)
				}
				return runPravo(t, dir, "plan", "--actor", "bob")
			}, "reserved by ci"},
		},
		5: {
			{"a plan that violates a policy", func(t *testing.T) (string, int) {
				return runPravo(t, policyInForce(t, "summary: destroy: 0").dir, "plan")
			}, "violates the policy"},
			{"a policy in a state that is not sealed", func(t *testing.T) (string, int) {
				dir := specDir(t, map[string]string{"main.cue": "package infra\n\nimport \"pravo.dev/pravo\"\n\nno: pravo.#Policy & {rule: \"summary: destroy: 0\"}\n"})
				if text, code := runPravo(t, dir, "init"); code != 0 {
					t.Fatalf("init exited %d:\n%s", code, text)
				}
				return runPravo(t, dir, "plan")
			}, "must be sealed"},
			{"a module that does not match its lock", func(t *testing.T) (string, int) {
				dir := specDir(t, map[string]string{
					"main.cue":       "package infra\n\nsize: 3\n",
					"pravo.lock.cue": "modules: \"example.com/x@v0\": {version: \"v0.1.0\", hash: \"h1:abc\"}\n",
				})
				return runPravo(t, dir, "mod", "verify")
			}, "locked but no longer used"},
		},
		6: {
			{"a conflict in the specification", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 11\nsize: 3\n"}), "validate")
			}, "conflicting values"},
			{"a syntax error", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nx: [\n"}), "plan")
			}, "Error:"},
			{"a file outside the package", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n", "other.cue": "package other\n"}), "eval")
			}, "the other files are package infra"},
			{"a type of a provider that is not declared", func(t *testing.T) (string, int) {
				return runPravo(t, specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"}), "schema", "random_pet")
			}, `no provider "random"`},
		},
		7: {
			{"a record that does not match its name", func(t *testing.T) (string, int) {
				return runPravo(t, untrustedDir(t), "protect", "status")
			}, "Untrusted: "},
		},
	}
}

// Every code of the table ends a command in the command layer, and no code
// ends one that the table does not list.
func TestEveryDocumentedExitCodeHasAScenario(t *testing.T) {
	scenarios := exitScenarios()
	for _, row := range exitCodeTable {
		if len(scenarios[row.code]) == 0 {
			t.Errorf("no scenario ends with exit code %d (%s)", row.code, row.name)
		}
	}
	for code := range scenarios {
		found := false
		for _, row := range exitCodeTable {
			found = found || row.code == code
		}
		if !found {
			t.Errorf("scenarios for exit code %d, which the table does not list", code)
		}
	}
	for _, row := range exitCodeTable {
		code := row.code
		for _, sc := range scenarios[code] {
			t.Run(strconv.Itoa(code)+" "+sc.name, func(t *testing.T) {
				text, got := sc.run(t)
				if got != code {
					t.Errorf("exit code %d, want %d:\n%s", got, code, text)
				}
				if !strings.Contains(text, sc.want) {
					t.Errorf("the output lacks %q:\n%s", sc.want, text)
				}
			})
		}
	}
}

// The process ends with the code: main prints the error and calls os.Exit.
func TestProcessExitStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool on the path")
	}
	bin := filepath.Join(t.TempDir(), "pravo")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command(goBin, "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	home := t.TempDir()
	run := func(dir string, args ...string) (string, int) {
		cmd := exec.Command(bin, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "PRAVO_HOME="+home, "NO_COLOR=1")
		out, err := cmd.Output()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	good := specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"})
	bad := specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 11\nsize: 3\n"})
	pending, _ := pendingDir(t)
	cases := []struct {
		dir  string
		args []string
		want int
	}{
		{good, []string{"validate"}, 0},
		{good, []string{"output", "nope"}, 1},
		{pending, []string{"plan", "--detailed-exitcode"}, 2},
		{policyInForce(t, "summary: destroy: 0").dir, []string{"plan", "--detailed-exitcode"}, 5},
		{bad, []string{"validate"}, 6},
	}
	for _, c := range cases {
		out, got := run(c.dir, c.args...)
		if got != c.want {
			t.Errorf("pravo %s: exit status %d, want %d:\n%s", strings.Join(c.args, " "), got, c.want, out)
		}
	}
	// Under --json an error is also an object that carries the code.
	out, got := run(bad, "validate", "--json")
	if got != 6 || !strings.Contains(out, `"code":6`) || !strings.Contains(out, `"kind":"error"`) {
		t.Errorf("validate --json: exit status %d:\n%s", got, out)
	}
}

// missingFileMessage is what the system says of a file that does not exist.
var missingFileMessage = func() string {
	if runtime.GOOS == "windows" {
		return "cannot find the file specified"
	}
	return "no such file"
}()

// The errors of the state package that a sealed state adds map to the codes of
// the reference, whatever their text.
func TestExitCodeOfTheErrorsOfASealedState(t *testing.T) {
	for _, c := range []struct {
		err  error
		code engine.ExitCode
	}{
		{state.ErrUntrusted, engine.ExitUntrusted},
		{fmt.Errorf("rec/ab: %w", state.ErrUntrusted), engine.ExitUntrusted},
		{state.ErrNotReader, engine.ExitRefused},
		{protect.ErrNotReader, engine.ExitRefused},
		{state.ErrSealing, engine.ExitLocked},
		{&state.ErrLocked{Lock: state.Lock{Addr: "a"}}, engine.ExitLocked},
		{errors.New("something else"), engine.ExitError},
		// A write whose outcome the store did not say is a failure like the others
		// of the store: the command is run again.
		{state.Advise(fmt.Errorf("writing resources/a.json: %w", state.ErrUnknownOutcome)), engine.ExitError},
	} {
		if got := exitCodeOf(c.err); got != int(c.code) {
			t.Errorf("exitCodeOf(%v) = %d, want %d", c.err, got, c.code)
		}
	}
}
