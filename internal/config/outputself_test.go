package config

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func TestSelfReference(t *testing.T) {
	cases := []struct{ src, want string }{
		{"pet: {id: 1}\nloop: {value: {pet: pet.id}}", "pet"},
		{"pet: {id: 1}\nloop: {value: {a: {pet: pet.id}}}", "pet"},
		{"pet: {id: 1}\nok: {value: {name: pet.id}}", ""},
		{"pet: {id: 1}\nlet _p = pet\nok: {value: {pet: _p.id}}", ""},
		{"pet: {id: 1}\nok: {value: {a: 1, b: a}}", ""},
		{"ok: {value: [1, 2]}", ""},
	}
	for _, c := range cases {
		v := cuecontext.New().CompileString(c.src)
		name := "loop"
		if v.LookupPath(cue.ParsePath("ok")).Exists() {
			name = "ok"
		}
		if got := selfReference(v.LookupPath(cue.ParsePath(name + ".value"))); got != c.want {
			t.Errorf("%q: got %q, want %q", c.src, got, c.want)
		}
	}
}
