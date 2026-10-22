package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/registry"
)

var storedID = regexp.MustCompile(`plan-[0-9a-f]{8}`)

// storePlanAs stores the plan of dir as an actor and returns the id.
func storePlanAs(t *testing.T, dir, actor string, extra ...string) string {
	t.Helper()
	args := append([]string{"--actor", actor, "plan", "--store"}, extra...)
	text, code := runPravo(t, dir, args...)
	if code != 0 {
		t.Fatalf("plan --store exited %d:\n%s", code, text)
	}
	id := storedID.FindString(text[strings.Index(text, "Stored the plan as"):])
	if id == "" {
		t.Fatalf("no id in:\n%s", text)
	}
	return id
}

// localRoot is a root with one file to create through the local provider, for
// the tests that load a plan against real schemas. It skips when the provider
// is not at hand, so that the test runs without a network.
func localRoot(t *testing.T, spec string) string {
	t.Helper()
	c := registry.NewClient()
	src, _ := registry.ParseSource("hashicorp/local")
	if _, ok := c.FindInstalled(src, "2.9.1"); !ok {
		t.Skip("the local provider is not installed")
	}
	if _, err := os.Stat(c.SchemaCachePath(src, "2.9.1")); err != nil {
		t.Skip("the schema of the local provider is not cached")
	}
	if spec == "" {
		spec = "hello: $local.#Resource.local_file & {filename: \"hello.txt\", content: \"hello\\n\"}\n"
	}
	dir := specDir(t, map[string]string{"main.cue": "package infra\n\nimport loc \"pravo.dev/providers/hashicorp/local\"\n\n$local: loc.#Provider\n\n" + spec})
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Skipf("pravo init exited %d: %s", code, text)
	}
	return dir
}

func planObjects(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, "pravo.state", "plans"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestPlanDoesNotStoreAnythingUnlessAskedTo(t *testing.T) {
	dir, _ := pendingDir(t)
	text, code := runPravo(t, dir, "plan")
	if code != 0 || strings.Contains(text, "Stored") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	if got := planObjects(t, dir); len(got) != 0 {
		t.Errorf("plan wrote %v", got)
	}
	if text, code := runPravo(t, dir, "plan", "--ttl", "1h"); code != 1 || !strings.Contains(text, "--ttl applies to --store") {
		t.Errorf("--ttl without --store: exit %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "plan", "--store", "--ttl", "soon"); code != 1 || !strings.Contains(text, "want a length of time such as 90m, 36h or 7d") {
		t.Errorf("bad --ttl: exit %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "plan", "--store", "--ttl", "0"); code != 1 || !strings.Contains(text, "above zero") {
		t.Errorf("zero --ttl: exit %d:\n%s", code, text)
	}
	if got := planObjects(t, dir); len(got) != 0 {
		t.Errorf("a refused plan wrote %v", got)
	}
}

func TestAPlanThatHasNoChangesIsNotStored(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"})
	text, code := runPravo(t, dir, "plan", "--store")
	if code != 0 || !strings.Contains(text, "Nothing to store") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	if got := planObjects(t, dir); len(got) != 0 {
		t.Errorf("stored %v", got)
	}
}

func TestStoreListShowAndApplyAPlan(t *testing.T) {
	dir := localRoot(t, "")
	id := storePlanAs(t, dir, "alice", "--reason", "say hello")

	text, code := runPravo(t, dir, "plan", "list")
	if code != 0 || !strings.Contains(text, id) || !strings.Contains(text, "pending") || !strings.Contains(text, "alice") || !strings.Contains(text, "+1 ~0 -0") || !strings.Contains(text, "expires in 7 days") {
		t.Errorf("list exited %d:\n%s", code, text)
	}
	if len(planObjects(t, dir)) != 2 {
		t.Errorf("objects %v", planObjects(t, dir))
	}

	// show prints the plan, by the id and by a prefix, with who made it.
	for _, args := range [][]string{{"plan", "show", id}, {"show", id}, {"show", id[:len("plan-")+6]}} {
		text, code = runPravo(t, dir, args...)
		if code != 0 || !strings.Contains(text, "Stored plan "+id+" by alice") || !strings.Contains(text, "say hello") || !strings.Contains(text, "pravo apply "+id) || !strings.Contains(text, "1 to add") {
			t.Errorf("%v exited %d:\n%s", args, code, text)
		}
	}

	// pull gives the same plan as a file.
	file := filepath.Join(t.TempDir(), "pulled.plan")
	if text, code = runPravo(t, dir, "plan", "pull", id, file); code != 0 || !strings.Contains(text, "Wrote "+id) {
		t.Fatalf("pull exited %d:\n%s", code, text)
	}
	if pf, err := engine.ReadPlanFile(file); err != nil || len(pf.Changes) != 1 || pf.Changes[0].Addr != "hello" {
		t.Fatalf("pulled file %+v, %v", pf, err)
	}
	if text, code = runPravo(t, dir, "show", file); code != 0 || !strings.Contains(text, "1 to add") {
		t.Errorf("show of the pulled file exited %d:\n%s", code, text)
	}

	// apply takes the id; the plan is then applied and kept for a while.
	text, code = runPravo(t, dir, "apply", id)
	if code != 0 || !strings.Contains(text, "Apply complete!") || !strings.Contains(text, "1 added, 0 changed, 0 destroyed") {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "hello.txt")); err != nil || string(got) != "hello\n" {
		t.Errorf("hello.txt: %q %v", got, err)
	}
	if text, _ = runPravo(t, dir, "plan", "list"); !strings.Contains(text, "No stored plan can be applied") {
		t.Errorf("list after the apply:\n%s", text)
	}
	text, _ = runPravo(t, dir, "plan", "list", "--all")
	if !strings.Contains(text, id) || !strings.Contains(text, "applied") {
		t.Errorf("list --all after the apply:\n%s", text)
	}
	// A plan that was applied has nothing left to apply.
	if text, code = runPravo(t, dir, "apply", id); !(code == 0 && strings.Contains(text, "nothing left")) {
		t.Errorf("a second apply exited %d:\n%s", code, text)
	}
	// And it is collected a day later, not at once.
	if text, code = runPravo(t, dir, "plan", "gc"); code != 0 || !strings.Contains(text, "No stored plan needs to be removed.") {
		t.Errorf("gc exited %d:\n%s", code, text)
	}
}
func TestStoringAPlanWithJSONSaysWhereItWentAndEveryCommandHasJSON(t *testing.T) {
	dir, _ := pendingDir(t)
	text, code := runPravo(t, dir, "--json", "--actor", "alice", "plan", "--store")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	var doc struct {
		Summary engine.Summary `json:"summary"`
		Stored  *struct {
			ID           string `json:"id"`
			Expires      string `json:"expires"`
			ApplyCommand string `json:"apply_command"`
		} `json:"stored"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, text)
	}
	if doc.Stored == nil || !storedID.MatchString(doc.Stored.ID) || doc.Stored.ApplyCommand != "pravo apply "+doc.Stored.ID || doc.Summary.Destroy != 1 {
		t.Fatalf("document %+v", doc)
	}
	id := doc.Stored.ID

	text, code = runPravo(t, dir, "--json", "plan", "list")
	var list struct {
		Plans []struct {
			ID, Status, Author string
			Summary            engine.PlanCounts
			Changes            []engine.PlanTouch
			ApplyCommand       string `json:"apply_command"`
		}
	}
	if err := json.Unmarshal([]byte(text), &list); err != nil || code != 0 || len(list.Plans) != 1 {
		t.Fatalf("list --json: %v, exit %d\n%s", err, code, text)
	}
	if p := list.Plans[0]; p.ID != id || p.Status != "pending" || p.Author != "alice" || p.Summary.Destroy != 1 || len(p.Changes) != 1 || p.Changes[0].Addr != "cfg" || p.ApplyCommand != "pravo apply "+id {
		t.Errorf("plan %+v", p)
	}

	text, code = runPravo(t, dir, "--json", "plan", "gc", "--dry-run")
	var res engine.GCResult
	if err := json.Unmarshal([]byte(text), &res); err != nil || code != 0 || res.Looked != 1 || len(res.Removed) != 0 || !res.DryRun {
		t.Fatalf("gc --json: %v, exit %d\n%s", err, code, text)
	}

}

func TestShowAndApplyOfAStoredPlanWithJSON(t *testing.T) {
	dir := localRoot(t, "")
	id := storePlanAs(t, dir, "alice")
	text, code := runPravo(t, dir, "--json", "plan", "show", id)
	var shown struct {
		PlanID string `json:"plan_id"`
		Stored struct{ Status string }
		Plan   struct{ Summary engine.Summary }
	}
	if err := json.Unmarshal([]byte(text), &shown); err != nil || code != 0 || shown.PlanID != id || shown.Stored.Status != "pending" || shown.Plan.Summary.Add != 1 {
		t.Fatalf("show --json: %v, exit %d\n%s", err, code, text)
	}
	text, code = runPravo(t, dir, "--json", "apply", id)
	if code != 0 {
		t.Fatalf("apply --json exited %d:\n%s", code, text)
	}
	var done map[string]any
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m["phase"] == "done" && m["kind"] == "apply" {
			done = m
		}
	}
	if done == nil || done["plan"] != id || done["planfile"] != nil || done["added"] != float64(1) {
		t.Errorf("the final line of apply --json: %v\n%s", done, text)
	}
	if text, _ = runPravo(t, dir, "--json", "plan", "list"); strings.TrimSpace(text) != "{\n  \"plans\": []\n}" {
		t.Errorf("list --json of nothing:\n%s", text)
	}
}

func TestListFiltersAndWarnsAboutPlansThatChangeTheSameResources(t *testing.T) {
	dir, _ := pendingDir(t)
	first := storePlanAs(t, dir, "alice")
	text, code := runPravo(t, dir, "--actor", "bob", "plan", "--store")
	if code != 0 || !strings.Contains(text, "Warning: "+first+" by alice") || !strings.Contains(text, "also changes 1 of these resources: cfg") {
		t.Fatalf("no warning, exit %d:\n%s", code, text)
	}
	// A plan that is not stored warns too, and writes nothing.
	text, code = runPravo(t, dir, "--actor", "carol", "plan")
	if code != 0 || strings.Count(text, "Warning: plan-") != 2 {
		t.Errorf("plan exited %d:\n%s", code, text)
	}
	if got := planObjects(t, dir); len(got) != 4 {
		t.Errorf("objects %v", got)
	}
	text, _ = runPravo(t, dir, "plan", "list", "--touching", "cfg")
	if strings.Count(text, "plan-") != 2 {
		t.Errorf("touching cfg:\n%s", text)
	}
	text, _ = runPravo(t, dir, "plan", "list", "--touching", "other")
	if !strings.Contains(text, "No stored plan changes other.") {
		t.Errorf("touching other:\n%s", text)
	}
	if text, code = runPravo(t, dir, "plan", "list", "--pending", "--all"); code != 1 || !strings.Contains(text, "exclude each other") {
		t.Errorf("--pending --all: exit %d:\n%s", code, text)
	}
}

func TestApplyingOnePlanMakesTheOtherStale(t *testing.T) {
	dir := localRoot(t, "")
	first := storePlanAs(t, dir, "alice")
	second := storePlanAs(t, dir, "bob")
	if text, code := runPravo(t, dir, "apply", first); code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "apply", second)
	if code != int(engine.ExitStale) {
		t.Errorf("exit %d, want %d:\n%s", code, engine.ExitStale, text)
	}
	text, _ = runPravo(t, dir, "plan", "list", "--all")
	if !regexp.MustCompile(second + `\s+stale\s+bob`).MatchString(text) {
		t.Errorf("list --all:\n%s", text)
	}
	// The first collection marks it; a later one, an hour on, removes it. Here it is marked.
	text, code = runPravo(t, dir, "plan", "gc")
	if code != 0 || !strings.Contains(text, "1 plan can no longer be applied") || !strings.Contains(text, second) {
		t.Errorf("gc exited %d:\n%s", code, text)
	}
	if text, _ = runPravo(t, dir, "plan", "show", second); !strings.Contains(text, "can no longer be applied") {
		t.Errorf("show:\n%s", text)
	}
}
func TestAPlanThatExpiredIsNotApplied(t *testing.T) {
	dir, _ := pendingDir(t)
	id := storePlanAs(t, dir, "alice", "--ttl", "1ms")
	time.Sleep(20 * time.Millisecond)
	text, code := runPravo(t, dir, "apply", id)
	if code != int(engine.ExitStale) || !strings.Contains(text, id+" expired") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	if text, _ = runPravo(t, dir, "plan", "list"); !strings.Contains(text, "No stored plan can be applied") {
		t.Errorf("list:\n%s", text)
	}
	// gc removes it, once, and says so.
	text, code = runPravo(t, dir, "plan", "gc", "--dry-run")
	if code != 0 || !strings.Contains(text, "Would remove 1 stored plan") || !strings.Contains(text, id) {
		t.Errorf("dry run exited %d:\n%s", code, text)
	}
	if got := planObjects(t, dir); len(got) != 2 {
		t.Errorf("a dry run left %v", got)
	}
	text, code = runPravo(t, dir, "plan", "gc")
	if code != 0 || !strings.Contains(text, "Removed 1 stored plan") || !strings.Contains(text, "expired") {
		t.Errorf("gc exited %d:\n%s", code, text)
	}
	if got := planObjects(t, dir); len(got) != 0 {
		t.Errorf("gc left %v", got)
	}
	if text, code = runPravo(t, dir, "apply", id); code != 1 || !strings.Contains(text, "no such stored plan") {
		t.Errorf("apply of a collected plan exited %d:\n%s", code, text)
	}
}

func TestOlderThanAndOpportunisticCollection(t *testing.T) {
	dir, _ := pendingDir(t)
	old := storePlanAs(t, dir, "alice")
	time.Sleep(30 * time.Millisecond)
	if text, code := runPravo(t, dir, "plan", "gc", "--older-than", "1h"); code != 0 || !strings.Contains(text, "No stored plan needs to be removed.") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "plan", "gc", "--older-than", "later"); code != 1 || !strings.Contains(text, "want a length of time") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "plan", "gc", "--older-than", "10ms"); code != 0 || !strings.Contains(text, "Removed 1 stored plan") || !strings.Contains(text, old) || !strings.Contains(text, "made more than") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	// A plan that is stored removes the plans that expired.
	gone := storePlanAs(t, dir, "alice", "--ttl", "1ms")
	time.Sleep(20 * time.Millisecond)
	text, code := runPravo(t, dir, "--actor", "bob", "plan", "--store")
	if code != 0 || !strings.Contains(text, "Removed 1 stored plan that was no longer needed.") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	if text, _ = runPravo(t, dir, "plan", "list", "--all"); strings.Contains(text, gone) || strings.Count(text, "plan-") != 1 {
		t.Errorf("list:\n%s", text)
	}
}

func TestAStoredPlanNeedsTheTagsItWasMadeWith(t *testing.T) {
	dir := localRoot(t, "msg: *\"hello\" | string @tag(msg)\nhello: $local.#Resource.local_file & {filename: \"hello.txt\", content: msg}\n")
	id := storePlanAs(t, dir, "alice", "-t", "msg=hi")

	text, _ := runPravo(t, dir, "plan", "list", "-t", "msg=hi")
	if !strings.Contains(text, id) {
		t.Errorf("list:\n%s", text)
	}
	if text, _ = runPravo(t, dir, "--json", "plan", "list", "-t", "msg=hi"); !strings.Contains(text, `"apply_command": "pravo apply `+id+` -t msg=hi"`) {
		t.Errorf("list --json:\n%s", text)
	}
	text, code := runPravo(t, dir, "apply", id)
	if code != 1 || !strings.Contains(text, "was made with other -t and -T than these") || !strings.Contains(text, "pravo apply "+id+" -t msg=hi") {
		t.Errorf("apply without the tag: exit %d:\n%s", code, text)
	}
	if text, code = runPravo(t, dir, "show", id); code != 1 || !strings.Contains(text, "pravo plan show "+id+" -t msg=hi") {
		t.Errorf("show without the tag: exit %d:\n%s", code, text)
	}
	if text, code = runPravo(t, dir, "apply", id, "-t", "msg=bye"); code != 1 || !strings.Contains(text, "pravo apply "+id+" -t msg=hi") {
		t.Errorf("apply with another tag: exit %d:\n%s", code, text)
	}
	if text, code = runPravo(t, dir, "apply", id, "-t", "msg=hi"); code != 0 || !strings.Contains(text, "Apply complete!") {
		t.Errorf("apply with the tag: exit %d:\n%s", code, text)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "hello.txt")); string(got) != "hi" {
		t.Errorf("hello.txt: %q", got)
	}
}
func TestAFileWinsOverAStoredPlanOfTheSameName(t *testing.T) {
	dir := localRoot(t, "")
	id := storePlanAs(t, dir, "alice")
	// A plan file that is called like an id is a file.
	if text, code := runPravo(t, dir, "plan", "--out", id); code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "apply", id)
	if code != 0 || !strings.Contains(text, "Apply complete!") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	// The stored plan was not used: it has no progress, and it is stale now.
	text, _ = runPravo(t, dir, "plan", "list", "--all")
	if !regexp.MustCompile(id + `\s+stale`).MatchString(text) {
		t.Errorf("list:\n%s", text)
	}
}
func TestApplyOfAStoredPlanRefusesTheFlagsOfAPlanFile(t *testing.T) {
	dir := localRoot(t, "")
	id := storePlanAs(t, dir, "alice")
	for _, args := range [][]string{{"apply", id, "--replace", "hello"}, {"apply", id, "-refresh=false"}, {"apply", id, "--full"}} {
		if text, code := runPravo(t, dir, args...); code != 1 {
			t.Errorf("%v exited %d:\n%s", args, code, text)
		}
	}
	// A policy is judged against a stored plan as against a plan file.
	policy := writePolicy(t, "no-add.cue", "summary: add: 0\n")
	text, code := runPravo(t, dir, "apply", id, "--policy", policy)
	if code != int(engine.ExitRefused) {
		t.Errorf("policy: exit %d:\n%s", code, text)
	}
	if text, _ = runPravo(t, dir, "plan", "list"); !strings.Contains(text, id) {
		t.Errorf("the plan was lost:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err == nil {
		t.Error("the refused plan was applied")
	}
}
func TestPlanWithAWordThatIsNotACommand(t *testing.T) {
	dir, _ := pendingDir(t)
	text, code := runPravo(t, dir, "plan", "lst")
	if code != 1 || !strings.Contains(text, `pravo plan takes no arguments, but got "lst"`) {
		t.Errorf("exit %d:\n%s", code, text)
	}
	text, code = runPravo(t, dir, "plan", "-refresh", "false")
	if code != 1 || !strings.Contains(text, "write the value of -refresh with =, as in -refresh=false") {
		t.Errorf("exit %d:\n%s", code, text)
	}
}

func TestParseSpan(t *testing.T) {
	for in, want := range map[string]time.Duration{"90m": 90 * time.Minute, "36h": 36 * time.Hour, "7d": 7 * 24 * time.Hour, "0.5d": 12 * time.Hour, "1ms": time.Millisecond, "0": 0} {
		if got, err := parseSpan("ttl", in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"", "d", "-1d", "-5m", "soon", "1w", "1.2.3d"} {
		if _, err := parseSpan("ttl", in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

// A plan that a policy refuses is not stored, with or without --json: a stored plan
// can be applied by anyone, without the policy.
func TestAPlanThatAPolicyRefusesIsNotStored(t *testing.T) {
	dir, _ := pendingDir(t)
	policy := writePolicy(t, "no-destroy.cue", "summary: destroy: 0\n")
	for _, args := range [][]string{{"--json", "plan", "--store", "--policy", policy}, {"plan", "--store", "--policy", policy}} {
		text, code := runPravo(t, dir, args...)
		if code != int(engine.ExitRefused) {
			t.Errorf("%v exited %d:\n%s", args, code, text)
		}
		if got := planObjects(t, dir); len(got) != 0 {
			t.Errorf("%v stored %v", args, got)
		}
	}
}

// A plan that cannot be written to the file that --out names is not stored either,
// in either mode: the file is written first.
func TestAPlanThatCannotBeSavedToItsFileIsNotStored(t *testing.T) {
	dir, _ := pendingDir(t)
	missing := filepath.Join(t.TempDir(), "missing", "dir", "f.plan")
	for _, args := range [][]string{{"--json", "plan", "--store", "--out", missing}, {"plan", "--store", "--out", missing}} {
		text, code := runPravo(t, dir, args...)
		if code != 1 {
			t.Errorf("%v exited %d:\n%s", args, code, text)
		}
		if got := planObjects(t, dir); len(got) != 0 {
			t.Errorf("%v stored %v", args, got)
		}
	}
	// With a file that can be written, both are done, and the file is the stored plan.
	file := filepath.Join(t.TempDir(), "f.plan")
	text, code := runPravo(t, dir, "--json", "plan", "--store", "--out", file)
	if code != 0 || len(planObjects(t, dir)) != 2 {
		t.Fatalf("exit %d, objects %v:\n%s", code, planObjects(t, dir), text)
	}
	if _, err := engine.ReadPlanFile(file); err != nil {
		t.Error(err)
	}
}

// A plan that changes no resource, and only writes outputs or records, has no
// changes: it is not stored, and the output says so.
func TestAPlanThatOnlyWritesOutputsIsNotStored(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package infra\n\nimport \"pravo.dev/pravo\"\n\ngreeting: pravo.#Output & {value: \"hi\"}\n"})
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Skipf("pravo init exited %d: %s", code, text)
	}
	text, code := runPravo(t, dir, "plan", "--store")
	if code != 0 || !strings.Contains(text, "No changes.") || !strings.Contains(text, "Nothing to store: the plan has no changes.") || strings.Contains(text, "Stored the plan") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	if got := planObjects(t, dir); len(got) != 0 {
		t.Errorf("stored %v", got)
	}
}

// The commands that only look, and a collection of a state that has no plans,
// make no state.
func TestNoPlanCommandMakesAStateThatDoesNotExist(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"})
	for _, args := range [][]string{{"plan", "list"}, {"plan", "list", "--all"}, {"plan", "gc", "--dry-run"}, {"plan", "gc"}, {"plan", "show", "plan-abcdef"}} {
		runPravo(t, dir, args...)
		if _, err := os.Stat(filepath.Join(dir, "pravo.state")); err == nil {
			t.Fatalf("pravo %s made a state", strings.Join(args, " "))
		}
	}
	text, code := runPravo(t, dir, "plan", "gc")
	if code != 0 || !strings.Contains(text, "No stored plan needs to be removed.") {
		t.Errorf("gc exited %d:\n%s", code, text)
	}
}

// A plan that was applied completely says that nothing is left to apply, also after
// the time it was kept for has passed: it does not say that it expired.
func TestAnAppliedPlanHasNothingLeftAfterItsTTL(t *testing.T) {
	dir := localRoot(t, "")
	id := storePlanAs(t, dir, "alice", "--ttl", "2s")
	if text, code := runPravo(t, dir, "apply", id); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	time.Sleep(2200 * time.Millisecond)
	text, code := runPravo(t, dir, "apply", id)
	if code != 0 || !strings.Contains(text, "nothing left to apply") {
		t.Errorf("exit %d:\n%s", code, text)
	}
}

// The words for a length of time that a person chose are the ones that they chose.
func TestTheTTLIsSaidAsItWasWritten(t *testing.T) {
	dir, _ := pendingDir(t)
	text, code := runPravo(t, dir, "plan", "--store", "--ttl", "90m")
	if code != 0 || !strings.Contains(text, "it is removed after 90 minutes") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	text, _ = runPravo(t, dir, "plan", "list")
	if !strings.Contains(text, "expires in 1 hour 30 minutes") && !strings.Contains(text, "expires in 1 hour 29 minutes") {
		t.Errorf("list:\n%s", text)
	}
}

// What the command line says to do is something that a shell can be given.
func TestTheTagsOfAStoredPlanAreQuotedForAShell(t *testing.T) {
	m := &engine.PlanMeta{ID: "plan-12345678", Tags: []string{`msg=it's a "test" $x`}, SysTags: true}
	err := planTagsMatch(m, "apply")
	if err == nil {
		t.Fatal("no error")
	}
	want := `run pravo apply plan-12345678 -t 'msg=it'\''s a "test" $x' -T`
	if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), `\"`) {
		t.Errorf("%s\nwant %s", err, want)
	}
}

// A claim by a collector is why an apply is refused with the code of a plan that is
// stale; any other reason that the plan cannot be claimed is an error of its own.
func TestApplyRefusedByACollectorIsStaleAndAnUnreadablePlanIsNot(t *testing.T) {
	dir, _ := pendingDir(t)
	id := storePlanAs(t, dir, "alice")
	progress := filepath.Join(dir, "pravo.state", "plans", id+".progress.json")
	claim := `{"format":1,"id":"` + id + `","collecting":{"by":"other#1","at":"` + time.Now().UTC().Format(time.RFC3339) + `"},"updated":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(progress, []byte(claim), 0o600); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, dir, "apply", id)
	if code != int(engine.ExitStale) || !strings.Contains(text, "a collection is removing "+id) || !strings.Contains(text, "pravo plan --store") {
		t.Errorf("claimed: exit %d:\n%s", code, text)
	}
	if err := os.WriteFile(progress, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, dir, "apply", id)
	if code != 1 || !strings.Contains(text, "is not readable") {
		t.Errorf("unreadable progress: exit %d:\n%s", code, text)
	}
}

// The plan that was just stored does not warn that it changes what it changes.
func TestAStoredPlanDoesNotWarnAboutItself(t *testing.T) {
	dir, _ := pendingDir(t)
	storePlanAs(t, dir, "alice")
	text, code := runPravo(t, dir, "--actor", "bob", "plan", "--store")
	if code != 0 || strings.Count(text, "Warning:") != 1 {
		t.Errorf("exit %d, want one warning (alice's plan):\n%s", code, text)
	}
}

// A plan that the listing cannot read is named, with the reason, and not silently
// left out.
func TestListSaysWhichPlansItCouldNotRead(t *testing.T) {
	dir, _ := pendingDir(t)
	good := storePlanAs(t, dir, "alice")
	bad := storePlanAs(t, dir, "bob")
	if err := os.WriteFile(filepath.Join(dir, "pravo.state", "plans", bad+".progress.json"), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, dir, "plan", "list")
	if code != 0 || !strings.Contains(text, good) || !strings.Contains(text, "Warning: "+bad+" is not listed: ") || !strings.Contains(text, "not readable") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	text, code = runPravo(t, dir, "--json", "plan", "list")
	var doc struct {
		Plans      []struct{ ID string }
		Unreadable []struct{ ID, Error string }
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil || code != 0 || len(doc.Plans) != 1 || len(doc.Unreadable) != 1 || doc.Unreadable[0].ID != bad {
		t.Errorf("exit %d: %v\n%s", code, err, text)
	}
}

// When the configuration is not the one that a plan was made from, the reason names
// the files that differ, for a stored plan and for a plan file alike.
func TestAPlanThatFindsTheConfigurationChangedNamesTheFiles(t *testing.T) {
	dir := localRoot(t, "")
	id := storePlanAs(t, dir, "alice")
	if text, code := runPravo(t, dir, "plan", "--out", "saved.plan"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	main := filepath.Join(dir, "main.cue")
	original, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, append(original, []byte("// edited\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{id, "saved.plan"} {
		text, code := runPravo(t, dir, "apply", target)
		if code != int(engine.ExitStale) || !strings.Contains(text, "the configuration changed since the plan was made (main.cue changed)") {
			t.Errorf("apply %s: exit %d:\n%s", target, code, text)
		}
	}
	text, _ := runPravo(t, dir, "show", id)
	if !strings.Contains(text, "(main.cue changed)") {
		t.Errorf("show:\n%s", text)
	}
	// The module file is named as it is written in the repository.
	if err := os.WriteFile(main, original, 0o644); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(dir, "cue.mod", "module.cue")
	modData, err := os.ReadFile(module)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, append([]byte("// edited\n"), modData...), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, dir, "apply", id); code != int(engine.ExitStale) || !strings.Contains(text, "(cue.mod/module.cue changed)") {
		t.Errorf("module file: exit %d:\n%s", code, text)
	}
	// A file that is added, and a file that is gone.
	if err := os.WriteFile(module, modData, 0o644); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, "extra.cue")
	if err := os.WriteFile(extra, []byte("package infra\n\nextra: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, dir, "apply", id); code != int(engine.ExitStale) || !strings.Contains(text, "(extra.cue added)") {
		t.Errorf("added file: exit %d:\n%s", code, text)
	}
	os.Remove(extra)
	// With the configuration as it was, the plan applies.
	if text, code := runPravo(t, dir, "apply", id); code != 0 {
		t.Errorf("unchanged: exit %d:\n%s", code, text)
	}
}

// A plan file made before the files were recorded gives the plain reason.
func TestAPlanFileWithoutTheListOfFilesGivesThePlainReason(t *testing.T) {
	dir := localRoot(t, "")
	if text, code := runPravo(t, dir, "plan", "--out", "old.plan"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	path := filepath.Join(dir, "old.plan")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["config_files"]; !ok {
		t.Fatal("the plan file does not list its files")
	}
	delete(doc, "config_files")
	data, _ = json.Marshal(doc)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.cue")
	original, _ := os.ReadFile(main)
	if err := os.WriteFile(main, append(original, []byte("// edited\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, dir, "apply", "old.plan")
	if code != int(engine.ExitStale) || !strings.Contains(text, "the configuration changed since the plan was made\n") || strings.Contains(text, "main.cue changed") {
		t.Errorf("exit %d:\n%s", code, text)
	}
}

func TestOlderThanZeroIsRefused(t *testing.T) {
	dir, _ := pendingDir(t)
	if text, code := runPravo(t, dir, "plan", "gc", "--older-than", "0"); code != 1 || !strings.Contains(text, "above zero") {
		t.Errorf("exit %d:\n%s", code, text)
	}
}

// A plan made with -T is applied with -T.
func TestAStoredPlanNeedsTheSystemTagsItWasMadeWith(t *testing.T) {
	dir, _ := pendingDir(t)
	text, code := runPravo(t, dir, "--actor", "alice", "plan", "--store", "-T")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	id := storedID.FindString(text[strings.Index(text, "Stored the plan as"):])
	if !strings.Contains(text, "pravo apply "+id+" -T") {
		t.Errorf("apply command:\n%s", text)
	}
	if text, code = runPravo(t, dir, "apply", id); code != 1 || !strings.Contains(text, "other -t and -T") {
		t.Errorf("apply without -T: exit %d:\n%s", code, text)
	}
}

// "plan --store" removes at most a few plans on its way.
func TestOpportunisticCollectionIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("stores thirty plans and waits for them to expire")
	}
	dir, _ := pendingDir(t)
	const stored = opportunisticGC + 5
	for i := 0; i < stored; i++ {
		storePlanAs(t, dir, "alice", "--ttl", "6s")
	}
	if got := len(planObjects(t, dir)); got != 2*stored {
		t.Fatalf("%d objects of %d plans (the plans expired while they were stored?)", got, stored)
	}
	time.Sleep(6500 * time.Millisecond)
	text, code := runPravo(t, dir, "--actor", "bob", "plan", "--store")
	want := "Removed " + plural(opportunisticGC, "stored plan", "stored plans") + " that were no longer needed."
	if code != 0 || !strings.Contains(text, want) {
		t.Errorf("exit %d, want %q:\n%s", code, want, text)
	}
	if got := len(planObjects(t, dir)); got != 2*(stored-opportunisticGC)+2 {
		t.Errorf("%d objects left", got)
	}
}

func TestAStoredPlanCanBeAppliedInParts(t *testing.T) {
	dir := localRoot(t, "hello: $local.#Resource.local_file & {filename: \"hello.txt\", content: \"hello\\n\"}\nworld: $local.#Resource.local_file & {filename: \"world.txt\", content: \"world\\n\"}\n")
	id := storePlanAs(t, dir, "alice")
	text, code := runPravo(t, dir, "apply", id, "--only", "hello")
	if code != 0 || !strings.Contains(text, "Applying 1 of 2 remaining changes from "+id) || !strings.Contains(text, "1 added, 0 changed, 0 destroyed") || !strings.Contains(text, "1 change remains in "+id+`; apply with "pravo apply `+id+`".`) {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	if _, err := os.Stat(filepath.Join(dir, "world.txt")); err == nil {
		t.Error("world.txt was created by an apply of hello")
	}
	text, _ = runPravo(t, dir, "plan", "list")
	if !regexp.MustCompile(id+`\s+partial\s+alice`).MatchString(text) || !strings.Contains(text, "1 of 2 changes applied") {
		t.Errorf("list after part of it:\n%s", text)
	}
	text, _ = runPravo(t, dir, "plan", "show", id)
	if !strings.Contains(text, "1 of 2 changes applied") || !strings.Contains(text, "(already applied in txn") {
		t.Errorf("show after part of it:\n%s", text)
	}
	if text, code = runPravo(t, dir, "apply", id); code != 0 || !strings.Contains(text, "Apply complete!") || !strings.Contains(text, "1 added") {
		t.Fatalf("the rest: exit %d:\n%s", code, text)
	}
	if _, err := os.Stat(filepath.Join(dir, "world.txt")); err != nil {
		t.Error(err)
	}
	if text, _ = runPravo(t, dir, "plan", "list", "--all"); !regexp.MustCompile(id + `\s+applied`).MatchString(text) {
		t.Errorf("list --all after all of it:\n%s", text)
	}
}

// The note that a plan was made with (--reason) is the reason of the apply that
// applies it, in the history of the resources, unless the apply has its own.
func TestAnApplyTakesTheReasonOfThePlan(t *testing.T) {
	dir := localRoot(t, "")
	id := storePlanAs(t, dir, "alice", "--reason", "say hello to the team")
	if text, code := runPravo(t, dir, "--actor", "bob", "apply", id); code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	text, _ := runPravo(t, dir, "state", "log")
	if !strings.Contains(text, "bob") || !strings.Contains(text, "say hello to the team") {
		t.Errorf("the log of the apply:\n%s", text)
	}
	// A plan file does the same, and a reason of the apply wins.
	dir2 := localRoot(t, "")
	if text, code := runPravo(t, dir2, "plan", "--out", "r.plan", "--reason", "from the file"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir2, "apply", "r.plan", "--reason", "my own"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	text, _ = runPravo(t, dir2, "state", "log")
	if !strings.Contains(text, "my own") || strings.Contains(text, "from the file") {
		t.Errorf("the log of the second apply:\n%s", text)
	}
}

// A plan that holds a reservation says so in the list, and a plan that a collection holds is
// not offered for applying.
func TestListAndShowSayWhatStopsAPlan(t *testing.T) {
	dir := localRoot(t, "")
	id := storePlanAs(t, dir, "alice", "--reserve", "15m")
	text, _ := runPravo(t, dir, "plan", "list")
	if !regexp.MustCompile(id + `.*reserved until \d\d:\d\d`).MatchString(text) {
		t.Errorf("list:\n%s", text)
	}
	progress := filepath.Join(dir, "pravo.state", "plans", id+".progress.json")
	claim := `{"format":1,"id":"` + id + `","collecting":{"by":"other#1","at":"` + time.Now().UTC().Format(time.RFC3339) + `"},"updated":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(progress, []byte(claim), 0o600); err != nil {
		t.Fatal(err)
	}
	// the JSON run comes first: a run with --json leaves the printer on the error stream for the test after it
	text, _ = runPravo(t, dir, "--json", "plan", "show", id)
	if !strings.Contains(text, `"being_collected": true`) {
		t.Errorf("show --json:\n%s", text)
	}
	text, code := runPravo(t, dir, "plan", "show", id)
	if code != 0 || strings.Contains(text, "apply:   pravo apply") || !strings.Contains(text, "a collection is removing this plan") {
		t.Errorf("show: exit %d:\n%s", code, text)
	}
}
