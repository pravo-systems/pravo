package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// planFile is the saved plan of the changed situation.
const planFile = "change.plan"

// pravoRunner runs the pravo binary.
type pravoRunner struct {
	Bin string
	Log io.Writer
	// Timeout bounds one command.
	Timeout time.Duration
}

// result is what a command did.
type result struct {
	Out  string
	Exit int
}

// commandEnv is the environment of every command: the caller's, without the
// variables that change what pravo prints, records or reads, and without
// colour. A trace that the caller started (TRACEPARENT) would decide whether
// pravo records its runs, and an identity or a passphrase of the caller would
// open a state that the shots seal to people of their own.
func commandEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "PRAVO_LOG", "PRAVO_ACTOR", "PRAVO_REASON", "NO_COLOR",
			"PRAVO_TRACES", "PRAVO_TRACES_NET", "PRAVO_TRACE_DIR",
			"PRAVO_CONFIG_DIR", "PRAVO_IDENTITY", "PRAVO_IDENTITY_DIR", "PRAVO_IDENTITY_PASSPHRASE", "PRAVO_PASSPHRASE", "PRAVO_ACCESS_DIR",
			"TRACEPARENT", "TRACESTATE":
			continue
		}
		if strings.HasPrefix(name, "OTEL_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "NO_COLOR=1")
}

// run runs pravo in dir with the arguments. An exit status other than 0 is not
// an error here: the caller decides. An error is a command that could not run
// or did not finish.
func (r *pravoRunner) run(ctx context.Context, dir string, args ...string) (result, error) {
	return r.runEnv(ctx, dir, nil, args...)
}

// runEnv is run with variables added to the environment of the command.
func (r *pravoRunner) runEnv(ctx context.Context, dir string, env []string, args ...string) (result, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if r.Log != nil {
		fmt.Fprintf(r.Log, "  pravo %s\n", strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = dir
	cmd.Env = append(commandEnv(os.Environ()), env...)
	cmd.WaitDelay = 3 * time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	res := result{Out: buf.String()}
	if err != nil {
		ee := &exec.ExitError{}
		if errors.As(err, &ee) {
			res.Exit = ee.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("pravo %s: %w", strings.Join(args, " "), err)
	}
	return res, nil
}

// mustRun is run for a command that has to exit with one of the statuses.
func (r *pravoRunner) mustRun(ctx context.Context, dir string, ok []int, args ...string) (result, error) {
	return r.mustRunEnv(ctx, dir, nil, ok, args...)
}

// mustRunEnv is mustRun with variables added to the environment.
func (r *pravoRunner) mustRunEnv(ctx context.Context, dir string, env []string, ok []int, args ...string) (result, error) {
	res, err := r.runEnv(ctx, dir, env, args...)
	if err != nil {
		return res, err
	}
	for _, code := range ok {
		if res.Exit == code {
			return res, nil
		}
	}
	return res, fmt.Errorf("pravo %s exited with %d:\n%s", strings.Join(args, " "), res.Exit, indent(strings.TrimSpace(res.Out)))
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// A workspace is a copy of a scenario that has been through the steps of a
// situation.
type workspace struct {
	Dir       string
	Scenario  scenario
	Situation string
	// Plan is the saved plan, for the plan situation.
	Plan string
	// Plans are the ids of the stored plans by the actor that made each, for
	// the stored situation, and by the label of the step, for the team.
	Plans map[string]string
	// Traces are the ids of the recorded runs of the team by their label.
	Traces map[string]string
	// Env is what a command in the workspace needs in its environment, and
	// the viewer with it: the credentials of the store, the objects of the fake
	// provider and the identity of the person who reads the state.
	Env []string
}

// prepareClean copies the scenario to dir, initialises it and applies it.
func prepareClean(ctx context.Context, r *pravoRunner, sc scenario, dir string) (*workspace, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := copyScenario(sc.Name, dir); err != nil {
		return nil, err
	}
	if _, err := r.mustRun(ctx, dir, []int{0}, "init"); err != nil {
		return nil, err
	}
	res, err := r.mustRun(ctx, dir, []int{0}, "apply", "-auto-approve")
	if err != nil {
		return nil, err
	}
	if err := checkAdded(sc, res.Out); err != nil {
		return nil, err
	}
	return &workspace{Dir: dir, Scenario: sc, Situation: situationClean}, nil
}

// checkAdded reads the apply output for the number of resources the scenario
// declares.
func checkAdded(sc scenario, out string) error {
	if want := fmt.Sprintf("Resources: %d added", sc.Resources); !strings.Contains(out, want) {
		return fmt.Errorf("scenario %s: expected %q in the apply output, got:\n%s", sc.Name, want, indent(lastLines(out, 6)))
	}
	return nil
}

// prepareChanged is prepareClean followed by the changes of the scenario: the
// files on disk change and pravo drift marks the records, the specification is
// edited without an apply, and pravo plan saves a plan.
func prepareChanged(ctx context.Context, r *pravoRunner, sc scenario, dir string) (*workspace, error) {
	if !sc.hasChanges() {
		return nil, fmt.Errorf("scenario %s has no changed situation", sc.Name)
	}
	w, err := prepareClean(ctx, r, sc, dir)
	if err != nil {
		return nil, err
	}
	if err := applyDisk(dir, sc.Disk); err != nil {
		return nil, err
	}
	// Exit status 2 says that drift was found.
	res, err := r.mustRun(ctx, dir, []int{0, 2}, "drift")
	if err != nil {
		return nil, err
	}
	reported := driftAddrs(res.Out)
	for _, c := range sc.Disk {
		if !reported[c.Addr] {
			return nil, fmt.Errorf("pravo drift did not report %s; it reported %d resources:\n%s", c.Addr, len(reported), indent(lastLines(res.Out, 8)))
		}
	}
	if err := applyEdits(dir, sc.Edits); err != nil {
		return nil, err
	}
	if _, err := r.mustRun(ctx, dir, []int{0}, "plan", "--out", planFile); err != nil {
		return nil, err
	}
	w.Situation = situationChanged
	w.Plan = filepath.Join(dir, planFile)
	return w, nil
}

// prepareStored is prepareClean followed by the stored plans of the scenario.
// For each, the specification is edited, pravo plan --store puts the plan in the
// state as its actor, and the specification is put back, so that the estate is
// as it was applied and each plan is a plan that someone made earlier, against
// the same state. The times of the plan are then moved into the past.
func prepareStored(ctx context.Context, r *pravoRunner, sc scenario, dir string) (*workspace, error) {
	if !sc.hasStored() {
		return nil, fmt.Errorf("scenario %s has no stored plans", sc.Name)
	}
	w, err := prepareClean(ctx, r, sc, dir)
	if err != nil {
		return nil, err
	}
	w.Plans = map[string]string{}
	now := time.Now()
	for _, sp := range sc.Stored {
		id, err := storePlan(ctx, r, dir, sp, w.Plans)
		if err != nil {
			return nil, fmt.Errorf("the plan of %s: %w", sp.Actor, err)
		}
		w.Plans[sp.Actor] = id
		created := now.Add(-sp.Age)
		expires := created.Add(7 * 24 * time.Hour)
		if sp.Expired && !expires.Before(now) {
			return nil, fmt.Errorf("the plan of %s is not old enough to have expired: %s old, the time to live is 7 days", sp.Actor, sp.Age)
		}
		if err := backdate(dir, id, created, expires); err != nil {
			return nil, fmt.Errorf("the plan of %s: %w", sp.Actor, err)
		}
	}
	w.Situation = situationStored
	return w, nil
}

// storePlan makes one stored plan from the edits of sp and returns its id, the
// one of the plans that is new in the state and that sp.Actor made. The files that
// the edits touch are put back whatever happens.
func storePlan(ctx context.Context, r *pravoRunner, dir string, sp storedPlan, have map[string]string) (string, error) {
	restore, err := saveFiles(dir, sp.Edits)
	if err != nil {
		return "", err
	}
	if err := applyEdits(dir, sp.Edits); err != nil {
		restore()
		return "", err
	}
	_, runErr := r.mustRun(ctx, dir, []int{0}, "plan", "--store", "--actor", sp.Actor, "--reason", sp.Reason)
	if err := restore(); err != nil {
		return "", err
	}
	if runErr != nil {
		return "", runErr
	}
	known := map[string]bool{}
	for _, id := range have {
		known[id] = true
	}
	metas, err := filepath.Glob(filepath.Join(dir, "pravo.state", "plans", "*.meta.json"))
	if err != nil {
		return "", err
	}
	for _, m := range metas {
		id := strings.TrimSuffix(filepath.Base(m), ".meta.json")
		if known[id] {
			continue
		}
		var meta struct {
			Author string `json:"author"`
		}
		data, err := os.ReadFile(m)
		if err != nil {
			return "", err
		}
		if json.Unmarshal(data, &meta) == nil && meta.Author == sp.Actor {
			return id, nil
		}
	}
	return "", fmt.Errorf("pravo plan --store stored no plan of %s: %d plans are in the state", sp.Actor, len(metas))
}

// saveFiles reads the files that the edits touch and returns a function that
// writes them back.
func saveFiles(dir string, edits []edit) (func() error, error) {
	saved := map[string][]byte{}
	for _, e := range edits {
		if _, ok := saved[e.File]; ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(e.File)))
		if err != nil {
			return nil, err
		}
		saved[e.File] = data
	}
	return func() error {
		for file, data := range saved {
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(file)), data, 0o644); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// backdate moves the times of a stored plan: when it was made and when it
// lapses. A command cannot store a plan in the past, and the picture of a
// plan that is hours old, or has expired, is made from one that was made a
// moment ago.
func backdate(dir, id string, created, expires time.Time) error {
	stamp := func(t time.Time) json.RawMessage {
		b, _ := json.Marshal(t.UTC().Format(time.RFC3339Nano))
		return b
	}
	for kind, set := range map[string]map[string]json.RawMessage{
		"plan": {"created": stamp(created)},
		"meta": {"created": stamp(created), "expires": stamp(expires)},
	} {
		p := filepath.Join(dir, "pravo.state", "plans", id+"."+kind+".json")
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		for k, v := range set {
			doc[k] = v
		}
		out, err := json.MarshalIndent(doc, "", " ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(p, append(out, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// teamDeps are what the team situation takes from the program: the server
// that holds the bucket and the provider that lists objects.
type teamDeps struct {
	Store        *store
	FakeProvider string
}

// The placeholders of the files of a team scenario, for what the program
// decides when it runs.
const (
	storePlaceholder    = "@FAKES3@"
	providerPlaceholder = "@FAKEPROVIDER@"
)

// passphrase seals the identities of the team. The pictures never show it.
const passphrase = "an example passphrase"

// prepareTeam makes the team situation: the scenario with its state in the
// store, three identities with roles in the readers file, the state sealed to
// them and the roles pinned, the first apply, and then the steps of the team,
// each by its person. Every command that does work is recorded as a run.
func prepareTeam(ctx context.Context, r *pravoRunner, sc scenario, dir string, deps teamDeps) (*workspace, error) {
	t := sc.Team
	if t == nil {
		return nil, fmt.Errorf("scenario %s has no team situation", sc.Name)
	}
	if len(t.People) == 0 || t.People[0].Role != "admin" {
		return nil, fmt.Errorf("scenario %s: the first person of the team is the admin who seals the state", sc.Name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := copyScenario(sc.Name, dir); err != nil {
		return nil, err
	}
	if err := fillPlaceholders(dir, map[string]string{storePlaceholder: deps.Store.Endpoint, providerPlaceholder: deps.FakeProvider}); err != nil {
		return nil, err
	}
	demo := filepath.Join(filepath.Dir(dir), sc.Name+"-objects")
	if err := writeCatalog(demo, t.Catalog); err != nil {
		return nil, err
	}
	w := &workspace{Dir: dir, Scenario: sc, Situation: situationTeam, Plans: map[string]string{}, Traces: map[string]string{}}
	w.Env = append(append([]string(nil), storeEnv...), "FAKEPROVIDER_DIR="+demo, "PRAVO_PASSPHRASE="+passphrase)
	as := func(name string) []string {
		return append(append([]string(nil), w.Env...), "PRAVO_CONFIG_DIR="+filepath.Join(dir, "people", name))
	}
	// The viewer reads as the admin.
	w.Env = as(t.People[0].Name)
	admin := t.People[0].Name

	// The identities, and the readers file with their roles.
	var readers []string
	for _, p := range t.People {
		res, err := r.mustRunEnv(ctx, dir, as(p.Name), []int{0}, "id", "new", "--name", p.Name)
		if err != nil {
			return nil, err
		}
		line := readerLine(res.Out, p.Name)
		if line == "" {
			return nil, fmt.Errorf("pravo id new printed no line for the readers file:\n%s", indent(strings.TrimSpace(res.Out)))
		}
		readers = append(readers, line+" role:"+p.Role)
	}
	access := filepath.Join(dir, ".pravo", "access")
	if err := os.MkdirAll(access, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(access, "readers.txt"), []byte(strings.Join(readers, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	for _, args := range [][]string{
		{"init"},
		{"protect", "init", "--encrypt", "--yes"},
		{"protect", "roles", "--yes"},
	} {
		if _, err := r.mustRunEnv(ctx, dir, as(admin), []int{0}, args...); err != nil {
			return nil, err
		}
	}

	// The first apply, by the admin, and the work of the team.
	res, err := w.traced(ctx, r, "apply", as(admin), "apply", "-auto-approve", "--actor", admin, "--reason", "first apply of the "+sc.Base)
	if err != nil {
		return nil, err
	}
	if err := checkAdded(sc, res.Out); err != nil {
		return nil, err
	}
	for _, st := range t.Steps {
		if err := w.step(ctx, r, st, as); err != nil {
			return nil, fmt.Errorf("the step %s of %s: %w", st.Kind, st.Actor, err)
		}
	}
	if err := applyEdits(dir, t.After); err != nil {
		return nil, err
	}
	return w, nil
}

// step runs one step of the team.
func (w *workspace) step(ctx context.Context, r *pravoRunner, st teamStep, as func(string) []string) error {
	switch st.Kind {
	case "discover":
		_, err := w.traced(ctx, r, "discover", as(st.Actor), "discover", "--actor", st.Actor)
		return err
	case "change", "propose":
		if st.Label == "" {
			return fmt.Errorf("the step has no label")
		}
		restore, err := saveFiles(w.Dir, st.Edits)
		if err != nil {
			return err
		}
		if err := applyEdits(w.Dir, st.Edits); err != nil {
			return err
		}
		res, err := w.traced(ctx, r, "plan:"+st.Label, as(st.Actor), "plan", "--store", "--actor", st.Actor, "--reason", st.Reason)
		if st.Kind == "propose" {
			if rerr := restore(); err == nil {
				err = rerr
			}
		}
		if err != nil {
			return err
		}
		id := storedID(res.Out)
		if id == "" {
			return fmt.Errorf("pravo plan --store named no plan:\n%s", indent(strings.TrimSpace(res.Out)))
		}
		w.Plans[st.Label] = id
		if st.ApproveBy != "" {
			if _, err := r.mustRunEnv(ctx, w.Dir, as(st.ApproveBy), []int{0}, "plan", "approve", id, "--actor", st.ApproveBy, "--reason", "reviewed: "+st.Reason); err != nil {
				return err
			}
		}
		if st.Kind == "change" {
			if _, err := w.traced(ctx, r, "apply:"+st.Label, as(st.Actor), "apply", id, "-auto-approve", "--actor", st.Actor, "--reason", st.Reason); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown kind %q", st.Kind)
}

// traced runs a command with the record of runs on and keeps the id of the
// run under label.
func (w *workspace) traced(ctx context.Context, r *pravoRunner, label string, env []string, args ...string) (result, error) {
	before, _ := filepath.Glob(filepath.Join(w.Dir, ".pravo", "traces", "*.json"))
	res, err := r.mustRunEnv(ctx, w.Dir, append(append([]string(nil), env...), "PRAVO_TRACES=on"), []int{0}, args...)
	if err != nil {
		return res, err
	}
	after, _ := filepath.Glob(filepath.Join(w.Dir, ".pravo", "traces", "*.json"))
	seen := map[string]bool{}
	for _, f := range before {
		seen[f] = true
	}
	var fresh []string
	for _, f := range after {
		if !seen[f] {
			fresh = append(fresh, f)
		}
	}
	if len(fresh) != 1 {
		return res, fmt.Errorf("pravo %s left %d recorded runs, not one", strings.Join(args, " "), len(fresh))
	}
	w.Traces[label] = strings.TrimSuffix(filepath.Base(fresh[0]), ".json")
	return res, nil
}

// fillPlaceholders writes the values into the CUE files at the top of dir.
// A value is written as the contents of a CUE string.
func fillPlaceholders(dir string, values map[string]string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.cue"))
	if err != nil {
		return err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		text := string(data)
		for k, v := range values {
			q := strconv.Quote(v)
			text = strings.ReplaceAll(text, k, q[1:len(q)-1])
		}
		if text != string(data) {
			if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeCatalog writes the objects of the fake provider.
func writeCatalog(dir string, objs []catalogObject) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if objs == nil {
		objs = []catalogObject{}
	}
	data, err := json.MarshalIndent(objs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "catalog.json"), append(data, '\n'), 0o644)
}

// readerLine finds the line for the readers file in the output of pravo id
// new: the last line, which starts with the name.
func readerLine(out, name string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, name+" ") {
			return l
		}
	}
	return ""
}

var storedIDRe = regexp.MustCompile(`Stored the plan as (plan-[0-9a-f]+)`)

// storedID is the id that pravo plan --store printed.
func storedID(out string) string {
	if m := storedIDRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

var driftLineRe = regexp.MustCompile(`(?m)^  ([^\s:]+): \S+$`)

// driftAddrs reads the addresses that pravo drift lists: lines of the form
// "  api.cache.config: create".
func driftAddrs(out string) map[string]bool {
	m := map[string]bool{}
	for _, sub := range driftLineRe.FindAllStringSubmatch(out, -1) {
		m[sub[1]] = true
	}
	return m
}

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
