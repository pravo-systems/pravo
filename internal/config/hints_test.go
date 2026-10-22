package config

import (
	"errors"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func TestProviderImportHint(t *testing.T) {
	for _, c := range []struct {
		err  string
		want string // "" for no hint
	}{
		{`builtin package "hashicorp/random" undefined`, `import random "pravo.dev/providers/hashicorp/random"`},
		{`import failed: cannot find package "registry.opentofu.org/hashicorp/random": cannot find module`, `pravo.dev/providers/hashicorp/random`},
		{`cannot find package "registry.terraform.io/integrations/github"`, `pravo.dev/providers/integrations/github`},
		{`builtin package "hashicorp/random:random" undefined`, `pravo.dev/providers/hashicorp/random`},
		{`builtin package "hashicorp/google-beta" undefined`, `import google_beta "pravo.dev/providers/hashicorp/google-beta"`},
		{`builtin package "encoding/jsno" undefined`, ""},
		{`cannot find package "github.com/acme/vpc"`, ""},
		{`cannot find package "pravo.dev/providers/hashicorp/random"`, ""},
		{`cannot find package "random"`, ""},
		{`some other failure`, ""},
	} {
		got := providerImportHint(c.err)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: hint %q, want it to contain %q", c.err, got, c.want)
		}
	}
	err := ModuleNotFoundHint(errors.New(`evaluating x:` + "\n" + `builtin package "hashicorp/random" undefined`))
	if !strings.Contains(err.Error(), "hint: a provider is imported from pravo.dev/providers/hashicorp/random") {
		t.Errorf("ModuleNotFoundHint: %v", err)
	}
}

func TestHCLFieldNames(t *testing.T) {
	ctx := cuecontext.New()
	root := func(src string) *Root { return &Root{Value: ctx.CompileString(src)} }
	if got := root(`providers: {a: 1}, $providers: {b: 2}, resource: {}, size: 3`).HCLFieldNames(); strings.Join(got, ",") != "$providers,providers,resource" {
		t.Errorf("names %v", got)
	}
	if got := root(`size: 1, name: "x"`).HCLFieldNames(); len(got) != 0 {
		t.Errorf("plain CUE has names %v", got)
	}
	// Anything pravo read is a specification that works: no hint.
	r := root(`resource: {}`)
	r.Resources = []*Resource{{}}
	if got := r.HCLFieldNames(); len(got) != 0 {
		t.Errorf("a root with a resource has names %v", got)
	}
	r = root(`resource: {}`)
	r.ProviderDecls = []*ProviderDecl{{}}
	if got := r.HCLFieldNames(); len(got) != 0 {
		t.Errorf("a root with a provider has names %v", got)
	}
}
