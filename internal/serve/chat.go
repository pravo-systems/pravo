package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/pravo-systems/pravo/internal/chat"
)

// The chat is README rule 12. It reads through the same handlers as the page, with
// the token of the run, so that a value reaches a model only as the page would show
// it. Its tools are listed here and in no other place; none writes.

// ChatOptions turns the chat on. Nil in Options means it is off: the routes do not
// exist and the page draws no control.
type ChatOptions struct {
	Provider chat.Provider
	// Name, Model and Host are shown to the person: who is asked, and where.
	Name  string
	Model string
	Host  string
}

const (
	maxChatBody   = 64 << 10
	maxChatTurns  = 40
	maxChatText   = 4000
	maxToolResult = 32 << 10
	maxChatActive = 4
)

// ChatConfig is GET /api/chat/config: what the page tells a person before the
// first question. It never holds a key.
type ChatConfig struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Host     string `json:"host,omitempty"`
	// Tools names what the model can read, for the notice.
	Tools []string `json:"tools,omitempty"`
}

// ChatRequest is POST /api/chat. The conversation is kept by the page: the server
// holds none, and writes none.
type ChatRequest struct {
	Messages []ChatTurn `json:"messages"`
	// Context is what the person is looking at.
	Context struct {
		Addr string `json:"addr"`
		Plan string `json:"plan"`
		View string `json:"view"`
	} `json:"context"`
}

// ChatTurn is a question or an answer.
type ChatTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// chatToolSpec is one tool: a fixed GET of the page's own API. params maps the
// name of an argument to the name of the query parameter it fills; every argument is
// a string and every one listed is required unless it is in optional.
type chatToolSpec struct {
	name     string
	desc     string
	path     string
	params   [][2]string
	optional map[string]bool
}

// chatTools is the whole set. A test requires that every path is a GET of the
// page that cannot write, and that nothing else is reachable.
var chatTools = []chatToolSpec{
	{name: "overview", desc: "The estate at a glance: project, state, counts, the plan that is open, warnings, groups and every resource with its type, whether it is declared and recorded, the action of the open plan and marks (drifted, gone, locked, sealed). Start here.", path: "/api/graph"},
	{name: "search", desc: "Find resources, plans and findings by words.", path: "/api/search", params: [][2]string{{"q", "q"}}},
	{name: "node", desc: "One resource by its exact address: its values (sensitive ones are withheld), dependencies, dependents, history, lock and, when a plan is given, the planned change.", path: "/api/detail", params: [][2]string{{"addr", "addr"}, {"plan", "plan"}}, optional: map[string]bool{"plan": true}},
	{name: "source", desc: "The lines of the specification that write a resource, by its exact address.", path: "/api/source", params: [][2]string{{"addr", "addr"}, {"plan", "plan"}}, optional: map[string]bool{"plan": true}},
	{name: "plans", desc: "The plans stored in the state: who made each, its status, what it changes, approvals.", path: "/api/plans"},
	{name: "plan", desc: "One stored plan by its id (plan-…): its changes, reasons and the text its author wrote.", path: "/api/plan", params: [][2]string{{"id", "id"}}},
	{name: "policies", desc: "The policies in force, their versions, and what they said about plans.", path: "/api/policies"},
	{name: "discovery", desc: "What discoveries found that the state does not manage.", path: "/api/discovery"},
	{name: "secrets", desc: "The secrets of the state: names, versions, who set them, which resources read them, and what is wrong. Never a value.", path: "/api/secrets"},
	{name: "access", desc: "How the state is protected: members, roles and what needs attention.", path: "/api/access"},
	{name: "traces", desc: "The recorded runs of pravo, newest first, with a summary of each.", path: "/api/traces"},
	{name: "trace", desc: "The spans of one recorded run, by its id.", path: "/api/trace", params: [][2]string{{"id", "id"}}},
}

func chatToolNames() []string {
	var out []string
	for _, t := range chatTools {
		out = append(out, t.name)
	}
	return out
}

// serverTools is the chat.Tools of a server.
type serverTools struct{ s *Server }

func (serverTools) Defs() []chat.ToolDef {
	var defs []chat.ToolDef
	for _, t := range chatTools {
		props := map[string]any{}
		var req []string
		for _, p := range t.params {
			props[p[0]] = map[string]any{"type": "string"}
			if !t.optional[p[0]] {
				req = append(req, p[0])
			}
		}
		sch := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(req) > 0 {
			sch["required"] = req
		}
		defs = append(defs, chat.ToolDef{Name: t.name, Description: t.desc, Schema: sch})
	}
	return defs
}

func (t serverTools) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	for _, spec := range chatTools {
		if spec.name != name {
			continue
		}
		in := map[string]string{}
		if len(bytes.TrimSpace(args)) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errors.New("the arguments have to be an object of strings")
			}
		}
		q := url.Values{}
		allowed := map[string]string{}
		for _, p := range spec.params {
			allowed[p[0]] = p[1]
		}
		for k, v := range in {
			qk, ok := allowed[k]
			if !ok {
				return "", fmt.Errorf("%s takes no argument %q", name, k)
			}
			if len(v) > 2048 || !utf8.ValidString(v) {
				return "", fmt.Errorf("argument %q is not usable", k)
			}
			if v != "" {
				q.Set(qk, v)
			}
		}
		for _, p := range spec.params {
			if !spec.optional[p[0]] && q.Get(p[1]) == "" {
				return "", fmt.Errorf("%s needs %q", name, p[0])
			}
		}
		body, status, err := t.s.readAPI(spec.path, q)
		if err != nil {
			return "", err
		}
		if status != http.StatusOK {
			return "", fmt.Errorf("%s", apiErrorText(body, status))
		}
		if name == "overview" {
			body = overviewOf(body)
		}
		return clipTool(body), nil
	}
	return "", errors.New("no such tool: " + name)
}

func apiErrorText(body []byte, status int) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return fmt.Sprintf("the page answered %d", status)
}

func clipTool(b []byte) string {
	if len(b) <= maxToolResult {
		return string(b)
	}
	cut := maxToolResult
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n[cut: %d more bytes. Ask for something narrower.]", b[:cut], len(b)-cut)
}

// readAPI asks the page's own API, as the page does, in the process: the request
// is a GET with the token of the run, so it goes through the same checks and the
// same redaction.
func (s *Server) readAPI(path string, q url.Values) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	rec := &memWriter{h: http.Header{}, status: http.StatusOK}
	s.api(rec, req)
	return rec.buf.Bytes(), rec.status, nil
}

type memWriter struct {
	h      http.Header
	buf    bytes.Buffer
	status int
}

func (w *memWriter) Header() http.Header         { return w.h }
func (w *memWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *memWriter) WriteHeader(status int)      { w.status = status }

// overviewOf shortens the graph to lines a model can read: the nodes without their
// positions or sources.
func overviewOf(raw []byte) []byte {
	var g Graph
	if json.Unmarshal(raw, &g) != nil {
		return raw
	}
	const maxNodes = 400
	var nodes []string
	for i, n := range g.Nodes {
		if i == maxNodes {
			nodes = append(nodes, fmt.Sprintf("… %d more: use search", len(g.Nodes)-maxNodes))
			break
		}
		var marks []string
		if n.Action != "" {
			marks = append(marks, "plan:"+n.Action)
		}
		for _, m := range []struct {
			on   bool
			name string
		}{{n.Drifted, "drifted"}, {n.Gone, "gone"}, {n.Locked, "locked"}, {n.Sealed, "sealed"}, {n.NoSchema, "no-schema"}} {
			if m.on {
				marks = append(marks, m.name)
			}
		}
		nodes = append(nodes, strings.TrimSpace(fmt.Sprintf("%s %s %s %s", n.Addr, n.Type, n.Presence, strings.Join(marks, ","))))
	}
	var spaces []string
	for _, ns := range g.Namespaces {
		spaces = append(spaces, fmt.Sprintf("%s (%d)", ns.Path, ns.Resources))
	}
	out, err := json.Marshal(map[string]any{
		"project":    map[string]any{"package": g.Project.Package, "version": g.Project.Version, "state": g.Project.State, "spec_error": g.Project.SpecError},
		"plan":       g.Plan,
		"warnings":   g.Warnings,
		"groups":     spaces,
		"outputs":    len(g.Outputs),
		"node_lines": nodes,
		"edges":      len(g.Edges),
	})
	if err != nil {
		return raw
	}
	return out
}

func (s *Server) chatConfig(w http.ResponseWriter) {
	c := s.opts.Chat
	if c == nil {
		writeJSON(w, http.StatusOK, ChatConfig{})
		return
	}
	writeJSON(w, http.StatusOK, ChatConfig{Enabled: true, Provider: c.Name, Model: c.Model, Host: c.Host, Tools: chatToolNames()})
}

// chatHandler answers one question as a stream of events: a "read" for each tool as
// it is called, then "text" and "done" with everything that was sent to the model,
// or "error". It is the one request of the page that talks to another host, and
// reads nothing that the page's own requests do not read.
func (s *Server) chatHandler(w http.ResponseWriter, r *http.Request) {
	c := s.opts.Chat
	if c == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "send application/json")
		return
	}
	var req ChatRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxChatBody+1))
	if err := dec.Decode(&req); err != nil || dec.More() {
		writeError(w, http.StatusBadRequest, "the body is not a question")
		return
	}
	hist, err := chatHistory(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.chatActive.Add(1) > maxChatActive {
		s.chatActive.Add(-1)
		writeError(w, http.StatusServiceUnavailable, "too many questions at once")
		return
	}
	defer s.chatActive.Add(-1)
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not possible here")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	emit := func(e chat.Event) {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "data: %s\n\n", data)
		fl.Flush()
	}
	_, _, err = chat.Run(r.Context(), c.Provider, serverTools{s}, hist, chat.Limits{}, emit)
	if err != nil {
		emit(chat.Event{Type: "error", Text: err.Error()})
	}
}

// chatHistory checks the conversation the page sends and puts what the person is
// looking at after the last question.
func chatHistory(req *ChatRequest) ([]chat.Message, error) {
	n := len(req.Messages)
	if n == 0 || n > maxChatTurns || req.Messages[n-1].Role != "user" {
		return nil, errors.New("send the conversation so far, ending with a question")
	}
	var out []chat.Message
	for i, m := range req.Messages {
		if m.Role != "user" && m.Role != "assistant" || m.Text == "" || len(m.Text) > maxChatText || !utf8.ValidString(m.Text) {
			return nil, fmt.Errorf("message %d is not usable", i+1)
		}
		out = append(out, chat.Message{Role: m.Role, Text: m.Text})
	}
	var look []string
	for _, kv := range [][2]string{{"resource", req.Context.Addr}, {"stored plan", req.Context.Plan}, {"section", req.Context.View}} {
		v := kv[1]
		if v == "" || len(v) > 256 || !utf8.ValidString(v) || strings.ContainsAny(v, "\r\n") {
			continue
		}
		look = append(look, kv[0]+": "+v)
	}
	if len(look) > 0 {
		out[n-1].Text += "\n\n(The person is looking at " + strings.Join(look, "; ") + ".)"
	}
	return out, nil
}
