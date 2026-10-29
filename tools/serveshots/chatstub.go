package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The shots of the assistant need a model that answers. The program serves one
// on the address that the documentation gives for a model on the machine, in
// the protocol that the viewer speaks to an openai-compatible provider. It
// plays one conversation: the first request is answered with a read of the
// stored plans, which the viewer runs for real, and the second with an answer
// that is written from what that read returned, so that the picture holds the
// plans and the resources of the estate and not text that could drift from it.
const (
	chatAddr  = "127.0.0.1:11434"
	chatModel = "llama3.1"
	// chatQuestion is the question that the shots ask.
	chatQuestion = "Which plans can I apply, and do they touch the same resources?"
)

// chatEnv is what makes the viewer's assistant talk to the stub.
var chatEnv = []string{
	"PRAVO_CHAT=openai-compatible:" + chatModel,
	"PRAVO_CHAT_ENDPOINT=http://" + chatAddr + "/v1",
}

// A chatStub is the model.
type chatStub struct {
	srv  *http.Server
	done chan struct{}
}

// startChatStub serves the model on chatAddr.
func startChatStub() (*chatStub, error) {
	ln, err := net.Listen("tcp", chatAddr)
	if err != nil {
		return nil, fmt.Errorf("the shots of the assistant serve a model on %s, which is in use: %w", chatAddr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", completeChat)
	s := &chatStub{srv: &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		_ = s.srv.Serve(ln)
	}()
	return s, nil
}

// stop ends the server.
func (s *chatStub) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	<-s.done
}

// completeChat answers a chat completion: with a read of the plans while the
// request holds no result of a tool, and with the answer once it does.
func completeChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var result string
	for _, m := range req.Messages {
		if m.Role == "tool" {
			result = m.Content
		}
	}
	msg := map[string]any{"role": "assistant"}
	if result == "" {
		msg["content"] = ""
		msg["tool_calls"] = []map[string]any{{"id": "call_1", "type": "function", "function": map[string]any{"name": "plans", "arguments": "{}"}}}
	} else {
		msg["content"] = plansAnswer(result)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": msg, "finish_reason": "stop"}}})
}

// plansAnswer writes the answer from what the read of the plans returned: the
// plans that can be applied, and the resources that more than one of them
// replaces.
func plansAnswer(body string) string {
	var d struct {
		Plans []struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Author  string `json:"author"`
			Summary struct {
				Destroy int `json:"destroy"`
			} `json:"summary"`
			Touches []struct {
				Addr   string `json:"addr"`
				Action string `json:"action"`
			} `json:"touches"`
			Overlaps []struct {
				ID    string   `json:"id"`
				Addrs []string `json:"addrs"`
			} `json:"overlaps"`
		} `json:"plans"`
	}
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return "The read of the plans did not return a list."
	}
	var lines []string
	shared := map[string]bool{}
	apply := ""
	for _, p := range d.Plans {
		if p.Status != "pending" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s`, by %s, replaces %d resources.", p.ID, p.Author, p.Summary.Destroy))
		if apply == "" {
			apply = p.ID
		}
		for _, o := range p.Overlaps {
			for _, a := range o.Addrs {
				shared[a] = true
			}
		}
	}
	var addrs []string
	for a := range shared {
		addrs = append(addrs, "`"+a+"`")
	}
	sort.Strings(addrs)
	var b strings.Builder
	fmt.Fprintf(&b, "**%d plans can be applied.**\n%s\n\n", len(lines), strings.Join(lines, "\n"))
	if len(addrs) > 0 {
		fmt.Fprintf(&b, "They both replace %s. Apply one, then plan again, because the state differs from what the other expects.\n\n", strings.Join(addrs, ", "))
	}
	if apply != "" {
		fmt.Fprintf(&b, "To apply the first, run this yourself:\n\n```\npravo apply %s\n```", apply)
	}
	return b.String()
}
