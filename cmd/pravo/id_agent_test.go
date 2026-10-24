package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// testAgent is an ssh-agent that holds Ed25519 keys: it lists them and signs with
// them. It counts the signatures it makes.
type testAgent struct {
	mu       sync.Mutex
	keys     []ed25519.PrivateKey
	comments []string
	signs    int
}

func wire(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(binary.BigEndian.AppendUint32(b, uint32(len(p))), p...)
	}
	return b
}

func (a *testAgent) add(t *testing.T, comment string) ed25519.PublicKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a.keys, a.comments = append(a.keys, priv), append(a.comments, comment)
	return pub
}

func (a *testAgent) signCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.signs
}

func (a *testAgent) handle(body []byte) []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	blobOf := func(k ed25519.PrivateKey) []byte {
		return wire([]byte("ssh-ed25519"), k.Public().(ed25519.PublicKey))
	}
	switch body[0] {
	case 11: // request identities
		reply := binary.BigEndian.AppendUint32([]byte{12}, uint32(len(a.keys)))
		for i, k := range a.keys {
			reply = append(reply, wire(blobOf(k), []byte(a.comments[i]))...)
		}
		return reply
	case 13: // sign request: blob, data, flags
		n := binary.BigEndian.Uint32(body[1:])
		blob, rest := body[5:5+n], body[5+n:]
		m := binary.BigEndian.Uint32(rest)
		data := rest[4 : 4+m]
		for _, k := range a.keys {
			if string(blob) == string(blobOf(k)) {
				a.signs++
				sig := wire([]byte("ssh-ed25519"), ed25519.Sign(k, data))
				return append([]byte{14}, wire(sig)...)
			}
		}
	}
	return []byte{5} // failure
}

// useAgent makes the agent the one that pravo talks to, until the test ends.
func (a *testAgent) use(t *testing.T) {
	t.Helper()
	old := protect.DialAgentFunc
	t.Cleanup(func() { protect.DialAgentFunc = old })
	protect.DialAgentFunc = func() (io.ReadWriteCloser, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close()
			for {
				var hdr [4]byte
				if _, err := io.ReadFull(s, hdr[:]); err != nil {
					return
				}
				body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
				if _, err := io.ReadFull(s, body); err != nil {
					return
				}
				reply := a.handle(body)
				if _, err := s.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(reply))), reply...)); err != nil {
					return
				}
			}
		}()
		return c, nil
	}
}

func noAgent(t *testing.T) {
	t.Helper()
	old := protect.DialAgentFunc
	t.Cleanup(func() { protect.DialAgentFunc = old })
	protect.DialAgentFunc = func() (io.ReadWriteCloser, error) {
		return nil, os.ErrNotExist
	}
}

func TestIDNewCanSealTheIdentityUnderTheSSHAgent(t *testing.T) {
	ids := isolateIdentities(t)
	var ag testAgent
	pub := ag.add(t, "pravo")
	ag.use(t)
	dir := t.TempDir()
	askNewPassphrase = func(string) ([]byte, error) {
		t.Error("a passphrase was asked for")
		return nil, os.ErrInvalid
	}

	text, code := runPravo(t, dir, "id", "new", "--name", "ada-mac", "--unlock", "agent")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	fp := protect.SSHFingerprint(pub)
	reader := publicLine(t, text)
	for _, want := range []string{
		"Identity ada-mac created\n",
		"  signing  ssh-ed25519 " + protect.SSHFingerprint(reader.Sign) + "  new key, kept in the identity file\n",
		"  reader   " + reader.Key.String() + "  sealed under ssh-agent key " + fp + " in ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "passphrase") {
		t.Errorf("the output talks of a passphrase:\n%s", text)
	}
	if n := ag.signCount(); n != 2 {
		t.Errorf("the agent signed %d times, want 2 (the second is the check)", n)
	}
	file := filepath.Join(ids, "ada-mac")
	if runtime.GOOS != "windows" {
		if m := mode(t, file); m != 0o600 {
			t.Errorf("identity file mode %v, want 0600", m)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	id, err := protect.ParseIdentity(data, nil)
	if err != nil || !id.Reader.Public().Equal(reader.Key) {
		t.Fatalf("the file does not open to the printed key: %v", err)
	}
	id.Close()

	t.Run("id show reads the public parts and never calls the agent", func(t *testing.T) {
		noAgent(t)
		text, code := runPravo(t, dir, "id", "show", "--name", "ada-mac")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if got := publicLine(t, text); got.String() != reader.String() {
			t.Errorf("the line is %q, want %q", got.String(), reader.String())
		}
		if want := "  reader   " + reader.Key.String() + "  sealed under ssh-agent key " + fp + "\n"; !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
		text, code = runPravo(t, dir, "--json", "id", "show", "--name", "ada-mac")
		var shown struct {
			Sealed bool
			Unlock map[string]string
		}
		if code != 0 || json.Unmarshal([]byte(text), &shown) != nil || !shown.Sealed || shown.Unlock["by"] != "ssh-agent" || shown.Unlock["key"] != fp {
			t.Errorf("exit %d, document %s", code, text)
		}
	})
	t.Run("as JSON", func(t *testing.T) {
		text, code := runPravo(t, dir, "--json", "id", "new", "--name", "ada-json", "--unlock", "agent")
		var made struct {
			Unlock map[string]string
		}
		if code != 0 || json.Unmarshal([]byte(text), &made) != nil || made.Unlock["by"] != "ssh-agent" || made.Unlock["key"] != fp || made.Unlock["comment"] != "pravo" {
			t.Errorf("exit %d, document %s", code, text)
		}
	})
}

func TestIDNewUnlockAgentChoosesAKeyOfTheAgent(t *testing.T) {
	ids := isolateIdentities(t)
	var ag testAgent
	github := ag.add(t, "GitHub key")
	pravo := ag.add(t, "pravo ada")
	ag.use(t)
	dir := t.TempDir()

	t.Run("several keys and no choice", func(t *testing.T) {
		text, code := runPravo(t, dir, "id", "new", "--name", "ada-mac", "--unlock", "agent")
		for _, want := range []string{"several ssh-ed25519 keys", "--agent-key", protect.SSHFingerprint(github), "GitHub key", protect.SSHFingerprint(pravo), "pravo ada"} {
			if !strings.Contains(text, want) {
				t.Errorf("the output lacks %q:\n%s", want, text)
			}
		}
		if code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if _, err := os.Stat(filepath.Join(ids, "ada-mac")); err == nil {
			t.Error("an identity was written")
		}
		if ag.signCount() != 0 {
			t.Error("the agent was asked to sign before a key was chosen")
		}
	})
	t.Run("by part of the comment", func(t *testing.T) {
		text, code := runPravo(t, dir, "id", "new", "--name", "ada-mac", "--unlock", "agent", "--agent-key", "pravo")
		if code != 0 || !strings.Contains(text, "sealed under ssh-agent key "+protect.SSHFingerprint(pravo)) {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("by fingerprint", func(t *testing.T) {
		text, code := runPravo(t, dir, "id", "new", "--name", "ada-gh", "--unlock", "agent", "--agent-key", protect.SSHFingerprint(github))
		if code != 0 || !strings.Contains(text, "sealed under ssh-agent key "+protect.SSHFingerprint(github)) {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a key the agent does not hold", func(t *testing.T) {
		text, code := runPravo(t, dir, "id", "new", "--name", "ada-x", "--unlock", "agent", "--agent-key", "work")
		if code != 1 || !strings.Contains(text, `matches "work"`) || !strings.Contains(text, "GitHub key") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

func TestIDNewUnlockRefusals(t *testing.T) {
	ids := isolateIdentities(t)
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	dir := t.TempDir()
	var ag testAgent
	ag.add(t, "pravo")
	ag.use(t)

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"another way to unlock":   {[]string{"--unlock", "touch"}, "--unlock touch: use passphrase or agent"},
		"a key without the agent": {[]string{"--agent-key", "pravo"}, "--agent-key goes with --unlock agent"},
		"a job":                   {[]string{"--ci", "--out", filepath.Join(t.TempDir(), "bot.identity"), "--unlock", "agent"}, "--unlock agent is for a person"},
	} {
		args := append([]string{"id", "new", "--name", "ada"}, tc.args...)
		if text, code := runPravo(t, dir, args...); code != 1 || !strings.Contains(text, tc.want) {
			t.Errorf("%s: exit %d\n%s", name, code, text)
		}
	}
	t.Run("an agent that is not there", func(t *testing.T) {
		noAgent(t)
		text, code := runPravo(t, dir, "id", "new", "--name", "ada", "--unlock", "agent")
		if code != 1 || !strings.Contains(text, "ssh-agent is not reachable") {
			t.Errorf("exit %d\n%s", code, text)
		}
	})
	t.Run("an agent that holds no key", func(t *testing.T) {
		(&testAgent{}).use(t)
		text, code := runPravo(t, dir, "id", "new", "--name", "ada", "--unlock", "agent")
		if code != 1 || !strings.Contains(text, "holds no ssh-ed25519 key") {
			t.Errorf("exit %d\n%s", code, text)
		}
	})
	if _, err := os.Stat(filepath.Join(ids, "ada")); err == nil {
		t.Error("an identity was written by a refused command")
	}
	t.Run("an identity that exists is found before the agent is asked", func(t *testing.T) {
		if text, code := runPravo(t, dir, "id", "new", "--name", "ada"); code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		before := ag.signCount()
		text, code := runPravo(t, dir, "id", "new", "--name", "ada", "--unlock", "agent")
		if code != 1 || !strings.Contains(text, "identity ada already exists") || ag.signCount() != before {
			t.Errorf("exit %d, %d signatures more\n%s", code, ag.signCount()-before, text)
		}
	})
}

func TestResolveIdentityOpensAnIdentityUnderTheAgentWithoutAskingForAPassphrase(t *testing.T) {
	dir := isolateIdentities(t)
	var ag testAgent
	pub := ag.add(t, "pravo")
	ag.use(t)
	id, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	file, err := id.MarshalAgentSealed(pub)
	if err != nil {
		t.Fatal(err)
	}
	putIdentity(t, dir, "ada", file)
	askPassphrase = func(string) ([]byte, error) {
		t.Error("a passphrase was asked for")
		return nil, os.ErrInvalid
	}
	stdinIsTerminal = func() bool { return true }
	before := ag.signCount()
	got, err := resolveIdentity(identityOptions{Prompt: true})
	if err != nil || !got.Reader.Public().Equal(id.Reader.Public()) {
		t.Fatalf("resolve = %v, %v", got, err)
	}
	if _, err := resolveIdentity(identityOptions{Prompt: true}); err != nil {
		t.Fatal(err)
	}
	if n := ag.signCount() - before; n != 1 {
		t.Errorf("the agent signed %d times, want once for the process", n)
	}
	// With no terminal and no passphrase, the agent is what opens it; with no agent
	// the error names the agent and not a passphrase.
	resetIdentityState(t)
	stdinIsTerminal = func() bool { return false }
	noAgent(t)
	_, err = resolveIdentity(identityOptions{})
	if err == nil || !strings.Contains(err.Error(), "ssh-agent") || strings.Contains(err.Error(), "passphrase") {
		t.Errorf("no agent: %v", err)
	}
}

// A person adds a second key for themselves: an identity that the ssh-agent unlocks
// beside the one with a passphrase. Either opens the state, and the passphrase one
// stays as the way back when the agent is gone.
func TestASecondIdentityUnderTheAgentReadsTheStateOfTheFirst(t *testing.T) {
	requireSealing(t)
	isolateIdentities(t)
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	t.Setenv("PRAVO_IDENTITY_DIR", filepath.Join(t.TempDir(), "ids"))
	dir := specDir(t, map[string]string{"main.cue": "package t\n"})
	var ag testAgent
	ag.add(t, "pravo")
	ag.use(t)

	step := func(args ...string) (string, int) {
		t.Helper()
		resetIdentityState(t)
		return runPravo(t, dir, args...)
	}
	made, code := step("id", "new", "--name", "ada")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, made)
	}
	ada := publicLine(t, made)
	access := filepath.Join(dir, ".pravo", "access")
	writeReaders(t, access, ada.String())
	if text, code := step("protect", "init", "--encrypt", "--yes", "--allow-local"); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, text)
	}

	made, code = step("id", "new", "--name", "ada-mac", "--unlock", "agent")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, made)
	}
	mac := publicLine(t, made)
	writeReaders(t, access, ada.String(), mac.String())

	text, code := step("protect", "rewrap", "--identity", "ada")
	if code != 0 || !strings.Contains(text, "Wrapped the data keys for ada-mac") {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}

	// The passphrase is not needed any more, and the agent opens the state.
	t.Setenv("PRAVO_PASSPHRASE", "")
	before := ag.signCount()
	text, code = step("protect", "status", "--identity", "ada-mac")
	if code != 0 || !strings.Contains(text, "You          ada-mac · reader key "+mac.Key.String()+"\n") || !strings.Contains(text, "2 readers (ada ada-mac)") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	if ag.signCount() != before+1 {
		t.Errorf("opening the identity took %d signatures, want 1", ag.signCount()-before)
	}
	if text, code := step("state", "list", "--identity", "ada-mac"); code != 0 || strings.Contains(text, "Warning") {
		t.Errorf("state list: exit %d\n%s", code, text)
	}

	// Without the agent the identity does not open, and says why; the other does.
	// (status reports an identity that cannot be opened on its You line.)
	noAgent(t)
	text, code = step("protect", "status", "--identity", "ada-mac")
	if code != 0 || !strings.Contains(text, "identity ada-mac is unlocked by the ssh-agent, which is not reachable") || strings.Contains(text, "passphrase") {
		t.Errorf("no agent: exit %d\n%s", code, text)
	}
	// A read goes on without the values, as it does for a passphrase that is wrong.
	stderr := withStderr(t, func() { text, code = step("state", "list", "--identity", "ada-mac") })
	if code != 0 || !strings.Contains(stderr, "Warning: identity ada-mac is unlocked by the ssh-agent, which is not reachable") {
		t.Errorf("state list with no agent: exit %d, standard error %q\n%s", code, stderr, text)
	}
	// A command that writes stops: the identity is the one that cannot be opened.
	text, code = step("state", "rm", "net.vpc", "--identity", "ada-mac")
	if code != 5 || !strings.Contains(text, "you are not a reader of this state (identity ada-mac is unlocked by the ssh-agent, which is not reachable") {
		t.Errorf("state rm with no agent: exit %d\n%s", code, text)
	}
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	text, code = step("protect", "status", "--identity", "ada")
	if code != 0 || !strings.Contains(text, "You          ada · reader key "+ada.Key.String()+"\n") {
		t.Errorf("the passphrase identity: exit %d\n%s", code, text)
	}
}
