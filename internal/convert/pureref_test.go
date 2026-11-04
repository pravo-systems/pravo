package convert

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// PureRefs may only take a body's values from the pending fills when each leaf that
// reads a pending task is a plain reference; anything else needs CUE to evaluate
// it, and says so by refusing.
func TestPureRefs(t *testing.T) {
	tasks := ResolverFunc(func(p cue.Path) (cue.Path, cue.Path, bool) {
		sels := p.Selectors()
		if len(sels) == 0 {
			return cue.Path{}, cue.Path{}, false
		}
		switch sels[0].String() {
		case "net", "sg":
			one := cue.MakePath(sels[0])
			return one, one, true
		}
		return cue.Path{}, cue.Path{}, false
	})
	pending := func(p cue.Path) bool { return p.String() == "net" }
	for name, tc := range map[string]struct {
		src  string
		ok   bool
		refs []string
	}{
		"a plain reference":     {`net: {id: string}, web: {subnet: net.id, size: 3}`, true, []string{"subnet"}},
		"in a list":             {`net: {id: string}, web: {subnets: [net.id, "x"]}`, true, []string{"subnets[0]"}},
		"with a constraint":     {`net: {id: string}, web: {subnet: string & net.id}`, true, []string{"subnet"}},
		"nested":                {`net: {id: string}, web: {a: {b: net.id}}`, true, []string{"a.b"}},
		"no pending task":       {`net: {id: string}, sg: {id: string}, web: {sg: sg.id, n: 1}`, true, nil},
		"an interpolation":      {`net: {id: string}, web: {subnet: "x-\(net.id)"}`, false, nil},
		"an operator":           {`net: {id: string}, web: {subnet: net.id + "x"}`, false, nil},
		"a whole struct":        {`net: {tags: {a: string}}, web: {tags: net.tags}`, false, nil},
		"two reads in one leaf": {`net: {id: string}, web: {subnet: net.id & net.id}`, false, nil},
		"a condition":           {`net: {id: string, on: bool}, web: {if net.on {size: 3}}`, false, nil},
		"a comprehension":       {`net: {l: [...string]}, web: {for x in net.l {"k\(x)": 1}}`, false, nil},
		"an embedded struct":    {`net: {t: {a: string}}, web: {net.t, size: 3}`, false, nil},
		"a let":                 {`net: {id: string}, web: {let x = net.id, subnet: x}`, false, nil},
		"through a plain field": {`net: {id: string}, name: net.id, web: {subnet: name}`, false, nil},
		"a comprehension unrelated to a pending task": {`net: {id: string}, sg: {l: [...string]}, web: {subnet: net.id, for x in sg.l {"k\(x)": 1}}`, false, nil},
	} {
		t.Run(name, func(t *testing.T) {
			v := cuecontext.New().CompileString(tc.src)
			if err := v.Err(); err != nil {
				t.Fatal(err)
			}
			refs, ok := PureRefs(v.LookupPath(cue.ParsePath("web")), cue.ParsePath("web"), tasks, pending)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (refs %v)", ok, tc.ok, refs)
			}
			var got []string
			for _, r := range refs {
				got = append(got, r.Path.String())
				if r.Task.String() != "net" {
					t.Errorf("leaf %s reads from %s, want net", r.Path, r.Task)
				}
			}
			if len(got) != len(tc.refs) {
				t.Fatalf("leaves %v, want %v", got, tc.refs)
			}
			for i := range got {
				if got[i] != tc.refs[i] {
					t.Errorf("leaf %d is %s, want %s", i, got[i], tc.refs[i])
				}
			}
		})
	}
}
