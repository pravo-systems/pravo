package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixedTools struct{ ran []string }

func (f *fixedTools) Defs() []ToolDef {
	return []ToolDef{{Name: "look", Description: "d", Schema: map[string]any{"type": "object"}}}
}
func (f *fixedTools) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	f.ran = append(f.ran, name)
	return "result of " + name, nil
}

func TestRunRefusesWhatIsNotOffered(t *testing.T) {
	tools := &fixedTools{}
	p := &Script{Replies: []Response{
		{Calls: []Call{{ID: "1", Name: "apply", Args: json.RawMessage(`{}`)}, {ID: "2", Name: "look", Args: json.RawMessage(`{}`)}}},
		{Text: "done"},
	}}
	var evs []Event
	_, sent, err := Run(context.Background(), p, tools, []Message{{Role: "user", Text: "q"}}, Limits{}, func(e Event) { evs = append(evs, e) })
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.ran) != 1 || tools.ran[0] != "look" {
		t.Errorf("ran %v", tools.ran)
	}
	if len(sent) != 2 || !strings.Contains(sent[0].Error, "no such tool") {
		t.Errorf("sent %+v", sent)
	}
	if evs[len(evs)-1].Type != "done" {
		t.Errorf("events %+v", evs)
	}
}

func TestRunBoundsTheCalls(t *testing.T) {
	tools := &fixedTools{}
	var replies []Response
	for range 20 {
		replies = append(replies, Response{Calls: []Call{{ID: "x", Name: "look"}}})
	}
	_, _, err := Run(context.Background(), &Script{Replies: replies}, tools, nil, Limits{Calls: 3}, func(Event) {})
	if err == nil {
		t.Fatal("a model that never answers was not stopped")
	}
	if len(tools.ran) != 3 {
		t.Errorf("ran %d tools, want 3", len(tools.ran))
	}
}

type actTools struct{ fixedTools }

func (f *actTools) Defs() []ToolDef {
	return append(f.fixedTools.Defs(), ToolDef{Name: "act", Description: "d", Schema: map[string]any{"type": "object"}})
}

// A tool with its own count can still be called when the reads are spent, as many times as its own count allows.
func TestRunCountsOwnToolsApart(t *testing.T) {
	tools := &actTools{}
	look, act := Call{ID: "l", Name: "look"}, Call{ID: "a", Name: "act"}
	p := &Script{Replies: []Response{
		{Calls: []Call{look, look, look}},
		{Calls: []Call{act, act, act}},
		{Text: "done"},
	}}
	_, sent, err := Run(context.Background(), p, tools, nil, Limits{Calls: 2, Own: map[string]int{"act": 2}}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tools.ran, ","); got != "look,look,act,act" {
		t.Errorf("ran %s", got)
	}
	if !strings.Contains(sent[2].Error, "you can still call act") || !strings.Contains(sent[5].Error, "act was called 2 times") {
		t.Errorf("sent %+v", sent)
	}
}

// A call cut off at the length of an answer does not run and is not counted, and the history takes back whole arguments.
func TestRunRefusesACutCall(t *testing.T) {
	tools := &actTools{}
	p := &Script{Replies: []Response{
		{Calls: []Call{{ID: "a", Name: "act", Args: json.RawMessage(`{"items":[{"lab`)}}, Cut: true},
		{Calls: []Call{{ID: "b", Name: "act", Args: json.RawMessage(`{}`)}}, Cut: true},
		{Calls: []Call{{ID: "c", Name: "act", Args: json.RawMessage(`{}`)}}},
		{Text: "done"},
	}}
	added, sent, err := Run(context.Background(), p, tools, nil, Limits{Own: map[string]int{"act": 1}, MaxTokens: 100}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.ran) != 1 || !strings.Contains(sent[0].Error, "cut off at the length limit of an answer (100 tokens)") || !strings.Contains(sent[1].Error, "cut off") || sent[2].Error != "" {
		t.Errorf("ran %v, sent %+v", tools.ran, sent)
	}
	if string(added[0].Calls[0].Args) != "{}" {
		t.Errorf("history keeps %s", added[0].Calls[0].Args)
	}
}

func TestAnthropicRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" || r.URL.Path != "/v1/messages" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["system"] == "" || body["tools"] == nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"look","input":{"a":"b"}}]}`))
	}))
	defer srv.Close()
	a := &anthropic{c: Config{Model: "m", Key: "k"}, base: srv.URL, hc: srv.Client()}
	resp, err := a.Complete(context.Background(), Request{System: "s", Messages: []Message{{Role: "user", Text: "q"}}, Tools: (&fixedTools{}).Defs(), MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi" || len(resp.Calls) != 1 || resp.Calls[0].Name != "look" {
		t.Errorf("resp %+v", resp)
	}
}

func TestOpenAIRoundTripAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "secret-in-error", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok","tool_calls":[{"id":"c","function":{"name":"look","arguments":"{}"}}]}}]}`))
	}))
	defer srv.Close()
	o := &openai{c: Config{Model: "m", Key: "k"}, base: srv.URL, hc: srv.Client()}
	resp, err := o.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Text: "q"}}})
	if err != nil || resp.Text != "ok" || len(resp.Calls) != 1 {
		t.Fatalf("%+v %v", resp, err)
	}
	bad := &openai{c: Config{Model: "m", Key: "wrong"}, base: srv.URL, hc: srv.Client()}
	if _, err := bad.Complete(context.Background(), Request{}); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Errorf("err = %v", err)
	}
}

func TestNewChecksTheEndpoint(t *testing.T) {
	for _, c := range []Config{
		{Provider: "openai-compatible", Model: "m", Endpoint: "http://example.com/v1"},
		{Provider: "anthropic", Model: "m"},
		{Provider: "nope", Model: "m"},
		{Provider: "anthropic"},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("%+v was accepted", c)
		}
	}
	if _, err := New(Config{Provider: "openai-compatible", Model: "m", Endpoint: "http://localhost:11434/v1"}); err != nil {
		t.Errorf("a local endpoint was refused: %v", err)
	}
}
