package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ladderSpec declares dev → staging → prod. Each environment keeps its state in a directory of
// its own, named by $environment.$name, and the image tag is the release.
const ladderSpec = `package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

image: *"1.0" | string @tag(image)

$environments: pravo.#Environments & {
	dev: {}
	staging: $after: "dev"
	prod: $after:    %s
}

$settings: pravo.#Settings & {
	state: path: "state/\($environment.$name)"
}
$local: loc.#Provider

app: $local.#Resource.local_file & {
	filename: "out/app-\($environment.$name).txt"
	content:  "image \(image)\n"
}
`

// ladder is a root with the three environments, each state sealed to the people of one readers
// file.
type ladder struct {
	dir string
}

func newLadder(t *testing.T, people ...string) *ladder {
	t.Helper()
	requireLocalProvider(t)
	requireSealing(t)
	ids := isolateIdentities(t)
	var lines []string
	for _, p := range people {
		name, role, _ := strings.Cut(p, ":")
		_, line := person(t, ids, name)
		if role != "" {
			line += " role:" + role
		}
		lines = append(lines, line)
	}
	l := &ladder{dir: specDir(t, map[string]string{})}
	l.write(t, `"staging"`)
	if text, code := runPravo(t, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	writeReaders(t, filepath.Join(l.dir, ".pravo", "access"), lines...)
	for _, env := range []string{"dev", "staging", "prod"} {
		if text, code := runPravo(t, l.dir, "--identity", "ada", "protect", "init", "--encrypt", "--yes", "--allow-local", "-t", "env="+env); code != 0 {
			t.Fatalf("protect init %s: exit %d\n%s", env, code, text)
		}
	}
	return l
}

func (l *ladder) write(t *testing.T, prodAfter string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(l.dir, "main.cue"), []byte(fmt.Sprintf(ladderSpec, prodAfter)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// as runs pravo with the identity of who.
func (l *ladder) as(t *testing.T, who string, args ...string) (string, int) {
	t.Helper()
	return runPravo(t, l.dir, append([]string{"--identity", who}, args...)...)
}

func (l *ladder) must(t *testing.T, who string, args ...string) string {
	t.Helper()
	text, code := l.as(t, who, args...)
	if code != 0 {
		t.Fatalf("pravo %s: exit %d\n%s", strings.Join(args, " "), code, text)
	}
	return text
}

func TestALadderTakesOnlyWhatWasPromoted(t *testing.T) {
	l := newLadder(t, "ada")

	// prod follows staging, which runs nothing yet: the apply is refused with the way there.
	text, code := l.as(t, "ada", "apply", "-t", "env=prod", "--auto-approve")
	if code != 5 || !strings.Contains(text, "prod only takes what was promoted from staging, and nobody has promoted this release yet") ||
		!strings.Contains(text, "    pravo apply --env dev\n    pravo promote staging\n    pravo apply --env staging\n    pravo promote prod") {
		t.Fatalf("first prod apply: exit %d\n%s", code, text)
	}
	if text, code = l.as(t, "ada", "promote", "prod", "--sign-only"); code != 5 || !strings.Contains(text, "staging does not run this release: nothing has been applied there yet") {
		t.Fatalf("promote before staging ran it: exit %d\n%s", code, text)
	}
	if text, code = l.as(t, "ada", "promote", "dev"); code != 5 || !strings.Contains(text, "dev follows no environment") {
		t.Fatalf("promote into the first environment: exit %d\n%s", code, text)
	}
	l.must(t, "ada", "apply", "-t", "env=dev", "--auto-approve")
	text = l.must(t, "ada", "promote", "staging", "--auto-approve")
	if !strings.Contains(text, "Promoted to staging: dev runs this release, applied by ada") || !strings.Contains(text, "Apply complete!") {
		t.Fatalf("promote staging:\n%s", text)
	}
	text = l.must(t, "ada", "promote", "prod", "--sign-only", "--note", "smoke tests passed")
	if !strings.Contains(text, "Promoted to prod: staging runs this release") || !strings.Contains(text, "    pravo apply --env prod") {
		t.Fatalf("promote prod:\n%s", text)
	}
	// The promotion lies in the state that takes it.
	if es, err := os.ReadDir(filepath.Join(l.dir, "state", "prod", "promotions")); err != nil || len(es) != 1 {
		t.Fatalf("promotions of prod: %v %v", es, err)
	}
	if _, err := os.Stat(filepath.Join(l.dir, "state", "staging", "promotions", "x")); err == nil {
		t.Fatal("staging holds a promotion into prod")
	}
	if text = l.must(t, "ada", "plan", "-t", "env=prod"); !strings.Contains(text, "Promotion: staging ran this release, and ada promoted it") {
		t.Fatalf("prod plan:\n%s", text)
	}
	l.must(t, "ada", "apply", "-t", "env=prod", "--auto-approve")

	text = l.must(t, "ada", "environments", "--json")
	var ladder struct {
		Flow         string
		Environments []struct {
			Name    string
			Release string
			Runs    *struct{ Fingerprint, By string }
		}
		Next string
	}
	if err := json.Unmarshal([]byte(text), &ladder); err != nil || ladder.Flow != "dev → staging → prod" || len(ladder.Environments) != 3 {
		t.Fatalf("environments --json: %v\n%s", err, text)
	}
	for _, e := range ladder.Environments {
		if e.Release != "runs" || e.Runs == nil || e.Runs.By != "ada" {
			t.Fatalf("%s does not run the release:\n%s", e.Name, text)
		}
	}
	if !strings.HasPrefix(ladder.Next, "nothing") {
		t.Fatalf("next: %q", ladder.Next)
	}

	// The comment of a pull request names the environment and how the release stands there.
	text, code = l.as(t, "ada", "plan", "-t", "env=prod", "-t", "image=1.1", "--markdown")
	if code != 0 || !strings.Contains(text, "### pravo plan for prod:") || !strings.Contains(text, "**Promotion:** none yet.") ||
		!strings.Contains(text, "```sh\npravo apply --env dev -t image=1.1\n") || !strings.Contains(text, "pravo promote prod -t image=1.1\n```") {
		t.Fatalf("plan --markdown of prod: exit %d\n%s", code, text)
	}

	// Release 1.1 is another tag: prod refuses it until it went through staging.
	text, code = l.as(t, "ada", "apply", "-t", "env=prod", "-t", "image=1.1", "--auto-approve")
	if code != 5 || !strings.Contains(text, "nobody has promoted this release yet") || !strings.Contains(text, "pravo apply --env dev -t image=1.1") {
		t.Fatalf("prod apply of 1.1: exit %d\n%s", code, text)
	}
	l.must(t, "ada", "apply", "-t", "env=dev", "-t", "image=1.1", "--auto-approve")
	text, code = l.as(t, "ada", "promote", "prod", "-t", "image=1.1", "--sign-only")
	if code != 5 || !strings.Contains(text, "staging does not run this release: it runs this specification with other tags: -t image was not given then and is given now") {
		t.Fatalf("promote prod 1.1 before staging: exit %d\n%s", code, text)
	}
	text = l.must(t, "ada", "environments", "-t", "image=1.1")
	if !strings.Contains(text, "Next: pravo promote staging -t image=1.1") {
		t.Fatalf("environments for 1.1:\n%s", text)
	}

	// An admin waives the check for a hotfix, and the apply keeps the reason in its note.
	text = l.must(t, "ada", "promote", "prod", "-t", "image=1.1", "--waive", "hotfix for the outage", "--auto-approve")
	if !strings.Contains(text, "Waived: prod may take this release although staging was not checked") || !strings.Contains(text, "Apply complete!") {
		t.Fatalf("waive:\n%s", text)
	}
	notes, _ := os.ReadDir(filepath.Join(l.dir, "state", "prod", "runs"))
	last, err := os.ReadFile(filepath.Join(l.dir, "state", "prod", "runs", notes[len(notes)-1].Name()))
	if err != nil || !strings.Contains(string(last), `"kind": "waived"`) || !strings.Contains(string(last), "hotfix for the outage") {
		t.Fatalf("the note of the waived apply: %v\n%s", err, last)
	}

	// A withdrawal ends the promotions signed before it.
	l.must(t, "ada", "promote", "prod", "-t", "image=1.1", "--revoke", "1.1 corrupts uploads")
	text, code = l.as(t, "ada", "apply", "-t", "env=prod", "-t", "image=1.1", "--auto-approve")
	if code != 5 || !strings.Contains(text, "its promotion was withdrawn by ada") || !strings.Contains(text, "1.1 corrupts uploads") {
		t.Fatalf("apply after the withdrawal: exit %d\n%s", code, text)
	}
}

func TestOnlyAnAdminWaivesTheCheckOfALadder(t *testing.T) {
	l := newLadder(t, "ada:admin", "bob:writer")
	text, code := l.as(t, "bob", "promote", "prod", "--waive", "in a hurry", "--sign-only")
	if code != 5 || !strings.Contains(text, "your identity is a writer") || !strings.Contains(text, "an admin is needed to waive the check of staging for prod") {
		t.Fatalf("waiver by a writer: exit %d\n%s", code, text)
	}
	text, code = l.as(t, "bob", "promote", "prod", "--revoke", "no", "--sign-only")
	if code != 5 || !strings.Contains(text, "an admin is needed to withdraw a promotion into prod") {
		t.Fatalf("withdrawal by a writer: exit %d\n%s", code, text)
	}
	// A writer promotes.
	l.must(t, "bob", "apply", "-t", "env=dev", "--auto-approve")
	if text = l.must(t, "bob", "promote", "staging", "--sign-only"); !strings.Contains(text, "Promoted to staging") {
		t.Fatalf("promotion by a writer:\n%s", text)
	}
}

func TestTheRecordOfAnEnvironmentHoldsItsLadder(t *testing.T) {
	l := newLadder(t, "ada")
	l.must(t, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, "ada", "promote", "staging", "--auto-approve")
	l.must(t, "ada", "promote", "prod", "--auto-approve")

	// An edit that makes prod follow dev is not taken by the state until an admin records it.
	l.write(t, `"dev"`)
	text, code := l.as(t, "ada", "plan", "-t", "env=prod")
	if code != 5 || !strings.Contains(text, "the record of this state says that prod follows staging, and the specification says it follows dev") ||
		!strings.Contains(text, "pravo environments record --env prod") {
		t.Fatalf("plan after the edit: exit %d\n%s", code, text)
	}
	if text = l.must(t, "ada", "environments", "record", "-t", "env=prod"); !strings.Contains(text, "Recorded: this state serves prod, which follows dev") {
		t.Fatalf("record:\n%s", text)
	}
	if text = l.must(t, "ada", "plan", "-t", "env=prod"); !strings.Contains(text, "prod only takes what was promoted from dev") {
		t.Fatalf("plan after the record:\n%s", text)
	}
	if text = l.must(t, "ada", "environments", "record", "-t", "env=prod"); !strings.Contains(text, "The record already says") {
		t.Fatalf("record again:\n%s", text)
	}
}

func TestALadderWithoutASealWarns(t *testing.T) {
	requireLocalProvider(t)
	isolateIdentities(t)
	l := &ladder{dir: specDir(t, map[string]string{})}
	l.write(t, `"staging"`)
	if text, code := runPravo(t, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, l.dir, "apply", "-t", "env=staging", "--auto-approve")
	if code != 0 || !strings.Contains(text, "Environment: staging follows dev, and its state is not sealed, so it takes any release") {
		t.Fatalf("staging apply: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, l.dir, "promote", "prod")
	if code != 5 || !strings.Contains(text, "the state of prod is not sealed") && !strings.Contains(text, "prod has no state yet") {
		t.Fatalf("promote into an unsealed state: exit %d\n%s", code, text)
	}
	// A run for another environment than the state serves is refused, sealed or not.
	staging := filepath.Join(l.dir, "state", "staging")
	if err := os.Rename(staging, filepath.Join(l.dir, "state", "prod")); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, l.dir, "plan", "-t", "env=prod")
	if code != 5 || !strings.Contains(text, "this state serves the environment staging, and this run is for prod") {
		t.Fatalf("plan of a copied state: exit %d\n%s", code, text)
	}
	// So is every other command that writes the records, and the state is left as it was.
	for _, args := range [][]string{{"state", "rm", "app"}, {"state", "mv", "app", "app2"}, {"import", "app", "x"}} {
		text, code = runPravo(t, l.dir, append(args, "--env", "prod")...)
		if code != 5 || !strings.Contains(text, "this state serves the environment staging, and this run is for prod") {
			t.Errorf("%s in a copied state: exit %d\n%s", strings.Join(args, " "), code, text)
		}
	}
	if text, code = runPravo(t, l.dir, "state", "list", "--env", "prod"); code != 0 || strings.TrimSpace(text) != "app" {
		t.Errorf("the copied state after the refusals: exit %d\n%s", code, text)
	}
}

func TestValidateChecksEveryEnvironment(t *testing.T) {
	requireLocalProvider(t)
	l := &ladder{dir: specDir(t, map[string]string{})}
	l.write(t, `"staging"`)
	if text, code := runPravo(t, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, l.dir, "validate")
	if code != 0 || !strings.Contains(text, "valid in dev, staging and prod") {
		t.Fatalf("validate: exit %d\n%s", code, text)
	}
	extra := `package estate

$environments: [_]: replicas: *1 | int
$environments: prod: replicas: 5

limit: $environment.replicas & <=3
`
	if err := os.WriteFile(filepath.Join(l.dir, "limit.cue"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, l.dir, "validate")
	if code != 6 || !strings.Contains(text, "not valid in prod") || !strings.Contains(text, "✓ staging") {
		t.Fatalf("validate with prod broken: exit %d\n%s", code, text)
	}
	if text, code = runPravo(t, l.dir, "validate", "-t", "env=staging"); code != 0 {
		t.Fatalf("validate staging: exit %d\n%s", code, text)
	}

	// A row that misses a field the others have, or holds a value that is not concrete, is
	// found by validate, before a plan of that environment would wait for it.
	rows := `package estate

$environments: dev: zone: "a"
$environments: staging: zone: "b"
$environments: prod: zone: string
`
	if err := os.WriteFile(filepath.Join(l.dir, "limit.cue"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, l.dir, "validate")
	if code != 6 || !strings.Contains(text, "$environments.prod.zone has no value") {
		t.Fatalf("validate with a row that is not concrete: exit %d\n%s", code, text)
	}
	rows = `package estate

$environments: dev: zone: "a"
$environments: prod: zone: "c"
`
	if err := os.WriteFile(filepath.Join(l.dir, "limit.cue"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, l.dir, "validate")
	if code != 6 || !strings.Contains(text, "not valid in staging: $environments.staging has no zone, which dev and prod have") {
		t.Fatalf("validate with a row that misses a field: exit %d\n%s", code, text)
	}
}

// --env NAME is -t env=NAME: it selects the environment, agrees with -t env= naming the same one,
// and refuses another, an empty name, and a binding of secret exec given to it by mistake.
func TestEnvFlagIsTheEnvironmentTag(t *testing.T) {
	requireLocalProvider(t)
	l := &ladder{dir: specDir(t, map[string]string{})}
	l.write(t, `"staging"`)
	if text, code := runPravo(t, l.dir, "init", "--env", "dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	for _, args := range [][]string{{"--env", "prod"}, {"-env", "prod"}, {"--env=prod"}, {"--env", "prod", "-t", "env=prod"}, {"-t", "env=prod"}} {
		text, code := runPravo(t, l.dir, append([]string{"eval", "-e", "app.filename"}, args...)...)
		if code != 0 || strings.TrimSpace(text) != `"out/app-prod.txt"` {
			t.Errorf("eval %s: exit %d\n%s", strings.Join(args, " "), code, text)
		}
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"eval", "-e", "app.filename", "--env", "prod", "-t", "env=dev"}, "--env prod and -t env=dev name two environments"},
		{[]string{"eval", "-e", "app.filename", "--env", ""}, "--env takes the name of an environment"},
		{[]string{"eval", "-e", "app.filename", "--env", "qa"}, `"qa" is not an environment of this specification; the environments are dev, staging and prod (pass --env NAME)`},
		{[]string{"promote", "prod", "--env", "dev"}, "the environment is the argument of promote: pravo promote prod, without --env dev"},
		{[]string{"secret", "exec", "--env", "PW=db", "--", "true"}, "a secret goes into an environment variable with --var VAR=NAME"},
	} {
		if text, code := runPravo(t, l.dir, c.args...); code != 6 || !strings.Contains(text, c.want) {
			t.Errorf("%s: exit %d\n%s", strings.Join(c.args, " "), code, text)
		}
	}
}

func TestEnvironmentsDiffComparesTheDeclarations(t *testing.T) {
	requireLocalProvider(t)
	l := &ladder{dir: specDir(t, map[string]string{})}
	l.write(t, `"staging"`)
	extra := `package estate

if $environment.$name == "prod" {
	alarm: $local.#Resource.local_file & {
		filename: "out/alarm.txt"
		content:  "on"
	}
}
key: $local.#Resource.local_sensitive_file & {
	filename: "out/key.txt"
	content:  "pw-\($environment.$name)"
}
`
	if err := os.WriteFile(filepath.Join(l.dir, "extra.cue"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, l.dir, "environments", "diff", "staging", "prod")
	if code != 0 || !strings.Contains(text, `filename: "out/app-staging.txt" → "out/app-prod.txt"`) ||
		!strings.Contains(text, "+ alarm  only in prod") || !strings.Contains(text, "content: differs (sensitive)") || strings.Contains(text, "pw-") {
		t.Fatalf("diff: exit %d\n%s", code, text)
	}
}

// TestEachEnvironmentReadsItsOwnReadersFile gives staging and prod a readers file each, through
// $settings.access.dir: the note of staging is checked against the file of staging, where its
// signer is a writer, and the promotion against the file of prod.
func TestEachEnvironmentReadsItsOwnReadersFile(t *testing.T) {
	requireLocalProvider(t)
	requireSealing(t)
	ids := isolateIdentities(t)
	_, ada := person(t, ids, "ada")
	_, bob := person(t, ids, "bob")
	l := &ladder{dir: specDir(t, map[string]string{})}
	spec := strings.Replace(fmt.Sprintf(ladderSpec, `"staging"`), `state: path: "state/\($environment.$name)"`,
		`state: path: "state/\($environment.$name)"
	access: dir: ".pravo/access/\($environment.$name)"`, 1)
	if err := os.WriteFile(filepath.Join(l.dir, "main.cue"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	files := map[string]string{"dev": bob + " role:admin", "staging": bob + " role:admin", "prod": ada + " role:admin"}
	who := map[string]string{"dev": "bob", "staging": "bob", "prod": "ada"}
	for _, env := range []string{"dev", "staging", "prod"} {
		writeReaders(t, filepath.Join(l.dir, ".pravo", "access", env), files[env])
		l.must(t, who[env], "protect", "init", "--encrypt", "--yes", "--allow-local", "-t", "env="+env)
	}
	l.must(t, "bob", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, "bob", "promote", "staging", "--auto-approve")
	text := l.must(t, "ada", "promote", "prod", "--sign-only")
	if !strings.Contains(text, "staging runs this release, applied by bob") || strings.Contains(text, "read one readers file") {
		t.Fatalf("promote prod:\n%s", text)
	}
	if text = l.must(t, "ada", "plan", "-t", "env=prod"); !strings.Contains(text, "Promotion: staging ran this release, and ada promoted it") {
		t.Fatalf("plan prod:\n%s", text)
	}
	// bob is no writer of staging any more: his note does not count.
	writeReaders(t, filepath.Join(l.dir, ".pravo", "access", "staging"), bob+" role:reader")
	text, code := l.as(t, "ada", "apply", "-t", "env=prod", "--auto-approve")
	if code != 5 || !strings.Contains(text, "the note of staging was signed by bob, who is a reader there, and a writer is needed") {
		t.Fatalf("apply with the note of a reader: exit %d\n%s", code, text)
	}
}
