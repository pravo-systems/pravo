package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake pravo: when SERVESHOTS_FAKE is set it acts
// as the command that the program runs, writes the line of each call to
// SERVESHOTS_FAKE_LOG and answers like the real one in the places that the
// program reads.
func TestMain(m *testing.M) {
	if os.Getenv("SERVESHOTS_FAKE") != "" {
		os.Exit(fakePravo(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// The line of a call names the identity of the caller (PRAVO_CONFIG_DIR, as
// "@NAME") and whether the run is recorded (PRAVO_TRACES=on, as "+traces"),
// so that a test sees who ran what and which runs are recorded.
func fakePravo(args []string) int {
	if log := os.Getenv("SERVESHOTS_FAKE_LOG"); log != "" {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			wd, _ := os.Getwd()
			line := strings.Join(args, " ")
			if id := os.Getenv("PRAVO_CONFIG_DIR"); id != "" {
				line += " @" + filepath.Base(id)
			}
			if os.Getenv("PRAVO_TRACES") == "on" {
				line += " +traces"
			}
			fmt.Fprintf(f, "%s | %s\n", filepath.Base(wd), line)
			f.Close()
		}
	}
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "init", "protect":
		return 0
	case "id":
		// id new --name NAME prints the line for the readers file last.
		name := ""
		for i, a := range args {
			if a == "--name" && i+1 < len(args) {
				name = args[i+1]
			}
		}
		fmt.Printf("Made the identity %s.\n", name)
		fmt.Println("Give whoever keeps .pravo/access/readers.txt this line, and nothing else:")
		fmt.Printf("%s ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeSigningKeyOf%s x25519:FakeReaderKeyOf%s\n", name, name, name)
		return 0
	case "discover":
		fakeRecord("pravo discover")
		fmt.Println("3 objects that nothing manages, 1 acknowledged, 2 managed.")
		return 0
	case "apply":
		n := os.Getenv("SERVESHOTS_FAKE_RESOURCES")
		if n == "" {
			n = "47"
		}
		for _, p := range []string{"out/network/dns.zone", "out/api/cache.conf"} {
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte("x\n"), 0o644)
		}
		fakeRecord("pravo apply")
		fmt.Printf("Apply complete! Resources: %s added, 0 changed, 0 destroyed. [txn x]\n", n)
		return 0
	case "drift":
		fmt.Println("Drift. 2 of 47 resources changed outside pravo:")
		fmt.Println("  api.cache.config: create")
		fmt.Println("    - refresh found the real state differs from the state (drift)")
		if os.Getenv("SERVESHOTS_FAKE_DRIFT_MISSING") == "" {
			fmt.Println("  network.dns.records: create")
			fmt.Println("    - refresh found the real state differs from the state (drift)")
		}
		return 2
	case "plan":
		if len(args) > 1 && args[1] == "approve" {
			fmt.Printf("Approved %s.\n", args[2])
			return 0
		}
		store, actor := false, ""
		for i, a := range args {
			if a == "--out" && i+1 < len(args) {
				os.WriteFile(args[i+1], []byte("plan"), 0o644)
			}
			store = store || a == "--store"
			if a == "--actor" && i+1 < len(args) {
				actor = args[i+1]
			}
		}
		fakeRecord("pravo plan")
		fmt.Println("Plan: 6 to add, 0 to change, 3 to destroy.")
		if store {
			// A stored plan is two objects below pravo.state/plans, named by its id.
			dir := filepath.Join("pravo.state", "plans")
			os.MkdirAll(dir, 0o755)
			have, _ := filepath.Glob(filepath.Join(dir, "*.meta.json"))
			id := fmt.Sprintf("plan-%08x", len(have)+1)
			now := time.Now().UTC().Format(time.RFC3339Nano)
			os.WriteFile(filepath.Join(dir, id+".plan.json"), []byte(`{"format":1,"created":"`+now+`","providers":{},"changes":[]}`), 0o644)
			os.WriteFile(filepath.Join(dir, id+".meta.json"), []byte(`{"format":1,"id":"`+id+`","author":"`+actor+`","created":"`+now+`","expires":"`+now+`"}`), 0o644)
			fmt.Printf("Stored the plan as %s; unless it is applied it is removed after 7 days. To apply exactly these actions, run:\n  pravo apply %s\n", id, id)
		}
		return 0
	case "serve":
		switch os.Getenv("SERVESHOTS_FAKE_SERVE") {
		case "garbage":
			fmt.Println("listening, somewhere")
			return 1
		case "text":
			fmt.Println("serving on http://127.0.0.1:4568/?token=t0k")
		default:
			fmt.Println(`{"url":"http://127.0.0.1:4567/?token=abc","token":"abc"}`)
		}
		time.Sleep(time.Hour)
		return 0
	}
	return 2
}

// fakeRecord writes the record of a run below .pravo/traces when the runs
// are recorded (PRAVO_TRACES=on), as the real command does: a root span
// named after the command and a request to the store below it.
func fakeRecord(name string) {
	if os.Getenv("PRAVO_TRACES") != "on" {
		return
	}
	dir := filepath.Join(".pravo", "traces")
	os.MkdirAll(dir, 0o755)
	have, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	trace := fmt.Sprintf("%032x", len(have)+1)
	root := fmt.Sprintf("%016x", 1)
	doc := fmt.Sprintf(`{"version":1,"trace_id":"%s","service":"pravo","start":"2026-10-28T10:00:00Z","duration_us":1500,"spans":[`+
		`{"id":"%s","name":"%s","start_us":0,"dur_us":1500,"attrs":{"pravo.command":"%s"}},`+
		`{"id":"%016x","parent":"%s","name":"network PUT 127.0.0.1","start_us":100,"dur_us":200}]}`,
		trace, root, name, strings.TrimPrefix(name, "pravo "), 2, root)
	os.WriteFile(filepath.Join(dir, trace+"-"+root+".json"), []byte(doc+"\n"), 0o644)
}

func fake(t *testing.T) (*pravoRunner, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("SERVESHOTS_FAKE", "1")
	t.Setenv("SERVESHOTS_FAKE_LOG", log)
	return &pravoRunner{Bin: os.Args[0], Timeout: time.Minute}, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		_, cmd, _ := strings.Cut(l, " | ")
		out = append(out, cmd)
	}
	return out
}

func TestPrepareClean(t *testing.T) {
	r, log := fake(t)
	sc, _ := findScenario("platform")
	dir := filepath.Join(t.TempDir(), "ws")
	w, err := prepareClean(context.Background(), r, sc, dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.Situation != situationClean || w.Plan != "" {
		t.Errorf("workspace = %+v", w)
	}
	want := []string{"init", "apply -auto-approve"}
	if got := calls(t, log); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "kit", "kit.cue")); err != nil {
		t.Error("the scenario was not copied:", err)
	}
}

func TestPrepareCleanChecksTheResourceCount(t *testing.T) {
	r, _ := fake(t)
	t.Setenv("SERVESHOTS_FAKE_RESOURCES", "12")
	sc, _ := findScenario("platform")
	_, err := prepareClean(context.Background(), r, sc, filepath.Join(t.TempDir(), "ws"))
	if err == nil || !strings.Contains(err.Error(), "Resources: 47 added") {
		t.Errorf("error = %v", err)
	}
}

func TestPrepareChanged(t *testing.T) {
	r, log := fake(t)
	sc, _ := findScenario("platform")
	dir := filepath.Join(t.TempDir(), "ws")
	w, err := prepareChanged(context.Background(), r, sc, dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.Situation != situationChanged || w.Plan != filepath.Join(dir, planFile) {
		t.Errorf("workspace = %+v", w)
	}
	want := []string{"init", "apply -auto-approve", "drift", "plan --out " + planFile}
	if got := calls(t, log); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
	if got := readFile(t, filepath.Join(dir, "out/network/dns.zone")); got != "x\n# edited by hand\n" {
		t.Errorf("the file on disk = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "out/api/cache.conf")); !os.IsNotExist(err) {
		t.Error("the deleted file is still there")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "api.cue")), "9082") {
		t.Error("the specification was not edited")
	}
	if _, err := os.Stat(w.Plan); err != nil {
		t.Error("no plan:", err)
	}
}

// The stored situation stores a plan for each actor, from the specification
// edited for it, puts the specification back each time, and moves the times of
// the plans into the past.
func TestPrepareStored(t *testing.T) {
	r, log := fake(t)
	sc, _ := findScenario("platform")
	dir := filepath.Join(t.TempDir(), "ws")
	w, err := prepareStored(context.Background(), r, sc, dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.Situation != situationStored || len(w.Plans) != 3 {
		t.Fatalf("workspace = %+v", w)
	}
	want := []string{"init", "apply -auto-approve",
		"plan --store --actor ana --reason move orders to port 9082",
		"plan --store --actor ben --reason new ports for orders and search",
		"plan --store --actor ci --reason listen on 8443 at the edge"}
	if got := calls(t, log); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
	original, _ := scenarioFS.ReadFile("scenarios/platform/api.cue")
	if got := readFile(t, filepath.Join(dir, "api.cue")); got != string(original) {
		t.Error("the specification was not put back")
	}
	for _, actor := range []string{"ana", "ben", "ci"} {
		if !planIDRe.MatchString(w.Plans[actor]) {
			t.Errorf("the plan of %s has the id %q", actor, w.Plans[actor])
		}
	}
	var meta struct {
		Created, Expires time.Time
	}
	age := func(actor string) (time.Duration, time.Duration) {
		data := readFile(t, filepath.Join(dir, "pravo.state", "plans", w.Plans[actor]+".meta.json"))
		if err := json.Unmarshal([]byte(data), &meta); err != nil {
			t.Fatal(err)
		}
		return time.Since(meta.Created), time.Until(meta.Expires)
	}
	if made, left := age("ana"); made < 179*time.Minute || made > 181*time.Minute || left < 6*24*time.Hour {
		t.Errorf("ana's plan: made %s ago, %s left", made, left)
	}
	if made, left := age("ci"); made < 9*24*time.Hour-time.Minute || left > -time.Hour {
		t.Errorf("the expired plan: made %s ago, %s left", made, left)
	}
	plan := readFile(t, filepath.Join(dir, "pravo.state", "plans", w.Plans["ben"]+".plan.json"))
	if !strings.Contains(plan, `"format"`) || !strings.Contains(plan, `"created"`) {
		t.Errorf("the plan object: %s", plan)
	}
}

func TestPrepareStoredNeedsPlansAndOldEnoughOnes(t *testing.T) {
	r, _ := fake(t)
	fleet, _ := findScenario("fleet")
	if _, err := prepareStored(context.Background(), r, fleet, filepath.Join(t.TempDir(), "ws")); err == nil {
		t.Error("a scenario without stored plans: no error")
	}
	sc, _ := findScenario("platform")
	young := sc
	young.Stored = []storedPlan{{Actor: "ci", Reason: "x", Expired: true, Age: time.Hour, Edits: sc.Stored[0].Edits}}
	if _, err := prepareStored(context.Background(), r, young, filepath.Join(t.TempDir(), "ws")); err == nil || !strings.Contains(err.Error(), "not old enough") {
		t.Errorf("a plan that did not expire: %v", err)
	}
}

func TestPrepareChangedNeedsTheDriftThatWasMade(t *testing.T) {
	r, _ := fake(t)
	t.Setenv("SERVESHOTS_FAKE_DRIFT_MISSING", "1")
	sc, _ := findScenario("platform")
	_, err := prepareChanged(context.Background(), r, sc, filepath.Join(t.TempDir(), "ws"))
	if err == nil || !strings.Contains(err.Error(), "network.dns.records") {
		t.Errorf("error = %v", err)
	}
}

func TestPrepareChangedRefusesAScenarioWithoutChanges(t *testing.T) {
	r, _ := fake(t)
	sc, _ := findScenario("fleet")
	if _, err := prepareChanged(context.Background(), r, sc, filepath.Join(t.TempDir(), "ws")); err == nil {
		t.Error("no error")
	}
}

func TestMustRunReportsTheOutput(t *testing.T) {
	r, _ := fake(t)
	_, err := r.mustRun(context.Background(), t.TempDir(), []int{0}, "drift")
	if err == nil || !strings.Contains(err.Error(), "exited with 2") || !strings.Contains(err.Error(), "api.cache.config") {
		t.Errorf("error = %v", err)
	}
}

func TestDriftAddrs(t *testing.T) {
	out := `Drift. 2 of 47 resources changed outside pravo:
  api.cache.config: create
    - refresh found the real state differs from the state (drift)
  network.dns.records: create
    - refresh found the real state differs from the state (drift)
`
	got := driftAddrs(out)
	if len(got) != 2 || !got["api.cache.config"] || !got["network.dns.records"] {
		t.Errorf("driftAddrs = %v", got)
	}
}

func TestCommandEnv(t *testing.T) {
	got := commandEnv([]string{"PATH=/bin", "PRAVO_LOG=debug", "PRAVO_ACTOR=a", "PRAVO_REASON=r", "NO_COLOR=0", "HOME=/h",
		"PRAVO_TRACES=on", "PRAVO_TRACES_NET=off", "TRACEPARENT=00-x-y-00", "TRACESTATE=a=b", "OTEL_EXPORTER_OTLP_ENDPOINT=http://x",
		"PRAVO_CONFIG_DIR=/me", "PRAVO_IDENTITY=me", "PRAVO_PASSPHRASE=secret", "PRAVO_ACCESS_DIR=/a", "AWS_PROFILE=p"})
	want := []string{"PATH=/bin", "HOME=/h", "AWS_PROFILE=p", "NO_COLOR=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commandEnv = %q, want %q", got, want)
	}
}

// The team situation makes an identity for each person, seals the state to
// them with their roles, applies as the admin, and then runs the steps as the
// people who made them: a change is stored, approved by the admin and applied
// by its id, a proposal is stored and undone, and the commands that do work
// are recorded.
func TestPrepareTeam(t *testing.T) {
	r, log := fake(t)
	t.Setenv("SERVESHOTS_FAKE_RESOURCES", "49")
	sc, _ := findScenario("team")
	dir := filepath.Join(t.TempDir(), "team")
	deps := teamDeps{Store: &store{Endpoint: "http://127.0.0.1:9000"}, FakeProvider: filepath.Join(t.TempDir(), "terraform-provider-demo")}
	w, err := prepareTeam(context.Background(), r, sc, dir, deps)
	if err != nil {
		t.Fatal(err)
	}
	if w.Situation != situationTeam || w.Plan != "" {
		t.Errorf("workspace = %+v", w)
	}
	want := []string{
		"id new --name ada @ada",
		"id new --name ben @ben",
		"id new --name cy @cy",
		"init @ada",
		"protect init --encrypt --yes @ada",
		"protect roles --yes @ada",
		"apply -auto-approve --actor ada --reason first apply of the platform @ada +traces",
		"discover --actor ada @ada +traces",
		"plan --store --actor ben --reason search moves to port 8084 @ben +traces",
		"plan approve plan-00000001 --actor ada --reason reviewed: search moves to port 8084 @ada",
		"apply plan-00000001 -auto-approve --actor ben --reason search moves to port 8084 @ben +traces",
		"plan --store --actor ben --reason retire the reload step of the balancer @ben +traces",
		"plan --store --actor cy --reason move orders to port 9082 @cy +traces",
		"plan approve plan-00000003 --actor ada --reason reviewed: move orders to port 9082 @ada",
	}
	if got := calls(t, log); !reflect.DeepEqual(got, want) {
		t.Errorf("calls =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	// The readers file lists the three with their roles.
	readers := strings.Split(strings.TrimSpace(readFile(t, filepath.Join(dir, ".pravo", "access", "readers.txt"))), "\n")
	if len(readers) != 3 || !strings.HasPrefix(readers[0], "ada ssh-ed25519 ") || !strings.HasSuffix(readers[0], " role:admin") ||
		!strings.HasSuffix(readers[1], " role:writer") || !strings.HasPrefix(readers[2], "cy ") || !strings.HasSuffix(readers[2], " role:reader") {
		t.Errorf("readers.txt:\n%s", strings.Join(readers, "\n"))
	}
	// The files name the store and the provider, as CUE strings.
	if got := readFile(t, filepath.Join(dir, "settings.cue")); !strings.Contains(got, `"http://127.0.0.1:9000"`) || strings.Contains(got, storePlaceholder) {
		t.Errorf("settings.cue:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, "storage.cue")); !strings.Contains(got, strconv.Quote(deps.FakeProvider)) || strings.Contains(got, providerPlaceholder) {
		t.Errorf("storage.cue:\n%s", got)
	}
	// The objects of the fake provider are written next to the workspace.
	var objs []catalogObject
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(filepath.Dir(dir), "team-objects", "catalog.json"))), &objs); err != nil || len(objs) != len(sc.Team.Catalog) {
		t.Errorf("catalog.json: %d objects, %v", len(objs), err)
	}
	// The change stayed, the proposals were undone, and the edits made last
	// are in place.
	api := readFile(t, filepath.Join(dir, "api.cue"))
	if !strings.Contains(api, "port:     8084") || strings.Contains(api, "port:     8083") || !strings.Contains(api, "port:     8082") {
		t.Error("api.cue does not show the change applied and the proposal undone")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "edge.cue")), "reload:") {
		t.Error("edge.cue lost the reload step, which is only proposed")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "policies.cue")), "min: 2") {
		t.Error("policies.cue was not edited last")
	}
	// The plans and the runs are known by their labels.
	if want := map[string]string{"search": "plan-00000001", "reload": "plan-00000002", "orders": "plan-00000003"}; !reflect.DeepEqual(w.Plans, want) {
		t.Errorf("plans = %v", w.Plans)
	}
	for _, label := range sc.traceLabels() {
		id, ok := w.Traces[label]
		if !ok {
			t.Errorf("no run labelled %s: %v", label, w.Traces)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, ".pravo", "traces", id+".json")); err != nil {
			t.Errorf("the run %s: %v", label, err)
		}
	}
	if len(w.Traces) != len(sc.traceLabels()) {
		t.Errorf("%d runs recorded, want %d: %v", len(w.Traces), len(sc.traceLabels()), w.Traces)
	}
	// The viewer reads as the admin, with what the store and the provider need.
	env := strings.Join(w.Env, "\n")
	for _, kv := range []string{"AWS_ACCESS_KEY_ID=test", "FAKEPROVIDER_DIR=" + filepath.Join(filepath.Dir(dir), "team-objects"), "PRAVO_PASSPHRASE=" + passphrase, "PRAVO_CONFIG_DIR=" + filepath.Join(dir, "people", "ada")} {
		if !strings.Contains(env, kv) {
			t.Errorf("the environment of the viewer lacks %s:\n%s", kv, env)
		}
	}
}

func TestPrepareTeamRefusesWhatIsNotATeam(t *testing.T) {
	r, _ := fake(t)
	deps := teamDeps{Store: &store{Endpoint: "http://127.0.0.1:9000"}, FakeProvider: "p"}
	platform, _ := findScenario("platform")
	if _, err := prepareTeam(context.Background(), r, platform, filepath.Join(t.TempDir(), "ws"), deps); err == nil {
		t.Error("a scenario without a team: no error")
	}
	sc, _ := findScenario("team")
	team := *sc.Team
	team.People = []person{{"ben", "writer"}, {"ada", "admin"}}
	sc.Team = &team
	if _, err := prepareTeam(context.Background(), r, sc, filepath.Join(t.TempDir(), "ws"), deps); err == nil || !strings.Contains(err.Error(), "admin") {
		t.Errorf("a team whose first person is not the admin: %v", err)
	}
}

func TestReaderLineAndStoredID(t *testing.T) {
	out := "Made the identity ada.\nGive whoever keeps .pravo/access/readers.txt this line, and nothing else:\nada ssh-ed25519 AAAA x25519:BBBB\n"
	if got := readerLine(out, "ada"); got != "ada ssh-ed25519 AAAA x25519:BBBB" {
		t.Errorf("readerLine = %q", got)
	}
	if got := readerLine(out, "ben"); got != "" {
		t.Errorf("readerLine of another name = %q", got)
	}
	if got := storedID("Plan: 1 to add.\nStored the plan as plan-2f9c1a4e; unless it is applied it is removed after 7 days.\n"); got != "plan-2f9c1a4e" {
		t.Errorf("storedID = %q", got)
	}
	if got := storedID("Plan: nothing to do.\n"); got != "" {
		t.Errorf("storedID of no plan = %q", got)
	}
}

func TestFillPlaceholdersQuotes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.cue"), "endpoint: \"@FAKES3@\"\npath: \"@FAKEPROVIDER@\"\n")
	write(t, filepath.Join(dir, "sub", "b.cue"), "x: \"@FAKES3@\"\n")
	if err := fillPlaceholders(dir, map[string]string{"@FAKES3@": "http://127.0.0.1:9000", "@FAKEPROVIDER@": `C:\p\demo "x"`}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "a.cue")); got != "endpoint: \"http://127.0.0.1:9000\"\npath: \"C:\\\\p\\\\demo \\\"x\\\"\"\n" {
		t.Errorf("a.cue = %q", got)
	}
	// Only the files at the top are filled in.
	if got := readFile(t, filepath.Join(dir, "sub", "b.cue")); got != "x: \"@FAKES3@\"\n" {
		t.Errorf("b.cue = %q", got)
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\nb\nc\nd\n", 2); got != "c\nd" {
		t.Errorf("lastLines = %q", got)
	}
	if got := lastLines("a", 5); got != "a" {
		t.Errorf("lastLines = %q", got)
	}
}
