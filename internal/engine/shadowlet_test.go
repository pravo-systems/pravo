package engine

import "testing"

// A name in the expression of a let is the one that the scope of the let declares,
// whatever fields of the same name sit between the let and the task that uses it.
func TestLetIsReadFromWhereItIsDeclared(t *testing.T) {
	cases := []struct{ name, cue, addr, want string }{
		{"the reader has the name", `
net: {
	vpc: {id: "v", name: "vpc"}
	let _vpc = vpc
	flow: {
		vpc: {id: "f", name: "flow", ref: _vpc.id}
	}
}`, "net.flow.vpc", "net.vpc"},
		{"another resource has the name", `
net: {
	vpc: {id: "v", name: "vpc"}
	let _vpc = vpc
	flow: {
		vpc:    {id: "f", name: "flow"}
		reader: {id: "r", name: "reader", ref: _vpc.id}
	}
}`, "net.flow.reader", "net.vpc"},
		{"the let and the reader share a struct", `
net: {
	vpc: {id: "v", name: "vpc"}
	let _vpc = vpc
	reader: {id: "r", name: "reader", ref: _vpc.id}
}`, "net.reader", "net.vpc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := edgesOf(t, c.cue, false)
			if got[c.addr] != c.want {
				t.Errorf("%s reads %q, want %q", c.addr, got[c.addr], c.want)
			}
		})
	}
}
