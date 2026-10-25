package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// policyRoot is a root over a sealed local state, with policies and files that the local
// provider writes. ada is the reader.
type policyRoot struct {
	dir string
	ada *protect.Identity
}

const policyHead = "package infra\n\nimport \"pravo.dev/pravo\"\nimport loc \"pravo.dev/providers/hashicorp/local\"\n\n$local: loc.#Provider\n\n#unused: pravo.#Policy\n"

// newPolicyRoot makes the root with the given policies and files, and the state sealed to ada.
func newPolicyRoot(t *testing.T, rules map[string]string, files ...string) *policyRoot {
	t.Helper()
	requireLocalProvider(t)
	requireSealing(t)
	ids := isolateIdentities(t)
	ada, line := person(t, ids, "ada")
	r := &policyRoot{ada: ada, dir: specDir(t, map[string]string{})}
	r.declare(t, rules, files...)
	if text, code := runPravo(t, r.dir, "init"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	writeReaders(t, filepath.Join(r.dir, ".pravo", "access"), line)
	st, err := state.Open(filepath.Join(r.dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Upgrade(context.Background(), state.UpgradeOptions{To: state.FormatSealed, Readers: []protect.Reader{ada.AsReader()}, Identity: ada, By: "ada"}); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *policyRoot) readers() []protect.Reader { return []protect.Reader{r.ada.AsReader()} }

func (r *policyRoot) store(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.OpenWith(filepath.Join(r.dir, "pravo.state"), state.Options{Identity: r.ada})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (r *policyRoot) policies(t *testing.T) *state.PolicySet {
	t.Helper()
	set, err := r.store(t).PolicySet(context.Background(), r.readers())
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// declare rewrites the specification: the policies, and a file for each name.
func (r *policyRoot) declare(t *testing.T, rules map[string]string, files ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString(policyHead)
	names := make([]string, 0, len(rules))
	for name := range rules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "%s: pravo.#Policy & {rule: {\n%s\n}}\n", name, rules[name])
	}
	for _, f := range files {
		fmt.Fprintf(&b, "%s: $local.#Resource.local_file & {filename: \"%s.txt\", content: \"x\"}\n", f, f)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "main.cue"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *policyRoot) apply(t *testing.T) (string, int) {
	t.Helper()
	return runPravo(t, r.dir, "apply", "--auto-approve")
}

// policyInForce is a root with the files a, b, c and d applied, and the policy in force.
func policyInForce(t *testing.T, rule string) *policyRoot {
	t.Helper()
	rules := map[string]string{"rule": rule}
	r := newPolicyRoot(t, rules, "a", "b", "c", "d")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("first apply: exit %d\n%s", code, text)
	}
	r.declare(t, rules, "b", "c", "d") // a is destroyed by the next plan
	return r
}

func TestPoliciesJudgeLaterPlansAndTheReceiptListsThem(t *testing.T) {
	rules := map[string]string{"few": "summary: destroy: <=1"}
	r := newPolicyRoot(t, rules, "a", "b", "c", "d")
	text, code := runPravo(t, r.dir, "plan")
	if code != 0 || !strings.Contains(text, "Policies: +1 new, ~0 changed, -0 removed") || !strings.Contains(text, "+ few (sha256:") {
		t.Fatalf("plan: exit %d\n%s", code, text)
	}
	// The plan that adds a policy is not judged by it; the apply writes the version last.
	if text, code = r.apply(t); code != 0 {
		t.Fatalf("first apply: exit %d\n%s", code, text)
	}
	set := r.policies(t)
	if len(set.InForce()) != 1 || set.InForce()[0].Name != "few" || set.InForce()[0].Signer != "ada" {
		t.Fatalf("policies: %+v", set.InForce())
	}
	if text, code = runPravo(t, r.dir, "plan"); code != 0 || strings.Contains(text, "Policies:") {
		t.Fatalf("an unchanged policy is not in the plan: exit %d\n%s", code, text)
	}

	// Within the rule: applied, and the signed start lists the version that judged.
	r.declare(t, rules, "b", "c", "d")
	if text, code = r.apply(t); code != 0 {
		t.Fatalf("apply within the rule: exit %d\n%s", code, text)
	}
	head, _ := set.Head("few")
	st := r.store(t)
	txns, err := st.Transactions()
	if err != nil || len(txns) == 0 {
		t.Fatal(err)
	}
	last := txns[0]
	if len(last.Policies) != 1 || last.Policies[0] != head.Ref() {
		t.Errorf("the receipt lists %v, want %s", last.Policies, head.Ref())
	}
	if v := state.VerifyTransaction(st.Lineage(), &last, r.readers()); v.Overall() != protect.SigVerified {
		t.Errorf("the transaction does not verify: %+v", v)
	}

	// A plan the rule refuses: exit 5 at plan and at apply, and nothing is destroyed.
	r.declare(t, rules, "d")
	text, code = runPravo(t, r.dir, "plan")
	if code != 5 || !strings.Contains(text, "violates the policy few@1") {
		t.Fatalf("plan: exit %d\n%s", code, text)
	}
	text, code = r.apply(t)
	if code != 5 || !strings.Contains(text, "violates the policy few@1") {
		t.Fatalf("apply: exit %d\n%s", code, text)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "c.txt")); err != nil {
		t.Errorf("a refused apply destroyed a file: %v", err)
	}
	if rejected := lastTxn(t, r.store(t)); rejected.Status != "rejected" || len(rejected.Policies) != 1 {
		t.Errorf("the refusal is recorded with the versions: %+v", rejected)
	}
}

func lastTxn(t *testing.T, st *state.Store) state.Transaction {
	t.Helper()
	txns, err := st.Transactions()
	if err != nil || len(txns) == 0 {
		t.Fatal(err)
	}
	return txns[0]
}

func TestAPolicyThatRefusesEveryPlanIsRetiredByAPolicyOnlyApply(t *testing.T) {
	rules := map[string]string{"none": "summary: destroy: 0"}
	r := newPolicyRoot(t, rules, "a")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	// The removal is part of a plan that also destroys: refused by the policy in force.
	r.declare(t, nil)
	if text, code := r.apply(t); code != 5 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	// Policy only: it asks, exits 2 under --detailed-exitcode, and is not judged.
	r.declare(t, nil, "a")
	text, code := runPravo(t, r.dir, "plan", "--detailed-exitcode")
	if code != 2 || !strings.Contains(text, "-1 removed") {
		t.Fatalf("plan: exit %d\n%s", code, text)
	}
	if text, code = r.apply(t); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	set := r.policies(t)
	if len(set.InForce()) != 0 {
		t.Fatalf("after the removal: %+v", set.InForce())
	}
	if head, ok := set.Head("none"); !ok || head.Seq != 2 || !head.Tombstone() {
		t.Errorf("head %+v", head)
	}
	if last := lastTxn(t, r.store(t)); len(last.Policies) != 0 {
		t.Errorf("a policy-only apply judges nothing: %v", last.Policies)
	}
}

func TestAChangedPolicyIsJudgedByTheVersionInForce(t *testing.T) {
	r := newPolicyRoot(t, map[string]string{"p": "summary: destroy: <=5"}, "a")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	// An edit that is stricter than the plan it comes with: the plan is judged by the
	// version in force, so the destroy passes, and the new version is written.
	r.declare(t, map[string]string{"p": "summary: destroy: 0"})
	text, code := runPravo(t, r.dir, "plan")
	if code != 0 || !strings.Contains(text, "~ p (version 1") {
		t.Fatalf("plan: exit %d\n%s", code, text)
	}
	if text, code = r.apply(t); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	head, _ := r.policies(t).Head("p")
	if head.Seq != 2 || head.Text != "summary: {\n\tdestroy: 0\n}\n" {
		t.Errorf("head %+v", head)
	}
}

func TestAStalePolicyEntryIsRefused(t *testing.T) {
	r := newPolicyRoot(t, map[string]string{"p": "summary: destroy: <=5"}, "a")
	plan := filepath.Join(t.TempDir(), "p.plan")
	if text, code := runPravo(t, r.dir, "plan", "--out", plan); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	// Another writer puts the first version before this plan is applied.
	if err := r.store(t).PutPolicyVersion(context.Background(), "p", 1, "summary: add: >=0", time.Now()); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, r.dir, "apply", plan)
	if code != 3 || !strings.Contains(text, "policy p changed") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}

func TestPoliciesNeedASealedStateAtPlan(t *testing.T) {
	requireLocalProvider(t)
	dir := specDir(t, map[string]string{})
	r := &policyRoot{dir: dir}
	r.declare(t, map[string]string{"no": "summary: destroy: 0"})
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "plan")
	if code != 5 || !strings.Contains(text, "must be sealed") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}

// A saved plan is judged whole, whatever --only selects, by the policies in force when
// the apply starts, including a version put after the plan was made.
func TestTheWholeFileIsJudgedWhateverOnlySelects(t *testing.T) {
	r := newPolicyRoot(t, nil, "a", "b", "c")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	r.declare(t, nil, "c")
	plan := filepath.Join(t.TempDir(), "p.plan")
	if text, code := runPravo(t, r.dir, "plan", "--out", plan); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if err := r.store(t).PutPolicyVersion(context.Background(), "lim", 1, "summary: destroy: <=1", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"apply", plan}, {"apply", plan, "--only", "a"}} {
		text, code := runPravo(t, r.dir, args...)
		if code != 5 || !strings.Contains(text, "violates the policy lim@1") {
			t.Errorf("pravo %s: exit %d\n%s", strings.Join(args, " "), code, text)
		}
	}
	if _, err := os.Stat(filepath.Join(r.dir, "a.txt")); err != nil {
		t.Errorf("the refused apply destroyed a file: %v", err)
	}
}

func TestVerifyChecksTheReceiptOfAStoredPlan(t *testing.T) {
	rules := map[string]string{"lim": "summary: {\n\tdestroy: <=2\n}\n"}
	r := newPolicyRoot(t, rules, "a", "b", "c", "d")
	// The version is dated well before the apply, as one that has been in force for a while.
	if err := r.store(t).PutPolicyVersion(context.Background(), "lim", 1, rules["lim"], time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	r.declare(t, rules, "c", "d")
	id := storePlanAs(t, r.dir, "ci")
	text, code := runPravo(t, r.dir, "apply", id)
	if code != 0 {
		t.Fatalf("apply exit %d\n%s", code, text)
	}
	txn := lastTxn(t, r.store(t))
	if len(txn.Policies) != 1 || txn.PlanID != id {
		t.Fatalf("receipt %+v", txn)
	}
	text, code = runPravo(t, r.dir, "state", "verify", txn.ID)
	if code != 0 || !strings.Contains(text, "lim@1:") || !strings.Contains(text, "Verified.") {
		t.Fatalf("verify: exit %d\n%s", code, text)
	}
	// An apply that judged the stored plan keeps it: the plan is the other half of the receipt.
	if prog, err := engine.NewPlanStoreFor(r.store(t)).Progress(context.Background(), id); err != nil || !prog.Kept {
		t.Errorf("the plan is not kept: %+v %v", prog, err)
	}

	// A transaction of the same plan that names no policy omits one that was in force.
	st := r.store(t)
	forged := st.Begin("apply", "pravo apply "+id)
	forged.SetPlan(id, txn.PlanDigest)
	if err := forged.Start(); err != nil {
		t.Fatal(err)
	}
	forged.Record("a", "delete", 1, 2)
	if err := forged.Finish("applied", "", true); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, r.dir, "state", "verify", forged.ID())
	if code != 7 || !strings.Contains(text, "does not list it") {
		t.Errorf("an omission: exit %d\n%s", code, text)
	}
}

// A rule that is written against the schema of pravo is stored without it: the state
// holds standalone CUE, and the judge compiles it without the module.
func TestARuleWrittenAgainstTheSchemaIsStoredWithoutIt(t *testing.T) {
	rules := map[string]string{"nodel": "changes: [...pravo.#Change & {action: string, addr: string\n\tif action == \"delete\" {error(addr)}\n}]"}
	r := newPolicyRoot(t, rules, "a", "b")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("first apply: exit %d\n%s", code, text)
	}
	head, _ := r.policies(t).Head("nodel")
	if strings.Contains(head.Text, "pravo") || !strings.Contains(head.Text, "if action == \"delete\"") {
		t.Errorf("stored text: %q", head.Text)
	}
	r.declare(t, rules, "b") // a is destroyed by the next plan
	if text, code := runPravo(t, r.dir, "plan"); code != 5 || !strings.Contains(text, "a: ") && !strings.Contains(text, "changes.") {
		t.Errorf("plan must be refused by the rule: exit %d\n%s", code, text)
	}
}
