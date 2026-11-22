package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/serve"
)

// The drawing tools give an agent the pen of the drawing board (the Inventory of pravo serve): it draws with the code
// a person draws with, the viewer's server in process, and sees what the page shows, sensitive values withheld. A
// drawing is checked, written as the pen writes it (a new file that no file has, and the fields it changes in the
// files where they are written, each only as it was read), and planned. Nothing here applies: a plan to apply is
// saved with pravo_plan and applied with pravo_apply_plan, as any other.

type mcpDrawer struct {
	rt      *runContext
	srv     *serve.Server
	closeFn func()
}

// close ends the server of the drawing tools, if one was started.
func (d *mcpDrawer) close() {
	if d.closeFn != nil {
		d.closeFn()
	}
}

// server starts the viewer's server in process on the first call, with the board of pravo serve --edit and without
// its start from a Terraform state, and keeps it: it reads the files again when they change.
func (d *mcpDrawer) server() (*serve.Server, error) {
	if d.srv != nil {
		return d.srv, nil
	}
	v, opts, err := viewerOptions(d.rt)
	if err != nil {
		return nil, err
	}
	board, closeBoard := boardOptions(d.rt, false)
	board.Start = nil
	opts.Board = board
	srv, err := serve.New(opts)
	if err != nil {
		closeBoard()
		v.close()
		return nil, engine.WithCode(engine.ExitInvalid, err)
	}
	v.started()
	d.srv, d.closeFn = srv, func() { closeBoard(); v.close() }
	return srv, nil
}

// call asks the board for one path and returns its answer as the tool's text: the last line of a plan, which streams,
// and a check without the whole text of each file it changes, whose diff it keeps. An answer the board refuses is an
// error that carries the answer, since a refused check says why in its problems.
func (d *mcpDrawer) call(path string, q url.Values, body any) (string, error) {
	srv, err := d.server()
	if err != nil {
		return "", err
	}
	status, out := srv.Draw(path, q, body)
	if path == "/api/board/plan" && status == http.StatusOK {
		lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
		out = lines[len(lines)-1]
		var last struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(out, &last) == nil && last.Error != "" {
			return "", engine.WithCode(engine.ExitInvalid, errors.New(last.Error))
		}
	}
	out = trimDrawFiles(out)
	if status != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(out, &e) == nil && e.Error != "" {
			return "", engine.WithCode(engine.ExitInvalid, errors.New(e.Error))
		}
		return "", engine.WithCode(engine.ExitInvalid, errors.New(string(out)))
	}
	return string(out), nil
}

// trimDrawFiles drops the whole text of each file a check changes: the diff says what changes, and an agent that
// wants the file reads it. The answer is written again with & and < as they are, as the code has them.
func trimDrawFiles(b []byte) []byte {
	var doc map[string]any
	if json.Unmarshal(b, &doc) != nil {
		return b
	}
	files, _ := doc["files"].([]any)
	for _, f := range files {
		if m, ok := f.(map[string]any); ok {
			delete(m, "text")
		}
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false) // the code holds & and < as written
	if enc.Encode(doc) != nil {
		return b
	}
	return bytes.TrimSpace(out.Bytes())
}

// drawingArg is the drawing of a tool call, as the board reads one.
func drawingArg(in map[string]any) (*serve.BoardDraft, error) {
	raw, ok := in["drawing"]
	if !ok {
		return nil, errors.New("drawing is required: {items, edits, fold}")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var d serve.BoardDraft
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, errors.New("drawing is not a drawing: " + err.Error())
	}
	return &d, nil
}

var drawingProp = map[string]any{"type": "object", "description": `The drawing, as the board reads it: {"items": [{"label": "orders", "type": "aws_db_instance", "provider": "$aws", "values": {"engine": {"text": "postgres"}, "allocated_storage": {"expr": "20"}, "vpc_security_group_ids": {"wires": [{"to": "db_sg", "read": "id"}]}}}], "edits": [{"addr": "web", "base": "<digest from pravo_draw_resource>", "set": {"instance_type": {"text": "t3.small"}}, "unset": ["tags"], "remove": false}], "fold": {"name": "web_app", "addrs": ["web", "db"], "ask": ["web.instance_type"]}, "file": "board.cue", "seen": {"main.cue": "<base of the file from pravo_draw_check>"}}. An item is a new thing at the top level: a resource, or with "kind" a data source (data), an ephemeral resource (ephemeral), an output, an environment variable (env, secret_env), a secret (secret, secret_version) or the outputs of another root (remote_outputs), or an instance of a template (with "template"); a value is "text" (a string), "expr" (CUE as written: a number, a bool, a list, an interpolation) or "wire"/"wires" (a reference to an attribute of another resource, drawn or of the specification). An edit changes a resource of the specification where it is written. A fold makes a template of resources of the specification, with one instance that keeps their records; its plan only moves them.`}

func mcpDrawTools(d *mcpDrawer) []mcpTool {
	draw := func(name string) func(in map[string]any) ([]string, error) {
		return func(map[string]any) ([]string, error) { return []string{name}, nil }
	}
	return []mcpTool{
		{Name: "pravo_draw_types", Description: "Find what can be drawn that answers some words (database, bucket, network, data vpc, output, env): the resource, data source and ephemeral types of the providers the specification declares, the values pravo reads for itself (output, env, secret_env, secret, secret_version, remote_outputs) and the templates. The first step of drawing. Each hit names its kind, its type and the provider struct ($aws) to draw it with; when the specification has several providers of one source, providers lists them all.",
			InputSchema: mcpObject(map[string]any{"q": map[string]any{"type": "string", "description": "words, or a type name"}}),
			args:        draw("draw-types"),
			run: func(_ context.Context, in map[string]any) (string, error) {
				return d.call("/api/board/types", url.Values{"q": {strArg(in, "q")}}, nil)
			}},
		{Name: "pravo_draw_form", Description: "The form of a kind of thing, as the board fills it: each field with its type, whether it is required, whether it takes a wire (a reference to another resource) and its documentation. Give kind, type and provider as pravo_draw_types names them, kind alone for a value pravo reads for itself, or template.",
			InputSchema: mcpObject(map[string]any{"kind": map[string]any{"type": "string", "description": "resource (the default), data, ephemeral, output, env, secret_env, secret, secret_version or remote_outputs"}, "type": map[string]any{"type": "string"}, "provider": map[string]any{"type": "string", "description": "the provider struct, such as $aws"}, "template": map[string]any{"type": "string"}}),
			args:        draw("draw-form"),
			run: func(_ context.Context, in map[string]any) (string, error) {
				q := url.Values{}
				if t := strArg(in, "template"); t != "" {
					q.Set("template", t)
				} else {
					q.Set("kind", strArg(in, "kind"))
					q.Set("type", strArg(in, "type"))
					q.Set("provider", strArg(in, "provider"))
				}
				return d.call("/api/board/type", q, nil)
			}},
		{Name: "pravo_draw_resource", Description: "A node of the specification (a resource, a data source, an output, an environment variable...) as the pen can change it: its kind, its fields as written, its form, the file and the digest (base) that an edit names, or why the pen cannot change it (then edit the file).",
			InputSchema: mcpObject(map[string]any{"addr": map[string]any{"type": "string"}}, "addr"),
			args:        draw("draw-resource"),
			run: func(_ context.Context, in map[string]any) (string, error) {
				return d.call("/api/board/edit", url.Values{"addr": {strArg(in, "addr")}}, nil)
			}},
		{Name: "pravo_draw_check", Description: "Check a drawing as the board checks it, writing nothing: the code of the new file, the diff of each file an edit or a fold changes (with its base), the problems CUE, the schemas and the form find (state valid, incomplete or invalid), what each thing reads, and what the policies say of it. Sensitive values are masked as the page masks them.",
			InputSchema: mcpObject(map[string]any{"drawing": drawingProp}, "drawing"),
			args:        draw("draw-check"),
			run: func(_ context.Context, in map[string]any) (string, error) {
				dr, err := drawingArg(in)
				if err != nil {
					return "", err
				}
				return d.call("/api/board/check", nil, dr)
			}},
		{Name: "pravo_draw_plan", Description: "Plan the specification with the drawing in it, as pravo plan would, without writing the drawing: the action for each node it changes, the counts (add, change, destroy, move) and whether a policy refuses it. Nothing is saved or applied.",
			InputSchema: mcpObject(map[string]any{"drawing": drawingProp}, "drawing"),
			args:        draw("draw-plan"),
			run: func(_ context.Context, in map[string]any) (string, error) {
				dr, err := drawingArg(in)
				if err != nil {
					return "", err
				}
				return d.call("/api/board/plan", nil, dr)
			}},
		{Name: "pravo_draw_save", Description: "Write a drawing into the specification as the board's Save does: the new file under a name no file has, the fields of each edit in the file where the resource is written, a fold as its template file and the files that read what it folds. A file that changed since it was read is not written over: an edit names the base it was read at, and a drawing with a fold names in seen the base of each file pravo_draw_check showed. It applies nothing: plan the files with pravo_plan (out or store) and apply that plan with pravo_apply_plan.",
			InputSchema: mcpObject(map[string]any{"drawing": drawingProp}, "drawing"),
			args:        draw("draw-save"),
			run: func(_ context.Context, in map[string]any) (string, error) {
				dr, err := drawingArg(in)
				if err != nil {
					return "", err
				}
				return d.call("/api/board/save", nil, dr)
			}},
	}
}
