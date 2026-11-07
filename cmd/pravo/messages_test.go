package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestEvalExpressionSeesTopLevelLocals(t *testing.T) {
	rt := &runContext{}
	dir := specDir(t, map[string]string{"main.cue": "package x\n\n_prefix: \"app-\"\n_cfg: {size: 3}\nout: \"\\(_prefix)x\"\n"})
	for expr, want := range map[string]string{
		"_prefix":       `"app-"`,
		"_cfg.size + 1": "4",
		`"\(_prefix)y"`: `"app-y"`,
		"out":           `"app-x"`,
	} {
		text, code := runPravo(t, rt, dir, "eval", "-e", expr)
		if code != 0 || strings.TrimSpace(text) != want {
			t.Errorf("eval -e %q: exit %d, %q, want %q", expr, code, text, want)
		}
	}
}

func TestProviderValueErrorSaysWhoCheckedItAndWhere(t *testing.T) {
	rt := &runContext{}
	newDemoProvider(t)
	dir := demoEstate(t, rt, "known: $demo.#Resource.demo_server & {name: \"k\"}\n\nbig: $demo.#Resource.demo_server & {\n\tname: \"b\"\n\tsize: \"huge\"\n}")
	if out, code := runPravo(t, rt, dir, "validate"); code != 0 {
		t.Fatalf("validate exited %d:\n%s", code, out)
	}
	out, code := runPravo(t, rt, dir, "plan")
	if code == 0 {
		t.Fatalf("plan accepted the value:\n%s", out)
	}
	contains(t, out, "big: Invalid Attribute Value (at size, ", "main.cue:10", "got: huge", "checked by the provider")
}

func TestSpecErrorsNameTheClosestFields(t *testing.T) {
	rt := &runContext{}
	newDemoProvider(t)
	dir := demoEstate(t, rt, `known: $demo.#Resource.demo_server & {name: "k"}`)
	spec := func(body string) string {
		text := "package estate\n\nimport demo \"pravo.dev/providers/demo/demo\"\n\n$demo: demo.#Provider & {$path: " + fmt.Sprintf("%q", demoBinary) + "}\n" + body
		mustWrite(t, dir, "main.cue", text)
		out, code := runPravo(t, rt, dir, "validate")
		if code == 0 {
			t.Fatalf("%s is valid:\n%s", body, out)
		}
		return out
	}
	contains(t, spec(`known: $demo.#Resource.demo_servr & {name: "k"}`),
		"undefined field: demo_servr", "did you mean demo_server?", "$demo.#Resource has: demo_account, demo_bucket, demo_server")
	contains(t, spec("known: $demo.#Resource.demo_server & {name: \"k\"}\nother: $demo.#Resource.demo_server & {name: known.idd}"),
		"did you mean id?", "known has: id, name")
	contains(t, spec("known: $demo.#Resource.demo_server & {name: \"k\"}\nother: $demo.#Resource.demo_server & {name: \"x\\(known.nme)\"}"),
		"did you mean name?")
	contains(t, spec(`known: $demo.#Resource.demo_server & {name: "k", regionn: "eu"}`),
		"field not allowed", "known has: id, name", "pravo schema <type>")
	// A field of the same name around the reference is found first.
	contains(t, spec("name: $demo.#Resource.demo_server & {name: \"k\"}\nother: $demo.#Resource.demo_server & {name: \"x\", size: name.id}"),
		"name here is the field other.name", "_outer: name")
}
