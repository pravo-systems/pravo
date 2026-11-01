package engine

import (
	"context"
	"encoding/json"
	"testing"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"

	"github.com/pravo-systems/pravo/internal/schema"
)

// templateOfChanges plans s and gives, by address, the template that each change of the
// judged document names: "" for none, otherwise "NAME@INSTANCE". The judged document is
// made from the plan file, as an apply makes it.
func templateOfChanges(t *testing.T, h *harness, s spec) map[string]string {
	t.Helper()
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	pf, err := e.buildPlanFile(plan, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := e.PolicyDocument(pf)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Changes []struct {
			Addr     string
			Template *TemplateRef
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range doc.Changes {
		out[c.Addr] = ""
		if c.Template != nil {
			out[c.Addr] = c.Template.Name + "@" + c.Template.Instance
		}
	}
	// The plan schema takes the document as it is.
	pkg := e.Root.Ctx.CompileString(schema.SettingsPackage)
	judged, err := buildDoc(e.Root.Ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := pkg.LookupPath(cue.MakePath(cue.Def("Plan"))).Unify(judged).Validate(cue.Concrete(true)); err != nil {
		t.Fatalf("#Plan does not fit the judged document: %v", cueerrors.Details(err, nil))
	}
	return out
}

// A change names the template instance that holds its node, from the plan file, so that a
// policy can pick out the resources of an instance. The nearest marker wins, and a node
// outside every instance names none.
func TestAChangeNamesTheTemplateInstanceThatHoldsIt(t *testing.T) {
	h := newHarness(t)
	s := spec{
		cue: `services: web: {
	$template: {name: "service", title: "Web service"}
	config: ` + thing("web-config", "") + `
	queue: {
		$template: name: "queue"
		box: ` + thing("web-queue", "") + `
	}
}
loose: ` + thing("loose", "") + "\n",
		resources: []string{"services.web.config", "services.web.queue.box", "loose"},
	}
	got := templateOfChanges(t, h, s)
	want := map[string]string{
		"services.web.config":    "service@services.web",
		"services.web.queue.box": "queue@services.web.queue",
		"loose":                  "",
	}
	for addr, w := range want {
		if g, ok := got[addr]; !ok || g != w {
			t.Errorf("%s: template %q, want %q (all: %v)", addr, g, w, got)
		}
	}
}

// A resource that leaves an instance is deleted under the instance's template while the
// instance is there; the delete of a whole instance names none, since the specification
// no longer says what it was.
func TestADeleteNamesTheTemplateWhileTheInstanceIsThere(t *testing.T) {
	h := newHarness(t)
	h.apply(spec{
		cue: `services: web: {
	$template: name: "service"
	config: ` + thing("web-config", "") + `
	extra: ` + thing("web-extra", "") + `
}
services: api: {
	$template: name: "service"
	config: ` + thing("api-config", "") + `
}
`,
		resources: []string{"services.web.config", "services.web.extra", "services.api.config"},
	})
	got := templateOfChanges(t, h, spec{
		cue: `services: web: {
	$template: name: "service"
	config: ` + thing("web-config", "") + `
}
`,
		resources: []string{"services.web.config"},
	})
	if g := got["services.web.extra"]; g != "service@services.web" {
		t.Errorf("a resource that leaves its instance: template %q, want service@services.web (all: %v)", g, got)
	}
	if g, ok := got["services.api.config"]; !ok || g != "" {
		t.Errorf("a resource of an instance that is gone: template %q, present %v, want none", g, ok)
	}
}
