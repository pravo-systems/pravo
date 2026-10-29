package main

import (
	"strings"
	"testing"
)

// The answer of the stand-in model is written from the read of the plans: the
// plans that can be applied, the resources that more than one replaces, and the
// command of the first.
func TestPlansAnswerIsWrittenFromTheRead(t *testing.T) {
	body := `{"plans":[
	 {"id":"plan-aaaa1111","status":"pending","author":"ben","summary":{"destroy":6},"overlaps":[{"id":"plan-bbbb2222","addrs":["x.b","x.a"]}]},
	 {"id":"plan-bbbb2222","status":"pending","author":"ana","summary":{"destroy":3},"overlaps":[{"id":"plan-aaaa1111","addrs":["x.a","x.b"]}]},
	 {"id":"plan-cccc3333","status":"expired","author":"ci","summary":{"destroy":2}}]}`
	got := plansAnswer(body)
	for _, want := range []string{"**2 plans can be applied.**", "`plan-aaaa1111`, by ben, replaces 6", "`plan-bbbb2222`, by ana, replaces 3", "`x.a`, `x.b`", "pravo apply plan-aaaa1111"} {
		if !strings.Contains(got, want) {
			t.Errorf("the answer lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "plan-cccc3333") {
		t.Errorf("an expired plan is in the answer:\n%s", got)
	}
	if got := plansAnswer("not json"); !strings.Contains(got, "did not return") {
		t.Errorf("a body that is not JSON: %q", got)
	}
}
