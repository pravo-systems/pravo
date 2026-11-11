package convert

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// An attribute that the provider requires, and that nobody gave a value, is found by the check and not
// by the provider in the middle of a plan. A value that is given is not.
func TestRequiredAttributeWithNoValue(t *testing.T) {
	cfg := `
pet: {filename: string, id: string | null}
given: {filename: "x.txt", id: string | null}
`
	v := cuecontext.New().CompileString(cfg)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	run := func(name string) error {
		c := &Converter{Resolve: ResolverFunc(tasks), Self: cue.ParsePath(name)}
		c.Block(name, fileBlock(), v.LookupPath(cue.ParsePath(name)))
		return c.Err()
	}
	if err := run("pet"); err == nil || !strings.Contains(err.Error(), "pet.filename: the attribute is required and has no value") {
		t.Errorf("a required attribute without a value: %v", err)
	}
	if err := run("given"); err != nil {
		t.Errorf("a given value: %v", err)
	}
}
