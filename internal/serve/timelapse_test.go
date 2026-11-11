package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	if l.Steps[0].Time.After(l.Steps[1].Time) {
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

func attrValue(d Detail, name string) (any, bool) {
	for _, a := range d.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return nil, false
}

// The detail of a node as an earlier run left it: the record with the later runs undone, newest first. A value that a
// diff does not keep is never made up, and a node that a run removed is told by its history alone.
func TestDetailAsOfARun(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	r := st.Get("services.a.db")
	before := string(r.Attributes)
	mid := strings.Replace(before, `"port":5432`, `"port":5433`, 1)
	last := strings.Replace(mid, `"port":5433`, `"port":5434`, 1)
	for _, to := range []string{mid, last} {
		cur := st.Get("services.a.db")
		txn := st.Begin("apply", "pravo apply")
		next := *cur
		next.Attributes = json.RawMessage(to)
		if err := st.Set(&next, txn.ID()); err != nil {
			t.Fatal(err)
		}
		txn.Record("services.a.db", "update", cur.Revision, next.Revision)
		txn.RecordDiff("services.a.db", cur.Attributes, next.Attributes, nil)
		if err := txn.Finish("applied", "1 changed", true); err != nil {
			t.Fatal(err)
		}
	}
	gone := st.Begin("apply", "pravo apply")
	old := st.Get("old.thing")
	if _, err := st.Remove("old.thing"); err != nil {
		t.Fatal(err)
	}
	gone.Record("old.thing", "delete", old.Revision, 0)
	gone.RecordDiff("old.thing", old.Attributes, nil, nil)
	if err := gone.Finish("applied", "1 deleted", true); err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	l := lapseOf(t, p)
	if len(l.Steps) != 4 {
		t.Fatalf("steps %d", len(l.Steps))
	}
	at := func(addr string, step int) Detail {
		var d Detail
		if err := json.Unmarshal(p.get("/api/detail?addr="+addr+"&at="+l.Steps[step].ID), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	for step, want := range []string{"5432", "5433", "5434", "5434"} {
		d := at("services.a.db", step)
		if d.AsOf == nil || !d.AsOf.Known || d.AsOf.ID != l.Steps[step].ID {
			t.Fatalf("step %d: as of %+v", step, d.AsOf)
		}
		if v, ok := attrValue(d, "port"); !ok || fmt.Sprint(v) != want {
			t.Errorf("step %d: port %v, want %s", step, v, want)
		}
		if d.Change != nil || d.Lock != nil {
			t.Errorf("step %d: a change or a lock of today", step)
		}
	}
	// the history says which of its entries show in the time lapse
	cur := p.detail("services.a.db").History
	lapse := 0
	for _, e := range cur {
		if e.InLapse {
			lapse++
		}
	}
	if lapse != 3 {
		t.Errorf("entries that link to the time lapse: %d of %+v", lapse, cur)
	}
	// a node that a run removed: not in the graph, but its history and the names of what it held are told
	g := at("old.thing", 3)
	if g.AsOf == nil || !g.AsOf.Gone || len(g.History) < 2 {
		t.Fatalf("removed node: %+v", g)
	}
	if early := at("old.thing", 0); early.AsOf == nil || !early.AsOf.Gone {
		t.Errorf("removed node, before it went: %+v", early)
	}
	// nothing leaks that the plain detail withholds
	raw, _ := json.Marshal(at("services.a.db", 0))
	for _, secret := range []string{secretBlob, secretToken, secretMapKey} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the detail of an earlier run shows %q", secret)
		}
	}
	// a run that is not in the state is not found
	if resp, _ := p.do("GET", "/api/detail?addr=services.a.db&at=nope", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown run: status %d", resp.StatusCode)
	}
}
