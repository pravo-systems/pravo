package engine

import (
	"strings"
	"testing"
	"time"
)

func TestMarkdownNamesChangesAndNeverValues(t *testing.T) {
	v := &View{
		Actor: "ci", Reason: "bump the size", GitRevision: "0123456789ab",
		Summary: Summary{Add: 1, Change: 1, Destroy: 1, BlastRadius: 4},
		Changes: []ChangeView{
			{Addr: "db", Type: "aws_db_instance", Action: ActionUpdate,
				Before:     map[string]any{"size": "small", "password": "hunter2", "name": "x"},
				After:      map[string]any{"size": "large", "password": "hunter3", "name": "x"},
				Dependents: []string{"app", "dns"}},
			{Addr: "queue", Type: "aws_sqs_queue", Action: ActionCreate},
			{Addr: "old|one", Type: "aws_s3_bucket", Action: ActionDelete, Drifted: true},
			{Addr: "settled", Type: "aws_vpc", Action: ActionNoop},
		},
		Hints: []string{"line one\nline two"},
	}
	got := v.Markdown(MarkdownOptions{
		ID: "plan-0a1b2c3d", Expires: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC),
		Apply: "pravo apply plan-0a1b2c3d", Refused: "policy no_destroy refuses a delete",
	})
	for _, want := range []string{
		"### pravo plan: 1 to add, 1 to change, 1 to destroy",
		"by `ci` · bump the size · at `0123456789ab`",
		"**Refused by policy.** policy no_destroy refuses a delete",
		"| update | `db` | `aws_db_instance` | changes `password`, `size`; needed by 2 resources |",
		"| create | `queue` | `aws_sqs_queue` |  |",
		"| delete | `old\\|one` | `aws_s3_bucket` | drifted |",
		"The changes reach 4 resources.",
		"- line one line two",
		"Stored as `plan-0a1b2c3d`, removed on 2026-11-04 12:00 UTC unless it is applied.",
		"```sh\npravo apply plan-0a1b2c3d\n```",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the comment lacks %q:\n%s", want, got)
		}
	}
	for _, leak := range []string{"hunter2", "hunter3", "small", "large", "settled"} {
		if strings.Contains(got, leak) {
			t.Errorf("the comment has %q:\n%s", leak, got)
		}
	}
}

func TestMarkdownOfAnEmptyPlan(t *testing.T) {
	got := (&View{}).Markdown(MarkdownOptions{Title: "Saved plan"})
	if got != "### Saved plan: no changes\n" {
		t.Errorf("an empty plan reads %q", got)
	}
}

func TestMarkdownStaysUnderTheLimit(t *testing.T) {
	v := &View{Summary: Summary{Add: 5000}}
	for i := range 5000 {
		v.Changes = append(v.Changes, ChangeView{Addr: strings.Repeat("a", 40) + string(rune('a'+i%26)), Type: "t", Action: ActionCreate,
			After: map[string]any{"k": "v"}})
	}
	got := v.Markdown(MarkdownOptions{ID: "plan-0a1b2c3d", Apply: "pravo apply plan-0a1b2c3d"})
	if len(got) > MarkdownLimit {
		t.Errorf("the comment has %d bytes, over %d", len(got), MarkdownLimit)
	}
	if !strings.Contains(got, "more changes are not listed here") || !strings.Contains(got, "pravo apply plan-0a1b2c3d") {
		t.Errorf("the cut comment lost its note or the apply command:\n%s", got[len(got)-300:])
	}
}
