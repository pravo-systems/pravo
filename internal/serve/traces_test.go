package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

const (
	traceA = "46e115fc55d648de7ac0cb939d8dc438-82f7f202630ca525"
	traceB = "00000000000000000000000000000001-0000000000000002"
)

// runFile writes a recorded run the way the commands do.
func runFile(t *testing.T, dir, id string, rt *telemetry.RunTrace) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".pravo", "traces"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(rt)
	if err := os.WriteFile(filepath.Join(dir, ".pravo", "traces", id+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sampleRun(reason string) *telemetry.RunTrace {
	return &telemetry.RunTrace{
		Version: telemetry.TraceFormat, TraceID: traceA[:32], Service: "pravo", Start: "2026-10-25T07:08:39.317964137Z", DurationUS: 55865,
		Spans: []telemetry.SpanData{
			{ID: "82f7f202630ca525", Name: "pravo apply", StartUS: 0, DurUS: 55865, Attrs: map[string]any{"pravo.command": "pravo apply", "pravo.exit_code": 1.0, "pravo.actor": "ada", "pravo.reason": reason},
				Events: []telemetry.EventData{{Name: "loaded", AtUS: 100}}},
			{ID: "00000000000000a1", Parent: "82f7f202630ca525", Name: "engine apply", StartUS: 9000, DurUS: 40000, Attrs: map[string]any{"pravo.txn": "20261025T070839Z-144244"}},
			{ID: "00000000000000a2", Parent: "00000000000000a1", Name: "resource db", StartUS: 10000, DurUS: 30000, Status: "error", Message: "boom\x1b[31m " + secretToken,
				Attrs: map[string]any{"pravo.resource.address": "db", "pravo.action": "create", "list": []any{"a", "b"}, "n": 3.0}},
			{ID: "00000000000000a3", Parent: "00000000000000a2", Name: "provider ApplyResourceChange", StartUS: 11000, DurUS: 20000},
		},
	}
}

func TestTracesListAndShowARun(t *testing.T) {
	p := newProject(t)
	runFile(t, p.dir, traceA, sampleRun("ship it"))
	var list TraceList
	waitFor(t, "the run is listed", func() bool {
		list = TraceList{}
		_ = json.Unmarshal(p.get("/api/traces"), &list)
		return len(list.Traces) == 1
	})
	got := list.Traces[0]
	want := TraceInfo{ID: traceA, Command: "pravo apply", Start: "2026-10-25T07:08:39.317964137Z", DurationUS: 55865, Exit: 1, Status: "error", SpanCount: 4, Errors: 1, Resources: 1, Txn: "20261025T070839Z-144244", Actor: "ada", Reason: "ship it"}
	if got != want || list.Dir != ".pravo/traces" {
		t.Fatalf("list %+v dir %q", got, list.Dir)
	}
	var tr Trace
	if err := json.Unmarshal(p.get("/api/trace?id="+traceA), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.TraceInfo != want || tr.TraceID != traceA[:32] || len(tr.Spans) != 4 {
		t.Fatalf("trace %+v", tr)
	}
	kinds := []string{"command", "engine", "resource", "provider"}
	for i, s := range tr.Spans {
		if s.Kind != kinds[i] {
			t.Errorf("%s is %s", s.Name, s.Kind)
		}
	}
	res := tr.Spans[2]
	if strings.Contains(res.Message, secretToken) || strings.ContainsRune(res.Message, 0x1b) || !strings.HasPrefix(res.Message, "boom") {
		t.Errorf("message %q", res.Message)
	}
	if res.Attrs["n"] != 3.0 || len(res.Attrs["list"].([]any)) != 2 {
		t.Errorf("attrs %v", res.Attrs)
	}
}

func TestTracesNameATransactionOnlyForAnApply(t *testing.T) {
	rt := sampleRun("")
	rt.Spans[1].Name = "engine plan"
	if got := summarize(traceA, rt).Txn; got != "" {
		t.Errorf("a plan names the transaction %q", got)
	}
}

func TestTracesMaskWhatTheStateKeepsSecret(t *testing.T) {
	p := newProject(t)
	runFile(t, p.dir, traceA, sampleRun("token "+secretToken))
	for _, path := range []string{"/api/traces", "/api/trace?id=" + traceA} {
		noSecrets(t, path, p.get(path), allSecrets...)
	}
}

// The host of a request is sent in the clear and is not masked, even when a word of it is
// a sensitive value of the state; the rest of the request is.
func TestTracesKeepTheHostOfARequestAndMaskTheRest(t *testing.T) {
	p := newProject(t)
	host := "bucket-" + secretToken + ".s3.amazonaws.com"
	rt := sampleRun("")
	rt.Spans = append(rt.Spans, telemetry.SpanData{ID: "00000000000000b1", Parent: "82f7f202630ca525", Name: "network GET " + host, StartUS: 12000, DurUS: 1000,
		Attrs: map[string]any{"server.address": host, "url.path": "/state/" + secretToken + "/x.json", "http.request.method": "GET"}})
	runFile(t, p.dir, traceA, rt)
	body := string(p.get("/api/trace?id=" + traceA))
	var got struct {
		Spans []struct {
			Name  string
			Attrs map[string]any
		}
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sp := range got.Spans {
		if !strings.HasPrefix(sp.Name, "network ") {
			continue
		}
		found = true
		if sp.Name != "network GET "+host || sp.Attrs["server.address"] != host {
			t.Errorf("the host is masked: %q, %v", sp.Name, sp.Attrs["server.address"])
		}
		if path, _ := sp.Attrs["url.path"].(string); strings.Contains(path, secretToken) || !strings.Contains(path, mask) {
			t.Errorf("the path is not masked: %q", path)
		}
	}
	if !found {
		t.Fatal("no network span in the answer")
	}
}

func TestTracesRefuseWhatIsNotARun(t *testing.T) {
	p := newProject(t)
	for _, id := range []string{"", "x", "../../etc/passwd", traceA + "/..", strings.ToUpper(traceA), traceA + ".json"} {
		if resp, _ := p.do("GET", "/api/trace?id="+queryEscape(id), nil); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("id %q: %d", id, resp.StatusCode)
		}
	}
	if resp, _ := p.do("GET", "/api/trace?id="+traceB, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a run that is not there: %d", resp.StatusCode)
	}
	// A file that is not a run is counted, not shown; a link is not followed.
	dir := filepath.Join(p.dir, ".pravo", "traces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, traceB+".json"), []byte(`{"version":9}`), 0o644)
	runFile(t, p.dir, traceA, sampleRun(""))
	secret := filepath.Join(p.dir, "outside.json")
	os.WriteFile(secret, []byte(`{"version":1,"spans":[{"id":"x","name":"engine x"}]}`), 0o644)
	other := "00000000000000000000000000000003-0000000000000004"
	if err := os.Symlink(secret, filepath.Join(dir, other+".json")); err == nil {
		if resp, _ := p.do("GET", "/api/trace?id="+other, nil); resp.StatusCode == http.StatusOK {
			t.Error("a link was followed")
		}
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	var list TraceList
	json.Unmarshal(p.get("/api/traces"), &list)
	if len(list.Traces) != 1 || list.Unreadable < 1 {
		t.Errorf("list %+v", list)
	}
	if resp, _ := p.do("GET", "/api/trace?id="+traceB, nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a file of another format: %d", resp.StatusCode)
	}
}

func TestTracesNeedTheTokenAndWriteNothing(t *testing.T) {
	p := newProject(t)
	runFile(t, p.dir, traceA, sampleRun(""))
	for _, path := range []string{"/api/traces", "/api/trace?id=" + traceA} {
		req, _ := http.NewRequest(http.MethodGet, p.base+path, nil)
		if resp, _ := p.send(req); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a token: %d", path, resp.StatusCode)
		}
	}
	before := digestTree(t, p.dir)
	p.get("/api/traces")
	p.get("/api/trace?id=" + traceA)
	p.get("/api/graph")
	time.Sleep(200 * time.Millisecond)
	if digestTree(t, p.dir) != before {
		t.Error("reading the runs changed the project directory")
	}
}

func TestTracesFromAnotherDirectory(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, _ := state.Open(filepath.Join(dir, "pravo.state"))
	seed(t, st)
	other := t.TempDir()
	data, _ := json.Marshal(sampleRun(""))
	os.WriteFile(filepath.Join(other, traceA+".json"), data, 0o644)
	p := start(t, dir, st, Options{TraceDir: other})
	var list TraceList
	json.Unmarshal(p.get("/api/traces"), &list)
	if len(list.Traces) != 1 || list.Dir != other {
		t.Errorf("list %+v", list)
	}
}

func TestWatcherSeesRecordedRuns(t *testing.T) {
	dir := t.TempDir()
	traces := filepath.Join(dir, "traces")
	w := newWatcher(time.Hour, dir, "")
	w.traces = traces
	sub, cancel := w.bus.subscribe()
	defer cancel()
	w.sample()
	before := w.revision()
	os.MkdirAll(traces, 0o755)
	data, _ := json.Marshal(sampleRun(""))
	os.WriteFile(filepath.Join(traces, traceA+".json"), data, 0o644)
	w.sample()
	if w.revision() != before {
		t.Fatal("a recorded run moved the revision of the model")
	}
	select {
	case ev := <-sub:
		found := false
		for _, c := range ev.What {
			found = found || c == "traces"
		}
		if !found {
			t.Errorf("event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestSpanKinds(t *testing.T) {
	for name, want := range map[string]string{
		"pravo plan": "command", "engine apply": "engine", "engine discover prod": "engine", "phase lock": "phase",
		"setup load specification": "setup", "config evaluate": "setup", "resource a.b": "resource",
		"provider ReadResource": "provider", "state get resources/a.json": "state", "mcp tools/call x": "mcp", "unseal resource a.b": "seal", "seal outputs": "seal", "unseal state keys": "seal", "network GET s3.amazonaws.com": "network", "network tunnel s3.amazonaws.com": "network", "anything": "other",
	} {
		if got := spanKind(name); got != want {
			t.Errorf("%q is %s, want %s", name, got, want)
		}
	}
}
