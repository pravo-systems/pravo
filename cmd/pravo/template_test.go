package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/serve"
)

// templateSpec has one template with a string, a number and an enum, and one instance of it.
const templateSpec = `package estate

import "pravo.dev/pravo"

// A service.
#Service: pravo.#Template & {
	$template: {name: "service", title: "Service", description: "A service.", inputs: {name: {label: "Name"}, port: {}, tier: {}}}
	name: string
	port: *8080 | int & >=1024
	tier: *"small" | "large"
}
`

func templateRoot(t *testing.T, rt *runContext) string {
	t.Helper()
	dir := specDir(t, map[string]string{"main.cue": templateSpec})
	if text, code := runPravo(t, rt, dir, "init"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	return dir
}

func TestTemplateListSaysWhatTheViewerSays(t *testing.T) {
	rt := &runContext{}
	dir := templateRoot(t, rt)

	text, code := runPravo(t, rt, dir, "template", "list")
	if code != 0 || !strings.Contains(text, "service") || !strings.Contains(text, "#Service") {
		t.Fatalf("list: exit %d\n%s", code, text)
	}

	text, code = runPravo(t, rt, dir, "template", "list", "--json")
	if code != 0 {
		t.Fatalf("list --json: exit %d\n%s", code, text)
	}
	var cat serve.Templates
	if err := json.Unmarshal([]byte(text), &cat); err != nil {
		t.Fatalf("list --json is not the catalog of the viewer: %v\n%s", err, text)
	}
	if !cat.SpecOK || len(cat.Templates) != 1 || cat.Templates[0].Name != "service" || len(cat.Templates[0].Inputs) != 3 {
		t.Fatalf("catalog: %+v", cat)
	}
	if got := cat.Templates[0].Inputs[0]; got.Name != "name" || got.Label != "Name" || !got.Required || got.Kind != "string" {
		t.Errorf("first input: %+v", got)
	}
}

func TestTemplateTryMakesTheInstanceAndExitsByWhatItFound(t *testing.T) {
	rt := &runContext{}
	dir := templateRoot(t, rt)

	// A string input takes text, an enum its option, a number CUE.
	text, code := runPravo(t, rt, dir, "template", "try", "service", "--name", "web", "--at", "services", "--set", "name=web", "--set", "port=9000", "--set", "tier=large")
	if code != 0 {
		t.Fatalf("try: exit %d\n%s", code, text)
	}
	for _, want := range []string{"services: web: #Service & {", `name: "web"`, "port: 9000", `tier: "large"`, "services.web: valid"} {
		if !strings.Contains(text, want) {
			t.Errorf("try lacks %q:\n%s", want, text)
		}
	}

	text, code = runPravo(t, rt, dir, "template", "try", "service", "--name", "web", "--at", "services", "--set", "name=web", "--json")
	var try serve.TemplateTry
	if err := json.Unmarshal([]byte(text), &try); code != 0 || err != nil || try.State != "valid" || try.Address != "services.web" || !strings.Contains(try.Snippet, `name: "web"`) {
		t.Fatalf("try --json: exit %d, %v\n%s", code, err, text)
	}

	// A value the definition refuses, an input without a value and a template that is not there each end with 6, and say why.
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--set", "name=web", "--set", "port=80"}, "out of bound >=1024"},
		{[]string{"--set", "port=9000"}, "needs a value"},
	} {
		args := append([]string{"template", "try", "service", "--name", "web", "--at", "services"}, c.args...)
		text, code = runPravo(t, rt, dir, args...)
		if code != 6 || !strings.Contains(text, c.want) {
			t.Errorf("try %v: exit %d, want 6 and %q\n%s", c.args, code, c.want, text)
		}
	}
	text, code = runPravo(t, rt, dir, "template", "try", "nope", "--name", "web")
	if code != 6 || !strings.Contains(text, `no template "nope": the templates are service`) {
		t.Errorf("unknown template: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, rt, dir, "template", "try", "service", "--name", "web", "--set", "colour=red")
	if code != 6 || !strings.Contains(text, `has no input "colour"`) {
		t.Errorf("unknown input: exit %d\n%s", code, text)
	}
}
