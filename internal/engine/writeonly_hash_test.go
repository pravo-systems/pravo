package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOnlySpec is one resource whose write-only attribute takes a secret of the
// state, with the version attribute that sits next to it.
func writeOnlySpec(version string) spec {
	return spec{
		cue:       "sec: {name: \"prod/token\", value: string}\na: " + thing("a", "secret_wo: sec.value\nsecret_wo_version: \""+version+"\"") + "\n",
		resources: []string{"a"}, secrets: []string{"sec"},
	}
}

// planOf plans s and returns the change of the resource a.
func planOf(t *testing.T, h *harness, s spec, opts Options) (*Engine, *Plan, *Change) {
	t.Helper()
	e := h.engine(s, opts)
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range plan.Changes {
		if ch.Addr == "a" {
			return e, plan, ch
		}
	}
	t.Fatal("the plan has no change for a")
	return nil, nil, nil
}

// The digest of the configuration that a record keeps is short and has no key. A
// write-only value is in the state nowhere else, so the digest leaves it out:
// two states that were given different secrets hold the same digest, and nothing
// a reader of the record can compute tells which secret it was.
func TestAWriteOnlyValueIsNotInTheRecordedHash(t *testing.T) {
	hashOf := func(secret string) string {
		h, opts := secretHarness(t)
		h.setSecret("prod/token", secret)
		e, plan, ch := planOf(t, h, writeOnlySpec("1"), opts)
		if _, err := e.Apply(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		if got := h.fake.secrets; len(got) != 1 || got[0] != secret {
			t.Fatalf("the provider was given %q, want the secret", got)
		}
		if got := h.leaks(t, e, nil, secret); len(got) > 0 {
			t.Errorf("the secret is in %s", strings.Join(got, ", "))
		}
		file := filepath.Join(t.TempDir(), "p.plan")
		if err := e.SavePlan(file, plan, nil, false); err != nil {
			t.Fatal(err)
		}
		if raw, err := os.ReadFile(file); err != nil || strings.Contains(string(raw), secret) {
			t.Errorf("the plan file holds the secret (read: %v)", err)
		}
		r := h.state.Get("a")
		if r.ConfigHash == "" || r.ConfigHash != ch.ConfigHash {
			t.Fatalf("the record holds the hash %q, the plan %q", r.ConfigHash, ch.ConfigHash)
		}
		if r.ConfigHash == configHash(ch.Config) {
			t.Errorf("the hash %s is that of the configuration with its write-only value", r.ConfigHash)
		}
		return r.ConfigHash
	}
	if one, two := hashOf(secretOne), hashOf(secretTwo); one != two {
		t.Errorf("two secrets give the hashes %s and %s: the hash depends on the write-only value", one, two)
	}
}

// A provider cannot see that a write-only value changed, and neither does a plan:
// a new value alone plans nothing, so a value that is new on every run does not
// make every plan reach the resource. The version attribute next to it is an
// ordinary attribute: a new version plans an update, and the apply hands the
// provider the new value.
func TestANewWriteOnlyValueAlonePlansNothing(t *testing.T) {
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	h.apply2(t, writeOnlySpec("1"), opts)

	h.setSecret("prod/token", secretTwo)
	_, plan, ch := planOf(t, h, writeOnlySpec("1"), opts)
	if plan.HasChanges() || !ch.Skipped {
		t.Errorf("a new write-only value alone: action %s, skipped %v, reasons %q", ch.Action, ch.Skipped, ch.Reasons)
	}

	e, plan, ch := planOf(t, h, writeOnlySpec("2"), opts)
	if ch.Action != ActionUpdate {
		t.Fatalf("a new version: action %s, reasons %q; want an update", ch.Action, ch.Reasons)
	}
	if _, err := e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.secrets; len(got) != 2 || got[1] != secretTwo {
		t.Errorf("the provider was given %q, want the new secret with the new version", got)
	}
}

// A record that an earlier pravo wrote holds a digest that covers the write-only
// value. A plan says why it looks at the resource and plans no change, and the
// next apply records the digest that leaves the value out.
func TestARecordWithAHashOfAWriteOnlyValueIsReplaced(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	h.apply2(t, writeOnlySpec("1"), opts)
	_, _, same := planOf(t, h, writeOnlySpec("1"), opts)
	old := *h.state.Get("a")
	want := old.ConfigHash
	old.ConfigHash = configHash(same.Config)
	if old.ConfigHash == "" || old.ConfigHash == want {
		t.Fatalf("the hash with the write-only value is %q and the hash without it %q: the test shows nothing", old.ConfigHash, want)
	}
	if err := h.state.Set(&old, ""); err != nil {
		t.Fatal(err)
	}

	e, plan, ch := planOf(t, h, writeOnlySpec("1"), opts)
	if plan.HasChanges() || ch.Action != ActionNoop {
		t.Fatalf("the plan would %s (reasons %q); want no change", ch.Action, ch.Reasons)
	}
	if len(ch.Reasons) == 0 || !strings.Contains(ch.Reasons[0], "from an earlier pravo") || strings.Contains(strings.Join(ch.Reasons, " "), "the specification changed") {
		t.Errorf("reasons %q; want the one that names the hash of an earlier pravo", ch.Reasons)
	}
	if _, err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if got := h.state.Get("a").ConfigHash; got != want {
		t.Errorf("after the apply the record holds the hash %s, want %s", got, want)
	}
	if got := h.fake.secrets; len(got) != 1 {
		t.Errorf("the provider applied %d times, want only the create", len(got))
	}
	if _, _, ch := planOf(t, h, writeOnlySpec("1"), opts); !ch.Skipped {
		t.Errorf("the plan after: action %s, reasons %q; want the resource left alone", ch.Action, ch.Reasons)
	}
}

// The protocol has a provider answer null for a write-only attribute. One that
// answers the value it was given, in its plan and in the state after its apply,
// has the value dropped before anything keeps it, and the plan says so. Nothing
// is refused: the object exists after the apply and needs its record.
func TestAWriteOnlyValueThatAProviderReturnsIsDropped(t *testing.T) {
	ctx := context.Background()
	h, opts := secretHarness(t)
	h.fake.returnsWriteOnly = true
	h.setSecret("prod/token", secretOne)
	e, plan, ch := planOf(t, h, writeOnlySpec("1"), opts)
	if text := ch.Planned.String(); strings.Contains(text, secretOne) {
		t.Errorf("the planned state holds the secret: %s", text)
	}
	warned := false
	for _, w := range ch.Warnings {
		warned = warned || strings.Contains(w.Summary, "write-only attribute of fake_thing: secret_wo")
		if strings.Contains(w.Summary+w.Detail, secretOne) {
			t.Errorf("the warning holds the secret: %+v", w)
		}
	}
	if !warned {
		t.Errorf("warnings %+v; want one that names secret_wo", ch.Warnings)
	}
	if _, err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.secrets; len(got) != 1 || got[0] != secretOne {
		t.Fatalf("the provider was given %q, want the secret", got)
	}
	if got := h.leaks(t, e, nil, secretOne); len(got) > 0 {
		t.Errorf("the secret is in %s", strings.Join(got, ", "))
	}
	if attrs := string(h.state.Get("a").Attributes); !strings.Contains(attrs, `"secret_wo":null`) {
		t.Errorf("the record holds %s; want secret_wo null", attrs)
	}
	if _, plan, ch := planOf(t, h, writeOnlySpec("1"), opts); plan.HasChanges() {
		t.Errorf("the plan after: action %s, reasons %q; want no change", ch.Action, ch.Reasons)
	}
}
