package main

import (
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"reflect"
	"strings"
	"testing"
)

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

// The tools take a plan file or the id of a stored plan, with the tags that the
// stored plan was made with; a plan file carries its own tags.
func TestToolsTakePlanFilesAndStoredPlanIDs(t *testing.T) {
	byName := map[string]mcpTool{}
	for _, tool := range mcpTools() {
		byName[tool.Name] = tool
	}
	in := map[string]any{"planfile": "plan-2f9c1a4e", "tags": []any{"env=prod"}}
	for name, want := range map[string][]string{
		"pravo_show_plan":  {"show", "plan-2f9c1a4e", "-t", "env=prod"},
		"pravo_apply_plan": {"apply", "plan-2f9c1a4e", "-t", "env=prod"},
	} {
		got, err := byName[name].args(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v %v, want %v", name, got, err, want)
		}
	}
	if got, err := byName["pravo_apply_plan"].args(map[string]any{"planfile": "plan-2f9c1a4e", "only": []any{"a", "b"}}); err != nil || !reflect.DeepEqual(got, []string{"apply", "plan-2f9c1a4e", "-only", "a,b"}) {
		t.Errorf("apply --only: %v %v", got, err)
	}
	// Tags with a plan file are refused: the file carries its own.
	for _, name := range []string{"pravo_apply_plan", "pravo_show_plan"} {
		if _, err := byName[name].args(map[string]any{"planfile": "change.plan", "tags": []any{"env=prod"}}); err == nil || !strings.Contains(err.Error(), "stored plan") {
			t.Errorf("%s took tags with a plan file: %v", name, err)
		}
	}
}

// The tools that store and list plans offer what the commands do.
func TestToolsStoreAndListPlans(t *testing.T) {
	byName := map[string]mcpTool{}
	for _, tool := range mcpTools() {
		byName[tool.Name] = tool
	}
	got, err := byName["pravo_plan"].args(map[string]any{"store": true, "ttl": "36h", "out": "x.plan"})
	if err != nil || !reflect.DeepEqual(got, []string{"plan", "-out", "x.plan", "-store", "-ttl=36h"}) {
		t.Errorf("pravo_plan: %v %v", got, err)
	}
	got, err = byName["pravo_plan_list"].args(map[string]any{"all": true, "touching": "api.db", "tags": []any{"env=prod"}})
	if err != nil || !reflect.DeepEqual(got, []string{"plan", "list", "-t", "env=prod", "-all", "-touching=api.db"}) {
		t.Errorf("pravo_plan_list: %v %v", got, err)
	}
	if _, err := byName["pravo_plan_list"].args(map[string]any{"touching": "--json"}); err == nil {
		t.Error("an address that looks like a flag was accepted")
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

// A tool call writes a plan file only below the project directory.
func TestPlanToolWritesOnlyBelowTheProject(t *testing.T) {
	var plan mcpTool
	for _, tool := range mcpTools() {
		if tool.Name == "pravo_plan" {
			plan = tool
		}
	}
	for _, out := range []string{"/etc/cron.d/x", "../x.plan", "a/../../x.plan", "-x"} {
		if _, err := plan.args(map[string]any{"out": out}); err == nil {
			t.Errorf("out %q accepted", out)
		}
	}
	for _, out := range []string{"x.plan", "plans/x.plan", "a/../x.plan"} {
		if _, err := plan.args(map[string]any{"out": out}); err != nil {
			t.Errorf("out %q: %v", out, err)
		}
	}
}

func TestPravoParallelismComesFromTheEnvironment(t *testing.T) {
	old := engine.Parallelism
	defer func() { engine.Parallelism = old }()
	for _, v := range []string{"0", "-1", "x", "257", "2.5"} {
		if err := applyParallelism(env.New([]string{"PRAVO_PARALLELISM=" + v})); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
	if err := applyParallelism(env.New(nil)); err != nil || engine.Parallelism != old {
		t.Errorf("unset: %v, %d", err, engine.Parallelism)
	}
	if err := applyParallelism(env.New([]string{"PRAVO_PARALLELISM=32"})); err != nil || engine.Parallelism != 32 {
		t.Errorf("32: %v, %d", err, engine.Parallelism)
	}
}
