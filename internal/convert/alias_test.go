package convert

import "testing"

// A name that stands for a task is a dependency on that task, whatever way the
// name is reached: a let, a let of a let, a loop variable. The analysis must
// report the task itself and not what the task is built from.
func TestLetNamingATaskIsADependencyOnIt(t *testing.T) {
	const head = `
pet: {id: string | null}
prefix: {value: string}
bucket: {name: "\(prefix.value)-\(pet.id)", id: string | null}
`
	cases := []struct{ name, src, want string }{
		{"a let at the top", `
let _main = bucket
hello: {bucket: _main.id}`, "bucket"},
		{"a let in the struct", `
hello: {
	let _main = bucket
	name: _main.id
}`, "bucket"},
		{"a let outside a struct whose field has the name of the task", `
let _main = bucket
hello: {
	bucket: _main.id
	key:    "k"
}`, "bucket"},
		{"a let of a let", `
let _a = bucket
let _b = _a
hello: {bucket: _b.id}`, "bucket"},
		{"a let of a field of the task", `
let _id = bucket.id
hello: {bucket: _id}`, "bucket"},
		{"a let inside interpolation", `
let _main = bucket
hello: {bucket: "id-\(_main.id)"}`, "bucket"},
		{"a let beside a plain reference", `
let _main = bucket
hello: {bucket: _main.id, key: pet.id}`, "bucket,pet"},
		{"a let that names no task", `
let _p = prefix.value
hello: {key: _p}`, "prefix"},
		{"a let of a plain value", `
let _n = "x"
hello: {key: _n}`, ""},
		{"a plain field built from a let in its struct", `
events: {
	let _main = bucket
	arn: _main.id
}
hello: {key: events.arn}`, "bucket"},
		{"a plain field built from a let, read through a comprehension", `
events: {
	let _main = bucket
	arn: _main.id
	sha: "x"
}
hello: {names: [for k, v in {A: events.arn, B: events.sha} {name: k, value: v}]}`, "bucket"},
		{"a plain field built from a let, read by a let's comprehension", `
events: {
	let _main = bucket
	arn: _main.id
}
hello: {
	let env = [for k, v in {A: events.arn} {name: k, value: v}]
	names: env
}`, "bucket"},
		{"a let in a comprehension", `
hello: {names: [for k in ["a"] let b = bucket {b.id}]}`, "bucket"},
	}
	for _, c := range cases {
		if got := depsOf(t, head+c.src, "hello", "pet", "prefix", "bucket", "hello"); got != c.want {
			t.Errorf("%s: dependencies = %q, want %q", c.name, got, c.want)
		}
	}
}

// A task inside a struct, named by a let of that struct and read through a field
// that the let builds, is a dependency of whatever reads the field.
func TestFieldBuiltFromALetOfASiblingTask(t *testing.T) {
	const src = `
events: {
	bus: {result: string | null}
	let _bus = bus
	arn: _bus.result
}
hello: {
	let env = [for k, v in {A: events.arn} {name: k, value: v}]
	names: env
}
plain: {key: events.arn}
`
	for _, self := range []string{"hello", "plain"} {
		if got := depsOf(t, src, self, "events.bus", "hello", "plain"); got != "events.bus" {
			t.Errorf("%s: dependencies = %q, want events.bus", self, got)
		}
	}
}

// A loop variable that stands for a task, or for a struct of tasks, is a
// dependency on those tasks.
func TestLoopVariableNamingATaskIsADependencyOnIt(t *testing.T) {
	const head = `
api: services: {
	a: {id: string | null}
	b: {id: string | null}
}
`
	cases := []struct{ name, src, want string }{
		{"the value", `
out: {names: [for s, svc in api.services {svc.id}]}`, "api.services.a,api.services.b"},
		{"the value in a struct", `
out: {names: {for s, svc in api.services {(s): svc.id}}}`, "api.services.a,api.services.b"},
		{"in interpolation", `
out: {names: [for s, svc in api.services {"\(s)-\(svc.id)"}]}`, "api.services.a,api.services.b"},
		{"a let of the loop variable", `
out: {names: [for s, svc in api.services let x = svc {x.id}]}`, "api.services.a,api.services.b"},
		{"a name that is not a task", `
out: {names: [for s, svc in {a: "x"} {svc}]}`, ""},
	}
	for _, c := range cases {
		if got := depsOf(t, head+c.src, "out", "api.services.a", "api.services.b", "out"); got != c.want {
			t.Errorf("%s: dependencies = %q, want %q", c.name, got, c.want)
		}
	}
}
