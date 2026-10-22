package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// commandEnv is the environment of every command: the caller's, without the variables
// that change what pravo prints or records, and without colour.
func commandEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "PRAVO_LOG", "PRAVO_ACTOR", "PRAVO_REASON", "NO_COLOR":
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
	cmd.Env = commandEnv(os.Environ())
	cmd.WaitDelay = 3 * time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	res := result{Out: buf.String()}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.Exit = ee.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("pravo %s: %w", strings.Join(args, " "), err)
	}
	return res, nil
}

// mustRun is run for a command that has to exit with one of the statuses.
func (r *pravoRunner) mustRun(ctx context.Context, dir string, ok []int, args ...string) (result, error) {
	res, err := r.run(ctx, dir, args...)
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
	// the stored situation.
	Plans map[string]string
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
	if want := fmt.Sprintf("Resources: %d added", sc.Resources); !strings.Contains(res.Out, want) {
		return nil, fmt.Errorf("scenario %s: expected %q in the apply output, got:\n%s", sc.Name, want, indent(lastLines(res.Out, 6)))
	}
	return &workspace{Dir: dir, Scenario: sc, Situation: situationClean}, nil
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
	saved := map[string][]byte{}
	for _, e := range sp.Edits {
		if _, ok := saved[e.File]; ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(e.File)))
		if err != nil {
			return "", err
		}
		saved[e.File] = data
	}
	restore := func() error {
		for file, data := range saved {
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(file)), data, 0o644); err != nil {
				return err
			}
		}
		return nil
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
