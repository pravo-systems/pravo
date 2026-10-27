package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/ui"
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
		flags.Identity, flags.AccessDir = "", ""
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

// --identity is global: every command that opens a state takes it, and it wins
// over $PRAVO_IDENTITY.
func TestGlobalIdentityFlag(t *testing.T) {
	s := sealedBucket(t)
	_, eve := newIdentityFile(t, "eve", true)
	evePath := putIdentity(t, t.TempDir(), "eve", eve)
	t.Setenv("PRAVO_IDENTITY", evePath)
	resetIdentityState(t)

	text, code := runPravo(t, s.dir, "state", "show", "net.vpc")
	if code != 5 || !strings.Contains(text, "identity eve is not a reader") {
		t.Fatalf("the environment identity should not read: exit %d\n%s", code, text)
	}
	for _, args := range [][]string{
		{"state", "show", "net.vpc"},
		{"state", "query", "len(resources)"},
		{"output"},
		{"protect", "status"},
	} {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY", evePath)
		for _, form := range [][]string{append(args[:len(args):len(args)], "--identity", "dan"), append([]string{"--identity", "dan"}, args...)} {
			text, code := runPravo(t, s.dir, form...)
			if code != 0 {
				t.Errorf("%v: exit %d\n%s", form, code, text)
			}
			if strings.Contains(text, "not a reader") {
				t.Errorf("%v: the flag was not used\n%s", form, text)
			}
		}
	}
	t.Run("id show reads the named identity", func(t *testing.T) {
		resetIdentityState(t)
		text, code := runPravo(t, s.dir, "id", "show", "--identity", "dan")
		if code != 0 || !strings.Contains(text, "Identity dan ") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a name that does not exist", func(t *testing.T) {
		resetIdentityState(t)
		text, code := runPravo(t, s.dir, "state", "show", "net.vpc", "--identity", "zed")
		if code != 5 || !strings.Contains(text, "no identity named zed") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("the help lists it once", func(t *testing.T) {
		for _, args := range [][]string{{"state", "show"}, {"protect", "status"}, {"serve"}, {"plan"}} {
			text, _ := runPravo(t, s.dir, append(args, "--help")...)
			if n := strings.Count(text, "--identity string"); n != 1 {
				t.Errorf("%v: --identity is listed %d times\n%s", args, n, text)
			}
		}
	})
}

// With several identity files and none named like the user, the identity is the
// one whose reader key has a lock in the state.
func TestIdentityIsChosenByTheLocksOfTheState(t *testing.T) {
	s := sealedBucket(t)
	t.Setenv("USER", "bob")
	put := func(t *testing.T, names ...string) string {
		t.Helper()
		dir := t.TempDir()
		for _, n := range names {
			// ada and dan are the readers of the state; the others are strangers.
			if n == "ada" || n == "dan" {
				putIdentity(t, dir, n, s.identityFile(t, n))
				continue
			}
			_, data := newIdentityFile(t, n, true)
			putIdentity(t, dir, n, data)
		}
		t.Setenv("PRAVO_IDENTITY_DIR", dir)
		resetIdentityState(t)
		return dir
	}

	t.Run("the only identity that reads the state", func(t *testing.T) {
		put(t, "ada", "eve", "fay")
		for _, args := range [][]string{{"state", "show", "net.vpc"}, {"output"}, {"protect", "status"}} {
			resetIdentityState(t)
			text, code := runPravo(t, s.dir, args...)
			if code != 0 || strings.Contains(text, "several identities") {
				t.Errorf("%v: exit %d\n%s", args, code, text)
			}
		}
		text, _ := runPravo(t, s.dir, "protect", "status")
		if !strings.Contains(text, "You          ada · reader key") {
			t.Errorf("status does not name ada:\n%s", text)
		}
	})
	t.Run("a name that matches the user wins", func(t *testing.T) {
		put(t, "ada", "dan", "eve")
		t.Setenv("USER", "dan")
		text, code := runPravo(t, s.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "You          dan · reader key") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("several identities that read it", func(t *testing.T) {
		put(t, "ada", "dan", "eve")
		text, code := runPravo(t, s.dir, "state", "show", "net.vpc")
		if code != 5 || !strings.Contains(text, "holds several identities (ada dan eve) and none is named like you (ada dan all read this state); choose one with --identity NAME") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if strings.Contains(text, secretA) {
			t.Error("the output has a secret")
		}
		resetIdentityState(t)
		if text, code := runPravo(t, s.dir, "state", "show", "net.vpc", "--identity", "dan"); code != 0 || !strings.Contains(text, secretA) {
			t.Errorf("--identity dan: exit %d\n%s", code, text)
		}
	})
	t.Run("none that reads it", func(t *testing.T) {
		put(t, "eve", "fay")
		text, code := runPravo(t, s.dir, "state", "show", "net.vpc")
		if code != 5 || !strings.Contains(text, "holds several identities (eve fay) and none is named like you; choose one") || strings.Contains(text, "all read this state") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a file that is no identity is no candidate", func(t *testing.T) {
		dir := put(t, "ada", "eve")
		if err := os.WriteFile(filepath.Join(dir, "notes"), []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		resetIdentityState(t)
		if text, code := runPravo(t, s.dir, "protect", "status"); code != 0 || !strings.Contains(text, "You          ada · reader key") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("the passphrase of the chosen identity only", func(t *testing.T) {
		dir := t.TempDir()
		// ada's file is sealed with the passphrase; eve's is a stranger's, sealed too.
		adaData, err := s.ada.MarshalSealed([]byte(testPass))
		if err != nil {
			t.Fatal(err)
		}
		putIdentity(t, dir, "ada", adaData)
		eve, err := protect.NewIdentity("eve", "")
		if err != nil {
			t.Fatal(err)
		}
		eveData, err := eve.MarshalSealed([]byte("another passphrase"))
		if err != nil {
			t.Fatal(err)
		}
		putIdentity(t, dir, "eve", eveData)
		t.Setenv("PRAVO_IDENTITY_DIR", dir)
		resetIdentityState(t)
		var prompts []string
		stdinIsTerminal = func() bool { return true }
		askPassphrase = func(prompt string) ([]byte, error) {
			prompts = append(prompts, prompt)
			return []byte(testPass), nil
		}
		text, code := runPravo(t, s.dir, "state", "show", "net.vpc")
		if code != 0 || !strings.Contains(text, secretA) {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if len(prompts) != 1 || prompts[0] != "Passphrase for ada: " {
			t.Errorf("prompts: %q", prompts)
		}
	})
	t.Run("a state that is not sealed asks for nothing", func(t *testing.T) {
		put(t, "eve", "fay")
		e := newS3Env(t, false)
		e.record(t, e.store(t), "a", "x")
		if text, code := runPravo(t, e.dir, "state", "list"); code != 0 || text != "a\n" {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

// protect init, which has no state to look in yet, chooses by the readers file.
func TestProtectInitChoosesTheIdentityOnTheReadersList(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	t.Setenv("USER", "bob")
	e := newS3Env(t, false)
	e.record(t, e.store(t), "a", "TOPSECRET")
	_, adaLine := person(t, ids, "ada")
	person(t, ids, "eve")
	e.readers(t, adaLine)
	text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, e.dir, "protect", "status"); code != 0 || !strings.Contains(text, "You          ada · reader key") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}

// The viewer chooses among several identity files by the locks of the state, too.
func TestViewerChoosesTheIdentityByTheLocks(t *testing.T) {
	ids := isolateIdentities(t)
	t.Setenv("USER", "bob")
	ada, _ := person(t, ids, "ada")
	person(t, ids, "eve")
	saved := out
	out = ui.NewPrinter(io.Discard, true)
	t.Cleanup(func() { out = saved })
	v := &viewerState{}
	defer v.close()
	if err := v.resolve(ada.Reader.Public()); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := v.identity(); id == nil || id.Name != "ada" {
		t.Errorf("the identity is %v", id)
	}
}
