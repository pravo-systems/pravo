package chat

import (
	"context"
	"encoding/json"
	"net/http"
)

type openai struct {
	c    Config
	base string
	hc   *http.Client
}

func (o *openai) Complete(ctx context.Context, r Request) (Response, error) {
	msgs := []map[string]any{{"role": "system", "content": r.System}}
	for _, m := range r.Messages {
		switch m.Role {
		case "user":
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Text})
		case "assistant":
			am := map[string]any{"role": "assistant", "content": m.Text}
			var tcs []map[string]any
			for _, c := range m.Calls {
				args := string(c.Args)
				if args == "" {
					args = "{}"
				}
				tcs = append(tcs, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": args}})
			}
			if len(tcs) > 0 {
				am["tool_calls"] = tcs
			}
			msgs = append(msgs, am)
		case "tool":
			for _, res := range m.Results {
				content := res.Body
				if res.Error != "" {
					content = "error: " + res.Error
				}
				msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": res.ID, "content": content})
			}
		}
	}
	var tools []map[string]any
	for _, t := range r.Tools {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
	}
	body := map[string]any{"model": o.c.Model, "messages": msgs, "max_tokens": r.MaxTokens}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	hdr := map[string]string{}
	if o.c.Key != "" {
		hdr["Authorization"] = "Bearer " + o.c.Key
	}
	if err := post(ctx, o.hc, o.base+"/chat/completions", hdr, body, &out); err != nil {
		return Response{}, err
	}
	var resp Response
	if len(out.Choices) > 0 {
		m := out.Choices[0].Message
		resp.Text = m.Content
		for _, c := range m.ToolCalls {
			resp.Calls = append(resp.Calls, Call{ID: c.ID, Name: c.Function.Name, Args: json.RawMessage(c.Function.Arguments)})
		}
	}
	return resp, nil
}
