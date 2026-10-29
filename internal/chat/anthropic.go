package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type anthropic struct {
	c    Config
	base string
	hc   *http.Client
}

type aBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type aMessage struct {
	Role    string   `json:"role"`
	Content []aBlock `json:"content"`
}

func (a *anthropic) Complete(ctx context.Context, r Request) (Response, error) {
	var msgs []aMessage
	for _, m := range r.Messages {
		switch m.Role {
		case "user":
			msgs = append(msgs, aMessage{Role: "user", Content: []aBlock{{Type: "text", Text: m.Text}}})
		case "assistant":
			var bs []aBlock
			if strings.TrimSpace(m.Text) != "" {
				bs = append(bs, aBlock{Type: "text", Text: m.Text})
			}
			for _, c := range m.Calls {
				in := c.Args
				if len(in) == 0 {
					in = json.RawMessage(`{}`)
				}
				bs = append(bs, aBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: in})
			}
			msgs = append(msgs, aMessage{Role: "assistant", Content: bs})
		case "tool":
			var bs []aBlock
			for _, res := range m.Results {
				b := aBlock{Type: "tool_result", ToolUseID: res.ID, Content: res.Body}
				if res.Error != "" {
					b.Content, b.IsError = res.Error, true
				}
				bs = append(bs, b)
			}
			msgs = append(msgs, aMessage{Role: "user", Content: bs})
		}
	}
	var tools []map[string]any
	for _, t := range r.Tools {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
	}
	body := map[string]any{"model": a.c.Model, "max_tokens": r.MaxTokens, "system": r.System, "messages": msgs}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	var out struct {
		Content []aBlock `json:"content"`
	}
	hdr := map[string]string{"x-api-key": a.c.Key, "anthropic-version": "2023-06-01"}
	if err := post(ctx, a.hc, a.base+"/v1/messages", hdr, body, &out); err != nil {
		return Response{}, err
	}
	var resp Response
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			resp.Text += b.Text
		case "tool_use":
			resp.Calls = append(resp.Calls, Call{ID: b.ID, Name: b.Name, Args: b.Input})
		}
	}
	return resp, nil
}
