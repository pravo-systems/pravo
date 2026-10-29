package serve

import (
	"bufio"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/chat"
)

// The tools of the chat are these and no others, and each is a GET of a path that
// the page itself reads and that writes nothing.
func TestChatToolsAreTheReadPaths(t *testing.T) {
	want := []string{"access", "discovery", "node", "overview", "plan", "plans", "policies", "search", "secrets", "source", "trace", "traces"}
	got := chatToolNames()
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	readPaths := map[string]bool{"/api/graph": true, "/api/search": true, "/api/detail": true, "/api/source": true, "/api/plans": true, "/api/plan": true,
		"/api/policies": true, "/api/discovery": true, "/api/secrets": true, "/api/access": true, "/api/traces": true, "/api/trace": true}
	for _, tl := range chatTools {
		if !readPaths[tl.path] {
			t.Errorf("tool %s reads %s, which is not a read path of the page", tl.name, tl.path)
		}
	}
}

// The files of the chat start no process and reach no package that writes the state
// or applies a plan; the package chat reaches nothing of pravo at all.
func TestChatImportsNothingThatWrites(t *testing.T) {
	forbidden := []string{"os/exec", "github.com/pravo-systems/pravo/internal/engine", "github.com/pravo-systems/pravo/internal/state", "github.com/pravo-systems/pravo/internal/protect"}
	check := func(path string, forbid []string) {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			p := strings.Trim(im.Path.Value, `"`)
			for _, bad := range forbid {
				if p == bad {
					t.Errorf("%s imports %s", path, p)
				}
			}
		}
	}
	check("chat.go", forbidden)
	files, _ := filepath.Glob("../chat/*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		f2, _ := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		for _, im := range f2.Imports {
			p := strings.Trim(im.Path.Value, `"`)
			if strings.Contains(p, "pravo-systems") || p == "os/exec" || p == "os" {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
}

func askChat(t *testing.T, p *project, token string, body string, ctype string) (int, []chat.Event) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, p.base+"/api/chat", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var evs []chat.Event
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			var e chat.Event
			if json.Unmarshal([]byte(line[6:]), &e) == nil {
				evs = append(evs, e)
			}
		}
	}
	return resp.StatusCode, evs
}

func call(id, name, args string) chat.Call {
	return chat.Call{ID: id, Name: name, Args: json.RawMessage(args)}
}

// A model that tries every tool, and every write it can think of, changes nothing
// and is sent no secret, in the answers of the tools or in what the provider is asked.
func TestChatSessionIsReadOnlyAndRedacted(t *testing.T) {
	p := newProject(t)
	script := &chat.Script{Replies: []chat.Response{
		{Calls: []chat.Call{
			call("1", "overview", `{}`),
			call("2", "node", `{"addr":"services.a.db"}`),
			call("3", "source", `{"addr":"services.a.db"}`),
			call("4", "search", `{"q":"db"}`),
			call("5", "plans", `{}`),
			call("6", "policies", `{}`),
			call("7", "secrets", `{}`),
			call("8", "access", `{}`),
			call("9", "discovery", `{}`),
			call("10", "traces", `{}`),
		}},
		{Calls: []chat.Call{
			call("11", "pravo_apply_plan", `{"plan":"plan-abcdef"}`),
			call("12", "pravo_state_query", `{"expr":"resources"}`),
			call("13", "node", `{"addr":"services.a.db","path":"/etc/passwd"}`),
			call("14", "node", `{}`),
			call("15", "adopt", `{}`),
		}},
		{Text: "services.a.db is a database; its password is withheld."},
	}}
	p.srv.opts.Chat = &ChatOptions{Provider: script, Name: "fake", Model: "m", Host: "example.test"}
	before := digestTree(t, p.dir)

	status, evs := askChat(t, p, p.srv.Token(), `{"messages":[{"role":"user","text":"what is services.a.db?"}],"context":{"addr":"services.a.db","view":"inventory"}}`, "application/json")
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	var done *chat.Event
	for i := range evs {
		if evs[i].Type == "done" {
			done = &evs[i]
		}
		if evs[i].Type == "error" {
			t.Fatalf("error: %s", evs[i].Text)
		}
	}
	if done == nil || len(done.Sent) != 15 {
		t.Fatalf("done = %+v", done)
	}
	for _, s := range done.Sent {
		switch s.Tool {
		case "pravo_apply_plan", "pravo_state_query", "adopt":
			if !strings.HasPrefix(s.Error, "no such tool") {
				t.Errorf("%s was not refused: %+v", s.Tool, s)
			}
		}
		if s.Tool == "node" && s.Error == "" && strings.Contains(string(s.Args), "passwd") {
			t.Errorf("an argument that the tool does not take was passed: %+v", s)
		}
	}
	// What the provider was asked holds no secret, and says where the person looks.
	for _, r := range script.Requests {
		raw, _ := json.Marshal(r)
		for _, sec := range allSecrets {
			if strings.Contains(string(raw), sec) {
				t.Errorf("a request to the provider holds %q", sec)
			}
		}
	}
	last, _ := json.Marshal(script.Requests[0].Messages)
	if !strings.Contains(string(last), "services.a.db") {
		t.Errorf("the context was not given to the model: %s", last)
	}
	if after := digestTree(t, p.dir); after != before {
		t.Errorf("the project directory changed")
	}
	if p.srv.chatActive.Load() != 0 {
		t.Errorf("a question is still counted")
	}
}

func TestChatRouteIsGuarded(t *testing.T) {
	p := newProject(t)
	// Off: there is nothing to ask.
	if status, _ := askChat(t, p, p.srv.Token(), `{}`, "application/json"); status != 404 {
		t.Errorf("off: status %d", status)
	}
	if resp, body := p.do("GET", "/api/chat/config", map[string]string{"Authorization": "Bearer " + p.srv.Token()}); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"enabled":false`) {
		t.Errorf("config off: %d %s", resp.StatusCode, body)
	}
	p.srv.opts.Chat = &ChatOptions{Provider: &chat.Script{}, Name: "fake", Model: "m", Host: "example.test"}
	for name, c := range map[string]struct {
		token, body, ctype string
		want               int
	}{
		"no token":        {"", `{"messages":[{"role":"user","text":"x"}]}`, "application/json", 401},
		"wrong type":      {p.srv.Token(), `{"messages":[{"role":"user","text":"x"}]}`, "text/plain", 415},
		"not json":        {p.srv.Token(), `nope`, "application/json", 400},
		"no question":     {p.srv.Token(), `{"messages":[{"role":"assistant","text":"x"}]}`, "application/json", 400},
		"a tool turn":     {p.srv.Token(), `{"messages":[{"role":"tool","text":"x"},{"role":"user","text":"y"}]}`, "application/json", 400},
		"too long a text": {p.srv.Token(), `{"messages":[{"role":"user","text":"` + strings.Repeat("a", maxChatText+1) + `"}]}`, "application/json", 400},
	} {
		if status, _ := askChat(t, p, c.token, c.body, c.ctype); status != c.want {
			t.Errorf("%s: status %d, want %d", name, status, c.want)
		}
	}
	// A page of another origin cannot ask.
	req, _ := http.NewRequest(http.MethodPost, p.base+"/api/chat", strings.NewReader(`{"messages":[{"role":"user","text":"x"}]}`))
	req.Header.Set("Authorization", "Bearer "+p.srv.Token())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign origin: status %d", resp.StatusCode)
	}
}
