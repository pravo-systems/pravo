package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// The tests here run pravo.#Secret through the whole command line: the specification, the
// sealed bucket, a provider process that speaks the plugin protocol (tools/fakeprovider)
// and the plan and the state that come out. They are skipped by -short, as the other tests
// that build a provider are.

const (
	tokenOne = "TOKEN-planted-0001-abcdef"
	tokenTwo = "TOKEN-planted-0002-ghijkl"
	adminOne = "ADMIN-planted-0001-mnopqr"
	adminTwo = "ADMIN-planted-0002-stuvwx"
)

// sealedEmptyBucket is sealedBucket with a state that holds no record, so that a plan has
// nothing to destroy.
func sealedEmptyBucket(t *testing.T) *sealed {
	t.Helper()
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	e.store(t)
	ada, adaLine := person(t, ids, "ada")
	dan, danLine := person(t, ids, "dan")
	e.readers(t, adaLine, danLine)
	if text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	return &sealed{e, ids, ada, dan, adaLine, danLine}
}

// secretEstate writes a specification that sets up the demo provider with a secret, creates
// a demo_account whose write-only password is another, and rotates it through the number of
// the version. The bucket and its readers are those of s.
func (s *sealed) secretEstate(t *testing.T) {
	t.Helper()
	bin := buildDemoProvider(t)
	spec := "package t\n\n" +
		"import (\n\t\"pravo.dev/pravo\"\n\tdemo \"pravo.dev/providers/demo/demo\"\n)\n\n" +
		`$settings: state: {backend: "s3", bucket: "northwind-state", prefix: "prod", region: "eu-west-1", endpoint: "` + s.url + `", path_style: true, access_key: "test", secret_key: "test"}` + "\n\n" +
		"api_token: pravo.#Secret & {name: \"demo/token\"}\n" +
		"admin_password: pravo.#Secret & {name: \"demo/admin_password\"}\n" +
		"admin_version: pravo.#SecretVersion & {name: \"demo/admin_password\"}\n\n" +
		"$demo: demo.#Provider & {$path: \"" + filepath.ToSlash(bin) + "\", token: api_token.value}\n\n" +
		"admin: $demo.#Resource.demo_account & {\n\tname: \"admin\"\n\tpassword: admin_password.value\n\tpassword_version: \"\\(admin_version.version)\"\n}\n"
	if err := os.WriteFile(filepath.Join(s.dir, "main.cue"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "cue.mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "cue.mod", "module.cue"), []byte("module: \"pravo.local/test\"\nlanguage: version: \"v0.14.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, s.dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
}

func receivedBy(t *testing.T, p *demoProvider) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.dir, "received"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// A secret of the sealed state is the provider's token and a write-only password, and rotating
// it is a new version number: the provider reads the new password, and the plan, the state
// and the bucket never hold a value.
func TestSecretsReachAProviderAndLeaveNoTrace(t *testing.T) {
	s := sealedEmptyBucket(t)
	p := newDemoProvider(t)
	s.secretEstate(t)

	if text, code := s.setSecret(t, tokenOne, "demo/token"); code != 0 {
		t.Fatalf("set the token: exit %d\n%s", code, text)
	}
	if text, code := s.setSecret(t, adminOne, "demo/admin_password"); code != 0 {
		t.Fatalf("set the password: exit %d\n%s", code, text)
	}

	plan, code := runPravo(t, s.dir, "plan")
	if code != 0 {
		t.Fatalf("plan: exit %d\n%s", code, plan)
	}
	for _, v := range []string{tokenOne, adminOne} {
		if strings.Contains(plan, v) {
			t.Errorf("the plan shows %q:\n%s", v, plan)
		}
	}
	planFile := filepath.Join(t.TempDir(), "run.plan")
	if text, code := runPravo(t, s.dir, "plan", "-out", planFile); code != 0 {
		t.Fatalf("plan -out: exit %d\n%s", code, text)
	}
	if raw, err := os.ReadFile(planFile); err != nil || strings.Contains(string(raw), adminOne) || strings.Contains(string(raw), tokenOne) {
		t.Errorf("the plan file holds a secret (%v)", err)
	}

	text, code := runPravo(t, s.dir, "apply", "-auto-approve")
	if code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, text)
	}
	for _, v := range []string{tokenOne, adminOne} {
		if strings.Contains(text, v) {
			t.Errorf("the apply shows %q:\n%s", v, text)
		}
	}
	got := strings.Join(receivedBy(t, p), "\n")
	for _, want := range []string{"token=" + tokenOne, "account admin version=1 password=" + adminOne} {
		if !strings.Contains(got, want) {
			t.Errorf("the provider did not receive %q; it received:\n%s", want, got)
		}
	}

	// Nothing the run left is a value: the state of the root, the outputs, the transactions.
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	rec := st.Get("admin")
	if rec == nil || !strings.Contains(string(rec.Attributes), `"password_version":"1"`) {
		t.Fatalf("the record of admin: %+v", rec)
	}
	for _, addr := range []string{"api_token", "admin_password", "admin_version"} {
		if st.Get(addr) != nil {
			t.Errorf("%s has a record", addr)
		}
	}
	for _, v := range []string{tokenOne, adminOne} {
		if strings.Contains(string(rec.Attributes), v) {
			t.Errorf("the record of admin holds %q", v)
		}
		if strings.Contains(s.bucketText(), v) {
			t.Errorf("the bucket holds %q", v)
		}
	}
	// The record of the secret's use is the transaction of the apply, which names no value.
	if text, code := runPravo(t, s.dir, "state", "log"); code != 0 || strings.Contains(text, adminOne) || strings.Contains(text, tokenOne) {
		t.Errorf("state log: exit %d\n%s", code, text)
	}

	// Nothing changed, nothing to do.
	if text, code := runPravo(t, s.dir, "plan", "-detailed-exitcode"); code != 0 {
		t.Fatalf("a plan with no new version: exit %d\n%s", code, text)
	}

	// Setting the password again is a new version, the resource that holds its number
	// changes, and the provider reads the new password.
	if text, code := s.setSecret(t, adminTwo, "demo/admin_password", "--note", "rotated"); code != 0 {
		t.Fatalf("set the password again: exit %d\n%s", code, text)
	}
	plan, code = runPravo(t, s.dir, "plan", "-detailed-exitcode")
	if code != 2 || !strings.Contains(plan, "password_version") {
		t.Fatalf("a plan with a new version: exit %d\n%s", code, plan)
	}
	if strings.Contains(plan, adminTwo) {
		t.Errorf("the plan shows the new password:\n%s", plan)
	}
	if text, code := runPravo(t, s.dir, "apply", "-auto-approve"); code != 0 || strings.Contains(text, adminTwo) {
		t.Fatalf("apply the rotation: exit %d\n%s", code, text)
	}
	got = strings.Join(receivedBy(t, p), "\n")
	if !strings.Contains(got, "account admin version=2 password="+adminTwo) {
		t.Errorf("the provider did not receive the rotated password; it received:\n%s", got)
	}
	if strings.Contains(s.bucketText(), adminTwo) {
		t.Errorf("the bucket holds the rotated password")
	}
	// The history of the secret is there for whoever asks, with no value.
	text, code = runPravo(t, s.dir, "secret", "history", "demo/admin_password")
	if code != 0 || !strings.Contains(text, "v2") || !strings.Contains(text, "rotated") || strings.Contains(text, adminTwo) {
		t.Errorf("history: exit %d\n%s", code, text)
	}
}

// A run whose secret is not in the state stops before it changes anything, and says how to
// set the secret.
func TestRunWithoutItsSecretStopsAndSaysHow(t *testing.T) {
	s := sealedEmptyBucket(t)
	p := newDemoProvider(t)
	s.secretEstate(t)
	text, code := runPravo(t, s.dir, "apply", "-auto-approve")
	if code == 0 {
		t.Fatalf("an apply with no secrets succeeded:\n%s", text)
	}
	for _, want := range []string{"no secret named demo/", "pravo secret set demo/"} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	if got := receivedBy(t, p); len(got) != 0 {
		t.Errorf("the provider received %v", got)
	}
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	if rec := st.Get("admin"); rec != nil {
		t.Errorf("the run created admin")
	}
}
