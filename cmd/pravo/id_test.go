package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// publicLine finds the line that "pravo id" prints for the readers file: the
// last line of the output, indented.
func publicLine(t *testing.T, text string) protect.Reader {
	t.Helper()
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "  ") {
		t.Fatalf("the last line %q is not indented", last)
	}
	r, err := protect.ParseReaderLine(strings.TrimSpace(last))
	if err != nil {
		t.Fatalf("the last line is not a readers line: %v\n%s", err, text)
	}
	return r
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestIDNewWritesASealedIdentityAndPrintsItsLine(t *testing.T) {
	ids := isolateIdentities(t)
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	dir := t.TempDir()
	text, code := runPravo(t, dir, "id", "new", "--name", "ada")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	reader := publicLine(t, text)
	if reader.Name != "ada" {
		t.Errorf("the line is for %q", reader.Name)
	}
	for _, want := range []string{
		"Identity ada created\n",
		"  signing  ssh-ed25519 " + protect.SSHFingerprint(reader.Sign) + "  new key, kept in the identity file\n",
		"  reader   " + reader.Key.String() + "  sealed with a passphrase in ",
		"Give whoever keeps .pravo/access/readers.txt this line, and nothing else:\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "…") {
		t.Error("the output elides a key")
	}
	file := filepath.Join(ids, "ada")
	if runtime.GOOS != "windows" {
		if m := mode(t, file); m != 0o600 {
			t.Errorf("identity file mode %v, want 0600", m)
		}
		if m := mode(t, ids); m != 0o700 {
			t.Errorf("identity directory mode %v, want 0700", m)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"seal"`) {
		t.Error("the identity file is not sealed")
	}
	id, err := protect.ParseIdentity(data, func() ([]byte, error) { return []byte(testPass), nil })
	if err != nil || !id.Reader.Public().Equal(reader.Key) {
		t.Fatalf("the file does not open to the printed key: %v", err)
	}
	if entries(t, dir) != nil && len(entries(t, dir)) != 0 {
		t.Errorf("id new wrote to the working directory: %v", entries(t, dir))
	}
}

func TestIDNewNeverOverwritesAnIdentity(t *testing.T) {
	ids := isolateIdentities(t)
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	dir := t.TempDir()
	if text, code := runPravo(t, dir, "id", "new", "--name", "ada"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	before, _ := os.ReadFile(filepath.Join(ids, "ada"))
	text, code := runPravo(t, dir, "id", "new", "--name", "ada")
	if code != 1 || !strings.Contains(text, "Error: identity ada already exists at ") {
		t.Fatalf("exit %d, want 1 and the refusal\n%s", code, text)
	}
	after, _ := os.ReadFile(filepath.Join(ids, "ada"))
	if string(before) != string(after) {
		t.Error("the identity was overwritten")
	}
	// The refusal comes before the passphrase is asked for.
	asked := false
	askNewPassphrase = func(string) ([]byte, error) { asked = true; return []byte(testPass), nil }
	stdinIsTerminal = func() bool { return true }
	if _, code := runPravo(t, dir, "id", "new", "--name", "ada"); code != 1 || asked {
		t.Errorf("exit %d, asked %v; want the refusal before the prompt", code, asked)
	}
}

func TestIDNewNameDefaultsToTheUser(t *testing.T) {
	ids := isolateIdentities(t)
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	t.Setenv("USER", "grace")
	if text, code := runPravo(t, t.TempDir(), "id", "new"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if _, err := os.Stat(filepath.Join(ids, "grace")); err != nil {
		t.Error(err)
	}
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "")
	if text, code := runPravo(t, t.TempDir(), "id", "new"); code != 1 || !strings.Contains(text, "--name NAME") {
		t.Errorf("exit %d, want 1 and a request for a name\n%s", code, text)
	}
	if text, code := runPravo(t, t.TempDir(), "id", "new", "--name", "../x"); code != 1 || !strings.Contains(text, "identity name") {
		t.Errorf("exit %d, want 1 and a refusal of the name\n%s", code, text)
	}
}

func TestIDNewPassphrase(t *testing.T) {
	ids := isolateIdentities(t)
	dir := t.TempDir()
	t.Run("stdin is not a terminal and nothing is set", func(t *testing.T) {
		text, code := runPravo(t, dir, "id", "new", "--name", "ada")
		if code != 1 || !strings.Contains(text, "stdin is not a terminal") || !strings.Contains(text, "PRAVO_PASSPHRASE") || !strings.Contains(text, "--ci") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if _, err := os.Stat(filepath.Join(ids, "ada")); err == nil {
			t.Error("an identity was written")
		}
	})
	t.Run("too short", func(t *testing.T) {
		t.Setenv("PRAVO_PASSPHRASE", "short")
		text, code := runPravo(t, dir, "id", "new", "--name", "ada")
		if code != 1 || !strings.Contains(text, "at least 8 characters") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if _, err := os.Stat(filepath.Join(ids, "ada")); err == nil {
			t.Error("an identity was written")
		}
	})
	t.Run("from the terminal", func(t *testing.T) {
		stdinIsTerminal = func() bool { return true }
		defer func() { stdinIsTerminal = func() bool { return false } }()
		var prompt string
		askNewPassphrase = func(p string) ([]byte, error) { prompt = p; return []byte(testPass), nil }
		text, code := runPravo(t, dir, "id", "new", "--name", "ada")
		if code != 0 || !strings.Contains(prompt, "ada") {
			t.Fatalf("exit %d, prompt %q\n%s", code, prompt, text)
		}
		if strings.Contains(text, testPass) {
			t.Error("the passphrase is in the output")
		}
	})
}

func TestIDNewReusesAnSSHPublicKey(t *testing.T) {
	ids := isolateIdentities(t)
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := t.TempDir()
	private := filepath.Join(keys, "id_ed25519")
	const secretBody = "-----BEGIN OPENSSH PRIVATE KEY-----\nSECRETKEYBODY\n-----END OPENSSH PRIVATE KEY-----\n"
	if err := os.WriteFile(private, []byte(secretBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private+".pub", []byte("ssh-ed25519 "+protect.SSHBlob(pub)+" ada@laptop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	text, code := runPravo(t, dir, "id", "new", "--name", "ada", "--ssh", private)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	reader := publicLine(t, text)
	if !reader.Sign.Equal(pub) {
		t.Error("the line does not carry the ssh key")
	}
	if !strings.Contains(text, "  signing  ssh-ed25519 "+protect.SSHFingerprint(pub)+"  from ") || !strings.Contains(text, "id_ed25519.pub") {
		t.Errorf("the signing line does not say where the key is from:\n%s", text)
	}
	data, _ := os.ReadFile(filepath.Join(ids, "ada"))
	if strings.Contains(string(data), "SECRETKEYBODY") {
		t.Error("the private key was read into the identity")
	}

	t.Run("the .pub file itself", func(t *testing.T) {
		if text, code := runPravo(t, dir, "id", "new", "--name", "dan", "--ssh", private+".pub"); code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a key of another type", func(t *testing.T) {
		p := filepath.Join(keys, "id_rsa.pub")
		os.WriteFile(p, []byte("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7 me@laptop\n"), 0o644)
		text, code := runPravo(t, dir, "id", "new", "--name", "eve", "--ssh", p)
		if code != 1 || !strings.Contains(text, "ssh-ed25519") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if _, err := os.Stat(filepath.Join(ids, "eve")); err == nil {
			t.Error("an identity was written")
		}
	})
	t.Run("a private key is never read", func(t *testing.T) {
		only := filepath.Join(t.TempDir(), "id_ed25519")
		os.WriteFile(only, []byte(secretBody), 0o600)
		text, code := runPravo(t, dir, "id", "new", "--name", "fay", "--ssh", only)
		if code != 1 || strings.Contains(text, "SECRETKEYBODY") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a file that does not exist", func(t *testing.T) {
		if text, code := runPravo(t, dir, "id", "new", "--name", "gus", "--ssh", filepath.Join(keys, "missing")); code != 1 {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

func TestIDNewForAJob(t *testing.T) {
	ids := isolateIdentities(t)
	work := t.TempDir()
	out := filepath.Join(work, "deploy-bot.identity")
	text, code := runPravo(t, work, "id", "new", "--ci", "--name", "deploy-bot", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	// The line of the public key is not always the last: Windows prints a note about
	// who can read the file after it.
	var public string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "  public   ") {
			public = l
		}
	}
	reader := publicLine(t, strings.Replace(public, "  public   ", "  ", 1))
	for _, want := range []string{
		"Identity deploy-bot created for a job. It has no passphrase: protect the file.\n",
		"  private  " + out + "   (store it as the CI secret PRAVO_IDENTITY, then delete this file)\n",
		"  public   " + reader.String() + "\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	if runtime.GOOS != "windows" {
		if m := mode(t, out); m != 0o600 {
			t.Errorf("identity file mode %v, want 0600", m)
		}
	}
	data, _ := os.ReadFile(out)
	id, err := protect.ParseIdentity(data, nil)
	if err != nil || !id.Reader.Public().Equal(reader.Key) {
		t.Fatalf("the file does not open without a passphrase: %v", err)
	}
	if _, err := os.Stat(ids); err == nil {
		t.Error("--ci wrote under the configuration directory")
	}
	if _, err := os.Stat(filepath.Dir(ids)); err == nil {
		t.Error("--ci made the configuration directory")
	}

	t.Run("it never overwrites", func(t *testing.T) {
		text, code := runPravo(t, work, "id", "new", "--ci", "--name", "deploy-bot", "--out", out)
		if code != 1 || !strings.Contains(text, "already exists") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		after, _ := os.ReadFile(out)
		if string(after) != string(data) {
			t.Error("the file was overwritten")
		}
	})
	t.Run("it needs --out", func(t *testing.T) {
		if text, code := runPravo(t, work, "id", "new", "--ci", "--name", "x"); code != 1 || !strings.Contains(text, "--out") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if got := entries(t, work); len(got) != 1 {
			t.Errorf("files in the directory: %v", got)
		}
	})
	t.Run("--out is for --ci", func(t *testing.T) {
		t.Setenv("PRAVO_PASSPHRASE", testPass)
		if text, code := runPravo(t, work, "id", "new", "--name", "x", "--out", filepath.Join(work, "x")); code != 1 || !strings.Contains(text, "--ci") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("the file is usable as PRAVO_IDENTITY, as contents and as a path", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY", string(data))
		got, err := resolveIdentity(identityOptions{})
		if err != nil || !got.Reader.Public().Equal(reader.Key) {
			t.Fatalf("contents: %v", err)
		}
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY", out)
		got, err = resolveIdentity(identityOptions{})
		if err != nil || !got.Reader.Public().Equal(reader.Key) {
			t.Fatalf("path: %v", err)
		}
	})
}

func TestIDShow(t *testing.T) {
	ids := isolateIdentities(t)
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	dir := t.TempDir()

	t.Run("no identity", func(t *testing.T) {
		text, code := runPravo(t, dir, "id", "show")
		if code != 1 || !strings.Contains(text, "Error: no identity found in ") || !strings.Contains(text, "Make one with: pravo id new --name NAME") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})

	made, code := runPravo(t, dir, "id", "new", "--name", "ada")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, made)
	}
	reader := publicLine(t, made)
	t.Setenv("PRAVO_PASSPHRASE", "")
	resetIdentityState(t)

	t.Run("a sealed identity, without a passphrase", func(t *testing.T) {
		text, code := runPravo(t, dir, "id", "show")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if got := publicLine(t, text); got.String() != reader.String() {
			t.Errorf("the line is %q, want %q", got.String(), reader.String())
		}
		for _, want := range []string{"Identity ada (", filepath.Join(ids, "ada"), "  signing  ssh-ed25519 SHA256:", "  reader   " + reader.Key.String() + "  sealed with a passphrase"} {
			if !strings.Contains(text, want) {
				t.Errorf("the output lacks %q:\n%s", want, text)
			}
		}
	})
	t.Run("by name", func(t *testing.T) {
		if text, code := runPravo(t, dir, "id", "show", "--name", "ada"); code != 0 || !strings.Contains(text, "Identity ada") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if text, code := runPravo(t, dir, "id", "show", "--name", "bob"); code != 1 || !strings.Contains(text, "no identity named bob") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("an identity of a job in the environment", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "bot.identity")
		text, code := runPravo(t, dir, "id", "new", "--ci", "--name", "bot", "--out", out)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		data, _ := os.ReadFile(out)
		t.Setenv("PRAVO_IDENTITY", string(data))
		text, code = runPravo(t, dir, "id", "show")
		if code != 0 || !strings.Contains(text, "Identity bot (PRAVO_IDENTITY)") || !strings.Contains(text, "no passphrase") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if strings.Contains(text, strings.TrimSpace(strings.Split(string(data), `"key"`)[1][:20])) {
			t.Error("the output shows part of the private key")
		}
	})
	t.Run("a value that is not an identity is not repeated", func(t *testing.T) {
		t.Setenv("PRAVO_IDENTITY", "{\"secret\":\"SECRET-TOKEN\"}")
		text, code := runPravo(t, dir, "id", "show")
		if code != 1 || strings.Contains(text, "SECRET-TOKEN") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

func TestIDJSON(t *testing.T) {
	isolateIdentities(t)
	t.Setenv("PRAVO_PASSPHRASE", testPass)
	dir := t.TempDir()
	text, code := runPravo(t, dir, "--json", "id", "new", "--name", "ada")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	var made struct {
		Name, Path, Line string
		Reader           string
		Signing          map[string]string
	}
	if err := json.Unmarshal([]byte(text), &made); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if made.Name != "ada" || made.Signing["fingerprint"] == "" || !strings.HasPrefix(made.Reader, "x25519:") {
		t.Errorf("document = %+v", made)
	}
	if _, err := protect.ParseReaderLine(made.Line); err != nil {
		t.Errorf("the line does not parse: %v", err)
	}
	text, code = runPravo(t, dir, "--json", "id", "show")
	var shown struct{ Line string }
	if code != 0 || json.Unmarshal([]byte(text), &shown) != nil || shown.Line != made.Line {
		t.Errorf("exit %d, document %s", code, text)
	}
}

func TestIDCommandsTakeNoStrayArguments(t *testing.T) {
	isolateIdentities(t)
	for _, args := range [][]string{{"id", "stray"}, {"id", "new", "stray"}, {"id", "show", "stray"}} {
		if text, code := runPravo(t, t.TempDir(), args...); code != 1 || !strings.Contains(text, "stray") {
			t.Errorf("%v: exit %d\n%s", args, code, text)
		}
	}
}
