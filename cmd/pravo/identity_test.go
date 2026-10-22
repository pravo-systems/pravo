package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// The tests share one process, so the environment is not really emptied by
// resolveIdentity: a test that runs after another would find what the first
// one took. unsetEnv does nothing here, and the tests that look at it say so.
func init() {
	unsetEnv = func(string) error { return nil }
}

// isolateIdentities points every place an identity is looked for at a fresh
// directory, empties the variables that select one, and forgets what an earlier
// test cached. It returns the identity directory.
func isolateIdentities(t *testing.T) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "pravo")
	t.Setenv("PRAVO_CONFIG_DIR", cfg)
	for _, v := range []string{"PRAVO_IDENTITY", "PRAVO_IDENTITY_DIR", "PRAVO_PASSPHRASE", "PRAVO_IDENTITY_PASSPHRASE", "PRAVO_ACCESS_DIR"} {
		t.Setenv(v, "")
	}
	t.Setenv("USER", "ada")
	resetIdentityState(t)
	oldTerm, oldAsk, oldNew := stdinIsTerminal, askPassphrase, askNewPassphrase
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() {
		stdinIsTerminal, askPassphrase, askNewPassphrase = oldTerm, oldAsk, oldNew
		resetIdentityState(t)
		flagIdentity, flagAccessDir = "", ""
	})
	return filepath.Join(cfg, "id")
}

func resetIdentityState(t *testing.T) {
	t.Helper()
	taken.mu.Lock()
	taken.identity, taken.passphrase, taken.loaded = "", "", nil
	taken.mu.Unlock()
}

const testPass = "correct horse battery"

// newIdentityFile makes an identity and returns it with the contents of its file.
func newIdentityFile(t *testing.T, name string, ci bool) (*protect.Identity, []byte) {
	t.Helper()
	id, err := protect.NewIdentity(name, "")
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	if ci {
		data, err = id.MarshalUnsealed()
	} else {
		data, err = id.MarshalSealed([]byte(testPass))
	}
	if err != nil {
		t.Fatal(err)
	}
	return id, data
}

func putIdentity(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveIdentityOrder(t *testing.T) {
	dir := isolateIdentities(t)
	other := t.TempDir()
	ada, adaFile := newIdentityFile(t, "ada", true)
	dan, danFile := newIdentityFile(t, "dan", true)
	eve, eveFile := newIdentityFile(t, "eve", true)
	putIdentity(t, dir, "ada", adaFile)
	putIdentity(t, dir, "dan", danFile)

	same := func(t *testing.T, got *protect.Identity, want *protect.Identity) {
		t.Helper()
		if got == nil || !got.Reader.Public().Equal(want.Reader.Public()) {
			t.Fatalf("resolved %v, want %s", got, want.Name)
		}
	}

	t.Run("a name picks a file of the directory", func(t *testing.T) {
		resetIdentityState(t)
		got, err := resolveIdentity(identityOptions{Name: "dan"})
		if err != nil {
			t.Fatal(err)
		}
		same(t, got, dan)
	})
	t.Run("a name that has no file is no identity", func(t *testing.T) {
		resetIdentityState(t)
		_, err := resolveIdentity(identityOptions{Name: "nobody"})
		if !errors.Is(err, errNoIdentity) || !strings.Contains(err.Error(), "nobody") {
			t.Fatalf("error = %v, want errNoIdentity naming it", err)
		}
	})
	t.Run("a name is a name and not a path", func(t *testing.T) {
		resetIdentityState(t)
		_, err := resolveIdentity(identityOptions{Name: "../ada"})
		if err == nil {
			t.Fatal("a name with a slash was accepted")
		}
	})
	t.Run("PRAVO_IDENTITY as contents", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY", string(eveFile))
		got, err := resolveIdentity(identityOptions{})
		if err != nil {
			t.Fatal(err)
		}
		same(t, got, eve)
	})
	t.Run("PRAVO_IDENTITY as a path", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY", putIdentity(t, other, "bot.identity", eveFile))
		got, err := resolveIdentity(identityOptions{})
		if err != nil {
			t.Fatal(err)
		}
		same(t, got, eve)
	})
	t.Run("a name wins over the environment", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY", string(eveFile))
		got, err := resolveIdentity(identityOptions{Name: "ada"})
		if err != nil {
			t.Fatal(err)
		}
		same(t, got, ada)
	})
	t.Run("several files: the one named like the user", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("USER", "dan")
		got, err := resolveIdentity(identityOptions{})
		if err != nil {
			t.Fatal(err)
		}
		same(t, got, dan)
	})
	t.Run("several files and none is the user", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("USER", "zoe")
		_, err := resolveIdentity(identityOptions{})
		if !errors.Is(err, errNoIdentity) || !strings.Contains(err.Error(), "ada dan") {
			t.Fatalf("error = %v, want errNoIdentity that lists the names", err)
		}
	})
	t.Run("PRAVO_IDENTITY_DIR replaces the default directory", func(t *testing.T) {
		resetIdentityState(t)
		d := filepath.Join(t.TempDir(), "ids")
		putIdentity(t, d, "eve", eveFile)
		t.Setenv("PRAVO_IDENTITY_DIR", d)
		got, err := resolveIdentity(identityOptions{})
		if err != nil {
			t.Fatal(err)
		}
		same(t, got, eve)
	})
	t.Run("one file is enough", func(t *testing.T) {
		resetIdentityState(t)
		d := filepath.Join(t.TempDir(), "ids")
		putIdentity(t, d, "dan", danFile)
		t.Setenv("PRAVO_IDENTITY_DIR", d)
		t.Setenv("USER", "zoe")
		got, err := resolveIdentity(identityOptions{})
		if err != nil {
			t.Fatal(err)
		}
		same(t, got, dan)
	})
	t.Run("an empty directory has none", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY_DIR", t.TempDir())
		_, err := resolveIdentity(identityOptions{})
		if !errors.Is(err, errNoIdentity) || !strings.Contains(err.Error(), "pravo id new --name NAME") {
			t.Fatalf("error = %v, want errNoIdentity that says how to make one", err)
		}
	})
	t.Run("a directory that does not exist has none", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY_DIR", filepath.Join(t.TempDir(), "missing"))
		if _, err := resolveIdentity(identityOptions{}); !errors.Is(err, errNoIdentity) {
			t.Fatalf("error = %v, want errNoIdentity", err)
		}
	})
}

func TestResolveIdentityNeverEchoesAnEnvironmentValue(t *testing.T) {
	isolateIdentities(t)
	for _, v := range []string{"SECRET-TOKEN-0123456789", "{\"v\":1,\"name\":\"SECRET-TOKEN\"}", "{not json SECRET-TOKEN"} {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY", v)
		_, err := resolveIdentity(identityOptions{})
		if err == nil {
			t.Fatalf("PRAVO_IDENTITY=%q was accepted", v)
		}
		if strings.Contains(err.Error(), "SECRET-TOKEN") {
			t.Errorf("the error repeats the value of PRAVO_IDENTITY: %v", err)
		}
	}
}

func TestResolveIdentityOpensASealedIdentity(t *testing.T) {
	dir := isolateIdentities(t)
	ada, file := newIdentityFile(t, "ada", false)
	putIdentity(t, dir, "ada", file)
	asked := 0
	askPassphrase = func(prompt string) ([]byte, error) {
		asked++
		if !strings.Contains(prompt, "ada") {
			t.Errorf("the prompt %q does not say whose passphrase it asks for", prompt)
		}
		return []byte(testPass), nil
	}

	t.Run("from the terminal", func(t *testing.T) {
		resetIdentityState(t)
		stdinIsTerminal = func() bool { return true }
		defer func() { stdinIsTerminal = func() bool { return false } }()
		asked = 0
		got, err := resolveIdentity(identityOptions{Prompt: true})
		if err != nil || !got.Reader.Public().Equal(ada.Reader.Public()) {
			t.Fatalf("resolve = %v, %v", got, err)
		}
		if _, err := resolveIdentity(identityOptions{Prompt: true}); err != nil {
			t.Fatal(err)
		}
		if asked != 1 {
			t.Errorf("the passphrase was asked for %d times, want once for the process", asked)
		}
	})
	t.Run("a wrong passphrase is asked for again, three times", func(t *testing.T) {
		resetIdentityState(t)
		stdinIsTerminal = func() bool { return true }
		defer func() { stdinIsTerminal = func() bool { return false } }()
		asked = 0
		wrong := true
		askPassphrase = func(string) ([]byte, error) {
			asked++
			if wrong && asked < 3 {
				return []byte("not the passphrase"), nil
			}
			return []byte(testPass), nil
		}
		if _, err := resolveIdentity(identityOptions{Prompt: true}); err != nil {
			t.Fatalf("the third try was right and failed: %v", err)
		}
		resetIdentityState(t)
		asked, wrong = 0, false
		askPassphrase = func(string) ([]byte, error) { asked++; return []byte("never right"), nil }
		_, err := resolveIdentity(identityOptions{Prompt: true})
		if err == nil || asked != 3 || !strings.Contains(err.Error(), "wrong passphrase") {
			t.Errorf("asked %d times, error %v; want three tries and a wrong passphrase", asked, err)
		}
	})
	t.Run("from the environment when stdin is not a terminal", func(t *testing.T) {
		resetIdentityState(t)
		asked = 0
		t.Setenv("PRAVO_PASSPHRASE", testPass)
		got, err := resolveIdentity(identityOptions{Prompt: true})
		if err != nil || !got.Reader.Public().Equal(ada.Reader.Public()) {
			t.Fatalf("resolve = %v, %v", got, err)
		}
		if asked != 0 {
			t.Error("a terminal was asked although stdin is not one")
		}
	})
	t.Run("from PRAVO_IDENTITY_PASSPHRASE", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY_PASSPHRASE", testPass)
		if _, err := resolveIdentity(identityOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no passphrase and no terminal", func(t *testing.T) {
		resetIdentityState(t)
		_, err := resolveIdentity(identityOptions{Prompt: true})
		want := "the identity ada is sealed with a passphrase and stdin is not a terminal; set PRAVO_PASSPHRASE."
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
	})
	t.Run("a caller that must not prompt never does", func(t *testing.T) {
		resetIdentityState(t)
		stdinIsTerminal = func() bool { return true }
		defer func() { stdinIsTerminal = func() bool { return false } }()
		asked = 0
		_, err := resolveIdentity(identityOptions{Prompt: false})
		if err == nil || asked != 0 || !strings.Contains(err.Error(), "PRAVO_PASSPHRASE") {
			t.Fatalf("error = %v after %d prompts, want the environment named and no prompt", err, asked)
		}
	})
	t.Run("a wrong passphrase in the environment does not leak", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_PASSPHRASE", "WRONG-PASSPHRASE-VALUE")
		_, err := resolveIdentity(identityOptions{})
		if err == nil || strings.Contains(err.Error(), "WRONG-PASSPHRASE-VALUE") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestResolveIdentityRemovesTheSecretsFromTheEnvironment(t *testing.T) {
	isolateIdentities(t)
	removed := map[string]bool{}
	old := unsetEnv
	unsetEnv = func(name string) error {
		removed[name] = true
		return os.Unsetenv(name)
	}
	defer func() { unsetEnv = old }()
	_, file := newIdentityFile(t, "bot", false)
	t.Setenv("PRAVO_IDENTITY", string(file))
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	if _, err := resolveIdentity(identityOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range protect.SecretEnv {
		if !removed[name] {
			t.Errorf("%s was not removed from the environment", name)
		}
	}
	if os.Getenv("PRAVO_IDENTITY") != "" || os.Getenv("PRAVO_PASSPHRASE") != "" {
		t.Error("a secret is still in the environment")
	}
	// The process still has the identity, for a second open of the state.
	if _, err := resolveIdentity(identityOptions{}); err != nil {
		t.Errorf("the second resolve failed: %v", err)
	}
}

func TestResolveIdentityWarnsOfAnIdentityFileOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not kept on Windows")
	}
	dir := isolateIdentities(t)
	_, file := newIdentityFile(t, "ada", true)
	p := putIdentity(t, dir, "ada", file)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := resolveIdentity(identityOptions{})
	if err == nil || !strings.Contains(err.Error(), "refusing to use an identity with no passphrase") {
		t.Fatalf("error = %v, want a refusal of a key with no passphrase that others can read", err)
	}
}
