package main

import (
	"reflect"
	"testing"
)

func TestServerPolicyArgs(t *testing.T) {
	defer func(old []string) { flagPolicy = old }(flagPolicy)
	flagPolicy = []string{"/etc/pravo/prod.cue", "/etc/pravo/size.cue"}
	want := []string{"--policy", "/etc/pravo/prod.cue", "--policy", "/etc/pravo/size.cue"}
	for _, tool := range [][]string{{"plan", "-out", "x"}, {"apply", "x.plan"}, {"apply", "-auto-approve"}} {
		if got := serverPolicyArgs(tool); !reflect.DeepEqual(got, want) {
			t.Errorf("%v: got %v, want %v", tool, got, want)
		}
	}
	for _, tool := range [][]string{{"validate"}, {"show", "x.plan"}, {"state", "query"}, {"drift"}, {}} {
		if got := serverPolicyArgs(tool); got != nil {
			t.Errorf("%v takes no policy flag, got %v", tool, got)
		}
	}
	flagPolicy = nil
	if got := serverPolicyArgs([]string{"plan"}); got != nil {
		t.Errorf("no policies, no arguments: %v", got)
	}
}

// A tool argument that the command line would read as a flag is refused, so
// that a plan file named -auto-approve cannot turn an apply of a saved plan
// into a plain apply, and an address cannot carry -C=/other.
func TestToolsRefuseArgumentsThatLookLikeFlags(t *testing.T) {
	cases := map[string]map[string]any{
		"pravo_apply_plan":  {"planfile": "-auto-approve"},
		"pravo_show_plan":   {"planfile": "--json"},
		"pravo_state_show":  {"addr": "-C=/other"},
		"pravo_dependents":  {"addr": "-C=/other"},
		"pravo_why":         {"addrs": []any{"a", "-C=/other"}},
		"pravo_schema":      {"types": []any{"-required"}},
		"pravo_state_query": {"expression": "--json"},
	}
	byName := map[string]mcpTool{}
	for _, tool := range mcpTools() {
		byName[tool.Name] = tool
	}
	for name, in := range cases {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("no tool %s", name)
		}
		if _, err := tool.args(in); err == nil {
			t.Errorf("%s accepted %v", name, in)
		}
	}
	ok := map[string]any{"planfile": "change.plan"}
	if args, err := byName["pravo_apply_plan"].args(ok); err != nil || args[1] != "change.plan" {
		t.Errorf("an ordinary plan file must still work: %v %v", args, err)
	}
}

// The plan and drift tools offer what the commands do. Every value of -refresh
// that the command accepts is in the enum of the tool, the document of a plan can
// be asked to list the nodes it left alone, and the scheduled sweep of
// "drift --due --limit N" can be made through the tool.
func TestPlanAndDriftToolsOfferWhatTheCommandsDo(t *testing.T) {
	byName := map[string]mcpTool{}
	for _, tool := range mcpTools() {
		byName[tool.Name] = tool
	}
	props := func(name string) map[string]any {
		return byName[name].InputSchema["properties"].(map[string]any)
	}
	enum := props("pravo_plan")["refresh"].(map[string]any)["enum"].([]string)
	if !reflect.DeepEqual(enum, []string{"age", "expired", "false", "all"}) {
		t.Errorf("the refresh argument offers %v", enum)
	}
	for _, v := range enum {
		if _, err := parseRefresh(v); err != nil {
			t.Errorf("the tool offers -refresh=%s, which the command refuses: %v", v, err)
		}
	}
	for _, name := range []string{"include_skipped"} {
		if _, ok := props("pravo_plan")[name]; !ok {
			t.Errorf("pravo_plan offers no %s argument", name)
		}
	}
	for _, name := range []string{"due", "limit"} {
		if _, ok := props("pravo_drift")[name]; !ok {
			t.Errorf("pravo_drift offers no %s argument", name)
		}
	}

	cases := []struct {
		tool string
		in   map[string]any
		want []string
	}{
		{"pravo_plan", map[string]any{"refresh": "expired"}, []string{"plan", "-refresh=expired"}},
		{"pravo_plan", map[string]any{"include_skipped": true, "out": "x.plan"}, []string{"plan", "-out", "x.plan", "-include-skipped"}},
		{"pravo_plan", map[string]any{"include_skipped": false}, []string{"plan"}},
		{"pravo_drift", map[string]any{}, []string{"drift"}},
		{"pravo_drift", map[string]any{"due": true}, []string{"drift", "-due"}},
		{"pravo_drift", map[string]any{"due": true, "limit": float64(500), "tags": []any{"env=prod"}}, []string{"drift", "-t", "env=prod", "-due", "-limit", "500"}},
	}
	for _, c := range cases {
		got, err := byName[c.tool].args(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v: %v, %v; want %v", c.tool, c.in, got, err, c.want)
		}
	}
	for _, limit := range []any{float64(0), float64(-3), 2.5, "10", true} {
		if _, err := byName["pravo_drift"].args(map[string]any{"due": true, "limit": limit}); err == nil {
			t.Errorf("pravo_drift accepted the limit %v", limit)
		}
	}
	// The arguments reach the command: it takes them as flags.
	root := newRootCmd()
	for _, c := range cases {
		args, err := byName[c.tool].args(c.in)
		if err != nil {
			t.Fatal(err)
		}
		cmd, _, err := root.Find(args[:1])
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.ParseFlags(normaliseArgs(append([]string{"pravo"}, args[1:]...))[1:]); err != nil {
			t.Errorf("pravo %v: the command does not take these arguments: %v", args, err)
		}
	}
}

// The tools that read the state take tags, so that an agent reaches the state
// of the environment the tags select.
func TestStateToolsTakeTags(t *testing.T) {
	tags := map[string]any{"tags": []any{"env=prod", "region=eu"}}
	with := func(in map[string]any) map[string]any {
		out := map[string]any{"tags": tags["tags"]}
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	cases := map[string]struct {
		in   map[string]any
		want []string
	}{
		"pravo_state_query": {map[string]any{"expression": "resources"}, []string{"state", "-t", "env=prod", "-t", "region=eu", "query", "resources"}},
		"pravo_state_show":  {map[string]any{"addr": "a"}, []string{"state", "-t", "env=prod", "-t", "region=eu", "show", "a"}},
		"pravo_dependents":  {map[string]any{"addr": "a"}, []string{"state", "-t", "env=prod", "-t", "region=eu", "dependents", "a"}},
		"pravo_state_log":   {nil, []string{"state", "-t", "env=prod", "-t", "region=eu", "log"}},
		"pravo_locks":       {nil, []string{"state", "-t", "env=prod", "-t", "region=eu", "locks"}},
	}
	byName := map[string]mcpTool{}
	for _, tool := range mcpTools() {
		byName[tool.Name] = tool
	}
	for name, c := range cases {
		tool := byName[name]
		props, _ := tool.InputSchema["properties"].(map[string]any)
		if _, ok := props["tags"]; !ok {
			t.Errorf("%s offers no tags argument", name)
		}
		got, err := tool.args(with(c.in))
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s with tags: %v, %v; want %v", name, got, err, c.want)
		}
	}
	// Without tags the command line is what it was.
	if got, err := byName["pravo_state_show"].args(map[string]any{"addr": "a"}); err != nil || !reflect.DeepEqual(got, []string{"state", "show", "a"}) {
		t.Errorf("without tags: %v, %v", got, err)
	}
}
