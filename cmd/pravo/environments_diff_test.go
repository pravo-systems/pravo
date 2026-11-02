package main

import (
	"strconv"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

// A value that is not concrete, such as an attribute the provider computes, does not hide the
// values beside it: in a struct, and in a list, which a block of a provider is.
func TestEnvironmentsDiffFollowsLists(t *testing.T) {
	ctx := cuecontext.New()
	resource := func(days int) any {
		v := ctx.CompileString(`
bucket: string | null
rule: [{
	id:     "expire"
	filter: [{prefix: string | null}]
	expiration: [{days: ` + strconv.Itoa(days) + `}]
}]`)
		if v.Err() != nil {
			t.Fatal(v.Err())
		}
		return jsonOf(v)
	}
	diffs := valueDiffs("lifecycle", resource(7), resource(30), 0)
	if len(diffs) != 1 || diffs[0].Path != "lifecycle.rule[0].expiration[0].days" {
		t.Fatalf("diffs: %+v", diffs)
	}
	if diffs := valueDiffs("lifecycle", resource(7), resource(7), 0); len(diffs) != 0 {
		t.Fatalf("the same values differ: %+v", diffs)
	}
	// Lists of different lengths are one difference.
	short, long := []any{1.0}, []any{1.0, 2.0}
	if diffs := valueDiffs("ports", short, long, 0); len(diffs) != 1 || diffs[0].Path != "ports" {
		t.Fatalf("lengths: %+v", diffs)
	}
}
