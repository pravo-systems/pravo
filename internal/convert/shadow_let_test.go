package convert

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// A name in the expression of a let belongs to the scope that declares the let, not to
// the scope of whoever reads the let. A field of the same name between the two must not
// capture it.
func TestLetNameIsNotCapturedByAnInnerField(t *testing.T) {
	cases := []struct {
		name, src, self, want string
		tasks                 []string
	}{
		{"the reader is the shadowing field", `
net: {
	vpc: {id: string | null}
	let _vpc = vpc
	flowLogs: {
		vpc: {vpc_id: _vpc.id}
	}
}`, "net.flowLogs.vpc", "net.vpc", []string{"net.vpc", "net.flowLogs.vpc"}},
		{"the shadowing field is another task", `
net: {
	vpc: {id: string | null}
	let _vpc = vpc
	flowLogs: {
		vpc: {id: string | null}
		reader: {vpc_id: _vpc.id}
	}
}`, "net.flowLogs.reader", "net.vpc", []string{"net.vpc", "net.flowLogs.vpc", "net.flowLogs.reader"}},
		{"the shadowing field is a plain struct", `
net: {
	vpc: {id: string | null}
	let _vpc = vpc
	mid: {
		vpc: {x: 1}
		reader: {vpc_id: _vpc.id}
	}
}`, "net.mid.reader", "net.vpc", []string{"net.vpc", "net.mid.reader"}},
		{"a let in the same struct as the reader", `
net: {
	vpc: {id: string | null}
	let _vpc = vpc
	reader: {vpc_id: _vpc.id}
}`, "net.reader", "net.vpc", []string{"net.vpc", "net.reader"}},
		{"a let at the top of the file", `
vpc: {id: string | null}
let _vpc = vpc
net: {
	vpc: {id: string | null}
	reader: {vpc_id: _vpc.id}
}`, "net.reader", "vpc", []string{"vpc", "net.vpc", "net.reader"}},
	}
	for _, c := range cases {
		if got := depsOf(t, c.src, c.self, c.tasks...); got != c.want {
			t.Errorf("%s: dependencies = %q, want %q", c.name, got, c.want)
		}
	}
}

// The same for the source of a for clause: its name is looked up where the clause is
// written, and a field of the same name between there and the task does not take it.
func TestLoopSourceIsNotCapturedByAnInnerField(t *testing.T) {
	cases := []struct {
		name, src, self, want string
		tasks                 []string
	}{
		{"through a let", `
net: {
	subnets: {a: {id: string | null}, b: {id: string | null}}
	let _s = subnets
	inner: {
		subnets: {x: {id: string | null}}
		ids: [for k, s in _s {s.id}]
	}
}`, "net.inner.ids", "net.subnets.a,net.subnets.b", []string{"net.subnets.a", "net.subnets.b", "net.inner.subnets.x", "net.inner.ids"}},
		{"in the same struct", `
net: {
	subnets: {a: {id: string | null}, b: {id: string | null}}
	ids: [for k, s in subnets {s.id}]
}`, "net.ids", "net.subnets.a,net.subnets.b", []string{"net.subnets.a", "net.subnets.b", "net.ids"}},
	}
	for _, c := range cases {
		if got := depsOf(t, c.src, c.self, c.tasks...); got != c.want {
			t.Errorf("%s: dependencies = %q, want %q", c.name, got, c.want)
		}
	}
}

// The attribute that reads the outer task through the let is unknown until that task
// has run, and the task depends on it: it is not sent to the provider as null.
func TestAttributeReadThroughAShadowedLetIsUnknown(t *testing.T) {
	v := cuecontext.New().CompileString(`
net: {
	vpc: {id: string | null}
	let _vpc = vpc
	flowLogs: {
		vpc: {filename: "x", content: _vpc.id}
	}
}`)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	self := cue.ParsePath("net.flowLogs.vpc")
	c := &Converter{Resolve: taskPaths{paths: []string{"net.vpc", "net.flowLogs.vpc"}, root: v}, Self: self}
	got := c.Block("net.flowLogs.vpc", fileBlock(), v.LookupPath(self))
	if err := c.Err(); err != nil {
		t.Fatal(err)
	}
	var m map[string]tftypes.Value
	_ = got.As(&m)
	if m["content"].IsKnown() {
		t.Errorf("content should be unknown, got %s", m["content"])
	}
	if d := c.DepAddrs(); len(d) != 1 || d[0] != "net.vpc" {
		t.Errorf("deps = %v, want [net.vpc]", d)
	}
}

// A value that is read from the task itself and has none yet is refused, naming the
// attribute and the line, and is not sent as null.
func TestUnsettledValueReadFromTheTaskItselfIsRefused(t *testing.T) {
	v := cuecontext.New().CompileString(`
self: {filename: "x", content: self.id, id: string | null}
`)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	c := &Converter{Resolve: taskPaths{paths: []string{"self"}, root: v}, Self: cue.ParsePath("self")}
	c.Block("self", fileBlock(), v.LookupPath(cue.ParsePath("self")))
	err := c.Err()
	if err == nil || !strings.Contains(err.Error(), "self.content: value is read from the task itself") || !strings.Contains(err.Error(), "(at ") {
		t.Errorf("error = %v", err)
	}
}

type recordingResolver struct {
	Resolver
	asked []string
}

func (r *recordingResolver) Task(p cue.Path) (cue.Path, cue.Path, bool) {
	r.asked = append(r.asked, p.String())
	return r.Resolver.Task(p)
}

// A read through a let names the attribute that the reader takes:
// what is derived from a task's sensitive attributes depends on it.
func TestReadThroughALetNamesTheAttribute(t *testing.T) {
	v := cuecontext.New().CompileString(`
net: {
	vpc: {id: string | null, secret: string | null}
	let _vpc = vpc
	reader: {a: _vpc.id}
}`)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	self := cue.ParsePath("net.reader")
	rec := &recordingResolver{Resolver: taskPaths{paths: []string{"net.vpc", "net.reader"}, root: v}}
	Dependencies(v.LookupPath(self), self, rec)
	found := false
	for _, a := range rec.asked {
		if a == "net.vpc.id" {
			found = true
		}
	}
	if !found {
		t.Errorf("net.vpc.id was not read: %v", rec.asked)
	}
}
