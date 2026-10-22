package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// pravo mcp: a Model Context Protocol server over stdio. Every tool runs the
// pravo binary itself as a subprocess with --json, in the root the server
// was started in, so an agent gets exactly the documented machine-readable
// surface and nothing more. Applies are confined to reviewed plan files.

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	args        func(in map[string]any) ([]string, error)
}

func strArg(in map[string]any, key string) string {
	if v, ok := in[key].(string); ok {
		return v
	}
	return ""
}

func listArg(in map[string]any, key string) []string {
	var out []string
	if v, ok := in[key].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func boolArg(in map[string]any, key string) bool {
	v, _ := in[key].(bool)
	return v
}

// countArg reads a whole number above zero. It reports whether the argument was
// given at all.
func countArg(in map[string]any, key string) (n int, given bool, err error) {
	v, ok := in[key]
	if !ok || v == nil {
		return 0, false, nil
	}
	f, isNumber := v.(float64)
	if !isNumber || f != float64(int(f)) || f < 1 {
		return 0, true, fmt.Errorf("%s: want a whole number above zero", key)
	}
	return int(f), true, nil
}

func tagArgs(in map[string]any) []string {
	var out []string
	for _, t := range listArg(in, "tags") {
		out = append(out, "-t", t)
	}
	return out
}

var tagsProp = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "tag values for @tag() fields, as key=value"}

func mcpTools() []mcpTool {
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	return []mcpTool{
		{Name: "pravo_validate", Description: "Evaluate the specification and check it against provider schemas. Returns counts, or the evaluation errors.",
			InputSchema: obj(map[string]any{"tags": tagsProp}),
			args:        func(in map[string]any) ([]string, error) { return append([]string{"validate"}, tagArgs(in)...), nil }},
		{Name: "pravo_plan", Description: "Compute the plan: every change the plan ran, with before/after values, reasons, blast radius and statistics. The nodes it left alone are not listed unless include_skipped is true. Optionally save it to a plan file, or store it in the state, for review and later apply, and reserve the nodes for a duration.",
			InputSchema: obj(map[string]any{"tags": tagsProp, "out": map[string]any{"type": "string", "description": "save the plan to this file"},
				"refresh":         map[string]any{"type": "string", "enum": []string{"age", "expired", "false", "all"}, "description": "when to read records from their providers: age (the records the plan looks at, once past their refresh age), expired (every record past its refresh age), false (none) or all"},
				"full":            map[string]any{"type": "boolean"},
				"include_skipped": map[string]any{"type": "boolean", "description": "also list the nodes the plan left alone, each with its address, mode, type and name"},
				"reserve":         map[string]any{"type": "string", "description": "reservation duration such as 15m"}, "destroy": map[string]any{"type": "boolean"},
				"replace": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "resource addresses to destroy and create again"},
				"store":   map[string]any{"type": "boolean", "description": "store the plan in the state, where others can list it with pravo_plan_list and apply it by its id; the result says the id"},
				"ttl":     map[string]any{"type": "string", "description": "with store: keep the plan this long if it is not applied, such as 90m, 36h or 7d (the default is 7d)"}}),
			args: func(in map[string]any) ([]string, error) {
				a := append([]string{"plan"}, tagArgs(in)...)
				if v := strArg(in, "out"); v != "" {
					a = append(a, "-out", v)
				}
				if v := strArg(in, "refresh"); v != "" {
					a = append(a, "-refresh="+v)
				}
				if boolArg(in, "full") {
					a = append(a, "-full")
				}
				if boolArg(in, "include_skipped") {
					a = append(a, "-include-skipped")
				}
				if v := strArg(in, "reserve"); v != "" {
					a = append(a, "-reserve="+v)
				}
				if boolArg(in, "destroy") {
					a = append(a, "-destroy")
				}
				if boolArg(in, "store") {
					a = append(a, "-store")
				}
				if v := strArg(in, "ttl"); v != "" {
					a = append(a, "-ttl="+v)
				}
				if rs, ok := in["replace"].([]any); ok {
					for _, r := range rs {
						if v, ok := r.(string); ok && v != "" {
							a = append(a, "-replace", v)
						}
					}
				}
				return a, nil
			}},
		{Name: "pravo_plan_list", Description: "List the plans stored in the state with pravo_plan (store=true): who made each, how long ago, where it stands (pending, partial, partly-stale, stale, applied or expired; a partly stale plan can be applied in part, with the only list that the result gives) and what it changes. By default the plans that can still be applied; all=true lists the others that are not collected yet. A plan is judged by the state alone.",
			InputSchema: obj(map[string]any{"tags": tagsProp, "all": map[string]any{"type": "boolean", "description": "also list the stale, applied and expired plans"},
				"touching": map[string]any{"type": "string", "description": "only the plans that change this resource address"}}),
			args: func(in map[string]any) ([]string, error) {
				a := append([]string{"plan", "list"}, tagArgs(in)...)
				if boolArg(in, "all") {
					a = append(a, "-all")
				}
				if v := strArg(in, "touching"); v != "" {
					if err := positional("touching", v); err != nil {
						return nil, err
					}
					a = append(a, "-touching="+v)
				}
				return a, nil
			}},
		{Name: "pravo_show_plan", Description: "Show a saved plan as data, and whether it is still applicable: a plan file, or the id of a plan stored in the state (with the tags it was made with).",
			InputSchema: obj(map[string]any{"planfile": map[string]any{"type": "string", "description": "a plan file, or the id of a stored plan such as plan-2f9c1a4e"}, "tags": tagsProp}, "planfile"),
			args: func(in map[string]any) ([]string, error) {
				ref, err := planRefArg(in)
				if err != nil {
					return nil, err
				}
				return append([]string{"show", ref}, tagArgs(in)...), nil
			}},
		{Name: "pravo_apply_plan", Description: "Apply a saved plan exactly as reviewed, optionally only a dependency-closed subset of its changes: a plan file, or the id of a plan stored in the state (with the tags it was made with). Refused when the plan is stale or expired, when a node is locked or reserved by another actor, or when a policy named by the server forbids it. Streams progress events and ends with a summary.",
			InputSchema: obj(map[string]any{"planfile": map[string]any{"type": "string", "description": "a plan file, or the id of a stored plan such as plan-2f9c1a4e"}, "tags": tagsProp,
				"only": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "addresses to apply now"}}, "planfile"),
			args: func(in map[string]any) ([]string, error) {
				ref, err := planRefArg(in)
				if err != nil {
					return nil, err
				}
				a := append([]string{"apply", ref}, tagArgs(in)...)
				if only := listArg(in, "only"); len(only) > 0 {
					a = append(a, "-only", strings.Join(only, ","))
				}
				return a, nil
			}},
		{Name: "pravo_schema", Description: "The CUE definition of resource, data source, ephemeral or function types, with the provider's documentation. Use required=true for only what must be set.",
			InputSchema: obj(map[string]any{"types": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "type names such as aws_s3_bucket, or provider/type"}, "required": map[string]any{"type": "boolean"}}, "types"),
			args: func(in map[string]any) ([]string, error) {
				types := listArg(in, "types")
				if len(types) == 0 {
					return nil, fmt.Errorf("types is required")
				}
				if err := positional("types", types...); err != nil {
					return nil, err
				}
				a := append([]string{"schema"}, types...)
				if boolArg(in, "required") {
					a = append(a, "-required")
				}
				return a, nil
			}},
		{Name: "pravo_why", Description: "Explain why a plan would create, change, replace or skip the given resources, and what each change reaches.",
			InputSchema: obj(map[string]any{"addrs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "tags": tagsProp}, "addrs"),
			args: func(in map[string]any) ([]string, error) {
				addrs := listArg(in, "addrs")
				if len(addrs) == 0 {
					return nil, fmt.Errorf("addrs is required")
				}
				if err := positional("addrs", addrs...); err != nil {
					return nil, err
				}
				return append(append([]string{"why"}, addrs...), tagArgs(in)...), nil
			}},
		{Name: "pravo_state_query", Description: "Evaluate a CUE expression over the state: resources (by address), edges and outputs.",
			InputSchema: obj(map[string]any{"expression": map[string]any{"type": "string"}, "tags": tagsProp}, "expression"),
			args: func(in map[string]any) ([]string, error) {
				if err := positional("expression", strArg(in, "expression")); err != nil {
					return nil, err
				}
				return append(append([]string{"state"}, tagArgs(in)...), "query", strArg(in, "expression")), nil
			}},
		{Name: "pravo_state_show", Description: "One node of the state: attributes, revision, freshness, dependencies and dependents.",
			InputSchema: obj(map[string]any{"addr": map[string]any{"type": "string"}, "tags": tagsProp}, "addr"),
			args: func(in map[string]any) ([]string, error) {
				if err := positional("addr", strArg(in, "addr")); err != nil {
					return nil, err
				}
				return append(append([]string{"state"}, tagArgs(in)...), "show", strArg(in, "addr")), nil
			}},
		{Name: "pravo_dependents", Description: "Everything a change to the resource can reach: its blast radius.",
			InputSchema: obj(map[string]any{"addr": map[string]any{"type": "string"}, "tags": tagsProp}, "addr"),
			args: func(in map[string]any) ([]string, error) {
				if err := positional("addr", strArg(in, "addr")); err != nil {
					return nil, err
				}
				return append(append([]string{"state"}, tagArgs(in)...), "dependents", strArg(in, "addr")), nil
			}},
		{Name: "pravo_state_log", Description: "Recorded transactions, newest first: who did what, when, with which outcome and revisions.",
			InputSchema: obj(map[string]any{"tags": tagsProp}),
			args: func(in map[string]any) ([]string, error) {
				return append(append([]string{"state"}, tagArgs(in)...), "log"), nil
			}},
		{Name: "pravo_locks", Description: "Locks and reservations currently held on the state.",
			InputSchema: obj(map[string]any{"tags": tagsProp}),
			args: func(in map[string]any) ([]string, error) {
				return append(append([]string{"state"}, tagArgs(in)...), "locks"), nil
			}},
		{Name: "pravo_drift", Description: "Refresh every resource and report drift: resources changed or removed outside pravo. With due, read only the records that are older than their refresh age, the oldest first, at most limit of them; nothing is planned.",
			InputSchema: obj(map[string]any{"tags": tagsProp, "due": map[string]any{"type": "boolean", "description": "read only the records past their refresh age"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "description": "with due: read at most this many records"}}),
			args: func(in map[string]any) ([]string, error) {
				a := append([]string{"drift"}, tagArgs(in)...)
				if boolArg(in, "due") {
					a = append(a, "-due")
				}
				n, given, err := countArg(in, "limit")
				if err != nil {
					return nil, err
				}
				if given {
					a = append(a, "-limit", fmt.Sprint(n))
				}
				return a, nil
			}},
		{Name: "pravo_providers", Description: "The providers the specification requires, with resolved versions.",
			InputSchema: obj(map[string]any{}),
			args:        func(in map[string]any) ([]string, error) { return []string{"providers"}, nil }},
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve pravo's commands to an agent over the Model Context Protocol (stdio)",
		Args:  noArgs,
		Long: `Runs a Model Context Protocol server on stdin/stdout. Tools correspond to
pravo commands with --json: validate, plan, show_plan, apply_plan, schema,
why, state_query, state_show, dependents, state_log, locks, drift and
providers. The actor is a label taken from --actor or PRAVO_ACTOR when the
server starts; it is recorded on every transaction the agent causes and is
not verified.

With --policy <file> (repeatable, absolute paths) the server adds that policy
to every plan and apply it runs; a tool argument cannot remove it.
Reservations apply as they would on the command line. The server applies only
from saved plan files, and a tool argument that is empty or begins with "-" is
refused so that it cannot be read as a flag. That narrows what this tool does,
not what the process around it may do.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Read and compile the files now, so that a bad path fails the start
			// and not every later call. Paths are absolute so that the file a
			// policy names does not depend on the directory the server was
			// started in.
			for _, p := range flagPolicy {
				if !filepath.IsAbs(p) {
					return engine.WithCode(engine.ExitRefused, fmt.Errorf("--policy %q: the server needs an absolute path", p))
				}
			}
			if _, err := engine.LoadPolicyFiles(flagPolicy, "."); err != nil {
				return err
			}
			return serveMCP(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
	addPolicyFlag(cmd)
	return cmd
}

func serveMCP(ctx context.Context, in io.Reader, outW io.Writer) error {
	tools := mcpTools()
	byName := map[string]mcpTool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	enc := json.NewEncoder(outW)
	enc.SetEscapeHTML(false)
	reply := func(id json.RawMessage, result any, rpcErr *rpcError) {
		if id == nil {
			return // notification
		}
		_ = enc.Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			reply(json.RawMessage("null"), nil, &rpcError{Code: -32700, Message: "parse error"})
			continue
		}
		switch req.Method {
		case "initialize":
			reply(req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "pravo", "version": Version},
				"instructions":    "pravo plans and applies infrastructure specified in CUE. Work in this order: pravo_schema to learn a type, edit the .cue files, pravo_validate until it passes, pravo_plan with out=<file> to produce a reviewable plan, then pravo_apply_plan on that file. A plan can also be stored in the state (pravo_plan with store=true), listed with pravo_plan_list and applied by its id. Never try to bypass a refusal: a policy or a reservation is a decision by the operators.",
			}, nil)
		case "notifications/initialized", "initialized":
			// no reply
		case "ping":
			reply(req.ID, map[string]any{}, nil)
		case "tools/list":
			list := make([]map[string]any, 0, len(tools))
			for _, t := range tools {
				list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
			}
			reply(req.ID, map[string]any{"tools": list}, nil)
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			t, ok := byName[p.Name]
			if !ok {
				reply(req.ID, nil, &rpcError{Code: -32602, Message: "unknown tool " + p.Name})
				continue
			}
			if p.Arguments == nil {
				p.Arguments = map[string]any{}
			}
			cliArgs, err := t.args(p.Arguments)
			if err != nil {
				reply(req.ID, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}, nil)
				continue
			}
			callCtx, span := telemetry.Tracer().Start(ctx, "mcp tools/call "+p.Name, trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attribute.String("mcp.tool", p.Name), attribute.String("pravo.actor", flagActor)))
			text, code := runSelf(callCtx, cliArgs)
			span.SetAttributes(attribute.Int("pravo.exit_code", code))
			var callErr error
			if code != 0 && code != int(engine.ExitChanges) {
				callErr = fmt.Errorf("exit code %d", code)
			}
			telemetry.End(span, callErr)
			reply(req.ID, map[string]any{"isError": code != 0, "content": []map[string]any{{"type": "text", "text": text}}, "structuredContent": map[string]any{"exit_code": code}}, nil)
		default:
			reply(req.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
		}
	}
	return sc.Err()
}

// runSelf runs the pravo binary with --json in the current root, carrying
// the actor and reason, and returns its stdout (falling back to stderr).
func runSelf(ctx context.Context, args []string) (string, int) {
	exe, err := os.Executable()
	if err != nil {
		return err.Error(), 1
	}
	full := append([]string{"--json", "--no-color"}, args...)
	if flagActor != "" {
		full = append(full, "--actor", flagActor)
	}
	if flagReason != "" {
		full = append(full, "--reason", flagReason)
	}
	// The server's policies bind the runs that can change something, and only
	// those: the other commands do not take the flag.
	full = append(full, serverPolicyArgs(args)...)
	c := exec.CommandContext(ctx, exe, full...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	// The child continues the trace of the tool call.
	c.Env = append(os.Environ(), telemetry.Environ(ctx)...)
	err = c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		code = 1
	}
	text := strings.TrimSpace(stdout.String())
	if code != 0 && text == "" {
		text = strings.TrimSpace(stderr.String())
	} else if code != 0 && stderr.Len() > 0 {
		text += "\n" + strings.TrimSpace(stderr.String())
	}
	return text, code
}

// serverPolicyArgs are the --policy arguments the server adds to a tool call:
// to plan and apply, and to nothing else, since no other command takes the
// flag and a flag that is silently ignored would be implicit.
func serverPolicyArgs(args []string) []string {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "apply") {
		return nil
	}
	if args[0] == "plan" && len(args) > 1 {
		switch args[1] {
		case "list", "show", "pull", "gc":
			return nil // they list and read stored plans, and take no policy
		}
	}
	var out []string
	for _, p := range flagPolicy {
		out = append(out, "--policy", p)
	}
	return out
}

// positional refuses a tool argument that the command line would read as a
// flag, such as a plan file named -auto-approve or an address -C=/other, and
// an empty one. The tools pass these values as positional arguments.
// planRefArg checks the plan of a tool call: a plan file, or the id of a plan
// stored in the state. The tags are the -t of the command that made a stored plan,
// which it needs; a plan file carries its own, so tags with one are refused.
func planRefArg(in map[string]any) (string, error) {
	ref := strArg(in, "planfile")
	if err := positional("planfile", ref); err != nil {
		return "", err
	}
	if len(listArg(in, "tags")) > 0 && !isStoredRef(ref) {
		return "", fmt.Errorf("tags go with the id of a stored plan; a plan file carries its own")
	}
	return ref, nil
}

func positional(name string, values ...string) error {
	for _, v := range values {
		if v == "" || strings.HasPrefix(v, "-") {
			return fmt.Errorf("%s %q is not a usable value: it is empty or begins with \"-\"", name, v)
		}
	}
	return nil
}
