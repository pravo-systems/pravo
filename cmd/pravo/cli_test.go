package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue/format"
	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

// runPravo runs "pravo -C dir args..." in this process and returns what it
// printed on standard output and the exit code it would have.
func runPravo(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); done <- b }()
	root := newRootCmd()
	// main rewrites the single-dash long flags before cobra reads them.
	root.SetArgs(normaliseArgs(append([]string{"pravo", "-C", dir}, args...))[1:])
	err := root.Execute()
	w.Close()
	os.Stdout = stdout
	text := string(<-done)
	if err != nil {
		return text + "Error: " + err.Error() + "\n", exitCodeOf(err)
	}
	return text, 0
}

// specDir makes a directory with a module and the given files.
func specDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["cue.mod/module.cue"] = "module: \"pravo.local/test\"\nlanguage: version: \"v0.14.0\"\n"
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestInitInADirectoryWithoutASpecification(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"empty":     {},
		"lock file": {"pravo.lock.cue": "providers: \"registry.opentofu.org/hashicorp/random\": version: \"3.0.0\"\n"},
	} {
		dir := t.TempDir()
		for n, c := range files {
			os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644)
		}
		text, code := runPravo(t, dir, "init")
		if code != 0 {
			t.Fatalf("%s: init exited %d:\n%s", name, code, text)
		}
		for _, want := range []string{"Created " + filepath.Join("cue.mod", "module.cue"), "There is no specification yet", `"pravo eval"`, `"pravo validate"`} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the output lacks %q:\n%s", name, want, text)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "cue.mod", "module.cue")); err != nil {
			t.Errorf("%s: no module: %v", name, err)
		}
		// The second run has nothing to create and still succeeds.
		text, code = runPravo(t, dir, "init")
		if code != 0 || strings.Contains(text, "Created") || !strings.Contains(text, "There is no specification yet") {
			t.Errorf("%s: second init exited %d:\n%s", name, code, text)
		}
		// The directory is usable once a file is written.
		os.WriteFile(filepath.Join(dir, "main.cue"), []byte("package demo\n\nsize: 3\n"), 0o644)
		if text, code := runPravo(t, dir, "eval"); code != 0 || strings.TrimSpace(text) != "size: 3" {
			t.Errorf("%s: eval exited %d:\n%s", name, code, text)
		}
	}
}

func TestEveryCommandReportsAFileOutsideThePackage(t *testing.T) {
	for name, extra := range map[string]string{
		"x.cue": "size: 4\n",
		"y.cue": "package other\n\nsize: 4\n",
	} {
		dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n", name: extra})
		for _, args := range [][]string{{"validate"}, {"eval"}, {"plan"}, {"apply", "-auto-approve"}, {"destroy", "-auto-approve"}, {"init"}, {"gen"}} {
			text, code := runPravo(t, dir, args...)
			if code != int(engine.ExitInvalid) || !strings.Contains(text, name) || !strings.Contains(text, "the other files are package estate") {
				t.Errorf("pravo %s with %s: exit %d:\n%s", strings.Join(args, " "), name, code, text)
			}
		}
		if got := entries(t, dir); strings.Join(got, ",") != "cue.mod,main.cue,"+name {
			t.Errorf("a refused load left files behind: %v", got)
		}
	}
}

func TestErrorsInTheSpecificationExitInvalid(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 11\nsize: 3\n"})
	for _, args := range [][]string{
		{"validate"}, {"plan"}, {"apply", "-auto-approve"}, {"destroy", "-auto-approve"}, {"eval"}, {"init"}, {"gen"},
		{"output"}, {"providers"}, {"drift"}, {"why", "a"}, {"import", "a", "b"}, {"state", "list"}, {"state", "show", "a"},
		{"state", "log"}, {"state", "rm", "a"}, {"state", "query", "1"}, {"mod", "list"},
	} {
		text, code := runPravo(t, dir, args...)
		if code != int(engine.ExitInvalid) || !strings.Contains(text, "conflicting values") {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
		}
	}
	// A failure that is not in the specification keeps its own code.
	if text, code := runPravo(t, dir, "show", "missing.plan"); code != int(engine.ExitError) {
		t.Errorf("show of a missing plan file: exit %d:\n%s", code, text)
	}
	good := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	if text, code := runPravo(t, good, "output", "nope"); code != int(engine.ExitError) {
		t.Errorf("a missing output: exit %d:\n%s", code, text)
	}
	if text, code := runPravo(t, good, "eval", "-c", "-e", "int"); code != int(engine.ExitError) {
		t.Errorf("eval -c of a value that is not concrete: exit %d:\n%s", code, text)
	}
}

func TestStrayArgumentsAreRefused(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	for _, args := range [][]string{
		{"plan", "stray"}, {"validate", "stray"}, {"destroy", "stray"}, {"init", "stray"}, {"gen", "stray"}, {"eval", "stray"},
		{"fmt", "stray"}, {"version", "stray"}, {"drift", "stray"}, {"mcp", "stray"}, {"providers", "stray"},
		{"providers", "lock", "stray"}, {"state", "stray"}, {"state", "list", "stray"}, {"state", "log", "stray"},
		{"state", "locks", "stray"}, {"state", "graph", "stray"},
	} {
		text, code := runPravo(t, dir, args...)
		if code != int(engine.ExitError) || !strings.Contains(text, "stray") {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
		}
	}
	text, _ := runPravo(t, dir, "plan", "-refresh", "false")
	if !strings.Contains(text, `takes no arguments, but got "false"`) || !strings.Contains(text, "-refresh=false") {
		t.Errorf("the value of -refresh after a space is explained:\n%s", text)
	}
	text, code := runPravo(t, dir, "apply", "-refresh", "false")
	if code != int(engine.ExitError) || !strings.Contains(text, `"false" is not a plan file`) || !strings.Contains(text, "-refresh=false") {
		t.Errorf("apply -refresh false: exit %d:\n%s", code, text)
	}
	text, _ = runPravo(t, dir, "providers", "shema")
	if !strings.Contains(text, `unknown command "shema" for "pravo providers"`) {
		t.Errorf("a misspelt subcommand:\n%s", text)
	}
}

// A command whose usage names no argument must reject one: the rule lives on
// each command, so a new command that forgets it fails here.
func TestACommandWithoutArgumentsRejectsOne(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.Name() == "pravo" || c.Name() == "mod" || c.Name() == "help" || c.Name() == "completion" {
			return // mod hands its arguments to CUE
		}
		use := c.Use
		if strings.ContainsAny(use, "<[") {
			return
		}
		if c.Args == nil || c.Args(c, []string{"stray"}) == nil {
			t.Errorf("%q takes no arguments and accepts one", c.CommandPath())
		}
	}
	walk(newRootCmd())
}

func TestReadOnlyCommandsCreateNoState(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	for _, args := range [][]string{
		{"eval"}, {"output"}, {"providers"}, {"validate"},
		{"state", "list"}, {"state", "log"}, {"state", "locks"}, {"state", "graph"}, {"state", "query", "len(resources)"},
		{"protect", "status"},
	} {
		if text, code := runPravo(t, dir, args...); code != 0 {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
		}
	}
	for _, args := range [][]string{{"state", "show", "a"}, {"state", "dependents", "a"}} {
		if text, code := runPravo(t, dir, args...); code != int(engine.ExitError) || !strings.Contains(text, "a is not in state") {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
		}
	}
	if got := entries(t, dir); strings.Join(got, ",") != "cue.mod,main.cue" {
		t.Errorf("the directory holds %v; reading the state must not create it", got)
	}
	if text, _ := runPravo(t, dir, "state", "query", "len(resources)"); strings.TrimSpace(text) != "0" {
		t.Errorf("no state reads as an empty one: %q", text)
	}
	if text, _ := runPravo(t, dir, "state", "locks"); !strings.Contains(text, "No locks") {
		t.Errorf("no state holds no locks: %q", text)
	}
}

// A state that exists is read as it is.
func TestReadOnlyOpenReadsAnExistingState(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "bucket", Mode: "managed", Type: "t_bucket", Name: "bucket", Attributes: []byte(`{"id":"b1"}`)}, ""); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, dir, "state", "list"); code != 0 || strings.TrimSpace(text) != "bucket" {
		t.Errorf("state list: exit %d:\n%s", code, text)
	}
	root, err := config.Load(dir, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ro, err := openStateReadOnly(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if ro.Get("bucket") == nil {
		t.Error("the node is not read")
	}
	// Anything but the metadata is refused, so a command that should only look
	// cannot change the state without being noticed.
	if err := ro.Set(&state.Resource{Addr: "other", Mode: "managed", Type: "t_bucket", Name: "other", Attributes: []byte(`{}`)}, ""); err == nil || !strings.Contains(err.Error(), "open for reading") {
		t.Errorf("a write through the read-only store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.state", "resources", "other.json")); err == nil {
		t.Error("the write reached the disk")
	}
}

func TestStateCommandsTakeTags(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": `package estate

import "pravo.dev/pravo"

env: *"dev" | "prod" @tag(env)

$settings: pravo.#Settings & {state: path: "state/\(env)"}
`})
	// pravo.dev/pravo is generated by init.
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	st, err := state.Open(filepath.Join(dir, "state", "prod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "bucket", Mode: "managed", Type: "t_bucket", Name: "bucket", Attributes: []byte(`{"id":"b1"}`)}, ""); err != nil {
		t.Fatal(err)
	}
	if text, _ := runPravo(t, dir, "state", "list"); strings.TrimSpace(text) != "" {
		t.Errorf("the default environment holds nothing:\n%s", text)
	}
	for _, args := range [][]string{
		{"state", "list", "-t", "env=prod"},
		{"state", "-t", "env=prod", "list"},
		{"state", "list", "--tag", "env=prod"},
		{"state", "list", "--tag=env=prod"},
	} {
		if text, code := runPravo(t, dir, args...); code != 0 || strings.TrimSpace(text) != "bucket" {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
		}
	}
	for _, sub := range [][]string{{"show", "bucket"}, {"log"}, {"locks"}, {"graph"}, {"dependents", "bucket"}, {"query", "len(resources)"}} {
		args := append(append([]string{"state"}, sub...), "-t", "env=prod")
		if text, code := runPravo(t, dir, args...); code != 0 {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
		}
	}
	for _, sub := range [][]string{{"mv", "bucket", "vault"}, {"unlock", "vault"}, {"rm", "vault"}} {
		args := append(append([]string{"state"}, sub...), "-t", "env=prod")
		if text, code := runPravo(t, dir, args...); code != 0 {
			t.Errorf("pravo %s: exit %d:\n%s", strings.Join(args, " "), code, text)
		}
	}
	// The system variables flag is accepted as well.
	if text, code := runPravo(t, dir, "state", "list", "-T", "-t", "env=prod"); code != 0 {
		t.Errorf("-T: exit %d:\n%s", code, text)
	}
}

// The state of a root may be where a variable says: the $settings block reads
// a pravo.#Env, also in init, which reads it before the packages exist.
func TestSettingsReadAnEnvironmentVariable(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": `package estate

import "pravo.dev/pravo"

where: pravo.#Env & {name: "PRAVO_TEST_STATE_DIR", default: "state/dev"}

$settings: pravo.#Settings & {state: path: where.value}
`})
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	st, err := state.Open(filepath.Join(dir, "state", "prod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "bucket", Mode: "managed", Type: "t_bucket", Name: "bucket", Attributes: []byte(`{"id":"b1"}`)}, ""); err != nil {
		t.Fatal(err)
	}
	if text, _ := runPravo(t, dir, "state", "list"); strings.TrimSpace(text) != "" {
		t.Errorf("the default location holds nothing:\n%s", text)
	}
	t.Setenv("PRAVO_TEST_STATE_DIR", "state/prod")
	if text, code := runPravo(t, dir, "state", "list"); code != 0 || strings.TrimSpace(text) != "bucket" {
		t.Errorf("state list with the variable set: exit %d:\n%s", code, text)
	}
}

// pravo fmt must have nothing to do right after pravo init wrote the lock.
func TestLockFileIsWrittenFormatted(t *testing.T) {
	dir := t.TempDir()
	lock := &engine.Lock{
		Providers: map[string]*engine.LockEntry{
			"registry.opentofu.org/hashicorp/random": {Version: "3.9.1", Constraints: ">= 3.0", Hashes: map[string]string{"darwin_arm64": "sha256:aa", "linux_amd64": "sha256:bb"}},
			"registry.opentofu.org/hashicorp/local":  {Version: "2.9.1"},
		},
		Modules: map[string]*engine.ModuleEntry{"github.com/acme/vpc@v1": {Version: "v1.2.0", Hash: "sha256:cc"}},
	}
	if err := writeLock(lock, dir); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(dir, engine.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src, formatted) {
		t.Errorf("the lock file is not in the layout of pravo fmt:\n%s\nformatted:\n%s", src, formatted)
	}
	if !strings.Contains(string(src), "constraints: \">= 3.0\"") || !strings.Contains(string(src), "version:     \"3.9.1\"") {
		t.Errorf("the values are aligned:\n%s", src)
	}
	back, err := engine.ReadLock(dir)
	if err != nil || back.Providers["registry.opentofu.org/hashicorp/random"].Constraints != ">= 3.0" || back.Modules["github.com/acme/vpc@v1"].Hash != "sha256:cc" {
		t.Errorf("the formatted file reads back as %+v, %v", back, err)
	}
	if text, code := runPravo(t, dir, "fmt"); code != 0 || text != "" {
		t.Errorf("fmt after the lock was written: exit %d, printed %q", code, text)
	}
}

// init regenerates the packages it warns about elsewhere, so it does not warn.
func TestInitDoesNotWarnAboutSchemasItRegenerates(t *testing.T) {
	dir := specDir(t, map[string]string{
		"main.cue": "package estate\n\nimport dev \"pravo.dev/providers/x/dev\"\n\n$dev: dev.#Provider & {$protocol: \"tfplugin+reattach\", $address: \"tcp://127.0.0.1:4711\"}\n",
		// A package made from another version than the one selected.
		"cue.mod/gen/pravo.dev/providers/x/dev/dev_gen.cue": "// Provider registry.opentofu.org/x/dev v0.0.1\npackage dev\n",
	})
	root, err := config.LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	stdout := out
	defer func() { out = stdout }()
	for name, opts := range map[string]sessionOptions{
		"another command": {root: root},
		"init and gen":    {root: root, regenerates: true},
	} {
		var buf bytes.Buffer
		out = ui.NewPrinter(&buf, true)
		s, err := newSession(context.Background(), opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s.close()
		warned := strings.Contains(buf.String(), "made from a different version")
		if warned == opts.regenerates {
			t.Errorf("%s: warned = %v, output %q", name, warned, buf.String())
		}
	}
}

// A saved plan whose only change is an output records it, once. The plan shows
// the new value, and the apply of the file writes it.
func TestSavedPlanOfAnOutputIsApplied(t *testing.T) {
	spec := "package estate\n\nimport \"pravo.dev/pravo\"\n\nlabel: *\"a\" | string @tag(label)\n\nshown: pravo.#Output & {value: label}\n"
	dir := specDir(t, map[string]string{"main.cue": spec})
	for _, args := range [][]string{{"init"}, {"apply", "--auto-approve"}} {
		if text, code := runPravo(t, dir, args...); code != 0 {
			t.Fatalf("pravo %s exited %d:\n%s", strings.Join(args, " "), code, text)
		}
	}
	if text, code := runPravo(t, dir, "plan", "-t", "label=b", "--out", "change.plan"); code != 0 || !strings.Contains(text, `shown: "b"`) {
		t.Fatalf("plan --out exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "apply", "change.plan")
	if code != 0 || !strings.Contains(text, "Apply complete!") || !strings.Contains(text, "1 output, 1 changed (shown).") {
		t.Fatalf("apply of the file exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "output", "shown"); code != 0 || strings.TrimSpace(text) != `"b"` {
		t.Fatalf("output exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "apply", "change.plan"); code != 0 || !strings.Contains(text, "nothing left to apply") {
		t.Fatalf("a second apply of the file exited %d:\n%s", code, text)
	}
}

// An apply says how many outputs there are and which changed. It does not print
// their values: the log of an apply is kept.
func TestApplySummarisesOutputsWithoutTheirValues(t *testing.T) {
	spec := "package estate\n\nimport \"pravo.dev/pravo\"\n\nlabel: *\"a-value\" | string @tag(label)\n\nshown: pravo.#Output & {value: label}\nsteady: pravo.#Output & {value: \"unchanging\"}\n"
	dir := specDir(t, map[string]string{"main.cue": spec})
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	// A plan that only records outputs is applied from a file.
	if text, code := runPravo(t, dir, "plan", "--out", "first.plan"); code != 0 {
		t.Fatalf("plan --out exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "apply", "first.plan")
	if code != 0 || !strings.Contains(text, `2 outputs, 2 changed (shown, steady). See them with "pravo output" or "pravo serve".`) {
		t.Fatalf("first apply exited %d:\n%s", code, text)
	}
	after := text[strings.Index(text, "Apply complete!"):]
	if strings.Contains(after, "a-value") || strings.Contains(after, "unchanging") || strings.Contains(after, "Outputs:") {
		t.Errorf("the apply printed output values after it finished:\n%s", after)
	}
	if text, code := runPravo(t, dir, "plan", "-t", "label=b-value", "--out", "second.plan"); code != 0 {
		t.Fatalf("plan --out exited %d:\n%s", code, text)
	}
	text, code = runPravo(t, dir, "apply", "second.plan")
	if code != 0 || !strings.Contains(text, "2 outputs, 1 changed (shown).") {
		t.Fatalf("second apply exited %d:\n%s", code, text)
	}
}

// The summary of a drift --due run agrees in number with the counts it states.
func TestDueSummaryAgreesInNumber(t *testing.T) {
	for _, c := range []struct {
		read, records, due int
		want               string
	}{
		{0, 0, 0, "0 of 0 records read."},
		{1, 1, 1, "1 of 1 record read."},
		{2, 2, 2, "2 of 2 records read."},
		{5, 21, 21, "5 of 21 records read; 16 more are older than their refresh age."},
		{2, 4, 3, "2 of 4 records read; 1 more is older than its refresh age."},
	} {
		rep := &engine.DueReport{Records: c.records, Due: c.due, Read: make([]string, c.read)}
		if got := dueSummary(rep); got != c.want {
			t.Errorf("read %d of %d, %d due: %q, want %q", c.read, c.records, c.due, got, c.want)
		}
	}
}
