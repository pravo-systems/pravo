package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// promoRoot is one specification with a state for each environment, as the environments how-to lays it
// out: the state path comes from the env tag, and the production environment declares the policy that
// asks staging for an attestation. Both states are sealed to ada.
type promoRoot struct {
	dir, key string
}

const promoSpec = `package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

env:   *"dev" | "staging" | "prod" @tag(env)
image: *"1.0" | string @tag(image)

$settings: pravo.#Settings & {state: path: "state/\(env)"}
$local: loc.#Provider

app: $local.#Resource.local_file & {
	filename: "app-\(env).txt"
	content:  "image %s\n"
}

if env == "prod" {
	gate: pravo.#Policy & {
		rule: promote: {
			from: state: path: "state/staging"
			signers: ["%s"]
			vary: ["env"]%s
		}
	}
}
`

func newPromoRoot(t *testing.T, signers, extra string) *promoRoot {
	t.Helper()
	requireLocalProvider(t)
	requireSealing(t)
	ids := isolateIdentities(t)
	id, line := person(t, ids, "ada")
	key := protect.SSHFingerprint(id.SignPub)
	if signers == "" {
		signers = key
	}
	r := &promoRoot{dir: specDir(t, map[string]string{}), key: key}
	r.write(t, "1.0", signers, extra)
	if text, code := runPravo(t, r.dir, "init", "-t", "env=staging"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	writeReaders(t, filepath.Join(r.dir, ".pravo", "access"), line)
	for _, env := range []string{"staging", "prod"} {
		if text, code := runPravo(t, r.dir, "protect", "init", "--encrypt", "--yes", "--allow-local", "-t", "env="+env); code != 0 {
			t.Fatalf("protect init %s: exit %d\n%s", env, code, text)
		}
	}
	return r
}

func (r *promoRoot) write(t *testing.T, image, signers, extra string) {
	t.Helper()
	spec := fmt.Sprintf(promoSpec, image, signers, extra)
	if err := os.WriteFile(filepath.Join(r.dir, "main.cue"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *promoRoot) run(t *testing.T, env string, args ...string) (string, int) {
	t.Helper()
	return runPravo(t, r.dir, append(args, "-t", "env="+env)...)
}

// The policy is in force in production once a first apply has put it there; from then on production takes
// a specification only after staging vouched for it.
func (r *promoRoot) arm(t *testing.T) {
	t.Helper()
	for _, env := range []string{"staging", "prod"} {
		if text, code := r.run(t, env, "apply", "--auto-approve"); code != 0 {
			t.Fatalf("first apply in %s: exit %d\n%s", env, code, text)
		}
	}
}

func TestProductionTakesASpecificationOnlyAfterStagingVouchedForIt(t *testing.T) {
	r := newPromoRoot(t, "", "")
	r.arm(t)
	r.write(t, "1.1", r.key, "")

	// Staging runs the new specification. Production cannot take it yet, and the plan says so.
	if text, code := r.run(t, "staging", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("staging apply: exit %d\n%s", code, text)
	}
	text, code := r.run(t, "prod", "plan")
	if code != 0 || !strings.Contains(text, "Promotion: not yet.") || !strings.Contains(text, "holds no attestation of this specification") {
		t.Fatalf("prod plan: exit %d\n%s", code, text)
	}
	text, code = r.run(t, "prod", "apply", "--auto-approve")
	if code != 5 || !strings.Contains(text, "asks state/staging for an attestation") {
		t.Fatalf("prod apply without an attestation: exit %d\n%s", code, text)
	}

	// Nothing to vouch for in a state that did not apply it.
	if text, code := r.run(t, "prod", "promote", "attest"); code != 5 || !strings.Contains(text, "does not run the specification yet") {
		t.Fatalf("attest in prod: exit %d\n%s", code, text)
	}
	text, code = r.run(t, "staging", "promote", "attest", "--note", "smoke tests passed")
	if code != 0 || !strings.Contains(text, "Attested:") || !strings.Contains(text, "signers: [\""+r.key+"\"]") || !strings.Contains(text, "lineage:") {
		t.Fatalf("attest: exit %d\n%s", code, text)
	}
	text, code = r.run(t, "staging", "promote", "list", "--json")
	var list []struct {
		Fingerprint, Status, Note string
		Current                   bool
	}
	if err := json.Unmarshal([]byte(text), &list); code != 0 || err != nil || len(list) != 1 || list[0].Status != "verified" || !list[0].Current || list[0].Note != "smoke tests passed" {
		t.Fatalf("list --json: exit %d, %v\n%s", code, err, text)
	}

	text, code = r.run(t, "prod", "plan")
	if code != 0 || !strings.Contains(text, "Promotion: state/staging holds an attestation by ada") {
		t.Fatalf("prod plan after the attestation: exit %d\n%s", code, text)
	}
	if text, code = r.run(t, "prod", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("prod apply after the attestation: exit %d\n%s", code, text)
	}

	// The transaction of the apply says which specification it ran.
	text, code = r.run(t, "prod", "state", "log", "--json")
	if code != 0 || !strings.Contains(text, `"config_fingerprint": "`+list[0].Fingerprint+`"`) {
		t.Fatalf("state log: exit %d\n%s", code, text)
	}

	// A specification that staging has not run is another one.
	r.write(t, "1.2", r.key, "")
	text, code = r.run(t, "prod", "apply", "--auto-approve")
	if code != 5 || !strings.Contains(text, "the newest it holds is of "+list[0].Fingerprint[:12]) {
		t.Fatalf("prod apply of a newer specification: exit %d\n%s", code, text)
	}
}

func TestAnAttestationBySomeoneTheRuleDoesNotPinDoesNotCount(t *testing.T) {
	stranger := "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	r := newPromoRoot(t, stranger, "")
	r.arm(t)
	r.write(t, "1.1", stranger, "")
	if text, code := r.run(t, "staging", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("staging apply: exit %d\n%s", code, text)
	}
	if text, code := r.run(t, "staging", "promote", "attest"); code != 0 {
		t.Fatalf("attest: exit %d\n%s", code, text)
	}
	text, code := r.run(t, "prod", "apply", "--auto-approve")
	if code != 5 || !strings.Contains(text, "which the policy does not pin") {
		t.Fatalf("an unpinned key: exit %d\n%s", code, text)
	}
}

func TestOnlyTheTagsThatVaryMayDiffer(t *testing.T) {
	r := newPromoRoot(t, "", "")
	r.arm(t)
	r.write(t, "1.1", r.key, "")
	if text, code := r.run(t, "staging", "apply", "--auto-approve", "-t", "image=2.0"); code != 0 {
		t.Fatalf("staging apply: exit %d\n%s", code, text)
	}
	if text, code := r.run(t, "staging", "promote", "attest", "-t", "image=2.0"); code != 0 {
		t.Fatalf("attest: exit %d\n%s", code, text)
	}
	// Staging ran image 2.0, so production may not run another.
	text, code := r.run(t, "prod", "apply", "--auto-approve", "-t", "image=3.0")
	if code != 5 || !strings.Contains(text, "the tag image has another value") {
		t.Fatalf("another tag: exit %d\n%s", code, text)
	}
	if text, code = r.run(t, "prod", "apply", "--auto-approve", "-t", "image=2.0"); code != 0 {
		t.Fatalf("the same tags: exit %d\n%s", code, text)
	}
}

func TestASoakTimeHoldsAnAttestationBack(t *testing.T) {
	r := newPromoRoot(t, "", `
			min_age: "48h"`)
	r.arm(t)
	r.write(t, "1.1", r.key, `
			min_age: "48h"`)
	if text, code := r.run(t, "staging", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("staging apply: exit %d\n%s", code, text)
	}
	if text, code := r.run(t, "staging", "promote", "attest"); code != 0 {
		t.Fatalf("attest: exit %d\n%s", code, text)
	}
	// A soak time holds an attestation back until it has stood.
	text, code := r.run(t, "prod", "apply", "--auto-approve")
	if code != 5 || !strings.Contains(text, "wants it to stand for 48h first") {
		t.Fatalf("min_age: exit %d\n%s", code, text)
	}
}
