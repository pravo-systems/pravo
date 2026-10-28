package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// stdinIs makes the standard input of the next commands the text.
func stdinIs(t *testing.T, text string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

// setSecret runs pravo secret set with the value on standard input.
func (s *sealed) setSecret(t *testing.T, value string, args ...string) (string, int) {
	t.Helper()
	stdinIs(t, value)
	return runPravo(t, s.dir, append([]string{"secret", "set"}, args...)...)
}

func TestSecretSetGetListAndHistory(t *testing.T) {
	s := sealedBucket(t)
	const pw1, pw2, note = "hunter2-correct-horse", "battery-staple-9999", "rotated after the audit"

	text, code := runPravo(t, s.dir, "secret", "list")
	if code != 0 || !strings.Contains(text, "No secrets.") {
		t.Fatalf("an empty list: exit %d\n%s", code, text)
	}

	// A line break after a value of one line is not part of it.
	if text, code = s.setSecret(t, pw1+"\n", "prod/db_password"); code != 0 || text != "Set prod/db_password, version 1.\n" {
		t.Fatalf("set: exit %d\n%s", code, text)
	}
	if text, code = s.setSecret(t, pw2, "prod/db_password", "--note", note); code != 0 || text != "Set prod/db_password, version 2.\n" {
		t.Fatalf("set again: exit %d\n%s", code, text)
	}
	// A value of several lines is kept as it is, from a file.
	pem := "-----BEGIN KEY-----\nAAAA\nBBBB\n-----END KEY-----\n"
	file := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(file, []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	if text, code = runPravo(t, s.dir, "secret", "set", "prod/tls_key", "--file", file); code != 0 {
		t.Fatalf("set from a file: exit %d\n%s", code, text)
	}
	t.Setenv("TOKEN_FOR_THE_TEST", "tok-from-the-environment")
	if text, code = runPravo(t, s.dir, "secret", "set", "prod/api_token", "--from-env", "TOKEN_FOR_THE_TEST"); code != 0 {
		t.Fatalf("set from the environment: exit %d\n%s", code, text)
	}

	// The bucket holds none of the values and none of the note.
	bucket := s.bucketText()
	for _, v := range []string{pw1, pw2, note, "BBBB", "tok-from-the-environment"} {
		if strings.Contains(bucket, v) {
			t.Errorf("%q is in the bucket", v)
		}
	}

	t.Run("get prints the bytes of the value", func(t *testing.T) {
		for args, want := range map[string]string{
			"prod/db_password":               pw2,
			"prod/db_password --version 1":   pw1,
			"prod/tls_key":                   pem,
			"prod/api_token":                 "tok-from-the-environment",
			"prod/db_password --version 2":   pw2,
			"--version 1 prod/db_password":   pw1,
			"prod/db_password --version=0":   pw2,
			"prod/tls_key --identity dan":    pem,
			"prod/api_token --identity=dan ": "tok-from-the-environment",
		} {
			text, code := runPravo(t, s.dir, append([]string{"secret", "get"}, strings.Fields(args)...)...)
			if code != 0 || text != want {
				t.Errorf("get %s: exit %d, got %q, want %q", args, code, text, want)
			}
		}
	})
	t.Run("get --json", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "secret", "get", "prod/db_password", "--json")
		var doc struct {
			Name, Value, Note, By, Trust, Kind string
			Version                            int
		}
		if err := json.Unmarshal([]byte(text), &doc); code != 0 || err != nil {
			t.Fatalf("exit %d, %v\n%s", code, err, text)
		}
		if doc.Name != "prod/db_password" || doc.Version != 2 || doc.Value != pw2 || doc.Note != note || doc.By != "ada" || doc.Trust != "ok" || doc.Kind != "value" {
			t.Errorf("%+v", doc)
		}
	})
	t.Run("a value that is not text is base64 in JSON", func(t *testing.T) {
		raw := []byte{0xff, 0x00, 0xfe, 'x'}
		stdinIs(t, string(raw))
		if text, code := runPravo(t, s.dir, "secret", "set", "prod/blob"); code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		text, code := runPravo(t, s.dir, "secret", "get", "prod/blob", "--json")
		var doc struct {
			Value       *string `json:"value"`
			ValueBase64 string  `json:"value_base64"`
		}
		if err := json.Unmarshal([]byte(text), &doc); code != 0 || err != nil || doc.Value != nil {
			t.Fatalf("exit %d, %v\n%s", code, err, text)
		}
		if got, _ := base64.StdEncoding.DecodeString(doc.ValueBase64); string(got) != string(raw) {
			t.Errorf("value_base64 = %q", doc.ValueBase64)
		}
		if text, code := runPravo(t, s.dir, "secret", "get", "prod/blob"); code != 0 || text != string(raw) {
			t.Errorf("get: exit %d %q", code, text)
		}
	})
	t.Run("list", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "secret", "list")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{"NAME", "VERSION", "prod/api_token", "prod/db_password", "prod/tls_key", "ada"} {
			if !strings.Contains(text, want) {
				t.Errorf("the list lacks %q:\n%s", want, text)
			}
		}
		for _, v := range []string{pw1, pw2, note} {
			if strings.Contains(text, v) {
				t.Errorf("the list shows %q", v)
			}
		}
		text, code = runPravo(t, s.dir, "secret", "list", "--json")
		var list []struct {
			Name     string
			Versions int
			Head     struct {
				Version int
				Kind    string
				Trust   string
			}
		}
		if err := json.Unmarshal([]byte(text), &list); code != 0 || err != nil || len(list) != 4 {
			t.Fatalf("exit %d, %v\n%s", code, err, text)
		}
		if list[2].Name != "prod/db_password" || list[2].Versions != 2 || list[2].Head.Version != 2 || list[2].Head.Trust != "ok" {
			t.Errorf("%+v", list)
		}
	})
	t.Run("history", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "secret", "history", "prod/db_password")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{"prod/db_password\n", "  v2 ", "  v1 ", "ada", "value", note} {
			if !strings.Contains(text, want) {
				t.Errorf("the history lacks %q:\n%s", want, text)
			}
		}
		if strings.Index(text, "v2") > strings.Index(text, "v1") {
			t.Errorf("the newest version is not first:\n%s", text)
		}
		for _, v := range []string{pw1, pw2} {
			if strings.Contains(text, v) {
				t.Errorf("the history shows a value")
			}
		}
	})
	t.Run("the log names the secret and never shows a value", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "state", "log")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{"secret   applied", "pravo secret set prod/db_password\n", "secret:prod/db_password  v1\n", "secret:prod/db_password  v1 -> v2\n", "signed by ada (verified)"} {
			if !strings.Contains(text, want) {
				t.Errorf("the log lacks %q:\n%s", want, text)
			}
		}
		for _, v := range []string{pw1, pw2, note, "tok-from-the-environment", "--note"} {
			if strings.Contains(text, v) {
				t.Errorf("the log has %q", v)
			}
		}
	})
	t.Run("a value is not an argument", func(t *testing.T) {
		if text, code := runPravo(t, s.dir, "secret", "set", "prod/x", "--value", "oops"); code == 0 || !strings.Contains(text, "unknown flag") {
			t.Errorf("exit %d\n%s", code, text)
		}
		if text, code := runPravo(t, s.dir, "secret", "set", "prod/x", "--file", file, "--from-env", "TOKEN_FOR_THE_TEST"); code == 0 || !strings.Contains(text, "one source") {
			t.Errorf("two sources: exit %d\n%s", code, text)
		}
	})
	t.Run("what cannot be a secret", func(t *testing.T) {
		stdinIs(t, "")
		if text, code := runPravo(t, s.dir, "secret", "set", "prod/empty"); code == 0 || !strings.Contains(text, "cannot be empty") {
			t.Errorf("empty: exit %d\n%s", code, text)
		}
		stdinIs(t, "x")
		for _, name := range []string{"bad name", "../x", "a//b", "x@1", "Ünï"} {
			if text, code := runPravo(t, s.dir, "secret", "set", name); code == 0 || !strings.Contains(text, "is not the name of a secret") {
				t.Errorf("%q: exit %d\n%s", name, code, text)
			}
		}
		stdinIs(t, "x")
		if text, code := runPravo(t, s.dir, "secret", "set", "Prod/DB_PASSWORD"); code == 0 || !strings.Contains(text, "does not tell upper case from lower") {
			t.Errorf("a name that differs by case: exit %d\n%s", code, text)
		}
		if text, code := runPravo(t, s.dir, "secret", "get", "nope"); code == 0 || !strings.Contains(text, "there is no such secret") {
			t.Errorf("a secret that is not there: exit %d\n%s", code, text)
		}
		if text, code := runPravo(t, s.dir, "secret", "get", "prod/db_password", "--version", "9"); code == 0 || !strings.Contains(text, "has versions 1 to 2") {
			t.Errorf("a version that is not there: exit %d\n%s", code, text)
		}
	})
	t.Run("the notes are for readers", func(t *testing.T) {
		s.asNobody(t)
		text, code := runPravo(t, s.dir, "secret", "history", "prod/db_password")
		if code != 0 || strings.Contains(text, note) || !strings.Contains(text, "v2") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if text, code := runPravo(t, s.dir, "secret", "list"); code != 0 || !strings.Contains(text, "prod/db_password") {
			t.Errorf("list without a key: exit %d\n%s", code, text)
		}
		for _, args := range [][]string{{"secret", "get", "prod/db_password"}, {"secret", "set", "x"}, {"secret", "exec", "--env", "A=prod/db_password", "--", "true"}} {
			if text, code := runPravo(t, s.dir, args...); code != 5 || !strings.Contains(text, "you are not a reader of this state") {
				t.Errorf("%v without a key: exit %d\n%s", args, code, text)
			}
		}
	})
}

func TestSecretSetAsksOnTheTerminal(t *testing.T) {
	s := sealedBucket(t)
	var prompts []string
	oldAsk := askSecretValue
	stdinIsTerminal = func() bool { return true }
	askSecretValue = func(p string) ([]byte, error) { prompts = append(prompts, p); return []byte("typed-value"), nil }
	t.Cleanup(func() {
		stdinIsTerminal = func() bool { return false }
		askSecretValue = oldAsk
	})
	if text, code := runPravo(t, s.dir, "secret", "set", "prod/typed"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "prod/typed") {
		t.Errorf("the prompts: %q", prompts)
	}
	if text, code := runPravo(t, s.dir, "secret", "get", "prod/typed"); code != 0 || text != "typed-value" {
		t.Errorf("get: exit %d %q", code, text)
	}
	// A source that the flags name is used and nothing is asked.
	file := filepath.Join(t.TempDir(), "v")
	if err := os.WriteFile(file, []byte("from-a-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, s.dir, "secret", "set", "prod/typed", "--file", file); code != 0 || len(prompts) != 1 {
		t.Fatalf("exit %d, prompts %q\n%s", code, prompts, text)
	}
	if text, _ := runPravo(t, s.dir, "secret", "get", "prod/typed"); text != "from-a-file" {
		t.Errorf("get: %q", text)
	}
}

func TestSecretGetOnATerminal(t *testing.T) {
	s := sealedBucket(t)
	if text, code := s.setSecret(t, "plain-value", "prod/plain"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	stdinIs(t, "\x1b[31mred\x00")
	if text, code := runPravo(t, s.dir, "secret", "set", "prod/escape"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	oldOut := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdoutIsTerminal = oldOut })
	if text, code := runPravo(t, s.dir, "secret", "get", "prod/plain"); code != 0 || text != "plain-value\n" {
		t.Errorf("a terminal gets a line break: exit %d %q", code, text)
	}
	text, code := runPravo(t, s.dir, "secret", "get", "prod/escape")
	if code == 0 || strings.Contains(text, "\x1b") || !strings.Contains(text, "not printed to a terminal") {
		t.Errorf("a value with control characters: exit %d %q", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "get", "prod/escape", "--json"); code != 0 || !strings.Contains(text, `"value"`) {
		t.Errorf("--json prints it: exit %d %q", code, text)
	}
}

func TestSecretRemoveAndPurge(t *testing.T) {
	s := sealedBucket(t)
	for _, v := range []string{"one", "two", "three"} {
		if text, code := s.setSecret(t, v, "db"); code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
	}
	// A command that asks cannot, with no terminal.
	if text, code := runPravo(t, s.dir, "secret", "rm", "db"); code == 0 || !strings.Contains(text, "pass --yes") {
		t.Errorf("rm without --yes: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "purge", "db", "--keep", "0", "--yes"); code == 0 || !strings.Contains(text, "is live") {
		t.Errorf("purge of a live secret to nothing: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "purge", "db", "--yes"); code != 0 || !strings.Contains(text, "Forgot 2 values of db.") {
		t.Fatalf("purge: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "purge", "db", "--yes"); code != 0 || !strings.Contains(text, "Nothing to forget") {
		t.Errorf("purge again: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "get", "db", "--version", "1"); code == 0 || !strings.Contains(text, "was purged") {
		t.Errorf("a purged version: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "get", "db"); code != 0 || text != "three" {
		t.Errorf("the kept version: exit %d %q", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "rm", "db", "--yes"); code != 0 || !strings.Contains(text, "Removed db, version 4.") {
		t.Fatalf("rm: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "rm", "db", "--yes"); code == 0 || !strings.Contains(text, "was removed") {
		t.Errorf("rm again: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "get", "db"); code == 0 || !strings.Contains(text, "was removed") {
		t.Errorf("get of a removed secret: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "list"); code != 0 || !strings.Contains(text, "removed") {
		t.Errorf("list: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "purge", "db", "--keep", "0", "--yes"); code != 0 || !strings.Contains(text, "Forgot 1 value of db.") {
		t.Fatalf("purge of a removed secret: exit %d\n%s", code, text)
	}
	// The log tells a removal and a purge by the versions they concern.
	text, code := runPravo(t, s.dir, "state", "log")
	for _, want := range []string{"remove   secret:db  v3 -> v4\n", "purge    secret:db  v3\n", "purge    secret:db  v1\n"} {
		if code != 0 || !strings.Contains(text, want) {
			t.Errorf("the log lacks %q (exit %d):\n%s", want, code, text)
		}
	}
	// The last value is gone from the bucket: the objects of the secret hold no sealed part.
	for k, v := range s.f.Objects {
		if strings.Contains(k, "/secret/") && strings.Contains(string(v), `"sealed"`) {
			t.Errorf("%s still holds a sealed value", k)
		}
	}
	// A secret that was removed is set again, and the numbering goes on.
	if text, code := s.setSecret(t, "again", "db"); code != 0 || !strings.Contains(text, "version 5") {
		t.Errorf("set after removal: exit %d\n%s", code, text)
	}
	var kinds []string
	hist, _ := runPravo(t, s.dir, "secret", "history", "db", "--json")
	var docs []struct{ Kind string }
	if err := json.Unmarshal([]byte(hist), &docs); err != nil {
		t.Fatalf("%v\n%s", err, hist)
	}
	for _, d := range docs {
		kinds = append(kinds, d.Kind)
	}
	if got := strings.Join(kinds, ","); got != "value,removed,purged,purged,purged" {
		t.Errorf("kinds, newest first: %s", got)
	}
}

func TestSecretRolesGateTheCommands(t *testing.T) {
	s := sealedBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	dan := []string{"--identity", "dan"}
	run := func(who []string, args ...string) (string, int) {
		return runPravo(t, s.dir, append(append([]string{}, who...), args...)...)
	}

	stdinIs(t, "pw-from-a-writer")
	if text, code := run(dan, "secret", "set", "db"); code != 0 {
		t.Fatalf("a writer sets: exit %d\n%s", code, text)
	}
	for _, args := range [][]string{{"secret", "rm", "db", "--yes"}, {"secret", "purge", "db", "--yes"}} {
		text, code := run(dan, args...)
		if code != 5 || !strings.Contains(text, "your identity is a writer in") || !strings.Contains(text, "an admin is needed to") {
			t.Errorf("a writer's %v: exit %d\n%s", args, code, text)
		}
	}
	if text, code := run(nil, "secret", "get", "db"); code != 0 || text != "pw-from-a-writer" {
		t.Errorf("an admin reads: exit %d %q", code, text)
	}

	// dan is a reader from now on. What he wrote as a writer fails closed until an admin
	// vouches for it, which pravo protect roles does for every transaction that it finds.
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader")
	text, code := run(nil, "secret", "get", "db")
	if code != 7 || !strings.Contains(text, "run by dan, who is a reader") {
		t.Fatalf("the version of a writer who was demoted: exit %d\n%s", code, text)
	}
	if text, code := run(nil, "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	if text, code := run(nil, "secret", "get", "db", "--json"); code != 0 || !strings.Contains(text, `"trust": "vouched"`) || !strings.Contains(text, "pw-from-a-writer") {
		t.Fatalf("after the vouch: exit %d\n%s", code, text)
	}
	if text, code := run(nil, "secret", "history", "db"); code != 0 {
		t.Errorf("history after the vouch: exit %d\n%s", code, text)
	}

	// A reader reads and cannot write.
	stdinIs(t, "pw-from-a-reader")
	text, code = run(dan, "secret", "set", "db")
	if code != 5 || !strings.Contains(text, "your identity is a reader in") || !strings.Contains(text, "a writer is needed to change the state") {
		t.Errorf("a reader sets: exit %d\n%s", code, text)
	}
	if text, code := run(dan, "secret", "get", "db"); code != 0 || text != "pw-from-a-writer" {
		t.Errorf("a reader reads: exit %d %q", code, text)
	}
	for _, args := range [][]string{{"secret", "list"}, {"secret", "history", "db"}} {
		if text, code := run(dan, args...); code != 0 {
			t.Errorf("a reader's %v: exit %d\n%s", args, code, text)
		}
	}
	if text, code := run(dan, "secret", "rm", "db", "--yes"); code != 5 {
		t.Errorf("a reader removes: exit %d\n%s", code, text)
	}

	// A reader holds the data key and the credentials of the bucket. A version that one wrote
	// without the command is found, and refused.
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.dan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSecret(context.Background(), "db", []byte("pw-from-the-key-of-a-reader"), "", "pravo secret set db"); err != nil {
		t.Fatal(err)
	}
	text, code = run(nil, "secret", "get", "db")
	if code != 7 || !strings.Contains(text, "run by dan, who is a reader") || strings.Contains(text, "pw-from-the-key-of-a-reader") {
		t.Errorf("a version of a reader: exit %d\n%s", code, text)
	}
	if text, code := run(nil, "secret", "exec", "--env", "PW=db", "--", "true"); code != 7 {
		t.Errorf("exec on a version of a reader: exit %d\n%s", code, text)
	}
	if text, code := run(nil, "secret", "get", "db", "--version", "1"); code != 0 || text != "pw-from-a-writer" {
		t.Errorf("the version before: exit %d %q", code, text)
	}
	text, code = run(nil, "secret", "list")
	if code != 7 || !strings.Contains(text, "Untrusted: db version 2:") {
		t.Errorf("list: exit %d\n%s", code, text)
	}
	text, code = run(nil, "secret", "history", "db")
	if code != 7 || !strings.Contains(text, "Untrusted:") {
		t.Errorf("history: exit %d\n%s", code, text)
	}
	// An admin sets a version over it.
	stdinIs(t, "pw-from-the-admin")
	if text, code := run(nil, "secret", "set", "db"); code != 0 {
		t.Fatalf("set over it: exit %d\n%s", code, text)
	}
	if text, code := run(nil, "secret", "get", "db"); code != 0 || text != "pw-from-the-admin" {
		t.Errorf("get after: exit %d %q", code, text)
	}
}

func TestSecretsAreKeptInASealedState(t *testing.T) {
	// No state at all: nothing is created.
	dir := specDir(t, map[string]string{"main.cue": "package t\n"})
	stdinIs(t, "x")
	text, code := runPravo(t, dir, "secret", "set", "db")
	if code != 5 || !strings.Contains(text, "secrets are kept in a sealed state") || !strings.Contains(text, "there is no state yet") {
		t.Errorf("no state: exit %d\n%s", code, text)
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.state")); err == nil {
		t.Errorf("a refused set made a state")
	}
	if text, code := runPravo(t, dir, "secret", "list"); code != 0 || !strings.Contains(text, "No secrets.") {
		t.Errorf("list of no state: exit %d\n%s", code, text)
	}
	// A state that is not sealed.
	if _, err := state.Open(filepath.Join(dir, "pravo.state")); err != nil {
		t.Fatal(err)
	}
	stdinIs(t, "x")
	text, code = runPravo(t, dir, "secret", "set", "db")
	if code != 5 || !strings.Contains(text, "secrets are kept in a sealed state") || !strings.Contains(text, "pravo protect init --encrypt") {
		t.Errorf("a state that is not sealed: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "secret", "get", "db"); code != 5 || !strings.Contains(text, "secrets are kept in a sealed state") {
		t.Errorf("get from a state that is not sealed: exit %d\n%s", code, text)
	}
}

// TestSecretExecHelper is the command that the tests of exec run: this test binary. It writes the
// variables it was asked to and exits with the code it was asked to.
func TestSecretExecHelper(t *testing.T) {
	if os.Getenv("PRAVO_TEST_EXEC_HELPER") == "" {
		t.Skip("the command of the exec tests")
	}
	var lines []string
	for _, name := range strings.Fields(os.Getenv("PRAVO_TEST_EXEC_HELPER")) {
		lines = append(lines, name+"="+os.Getenv(name))
	}
	os.Stdout.WriteString(strings.Join(lines, "\n") + "\n")
	if os.Getenv("PRAVO_TEST_EXEC_EXIT") != "" {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestSecretExec(t *testing.T) {
	s := sealedBucket(t)
	if text, code := s.setSecret(t, "first", "db"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if text, code := s.setSecret(t, "second", "db"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if text, code := s.setSecret(t, "line one\nline two\n", "multi"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	helper := []string{os.Args[0], "-test.run=^TestSecretExecHelper$", "-test.count=1"}
	t.Setenv("PRAVO_TEST_EXEC_HELPER", "DB_PASSWORD OLD_PASSWORD BLOB UNRELATED")
	t.Setenv("UNRELATED", "stays")

	text, code := runPravo(t, s.dir, append([]string{"secret", "exec", "--env", "DB_PASSWORD=db", "--env", "OLD_PASSWORD=db@1", "--env", "BLOB=multi", "--"}, helper...)...)
	for _, want := range []string{"DB_PASSWORD=second\n", "OLD_PASSWORD=first\n", "BLOB=line one\nline two\n", "UNRELATED=stays\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the command's environment lacks %q:\n%s", want, text)
		}
	}
	if code != 0 {
		t.Errorf("exit %d\n%s", code, text)
	}

	// The exit code is the command's, and pravo says nothing more.
	t.Setenv("PRAVO_TEST_EXEC_EXIT", "1")
	text, code = runPravo(t, s.dir, append([]string{"secret", "exec", "--env", "DB_PASSWORD=db", "--"}, helper...)...)
	if code != 3 || !strings.HasSuffix(text, "Error: \n") || strings.Count(text, "Error") != 1 {
		t.Errorf("a command that fails: exit %d\n%s", code, text)
	}
	t.Setenv("PRAVO_TEST_EXEC_EXIT", "")

	// A secret that cannot be read means the command does not run.
	text, code = runPravo(t, s.dir, append([]string{"secret", "exec", "--env", "DB_PASSWORD=db", "--env", "BLOB=nope", "--"}, helper...)...)
	if code == 0 || strings.Contains(text, "DB_PASSWORD=") || !strings.Contains(text, "there is no such secret") {
		t.Errorf("a missing secret: exit %d\n%s", code, text)
	}
	for _, c := range []struct{ args, want string }{
		{"--env DB_PASSWORD", "want VAR=NAME"},
		{"--env 1X=db", "want VAR=NAME"},
		{"--env A=db --env A=multi", "is set twice"},
		{"--env A=db@0", "a version is a number from 1"},
		{"--env A=bad..name!", "is not the name of a secret"},
		{"", "name at least one secret"},
	} {
		args := append([]string{"secret", "exec"}, strings.Fields(c.args)...)
		args = append(args, "--")
		args = append(args, helper...)
		if text, code := runPravo(t, s.dir, args...); code == 0 || !strings.Contains(text, c.want) {
			t.Errorf("%q: exit %d\n%s", c.args, code, text)
		}
	}
	if _, err := exec.LookPath("pravo-no-such-command"); err == nil {
		t.Skip("a command that should not exist does")
	}
	text, code = runPravo(t, s.dir, "secret", "exec", "--env", "A=db", "--", "pravo-no-such-command")
	if code == 0 || !strings.Contains(text, "pravo-no-such-command") {
		t.Errorf("a command that is not there: exit %d\n%s", code, text)
	}
}

func TestArgumentsAfterTheDashesBelongToTheCommand(t *testing.T) {
	got := normaliseArgs([]string{"pravo", "-auto-approve", "secret", "exec", "-env", "A=db", "--", "ls", "-la", "-refresh=false", "--", "-x"})
	want := []string{"pravo", "--auto-approve", "secret", "exec", "--env", "A=db", "--", "ls", "-la", "-refresh=false", "--", "-x"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// The commands that look after a sealed state know about its secrets: status counts them and
// checks them, gc keeps them, an unseal would write them in the open and is refused, and a
// rewrap that takes a reader away seals them again under the new key.
func TestProtectCommandsKnowTheSecrets(t *testing.T) {
	s := sealedBucket(t)
	const one, two, key = "db-password-one-planted", "db-password-two-planted", "api-key-planted"
	for _, c := range []struct{ name, value string }{{"db", one}, {"db", two}, {"api", key}} {
		if text, code := s.setSecret(t, c.value, c.name); code != 0 {
			t.Fatalf("set %s: exit %d\n%s", c.name, code, text)
		}
	}

	text, code := runPravo(t, s.dir, "protect", "status")
	if code != 0 || !strings.Contains(text, "Secrets      2 secrets · 3 versions · 3 bound to a transaction of a writer\n") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "protect", "status", "--json")
	var doc struct {
		Secrets struct{ Secrets, Versions, Bound int }
	}
	if err := json.Unmarshal([]byte(text), &doc); code != 0 || err != nil || doc.Secrets.Secrets != 2 || doc.Secrets.Versions != 3 || doc.Secrets.Bound != 3 {
		t.Errorf("status --json: exit %d, %v, %+v\n%s", code, err, doc, text)
	}
	if text, code := runPravo(t, s.dir, "protect", "check"); code != 0 {
		t.Errorf("check: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "protect", "gc"); code != 0 {
		t.Errorf("gc: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "get", "db", "--version", "1"); code != 0 || text != one {
		t.Errorf("a secret after gc: exit %d %q", code, text)
	}

	// An unseal would write the three values in the open.
	text, code = runPravo(t, s.dir, "protect", "unseal", "--yes")
	if code != 5 || !strings.Contains(text, "3 values of secrets are sealed in it") || !strings.Contains(text, "pravo secret purge NAME --keep 0") {
		t.Errorf("unseal: exit %d\n%s", code, text)
	}
	if strings.Contains(text, "This will:") {
		t.Errorf("unseal described what it would do before it refused:\n%s", text)
	}

	// Dan leaves. A rewrap gives the state a new key, and the secrets are under the old one
	// until they are sealed again; gc must not drop the group that they are under.
	s.readers(t, s.adaLine)
	if text, code := runPravo(t, s.dir, "protect", "rewrap"); code != 0 {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "protect", "gc"); code != 0 {
		t.Errorf("gc after the rewrap: exit %d\n%s", code, text)
	}
	for version, want := range map[string]string{"1": one, "2": two} {
		if text, code := runPravo(t, s.dir, "secret", "get", "db", "--version", version); code != 0 || text != want {
			t.Errorf("ada reads version %s after gc: exit %d %q", version, code, text)
		}
	}
	// --all seals every secret again under the new key, and then the old group is dropped.
	if text, code := runPravo(t, s.dir, "protect", "rewrap", "--all"); code != 0 {
		t.Fatalf("rewrap --all: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "protect", "gc"); code != 0 {
		t.Errorf("gc after rewrap --all: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "secret", "get", "db", "--identity", "dan"); code != 5 || strings.Contains(text, two) {
		t.Errorf("dan reads a secret after he left: exit %d\n%s", code, text)
	}
	for version, want := range map[string]string{"1": one, "2": two} {
		if text, code := runPravo(t, s.dir, "secret", "get", "db", "--version", version); code != 0 || text != want {
			t.Errorf("ada reads version %s: exit %d %q", version, code, text)
		}
	}
	if text, code := runPravo(t, s.dir, "secret", "get", "api"); code != 0 || text != key {
		t.Errorf("ada reads api: exit %d %q", code, text)
	}
	for _, v := range []string{one, two, key} {
		if strings.Contains(s.bucketText(), v) {
			t.Errorf("the bucket holds %q", v)
		}
	}
	if text, code := runPravo(t, s.dir, "protect", "status"); code != 0 || !strings.Contains(text, "2 secrets · 3 versions") {
		t.Errorf("status after the rewrap: exit %d\n%s", code, text)
	}
}

// An agent that works through the MCP server reads the plans, the state and the log, and
// none of them holds a value of a secret. The server has no tool for secrets at all, so that
// there is nothing for a prompt to steer towards a value.
func TestNoMCPToolHandsOutASecret(t *testing.T) {
	for _, tool := range mcpTools() {
		if strings.Contains(strings.ToLower(tool.Name), "secret") || strings.Contains(strings.ToLower(tool.Description), "pravo secret") {
			t.Errorf("the tool %s has to do with secrets", tool.Name)
		}
	}
	s := sealedBucket(t)
	const value = "mcp-guard-planted-value"
	if text, code := s.setSecret(t, value, "guard/db"); code != 0 {
		t.Fatalf("set: exit %d\n%s", code, text)
	}
	for _, args := range [][]string{{"state", "log", "--json"}, {"state", "list", "--json"}, {"plan", "--json"}} {
		text, _ := runPravo(t, s.dir, args...)
		if strings.Contains(text, value) {
			t.Errorf("pravo %s shows the value", strings.Join(args, " "))
		}
	}
}
