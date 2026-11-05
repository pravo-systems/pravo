package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	dir    string
	perEnv bool
}

func newLadder(t *testing.T, rt *runContext, people ...string) *ladder {
	t.Helper()
	requireLocalProvider(t)
	requireSealing(t)
	ids := isolateIdentities(t, rt)
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
	if text, code := runPravo(t, rt, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	writeReaders(t, filepath.Join(l.dir, ".pravo", "access"), lines...)
	for _, env := range []string{"dev", "staging", "prod"} {
		if text, code := runPravo(t, rt, l.dir, "--identity", "ada", "protect", "init", "--encrypt", "--yes", "--allow-local", "-t", "env="+env); code != 0 {
			t.Fatalf("protect init %s: exit %d\n%s", env, code, text)
		}
	}
	return l
}

// newLadderPerEnv is a ladder with a readers file for each environment, through
// $settings.access.dir, as a team with roles in every state has.
func newLadderPerEnv(t *testing.T, rt *runContext, people ...string) *ladder {
	t.Helper()
	requireLocalProvider(t)
	requireSealing(t)
	ids := isolateIdentities(t, rt)
	var lines []string
	for _, p := range people {
		name, role, _ := strings.Cut(p, ":")
		_, line := person(t, ids, name)
		lines = append(lines, line+" role:"+role)
	}
	l := &ladder{dir: specDir(t, map[string]string{}), perEnv: true}
	spec := strings.Replace(fmt.Sprintf(ladderSpec, `"staging"`), `state: path: "state/\($environment.$name)"`,
		`state: path: "state/\($environment.$name)"
	access: dir: ".pravo/access/\($environment.$name)"`, 1)
	if err := os.WriteFile(filepath.Join(l.dir, "main.cue"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, rt, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	for _, env := range []string{"dev", "staging", "prod"} {
		writeReaders(t, filepath.Join(l.dir, ".pravo", "access", env), lines...)
		l.must(t, rt, "ada", "protect", "init", "--encrypt", "--yes", "--allow-local", "-t", "env="+env)
		l.must(t, rt, "ada", "protect", "roles", "--yes", "-t", "env="+env)
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
func (l *ladder) as(t *testing.T, rt *runContext, who string, args ...string) (string, int) {
	t.Helper()
	return runPravo(t, rt, l.dir, append([]string{"--identity", who}, args...)...)
}

func (l *ladder) must(t *testing.T, rt *runContext, who string, args ...string) string {
	t.Helper()
	text, code := l.as(t, rt, who, args...)
	if code != 0 {
		t.Fatalf("pravo %s: exit %d\n%s", strings.Join(args, " "), code, text)
	}
	return text
}

func TestALadderTakesOnlyWhatWasPromoted(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada")

	// prod follows staging, which runs nothing yet: the apply is refused with the way there.
	text, code := l.as(t, rt, "ada", "apply", "-t", "env=prod", "--auto-approve")
	if code != 5 || !strings.Contains(text, "prod only takes what was promoted from staging, and nobody has promoted this release yet") ||
		!strings.Contains(text, "    pravo apply --env dev\n    pravo promote staging\n    pravo promote prod") {
		t.Fatalf("first prod apply: exit %d\n%s", code, text)
	}
	if text, code = l.as(t, rt, "ada", "promote", "prod", "--sign-only"); code != 5 || !strings.Contains(text, "staging does not run this release: nothing has been applied there yet") {
		t.Fatalf("promote before staging ran it: exit %d\n%s", code, text)
	}
	if text, code = l.as(t, rt, "ada", "promote", "dev"); code != 5 || !strings.Contains(text, "dev follows no environment") {
		t.Fatalf("promote into the first environment: exit %d\n%s", code, text)
	}
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	text = l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	if !strings.Contains(text, "Promoted to staging: dev runs this release, applied by ada") || !strings.Contains(text, "Apply complete!") {
		t.Fatalf("promote staging:\n%s", text)
	}
	text = l.must(t, rt, "ada", "promote", "prod", "--sign-only", "--note", "smoke tests passed")
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
	if text = l.must(t, rt, "ada", "plan", "-t", "env=prod"); !strings.Contains(text, "Promotion: staging ran this release, and ada promoted it") {
		t.Fatalf("prod plan:\n%s", text)
	}
	l.must(t, rt, "ada", "apply", "-t", "env=prod", "--auto-approve")

	// Promoting what prod already runs signs nothing and applies nothing.
	text = l.must(t, rt, "ada", "promote", "prod", "--auto-approve")
	if !strings.Contains(text, "prod already runs this release") || !strings.Contains(text, "applied by ada") || !strings.Contains(text, "promoted by ada") ||
		!strings.Contains(text, "Nothing to promote.") || strings.Contains(text, "Apply complete!") {
		t.Fatalf("promote prod again:\n%s", text)
	}
	if es, _ := os.ReadDir(filepath.Join(l.dir, "state", "prod", "promotions")); len(es) != 1 {
		t.Fatalf("promote again signed another promotion: %d", len(es))
	}

	text = l.must(t, rt, "ada", "environments", "--json")
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
	text, code = l.as(t, rt, "ada", "plan", "-t", "env=prod", "-t", "image=1.1", "--markdown")
	if code != 0 || !strings.Contains(text, "### pravo plan for prod:") || !strings.Contains(text, "**Promotion:** none yet.") ||
		!strings.Contains(text, "```sh\npravo apply --env dev -t image=1.1\n") || !strings.Contains(text, "pravo promote prod -t image=1.1\n```") {
		t.Fatalf("plan --markdown of prod: exit %d\n%s", code, text)
	}

	// Release 1.1 is another tag: prod refuses it until it went through staging.
	text, code = l.as(t, rt, "ada", "apply", "-t", "env=prod", "-t", "image=1.1", "--auto-approve")
	if code != 5 || !strings.Contains(text, "nobody has promoted this release yet") || !strings.Contains(text, "pravo apply --env dev -t image=1.1") {
		t.Fatalf("prod apply of 1.1: exit %d\n%s", code, text)
	}
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "-t", "image=1.1", "--auto-approve")
	text, code = l.as(t, rt, "ada", "promote", "prod", "-t", "image=1.1", "--sign-only")
	if code != 5 || !strings.Contains(text, "staging does not run this release: it runs this specification with other tags: -t image was not given then and is given now") {
		t.Fatalf("promote prod 1.1 before staging: exit %d\n%s", code, text)
	}
	text = l.must(t, rt, "ada", "environments", "-t", "image=1.1")
	if !strings.Contains(text, "Next: pravo promote staging -t image=1.1") {
		t.Fatalf("environments for 1.1:\n%s", text)
	}

	// An admin waives the check for a hotfix, and the apply keeps the reason in its note.
	text = l.must(t, rt, "ada", "promote", "prod", "-t", "image=1.1", "--waive", "hotfix for the outage", "--auto-approve")
	if !strings.Contains(text, "Waived: prod may take this release although staging was not checked") || !strings.Contains(text, "Apply complete!") {
		t.Fatalf("waive:\n%s", text)
	}
	notes, _ := os.ReadDir(filepath.Join(l.dir, "state", "prod", "runs"))
	last, err := os.ReadFile(filepath.Join(l.dir, "state", "prod", "runs", notes[len(notes)-1].Name()))
	if err != nil || !strings.Contains(string(last), `"kind": "waived"`) || !strings.Contains(string(last), "hotfix for the outage") {
		t.Fatalf("the note of the waived apply: %v\n%s", err, last)
	}

	// A withdrawal ends the promotions signed before it. prod runs 1.1 already, so its apply
	// changes nothing and goes through, but it does not note that prod runs the release.
	l.must(t, rt, "ada", "promote", "prod", "-t", "image=1.1", "--revoke", "1.1 corrupts uploads")
	text, code = l.as(t, rt, "ada", "promote", "prod", "-t", "image=1.1", "--sign-only")
	if code != 5 || !strings.Contains(text, "staging does not run this release") {
		t.Fatalf("promote after the withdrawal: exit %d\n%s", code, text)
	}
	text = l.must(t, rt, "ada", "apply", "-t", "env=prod", "-t", "image=1.1", "--auto-approve")
	if !strings.Contains(text, "No changes") || strings.Contains(text, "Noted") {
		t.Fatalf("apply after the withdrawal:\n%s", text)
	}
	if again, _ := os.ReadDir(filepath.Join(l.dir, "state", "prod", "runs")); len(again) != len(notes) {
		t.Fatalf("the apply after the withdrawal noted a run: %d notes, was %d", len(again), len(notes))
	}
	// Once prod has something to change, the withdrawal holds it.
	if err := os.Remove(filepath.Join(l.dir, "out", "app-prod.txt")); err != nil {
		t.Fatal(err)
	}
	text, code = l.as(t, rt, "ada", "apply", "-t", "env=prod", "-t", "image=1.1", "--refresh=all", "--auto-approve")
	if code != 5 || !strings.Contains(text, "its promotion was withdrawn by ada") || !strings.Contains(text, "1.1 corrupts uploads") {
		t.Fatalf("apply after the withdrawal: exit %d\n%s", code, text)
	}
}

func TestOnlyAnAdminWaivesTheCheckOfALadder(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada:admin", "bob:writer")
	text, code := l.as(t, rt, "bob", "promote", "prod", "--waive", "in a hurry", "--sign-only")
	if code != 5 || !strings.Contains(text, "your identity is a writer") || !strings.Contains(text, "an admin is needed to waive the check of staging for prod") {
		t.Fatalf("waiver by a writer: exit %d\n%s", code, text)
	}
	text, code = l.as(t, rt, "bob", "promote", "prod", "--revoke", "no", "--sign-only")
	if code != 5 || !strings.Contains(text, "an admin is needed to withdraw a promotion into prod") {
		t.Fatalf("withdrawal by a writer: exit %d\n%s", code, text)
	}
	// A writer promotes.
	l.must(t, rt, "bob", "apply", "-t", "env=dev", "--auto-approve")
	if text = l.must(t, rt, "bob", "promote", "staging", "--sign-only"); !strings.Contains(text, "Promoted to staging") {
		t.Fatalf("promotion by a writer:\n%s", text)
	}
}

func TestTheRecordOfAnEnvironmentHoldsItsLadder(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	l.must(t, rt, "ada", "promote", "prod", "--auto-approve")

	// An edit that makes prod follow dev is not taken by the state until an admin records it.
	l.write(t, `"dev"`)
	text, code := l.as(t, rt, "ada", "plan", "-t", "env=prod")
	if code != 5 || !strings.Contains(text, "the record of this state says that prod follows staging, and the specification says it follows dev") ||
		!strings.Contains(text, "pravo environments record --env prod") {
		t.Fatalf("plan after the edit: exit %d\n%s", code, text)
	}
	if text = l.must(t, rt, "ada", "environments", "record", "-t", "env=prod"); !strings.Contains(text, "Recorded: this state serves prod, which follows dev") {
		t.Fatalf("record:\n%s", text)
	}
	// A plan that changes nothing needs no promotion, so the gate shows on a plan of a new image.
	if text = l.must(t, rt, "ada", "plan", "-t", "env=prod", "-t", "image=1.2"); !strings.Contains(text, "prod only takes what was promoted from dev") {
		t.Fatalf("plan after the record:\n%s", text)
	}
	if text = l.must(t, rt, "ada", "environments", "record", "-t", "env=prod"); !strings.Contains(text, "The record already says") {
		t.Fatalf("record again:\n%s", text)
	}
}

func TestALadderWithoutASealWarns(t *testing.T) {
	rt := &runContext{}

	requireLocalProvider(t)
	isolateIdentities(t, rt)
	l := &ladder{dir: specDir(t, map[string]string{})}
	l.write(t, `"staging"`)
	if text, code := runPravo(t, rt, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, rt, l.dir, "apply", "-t", "env=staging", "--auto-approve")
	if code != 0 || !strings.Contains(text, "Environment: staging follows dev, and its state is not sealed, so it takes any release") {
		t.Fatalf("staging apply: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, rt, l.dir, "promote", "prod")
	if code != 5 || !strings.Contains(text, "the state of prod is not sealed") && !strings.Contains(text, "prod has no state yet") {
		t.Fatalf("promote into an unsealed state: exit %d\n%s", code, text)
	}
	// A run for another environment than the state serves is refused, sealed or not.
	staging := filepath.Join(l.dir, "state", "staging")
	if err := os.Rename(staging, filepath.Join(l.dir, "state", "prod")); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, rt, l.dir, "plan", "-t", "env=prod")
	if code != 5 || !strings.Contains(text, "this state serves the environment staging, and this run is for prod") {
		t.Fatalf("plan of a copied state: exit %d\n%s", code, text)
	}
	// So is every other command that writes the records, and the state is left as it was.
	for _, args := range [][]string{{"state", "rm", "app"}, {"state", "mv", "app", "app2"}, {"import", "app", "x"}} {
		text, code = runPravo(t, rt, l.dir, append(args, "--env", "prod")...)
		if code != 5 || !strings.Contains(text, "this state serves the environment staging, and this run is for prod") {
			t.Errorf("%s in a copied state: exit %d\n%s", strings.Join(args, " "), code, text)
		}
	}
	if text, code = runPravo(t, rt, l.dir, "state", "list", "--env", "prod"); code != 0 || strings.TrimSpace(text) != "app" {
		t.Errorf("the copied state after the refusals: exit %d\n%s", code, text)
	}
}

func TestValidateChecksEveryEnvironment(t *testing.T) {
	rt := &runContext{}

	requireLocalProvider(t)
	l := &ladder{dir: specDir(t, map[string]string{})}
	l.write(t, `"staging"`)
	if text, code := runPravo(t, rt, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, rt, l.dir, "validate")
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
	text, code = runPravo(t, rt, l.dir, "validate")
	if code != 6 || !strings.Contains(text, "not valid in prod") || !strings.Contains(text, "✓ staging") {
		t.Fatalf("validate with prod broken: exit %d\n%s", code, text)
	}
	if text, code = runPravo(t, rt, l.dir, "validate", "-t", "env=staging"); code != 0 {
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
	text, code = runPravo(t, rt, l.dir, "validate")
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
	text, code = runPravo(t, rt, l.dir, "validate")
	if code != 6 || !strings.Contains(text, "not valid in staging: $environments.staging has no zone, which dev and prod have") {
		t.Fatalf("validate with a row that misses a field: exit %d\n%s", code, text)
	}
}

// --env NAME is -t env=NAME: it selects the environment, agrees with -t env= naming the same one,
// and refuses another, an empty name, and a binding of secret exec given to it by mistake.
func TestEnvFlagIsTheEnvironmentTag(t *testing.T) {
	rt := &runContext{}

	requireLocalProvider(t)
	l := &ladder{dir: specDir(t, map[string]string{})}
	l.write(t, `"staging"`)
	if text, code := runPravo(t, rt, l.dir, "init", "--env", "dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	for _, args := range [][]string{{"--env", "prod"}, {"-env", "prod"}, {"--env=prod"}, {"--env", "prod", "-t", "env=prod"}, {"-t", "env=prod"}} {
		text, code := runPravo(t, rt, l.dir, append([]string{"eval", "-e", "app.filename"}, args...)...)
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
		if text, code := runPravo(t, rt, l.dir, c.args...); code != 6 || !strings.Contains(text, c.want) {
			t.Errorf("%s: exit %d\n%s", strings.Join(c.args, " "), code, text)
		}
	}
}

func TestEnvironmentsDiffComparesTheDeclarations(t *testing.T) {
	rt := &runContext{}

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
	if text, code := runPravo(t, rt, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, rt, l.dir, "environments", "diff", "staging", "prod")
	if code != 0 || !strings.Contains(text, `filename: "out/app-staging.txt" → "out/app-prod.txt"`) ||
		!strings.Contains(text, "+ alarm  only in prod") || !strings.Contains(text, "content: differs (sensitive)") || strings.Contains(text, "pw-") {
		t.Fatalf("diff: exit %d\n%s", code, text)
	}
}

// TestEachEnvironmentReadsItsOwnReadersFile gives staging and prod a readers file each, through
// $settings.access.dir: the note of staging is checked against the file of staging, where its
// signer is a writer, and the promotion against the file of prod.
func TestEachEnvironmentReadsItsOwnReadersFile(t *testing.T) {
	rt := &runContext{}

	requireLocalProvider(t)
	requireSealing(t)
	ids := isolateIdentities(t, rt)
	_, ada := person(t, ids, "ada")
	_, bob := person(t, ids, "bob")
	l := &ladder{dir: specDir(t, map[string]string{})}
	spec := strings.Replace(fmt.Sprintf(ladderSpec, `"staging"`), `state: path: "state/\($environment.$name)"`,
		`state: path: "state/\($environment.$name)"
	access: dir: ".pravo/access/\($environment.$name)"`, 1)
	if err := os.WriteFile(filepath.Join(l.dir, "main.cue"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, rt, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	files := map[string]string{"dev": bob + " role:admin", "staging": bob + " role:admin", "prod": ada + " role:admin"}
	who := map[string]string{"dev": "bob", "staging": "bob", "prod": "ada"}
	for _, env := range []string{"dev", "staging", "prod"} {
		writeReaders(t, filepath.Join(l.dir, ".pravo", "access", env), files[env])
		l.must(t, rt, who[env], "protect", "init", "--encrypt", "--yes", "--allow-local", "-t", "env="+env)
	}
	l.must(t, rt, "bob", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "bob", "promote", "staging", "--auto-approve")
	text := l.must(t, rt, "ada", "promote", "prod", "--sign-only")
	if !strings.Contains(text, "staging runs this release, applied by bob") || strings.Contains(text, "read one readers file") {
		t.Fatalf("promote prod:\n%s", text)
	}
	if text = l.must(t, rt, "ada", "plan", "-t", "env=prod"); !strings.Contains(text, "Promotion: staging ran this release, and ada promoted it") {
		t.Fatalf("plan prod:\n%s", text)
	}
	// bob is no writer of staging any more: his note does not count.
	writeReaders(t, filepath.Join(l.dir, ".pravo", "access", "staging"), bob+" role:reader")
	text, code := l.as(t, rt, "ada", "apply", "-t", "env=prod", "--auto-approve")
	if code != 5 || !strings.Contains(text, "the note of staging was signed by bob, who is a reader there, and a writer is needed") {
		t.Fatalf("apply with the note of a reader: exit %d\n%s", code, text)
	}
}

func TestPromoteStoresThePlanOfTheApply(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	text := l.must(t, rt, "ada", "promote", "prod", "--store", "--ttl", "36h")
	m := regexp.MustCompile(`run:\n  (pravo apply .*plan-[0-9a-f]+.*)\n`).FindStringSubmatch(text)
	if !strings.Contains(text, "Promoted to prod: staging runs this release") || m == nil || strings.Contains(text, "Apply complete!") {
		t.Fatalf("promote --store:\n%s", text)
	}
	args := strings.Fields(m[1])[1:]
	if text = l.must(t, rt, "ada", append(args, "--auto-approve")...); !strings.Contains(text, "Apply complete!") {
		t.Fatalf("apply the stored plan (%v):\n%s", args, text)
	}
	if text, code := l.as(t, rt, "ada", "promote", "prod", "--store", "--sign-only"); code != 6 || !strings.Contains(text, "--store goes with neither") {
		t.Fatalf("--store with --sign-only: exit %d\n%s", code, text)
	}
}

// A release that changes nothing in prod needs no promotion there: its plan does not ask for
// one, and its apply goes through without noting that prod runs the release.
func TestAReleaseThatChangesNothingNeedsNoPromotion(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	l.must(t, rt, "ada", "promote", "prod", "--auto-approve")

	// A comment makes another release, and changes nothing that prod runs.
	main := filepath.Join(l.dir, "main.cue")
	src, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, append(src, "\n// The image is the release.\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	text := l.must(t, rt, "ada", "plan", "-t", "env=prod")
	if !strings.Contains(text, "No changes") || strings.Contains(text, "Promotion") {
		t.Fatalf("plan of prod:\n%s", text)
	}
	if text = l.must(t, rt, "ada", "plan", "-t", "env=prod", "--markdown"); strings.Contains(text, "Promotion") {
		t.Fatalf("plan --markdown of prod:\n%s", text)
	}
	runs := func() int {
		es, _ := os.ReadDir(filepath.Join(l.dir, "state", "prod", "runs"))
		return len(es)
	}
	before := runs()
	if text = l.must(t, rt, "ada", "apply", "-t", "env=prod", "--auto-approve"); strings.Contains(text, "Noted") {
		t.Fatalf("apply of prod:\n%s", text)
	}
	if n := runs(); n != before {
		t.Fatalf("the apply noted that prod runs a release nobody promoted: %d notes, was %d", n, before)
	}
	if text = l.must(t, rt, "ada", "environments"); !strings.Contains(text, "Next: pravo apply --env dev") {
		t.Fatalf("environments:\n%s", text)
	}

	// A release that changes prod still needs its promotion.
	text, code := l.as(t, rt, "ada", "apply", "-t", "env=prod", "-t", "image=1.2", "--auto-approve")
	if code != 5 || !strings.Contains(text, "nobody has promoted this release yet") {
		t.Fatalf("prod apply of 1.2: exit %d\n%s", code, text)
	}
}

// pinnedSpec declares dev and prod, each with a readers file of its own, named by ACCESS for
// prod so that a test can point prod at another directory.
const pinnedSpec = `package estate

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$environments: pravo.#Environments & {
	dev: {}
	prod: $after: "dev"
}

$settings: pravo.#Settings & {
	state: path: "state/\($environment.$name)"
	access: dir: [if $environment.$name == "prod" {%q}, ".pravo/access/\($environment.$name)"][0]
}
$local: loc.#Provider

app: $local.#Resource.local_file & {
	filename: "out/app-\($environment.$name).txt"
	content:  "app\n"
}
`

// The record of an environment pins the roles of its state, so that an edit of the
// specification that points the environment at another readers file, which says that a
// writer is an admin, does not make one: not where the environment runs, and not where a
// promotion checks the notes of the environment before.
func TestTheRecordPinsTheRolesOfItsEnvironment(t *testing.T) {
	rt := &runContext{}

	requireLocalProvider(t)
	requireSealing(t)
	ids := isolateIdentities(t, rt)
	_, ada := person(t, ids, "ada")
	_, bob := person(t, ids, "bob")
	l := &ladder{dir: specDir(t, map[string]string{})}
	write := func(prodAccess string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(l.dir, "main.cue"), []byte(fmt.Sprintf(pinnedSpec, prodAccess)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".pravo/access/prod")
	if text, code := runPravo(t, rt, l.dir, "init", "-t", "env=dev"); code != 0 {
		t.Skipf("init exited %d:\n%s", code, text)
	}
	for _, env := range []string{"dev", "prod"} {
		writeReaders(t, filepath.Join(l.dir, ".pravo", "access", env), ada+" role:admin", bob+" role:writer")
		l.must(t, rt, "ada", "protect", "init", "--encrypt", "--yes", "--allow-local", "-t", "env="+env)
		l.must(t, rt, "ada", "protect", "roles", "--yes", "-t", "env="+env)
	}
	l.must(t, rt, "bob", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "prod", "--auto-approve")
	recs, _ := os.ReadDir(filepath.Join(l.dir, "state", "prod", "environment"))
	if len(recs) == 0 {
		t.Fatal("prod has no record")
	}
	last, err := os.ReadFile(filepath.Join(l.dir, "state", "prod", "environment", recs[len(recs)-1].Name()))
	if err != nil || !strings.Contains(string(last), `"roles_pin"`) {
		t.Fatalf("the record of prod pins no roles: %v\n%s", err, last)
	}

	// The queries judge the state by the same readers and roles pin as plan does: an admin's
	// drift and why are not refused because they build their engine another way.
	for _, args := range [][]string{{"drift"}, {"drift", "--plan", "--store"}, {"why", "app"}} {
		args = append(args, "-t", "env=prod")
		if text, code := l.as(t, rt, "ada", args...); code != 0 {
			t.Fatalf("pravo %s with pinned roles: exit %d\n%s", strings.Join(args, " "), code, text)
		}
	}

	// bob points prod at a readers file of his own, which says he is an admin and pins nothing.
	writeReaders(t, filepath.Join(l.dir, "evil"), ada+" role:writer", bob+" role:admin")
	write("evil")
	text, code := l.as(t, rt, "bob", "plan", "-t", "env=prod")
	if code != 5 || !strings.Contains(text, "the record of this state pins the roles of prod, and evil/readers.txt does not pin those roles") ||
		!strings.Contains(text, "pravo environments record --env prod") {
		t.Fatalf("plan of prod with another readers file: exit %d\n%s", code, text)
	}
	text, code = l.as(t, rt, "bob", "environments", "record", "-t", "env=prod")
	if code != 5 || !strings.Contains(text, "your identity is a writer") {
		t.Fatalf("record by bob: exit %d\n%s", code, text)
	}

	// He points dev at it instead: prod checks the notes of dev against the roles dev holds.
	write(".pravo/access/prod")
	if err := os.WriteFile(filepath.Join(l.dir, "main.cue"), []byte(strings.Replace(fmt.Sprintf(pinnedSpec, ".pravo/access/prod"), `".pravo/access/\($environment.$name)"][0]`, `"evil"][0]`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	text, code = l.as(t, rt, "ada", "promote", "prod", "--sign-only")
	if code == 0 || !strings.Contains(text, "the record of this state pins the roles of dev") {
		t.Fatalf("promote with dev pointed at another readers file: exit %d\n%s", code, text)
	}

	// An admin of the pinned roles may record that prod takes another readers file.
	write("evil")
	text = l.must(t, rt, "ada", "environments", "record", "-t", "env=prod")
	if !strings.Contains(text, "Recorded") {
		t.Fatalf("record by ada:\n%s", text)
	}
}

// A note signed by a key that left the readers file does not count, and the refusal names the
// key and the steps that put a current writer's note in its place.
func TestPromoteAfterAnOffboardingNamesTheDepartedKey(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada:admin", "dan:writer")
	l.must(t, rt, "dan", "apply", "-t", "env=dev", "--auto-approve")
	readers := filepath.Join(l.dir, ".pravo", "access", "readers.txt")
	data, err := os.ReadFile(readers)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.HasPrefix(line, "dan ") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(readers, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"dev", "staging"} {
		l.must(t, rt, "ada", "protect", "rewrap", "--yes", "-t", "env="+env)
	}
	text, code := l.as(t, rt, "ada", "promote", "staging", "--sign-only")
	if code != 5 || !strings.Contains(text, "signed by dan, whose key is not in the readers file of dev") ||
		!strings.Contains(text, "    pravo apply --env dev\n    pravo promote staging") {
		t.Fatalf("promote after the offboarding: exit %d\n%s", code, text)
	}
	if !strings.Contains(text, "pravo protect roles --vouch-notes --env dev") {
		t.Fatalf("the refusal does not mention the flag:\n%s", text)
	}
}

// offboard removes who from the readers file of every environment and rewraps the states.
func (l *ladder) offboard(t *testing.T, rt *runContext, who string) {
	t.Helper()
	for _, env := range []string{"dev", "staging", "prod"} {
		readers := filepath.Join(l.dir, ".pravo", "access", env, "readers.txt")
		data, err := os.ReadFile(readers)
		if err != nil {
			t.Fatal(err)
		}
		var kept []string
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if !strings.HasPrefix(line, who+" ") {
				kept = append(kept, line)
			}
		}
		if err := os.WriteFile(readers, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		l.must(t, rt, "ada", "protect", "rewrap", "--yes", "-t", "env="+env)
	}
}

// An admin carries over what a key that left signed, with an explicit flag: the next promotion
// works without a new apply in the environments before. Without the flag the roles say how many
// objects stop counting, and nothing is vouched for.
func TestRolesVouchForTheNotesAndPromotionsOfAKeyThatLeft(t *testing.T) {
	rt := &runContext{}

	l := newLadderPerEnv(t, rt, "ada:admin", "dan:writer")
	l.must(t, rt, "dan", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "dan", "promote", "staging", "--auto-approve")
	l.offboard(t, rt, "dan")

	// Not vouched: roles count what stops counting, the note of dev does not count.
	text := l.must(t, rt, "ada", "protect", "roles", "--yes", "-t", "env=dev")
	if !strings.Contains(text, "will stop counting") || !strings.Contains(text, "--vouch-notes") || strings.Contains(text, "run note or promotion)") {
		t.Fatalf("protect roles without the flag:\n%s", text)
	}
	text, code := l.as(t, rt, "ada", "promote", "staging", "--sign-only")
	if code != 5 || !strings.Contains(text, "signed by dan, whose key is not in the readers file of dev") {
		t.Fatalf("promote without the vouch: exit %d\n%s", code, text)
	}

	// Vouched in dev: the note of dev counts again, and promoting staging needs no new apply.
	text = l.must(t, rt, "ada", "protect", "roles", "--yes", "--vouch-notes", "-t", "env=dev")
	if !strings.Contains(text, "vouched for 1 run note or promotion (what the environments after") {
		t.Fatalf("protect roles with the flag:\n%s", text)
	}
	l.must(t, rt, "ada", "promote", "staging", "--sign-only")
	// Staging holds dan's promotion and dan's note: vouch there as well.
	l.must(t, rt, "ada", "protect", "roles", "--yes", "--vouch-notes", "-t", "env=staging")
	l.must(t, rt, "ada", "promote", "prod", "--sign-only")
	text = l.must(t, rt, "ada", "environments")
	if strings.Contains(text, "whose key is not in the readers file") {
		t.Fatalf("environments after the vouch:\n%s", text)
	}
	// The vouch covers those objects only: a new note by dan is not covered, and dan is refused.
	if _, code := l.as(t, rt, "dan", "apply", "-t", "env=dev", "--auto-approve"); code == 0 {
		t.Fatal("dan, who left, applied")
	}
}

// After a destroy, "pravo environments" shows the environment as destroyed, not as running the
// release, and the next step is the apply.
func TestEnvironmentsShowsADestroyedEnvironment(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	l.must(t, rt, "ada", "destroy", "-t", "env=staging", "--auto-approve")
	text := l.must(t, rt, "ada", "environments")
	if !strings.Contains(text, "destroyed after it ran this release") {
		t.Fatalf("environments after the destroy:\n%s", text)
	}
	if !strings.Contains(text, "Next: pravo apply --env staging") {
		t.Fatalf("next step after the destroy:\n%s", text)
	}
	text, code := l.as(t, rt, "ada", "promote", "prod", "--sign-only")
	if code != 5 || !strings.Contains(text, "staging was destroyed after it ran this release") {
		t.Fatalf("promote from a destroyed environment: exit %d\n%s", code, text)
	}
}

// A withdrawal blocks the release: a writer's promotion is refused, naming who withdrew it, when
// and why; an admin's promotion lifts it; another release is not affected.
func TestAWithdrawnReleaseIsPromotedAgainByAnAdminOnly(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada:admin", "bob:writer")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	l.must(t, rt, "ada", "promote", "prod", "--revoke", "corrupts uploads")
	text, code := l.as(t, rt, "bob", "promote", "prod", "--sign-only")
	if code != 5 || !strings.Contains(text, "ada withdrew this release from prod") || !strings.Contains(text, "corrupts uploads") || !strings.Contains(text, "until an admin of prod promotes it again") {
		t.Fatalf("writer's promote after the withdrawal: exit %d\n%s", code, text)
	}
	// Nothing was signed, so the writer's apply is refused too.
	text, code = l.as(t, rt, "bob", "apply", "-t", "env=prod", "--auto-approve")
	if code != 5 || !strings.Contains(text, "withdrawn by ada") {
		t.Fatalf("writer's apply after the withdrawal: exit %d\n%s", code, text)
	}
	// An admin lifts the withdrawal, and says so.
	text = l.must(t, rt, "ada", "promote", "prod", "--sign-only")
	if !strings.Contains(text, "Promoted to prod") || !strings.Contains(text, "lifts that") {
		t.Fatalf("admin's promote after the withdrawal:\n%s", text)
	}
	// Another release is not affected.
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "-t", "image=1.1", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "-t", "image=1.1", "--auto-approve")
	if text = l.must(t, rt, "bob", "promote", "prod", "-t", "image=1.1", "--sign-only"); !strings.Contains(text, "Promoted to prod") {
		t.Fatalf("another release:\n%s", text)
	}
	l.must(t, rt, "bob", "apply", "-t", "env=prod", "--auto-approve")
}

// A secret set in a destroyed environment is a transaction after the destroy, and it does not
// make the environment promotable again: only a new apply does.
func TestADestroyedEnvironmentStaysDestroyedAfterASecretIsSet(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	l.must(t, rt, "ada", "destroy", "-t", "env=staging", "--auto-approve")
	stdinIs(t, "x")
	l.must(t, rt, "ada", "secret", "set", "db_password", "-t", "env=staging")
	text := l.must(t, rt, "ada", "environments")
	if !strings.Contains(text, "destroyed after it ran this release") {
		t.Fatalf("environments after the destroy and a secret:\n%s", text)
	}
	text, code := l.as(t, rt, "ada", "promote", "prod", "--sign-only")
	if code != 5 || !strings.Contains(text, "staging was destroyed after it ran this release") {
		t.Fatalf("promote from a destroyed environment after a secret: exit %d\n%s", code, text)
	}
	// A new apply makes it promotable again.
	l.must(t, rt, "ada", "apply", "-t", "env=staging", "--auto-approve")
	if text, code := l.as(t, rt, "ada", "promote", "prod", "--sign-only"); code != 0 {
		t.Fatalf("promote after the apply: exit %d\n%s", code, text)
	}
}

// A state mv in a state with roles writes the record bound to its transaction, so that
// protect status counts it as bound and not as from before the roles.
func TestStateMvBindsTheRecordItWrites(t *testing.T) {
	rt := &runContext{}

	l := newLadder(t, rt, "ada:admin")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "state", "mv", "app", "app2", "-t", "env=dev")
	text := l.must(t, rt, "ada", "protect", "status", "-t", "env=dev")
	if strings.Contains(text, "before roles") || !strings.Contains(text, "bound to a transaction of a writer") {
		t.Fatalf("protect status after state mv:\n%s", text)
	}
}

// A writer who cannot raise the format of the state is told before the promotion changes
// anything, with the command an admin runs.
func TestPromoteWithoutARecordTellsTheWriterWhatAnAdminRuns(t *testing.T) {
	rt := &runContext{}

	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	l := newLadder(t, rt, "ada:admin", "bob:writer")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	dir := filepath.Join(l.dir, "state", "staging")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	text, code := l.as(t, rt, "bob", "promote", "staging", "--sign-only")
	if code != 5 || !strings.Contains(text, "Nothing was changed. An admin records it once:") || !strings.Contains(text, "pravo environments record --env staging") {
		t.Fatalf("promote by a writer without a record: exit %d\n%s", code, text)
	}
}

// A writer's apply that finds nothing to change is refused like one that changes something
// when the state's record lacks the pin of the roles and the key cannot write meta.json: exit
// 5 with the command for an admin, not the error of the write, and the words say the pin is
// missing, not the record.
func TestAWritersApplyWithNothingToChangeNamesTheMissingPin(t *testing.T) {
	rt := &runContext{}

	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	l := newLadder(t, rt, "ada:admin", "bob:writer")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	// The roles are pinned after the record was written, as by an upgrade.
	l.must(t, rt, "ada", "protect", "roles", "--yes", "-t", "env=dev")
	dir := filepath.Join(l.dir, "state", "dev")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	text, code := l.as(t, rt, "bob", "apply", "-t", "env=dev", "--auto-approve")
	if code != 5 || !strings.Contains(text, "does not yet pin its roles") || !strings.Contains(text, "pravo environments record --env dev") {
		t.Fatalf("writer's apply with nothing to change: exit %d\n%s", code, text)
	}
}

// editReaders rewrites the line of who in the readers file of env: f returns the new line, or ""
// to drop it.
func (l *ladder) editReaders(t *testing.T, env, who string, f func(line string) string) {
	t.Helper()
	path := filepath.Join(l.dir, ".pravo", "access", env, "readers.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, who+" ") {
			if line = f(line); line == "" {
				continue
			}
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withdrawnLadder has ada withdraw the release from prod, with zoe as the second admin and cia a writer.
func withdrawnLadder(t *testing.T, rt *runContext) *ladder {
	l := newLadderPerEnv(t, rt, "ada:admin", "zoe:admin", "cia:writer")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	l.must(t, rt, "ada", "promote", "prod", "--revoke", "corrupts uploads")
	if text, code := l.as(t, rt, "cia", "promote", "prod", "--sign-only"); code != 5 || !strings.Contains(text, "ada withdrew this release") {
		t.Fatalf("cia before the change: exit %d\n%s", code, text)
	}
	return l
}

func (l *ladder) stillWithdrawn(t *testing.T, rt *runContext, when string) {
	t.Helper()
	text, code := l.as(t, rt, "cia", "promote", "prod", "--sign-only")
	if code != 5 || !strings.Contains(text, "ada withdrew this release") {
		t.Fatalf("promote %s: exit %d\n%s", when, code, text)
	}
	text, code = l.as(t, rt, "cia", "apply", "-t", "env=prod", "--auto-approve")
	if code != 5 || !strings.Contains(text, "withdrawn by ada") {
		t.Fatalf("apply %s: exit %d\n%s", when, code, text)
	}
}

// A withdrawal keeps holding when the admin who made it is demoted, or leaves, with or without
// --vouch-notes: it only restricts, so roles carry it over with no flag.
func TestAWithdrawalHoldsWhenItsAdminIsDemoted(t *testing.T) {
	rt := &runContext{}

	l := withdrawnLadder(t, rt)
	l.editReaders(t, "prod", "ada", func(s string) string { return strings.Replace(s, "role:admin", "role:writer", 1) })
	text := l.must(t, rt, "zoe", "protect", "roles", "--yes", "-t", "env=prod")
	if !strings.Contains(text, "a withdrawal stays in force") {
		t.Fatalf("protect roles:\n%s", text)
	}
	l.stillWithdrawn(t, rt, "after the demotion")
}

func TestAWithdrawalHoldsWhenItsAdminLeaves(t *testing.T) {
	rt := &runContext{}

	for _, flag := range []string{"", "--vouch-notes"} {
		l := withdrawnLadder(t, rt)
		l.editReaders(t, "prod", "ada", func(string) string { return "" })
		l.must(t, rt, "zoe", "protect", "rewrap", "--yes", "-t", "env=prod")
		args := []string{"protect", "roles", "--yes", "-t", "env=prod"}
		if flag != "" {
			args = append(args, flag)
		}
		text := l.must(t, rt, "zoe", args...)
		if strings.Contains(text, "will stop counting") && strings.Contains(text, "withdrawal") && !strings.Contains(text, "keeps holding") {
			t.Fatalf("the warning:\n%s", text)
		}
		l.stillWithdrawn(t, rt, "after the offboarding "+flag)
	}
}

// After an admin lifted the withdrawal, a writer's promote and apply of the release work.
func TestAWriterPromotesAfterAnAdminLiftedTheWithdrawal(t *testing.T) {
	rt := &runContext{}

	l := withdrawnLadder(t, rt)
	l.must(t, rt, "zoe", "promote", "prod", "--auto-approve")
	if text, code := l.as(t, rt, "cia", "promote", "prod", "--sign-only"); code != 0 || !strings.Contains(text, "already runs this release") {
		t.Fatalf("writer's promote after the lift: exit %d\n%s", code, text)
	}
	l.must(t, rt, "cia", "apply", "-t", "env=prod", "--auto-approve")
}

// A recovery stores the roles again from version 1, and the withdrawals of the roles it replaces
// stay in force: a writer is still refused, and the recovering admin's promotion lifts it.
func TestAWithdrawalSurvivesARecovery(t *testing.T) {
	rt := &runContext{}

	l := newLadderPerEnv(t, rt, "ada:admin", "zoe:admin", "cia:writer", "zed:reader")
	l.must(t, rt, "ada", "protect", "rewrap", "--yes", "--recovery", "2/3", "-t", "env=prod")
	l.must(t, rt, "ada", "apply", "-t", "env=dev", "--auto-approve")
	l.must(t, rt, "ada", "promote", "staging", "--auto-approve")
	l.must(t, rt, "ada", "promote", "prod", "--revoke", "corrupts uploads")

	// Every admin of prod is lost: zed recovers with two shares, with a readers file of zed alone.
	path := filepath.Join(l.dir, ".pravo", "access", "prod", "readers.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var zed, pin, cia string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		switch {
		case strings.HasPrefix(line, "zed "):
			zed = strings.Replace(line, "role:reader", "role:admin", 1)
		case strings.HasPrefix(line, "cia "):
			cia = line
		case strings.HasPrefix(line, "roles-genesis"):
			pin = line
		}
	}
	if zed == "" || pin == "" || cia == "" {
		t.Fatalf("readers file:\n%s", data)
	}
	write := func(lines ...string) {
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(zed, pin)
	shares := filepath.Join(l.dir, "recovery-shares")
	text := l.must(t, rt, "zed", "protect", "recover", "--yes", "--env", "prod",
		"--share", filepath.Join(shares, "share-1-of-3.txt"), "--share", filepath.Join(shares, "share-3-of-3.txt"))
	if !strings.Contains(text, "1 release withdrawn by an admin of the old roles stays in force") {
		t.Fatalf("protect recover:\n%s", text)
	}
	write(zed, cia)
	l.must(t, rt, "zed", "protect", "rewrap", "--yes", "-t", "env=prod")
	l.must(t, rt, "zed", "protect", "roles", "--yes", "-t", "env=prod")
	l.stillWithdrawn(t, rt, "after the recovery")

	// The recovering admin's promotion lifts it.
	l.must(t, rt, "zed", "promote", "prod", "--auto-approve")
	if text, code := l.as(t, rt, "cia", "promote", "prod", "--sign-only"); code != 0 {
		t.Fatalf("writer's promote after the lift: exit %d\n%s", code, text)
	}
}
