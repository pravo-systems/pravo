package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

func fakePravo(args []string) int {
	if log := os.Getenv("SERVESHOTS_FAKE_LOG"); log != "" {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			wd, _ := os.Getwd()
			fmt.Fprintf(f, "%s | %s\n", filepath.Base(wd), strings.Join(args, " "))
			f.Close()
		}
	}
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "init":
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
		if store {
			// A stored plan is two objects below pravo.state/plans, named by its id.
			dir := filepath.Join("pravo.state", "plans")
			os.MkdirAll(dir, 0o755)
			have, _ := filepath.Glob(filepath.Join(dir, "*.meta.json"))
			id := fmt.Sprintf("plan-%08x", len(have)+1)
			now := time.Now().UTC().Format(time.RFC3339Nano)
			os.WriteFile(filepath.Join(dir, id+".plan.json"), []byte(`{"format":1,"created":"`+now+`","providers":{},"changes":[]}`), 0o644)
			os.WriteFile(filepath.Join(dir, id+".meta.json"), []byte(`{"format":1,"id":"`+id+`","author":"`+actor+`","created":"`+now+`","expires":"`+now+`"}`), 0o644)
		}
		fmt.Println("Plan: 6 to add, 0 to change, 3 to destroy.")
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
	got := commandEnv([]string{"PATH=/bin", "PRAVO_LOG=debug", "PRAVO_ACTOR=a", "PRAVO_REASON=r", "NO_COLOR=0", "HOME=/h"})
	want := []string{"PATH=/bin", "HOME=/h", "NO_COLOR=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commandEnv = %q, want %q", got, want)
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
