package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// hookLog is a webhook that remembers the events it was sent.
type hookLog struct {
	mu     sync.Mutex
	events []planEvent
}

func newHook(t *testing.T) (*hookLog, string) {
	t.Helper()
	h := &hookLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev planEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			t.Errorf("webhook body %s: %v", body, err)
		}
		h.mu.Lock()
		h.events = append(h.events, ev)
		h.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return h, srv.URL
}

func (h *hookLog) kinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ks []string
	for _, e := range h.events {
		ks = append(ks, e.Event)
	}
	return ks
}

func TestDriftPlanStoresAPlanThatPutsTheDriftRight(t *testing.T) {
	prov := newDemoProvider(t)
	dir := demoEstate(t, "keep: $demo.#Resource.demo_server & {name: \"keep\"}\nlost: $demo.#Resource.demo_server & {name: \"lost\"}\n")
	if text, code := runPravo(t, dir, "apply", "--auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	hook, url := newHook(t)

	// Without drift there is nothing to propose and nothing to send.
	if text, code := runPravo(t, dir, "drift", "--plan", "--store", "--webhook", url); code != 0 || !strings.Contains(text, "No drift.") {
		t.Fatalf("drift without drift exited %d:\n%s", code, text)
	}
	if len(hook.kinds()) != 0 || len(planObjects(t, dir)) != 0 {
		t.Fatalf("events %v, plans %v", hook.kinds(), planObjects(t, dir))
	}

	// Someone deletes a server outside pravo.
	prov.remove("srv-lost")

	// --plan alone shows the plan and stores nothing.
	text, code := runPravo(t, dir, "drift", "--plan", "--webhook", url)
	if code != 2 || !strings.Contains(text, "Drift.") || !strings.Contains(text, "lost") || strings.Contains(text, "Stored the plan") {
		t.Fatalf("drift --plan exited %d:\n%s", code, text)
	}
	if got := hook.kinds(); len(got) != 1 || got[0] != eventDrifted {
		t.Fatalf("events %v", got)
	}
	if len(planObjects(t, dir)) != 0 {
		t.Fatalf("a plan was stored: %v", planObjects(t, dir))
	}

	// --store keeps it, and the event says where.
	text, code = runPravo(t, dir, "drift", "--plan", "--store", "--ttl", "36h", "--webhook", url)
	if code != 2 || !strings.Contains(text, "Stored the plan as") || !strings.Contains(text, "36 hours") {
		t.Fatalf("drift --plan --store exited %d:\n%s", code, text)
	}
	id := storedID.FindString(text)
	hook.mu.Lock()
	ev := hook.events[len(hook.events)-1]
	hook.mu.Unlock()
	if ev.Event != eventStored || ev.Plan == nil || ev.Plan.ID != id || ev.Plan.ApplyCommand != "pravo apply "+id || len(ev.Drifted) != 1 || ev.Drifted[0].Addr != "lost" {
		t.Fatalf("event %+v", ev)
	}
	// Drift never applies: the server is still gone.
	if prov.has("srv-lost") {
		t.Fatal("drift applied the plan")
	}

	// The plan is applied by its id, which puts the file right and says so.
	if text, code = runPravo(t, dir, "apply", id, "--webhook", url); code != 0 || !strings.Contains(text, "Apply complete!") {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	if !prov.has("srv-lost") {
		t.Fatal("the apply did not put the server back")
	}
	if got := hook.kinds(); got[len(got)-1] != eventApplied {
		t.Fatalf("events %v", got)
	}
}

func TestDriftPlanFlagsAgree(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package infra\n\nsize: 3\n"})
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"drift", "--store"}, "--store applies to --plan"},
		{[]string{"drift", "--plan", "--ttl", "1h"}, "--ttl applies to --store"},
		{[]string{"drift", "--plan", "--due"}, "do not go with --due"},
		{[]string{"drift", "--plan", "--store", "--ttl", "0"}, "above zero"},
		{[]string{"apply", "--webhook", "http://x"}, "--webhook applies to a stored plan"},
	} {
		if text, code := runPravo(t, dir, c.args...); code != 1 || !strings.Contains(text, c.want) {
			t.Errorf("%v exited %d:\n%s", c.args, code, text)
		}
	}
}

// remove deletes the objects with these ids from the catalog, as someone outside pravo would.
func (p *demoProvider) remove(ids ...string) {
	p.t.Helper()
	var keep []map[string]string
	for _, o := range p.catalog() {
		if !slices.Contains(ids, o["id"]) {
			keep = append(keep, o)
		}
	}
	b, _ := json.Marshal(keep)
	if err := os.WriteFile(filepath.Join(p.dir, "catalog.json"), b, 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func (p *demoProvider) has(id string) bool {
	for _, o := range p.catalog() {
		if o["id"] == id {
			return true
		}
	}
	return false
}
