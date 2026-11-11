package serve

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

func lapseOf(t *testing.T, p *project) TimeLapse {
	t.Helper()
	var l TimeLapse
	if err := json.Unmarshal(p.get("/api/timelapse"), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// The time lapse lists the runs that wrote nodes, oldest first, and leaves out what is not a change of the
// estate: a plan, a refresh, a secret and the environment record.
func TestTimeLapseListsTheRunsThatWroteNodes(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	more := st.Begin("apply", "pravo apply -t token=hunter2")
	more.Record("services.a.db", "update", 1, 2)
	more.Record("services.a.file", "replace", 1, 2)
	more.Record("old.thing", "delete", 1, 0)
	more.Record("services.b.db", "refresh", 1, 1)
	more.Record(state.SecretAddr("db"), "set", 0, 1)
	more.Record(state.EnvironmentAddr, "set", 0, 1)
	more.Record("services.c.db", "move-to", 0, 1)
	if err := more.Finish("applied", "changed", true); err != nil {
		t.Fatal(err)
	}
	quiet := st.Begin("apply", "pravo apply")
	quiet.Record("services.a.db", "drift", 2, 3)
	if err := quiet.Finish("applied", "nothing", true); err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	l := lapseOf(t, p)
	if l.Version != APIVersion || l.Revision == "" {
		t.Errorf("version %d revision %q", l.Version, l.Revision)
	}
	if len(l.Steps) != 2 {
		t.Fatalf("steps %+v, want the seed apply and the one after it (a plan and a run that wrote nothing are no steps)", l.Steps)
	}
	if first := l.Steps[0]; first.Kind != "apply" || len(first.Changes) == 0 || first.Changes[0].Action != "add" {
		t.Errorf("first step %+v", first)
	}
	got := map[string]LapseChange{}
	for _, c := range l.Steps[1].Changes {
		got[c.Addr] = c
	}
	for addr, want := range map[string]string{"services.a.db": "change", "services.a.file": "replace", "old.thing": "remove", "services.c.db": "move"} {
		if got[addr].Action != want {
			t.Errorf("%s: %+v, want %s", addr, got[addr], want)
		}
	}
	if len(got) != 4 {
		t.Errorf("changes %+v: a refresh, a secret and the environment record are not changes of the estate", got)
	}
	if got["services.a.db"].Namespace != "services.a" || got["old.thing"].Namespace != "old" {
		t.Errorf("namespaces %+v", got)
	}
	if strings.Contains(l.Steps[1].Command, "hunter2") {
		t.Errorf("the command line holds a value: %q", l.Steps[1].Command)
	}
	if !(l.Steps[0].Time.Before(l.Steps[1].Time) || l.Steps[0].Time.Equal(l.Steps[1].Time)) {
		t.Errorf("steps are not oldest first: %v %v", l.Steps[0].Time, l.Steps[1].Time)
	}
}

// A project with no recorded run answers with no steps and not with an error.
func TestTimeLapseOfAnEmptyHistory(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	if body := string(p.get("/api/timelapse")); !strings.Contains(body, `"steps":[]`) {
		t.Errorf("answer %s", body)
	}
}
