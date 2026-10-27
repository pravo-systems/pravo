package protect

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/env"
)

func mustReader(t *testing.T, name string) (Reader, *ReaderKey) {
	t.Helper()
	k, err := GenerateReaderKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	return Reader{Name: name, Sign: pub, Key: k.Public()}, k
}

func TestReaderKeyText(t *testing.T) {
	k, _ := GenerateReaderKey()
	s := k.Public().String()
	if !strings.HasPrefix(s, "x25519:") || len(s) != len("x25519:")+43 {
		t.Fatalf("bad text form %q", s)
	}
	p, err := ParsePublic(s)
	if err != nil || !p.Equal(k.Public()) {
		t.Fatal("round trip", err)
	}
	k2, err := ReaderKeyFromBytes(k.Bytes())
	if err != nil || !k2.Public().Equal(k.Public()) {
		t.Fatal("private round trip", err)
	}
	for _, bad := range []string{"", "x25519:", s + "=", s[:len(s)-1], "x25519:" + strings.Repeat("A", 43), // all zero: low order
		"X25519:" + s[7:], "ssh-ed25519:" + s[7:], s + "A", " " + s} {
		if _, err := ParsePublic(bad); err == nil {
			t.Errorf("ParsePublic(%q) accepted", bad)
		}
	}
	// base64url, not std: '+' and '/' are not allowed
	if _, err := ParsePublic("x25519:" + strings.Repeat("+", 43)); err == nil {
		t.Error("std alphabet accepted")
	}
}

func TestWrapUnwrap(t *testing.T) {
	r, k := mustReader(t, "ada")
	_, other := mustReader(t, "eve")
	dk := NewDataKey()
	c := WrapCtx{Lineage: "L1", Group: "g1"}
	box, err := Wrap(dk, r.Key, c)
	if err != nil || len(box) != 80 {
		t.Fatal(len(box), err)
	}
	got, err := Unwrap(box, k, c)
	if err != nil || !got.Equal(dk) {
		t.Fatal("unwrap", err)
	}
	box2, _ := Wrap(dk, r.Key, c)
	if bytes.Equal(box, box2) {
		t.Error("two wraps are equal: ephemeral key not fresh")
	}
	for name, f := range map[string]func() error{
		"other reader": func() error { _, err := Unwrap(box, other, c); return err },
		"other group":  func() error { _, err := Unwrap(box, k, WrapCtx{"L1", "g2"}); return err },
		"other state":  func() error { _, err := Unwrap(box, k, WrapCtx{"L2", "g1"}); return err },
		"truncated":    func() error { _, err := Unwrap(box[:79], k, c); return err },
		"empty":        func() error { _, err := Unwrap(nil, k, c); return err },
		"bit flip":     func() error { b := bytes.Clone(box); b[50] ^= 1; _, err := Unwrap(b, k, c); return err },
	} {
		if err := f(); !errors.Is(err, ErrAuth) {
			t.Errorf("%s: want ErrAuth, got %v", name, err)
		}
	}
}

func TestSealOpenBinding(t *testing.T) {
	dk := NewDataKey()
	c := Ctx{Lineage: "L1", Group: "g1", Kind: KindRecord, Name: "db.password", Generation: "tx1", Revision: 3}
	type rec struct {
		Attributes map[string]any `json:"attributes"`
		Private    []byte         `json:"private"`
	}
	in := rec{Attributes: map[string]any{"password": "hunter2"}, Private: []byte{1, 2, 3}}
	s, err := SealJSON(dk, c, in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	if !bytes.Contains(raw, []byte(`"alg":"hpke-x25519-aes256gcm","group":"g1","ct":"`)) || bytes.Contains(raw, []byte("hunter2")) {
		t.Fatalf("unexpected stored shape %s", raw)
	}
	var out rec
	if err := OpenJSON(dk, c, s, &out); err != nil || out.Attributes["password"] != "hunter2" {
		t.Fatal("round trip", err)
	}
	s2, _ := SealJSON(dk, c, in)
	if bytes.Equal(s.CT, s2.CT) {
		t.Error("sealing twice gave equal bytes: nonce not random")
	}
	// Moving the blob anywhere else must fail.
	moves := map[string]Ctx{}
	for name, mut := range map[string]func(*Ctx){
		"address":    func(c *Ctx) { c.Name = "web.password" },
		"revision":   func(c *Ctx) { c.Revision = 4 },
		"generation": func(c *Ctx) { c.Generation = "tx2" },
		"lineage":    func(c *Ctx) { c.Lineage = "L2" },
		"kind":       func(c *Ctx) { c.Kind = KindOutputs },
	} {
		m := c
		mut(&m)
		moves[name] = m
	}
	for name, m := range moves {
		if err := OpenJSON(dk, m, s, &out); !errors.Is(err, ErrAuth) {
			t.Errorf("moved to another %s: want ErrAuth, got %v", name, err)
		}
	}
	// Group: the stored group must match, and the aad carries it too.
	g2 := c
	g2.Group = "g2"
	if err := OpenJSON(dk, g2, s, &out); err == nil {
		t.Error("opened under another group")
	}
	forged := s
	forged.Group = "g2"
	if err := OpenJSON(dk, g2, forged, &out); !errors.Is(err, ErrAuth) {
		t.Errorf("relabelled group: want ErrAuth, got %v", err)
	}
	if err := OpenJSON(NewDataKey(), c, s, &out); !errors.Is(err, ErrAuth) {
		t.Error("opened with another key")
	}
	bad := s
	bad.CT = bytes.Clone(s.CT)
	bad.CT[len(bad.CT)-1] ^= 1
	if err := OpenJSON(dk, c, bad, &out); !errors.Is(err, ErrAuth) {
		t.Error("bit flip accepted")
	}
	bad.CT = s.CT[:10]
	if err := OpenJSON(dk, c, bad, &out); !errors.Is(err, ErrAuth) {
		t.Error("short ciphertext accepted")
	}
	old := s
	old.Alg = "hpke-x25519-chacha20"
	if err := OpenJSON(dk, c, old, &out); err == nil || errors.Is(err, ErrAuth) {
		t.Errorf("unknown alg should be named, got %v", err)
	}
	if len(s.CT) != len(raw)*0+len(s.CT) || len(s.CT)-Overhead < 20 {
		t.Error("overhead")
	}
	// AAD framing: ("ab","c") must differ from ("a","bc").
	a := Ctx{Name: "ab", Generation: "c"}.AAD()
	b := Ctx{Name: "a", Generation: "bc"}.AAD()
	if bytes.Equal(a, b) {
		t.Error("ambiguous AAD")
	}
}

func TestGroupLifecycle(t *testing.T) {
	ada, adaK := mustReader(t, "ada")
	dan, danK := mustReader(t, "dan")
	eve, eveK := mustReader(t, "eve")
	g, dk, err := NewGroup("g1", "L1", []Reader{ada, dan})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []*ReaderKey{adaK, danK} {
		got, err := g.Unlock("L1", k)
		if err != nil || !got.Equal(dk) {
			t.Fatal("unlock", err)
		}
	}
	if _, err := g.Unlock("L1", eveK); !errors.Is(err, ErrNotReader) {
		t.Errorf("eve: want ErrNotReader, got %v", err)
	}
	if _, err := g.Unlock("L2", adaK); !errors.Is(err, ErrAuth) {
		t.Errorf("other lineage: want ErrAuth, got %v", err)
	}
	missing, extra := g.Diff([]Reader{ada, dan, eve})
	if len(missing) != 1 || missing[0] != "eve" || len(extra) != 0 {
		t.Fatal(missing, extra)
	}
	before := bytes.Clone(g.Locks[0].Box)
	if err := g.Rewrap("L1", dk, []Reader{ada, dan, eve}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, g.Locks[0].Box) || len(g.Locks) != 3 {
		t.Error("existing locks must be kept byte for byte")
	}
	if got, err := g.Unlock("L1", eveK); err != nil || !got.Equal(dk) {
		t.Fatal("eve after rewrap", err)
	}
	if err := g.Rewrap("L1", dk, []Reader{ada, eve}); err != nil { // dan leaves
		t.Fatal(err)
	}
	if _, err := g.Unlock("L1", danK); !errors.Is(err, ErrNotReader) {
		t.Error("dan still has a lock")
	}
	raw, _ := json.Marshal(g)
	var back Group
	if err := json.Unmarshal(raw, &back); err != nil || len(back.Locks) != 2 {
		t.Fatal("group JSON round trip", err)
	}
	if got, err := back.Unlock("L1", adaK); err != nil || !got.Equal(dk) {
		t.Fatal("after JSON", err)
	}
}

func TestReadersFile(t *testing.T) {
	a, _ := mustReader(t, "ada")
	d, _ := mustReader(t, "dan")
	good := "\xef\xbb\xbf# readers\r\n\r\n" + a.String() + "\r\n  \t" + strings.ReplaceAll(d.String(), " ", "\t") + "  # dan, laptop\r\n"
	rs, err := ParseReaders(strings.NewReader(good))
	if err != nil || len(rs) != 2 || rs[0].Name != "ada" || rs[1].Line != 4 {
		t.Fatal(rs, err)
	}
	if !rs[1].Key.Equal(d.Key) || !rs[1].Sign.Equal(d.Sign) {
		t.Error("fields")
	}
	// No trailing newline; order and comments do not change the set id.
	rs2, err := ParseReaders(strings.NewReader(d.String() + "\n" + a.String()))
	if err != nil || SetID(rs) != SetID(rs2) {
		t.Fatal("set id depends on order", err)
	}
	if string(FormatReaders(rs2)) != d.String()+"\n"+a.String()+"\n" {
		t.Error("format")
	}
	line := a.String()
	f := strings.Fields(line)
	for name, bad := range map[string]string{
		"three fields":  strings.Join(f[:3], " "),
		"key comment":   line + " ada@laptop",
		"rsa":           "ada ssh-rsa " + f[2] + " " + f[3],
		"empty name":    " ssh-ed25519 " + f[2] + " " + f[3],
		"bad name":      "../ada " + strings.Join(f[1:], " "),
		"bad ssh":       "ada ssh-ed25519 AAAA " + f[3],
		"bad reader":    "ada ssh-ed25519 " + f[2] + " x25519:AAAA",
		"dup name":      line + "\n" + strings.Replace(d.String(), "dan", "ADA", 1),
		"dup key":       line + "\n" + "dan " + strings.Join(f[1:], " "),
		"nul":           line + "\x00",
		"long line":     "ada " + strings.Repeat("x", 5000),
		"x25519 sshkey": "ada ssh-ed25519 " + f[3] + " " + f[3],
	} {
		if _, err := ParseReaders(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), "line 1") && name != "dup name" && name != "dup key" && name != "nul" && name != "long line" {
			t.Errorf("%s: error does not name the line: %v", name, err)
		}
	}
	if e, _ := ParseReaders(strings.NewReader("")); len(e) != 0 {
		t.Error("empty file")
	}
}

// The line format is checked against OpenSSH itself where it is installed.
func TestSSHLineAgainstOpenSSH(t *testing.T) {
	kg, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("no ssh-keygen")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "k")
	if out, err := exec.Command(kg, "-q", "-t", "ed25519", "-N", "", "-C", "me@host", "-f", key).CombinedOutput(); err != nil {
		t.Skip("ssh-keygen failed:", string(out))
	}
	pubLine, _ := os.ReadFile(key + ".pub")
	pub, err := ParseSSHPublicLine(string(pubLine))
	if err != nil {
		t.Fatal(err)
	}
	if got := "ssh-ed25519 " + SSHBlob(pub); got != strings.Join(strings.Fields(string(pubLine))[:2], " ") {
		t.Errorf("blob differs from OpenSSH:\n %s\n %s", got, pubLine)
	}
	out, _ := exec.Command(kg, "-l", "-f", key+".pub").Output()
	if fp := strings.Fields(string(out))[1]; fp != SSHFingerprint(pub) {
		t.Errorf("fingerprint %s, OpenSSH says %s", SSHFingerprint(pub), fp)
	}
	for _, other := range []string{"ssh-rsa AAAAB3NzaC1yc2E", "ecdsa-sha2-nistp256 AAAA", "sk-ssh-ed25519@openssh.com AAAA", "ssh-ed25519", ""} {
		if _, err := ParseSSHPublicLine(other); err == nil {
			t.Errorf("accepted %q", other)
		}
	}
}

func TestConfigHashKeyed(t *testing.T) {
	h1, h2 := NewHashKey(), NewHashKey()
	cfg := []byte(`{"password":"hunter2"}`)
	if first, again := h1.Sum("L", cfg), h1.Sum("L", cfg); first != again {
		t.Error("not deterministic")
	}
	if h1.Sum("L", cfg) == h2.Sum("L", cfg) || h1.Sum("L", cfg) == h1.Sum("M", cfg) || h1.Sum("L", cfg) == h1.Sum("L", []byte(`{"password":"hunter3"}`)) {
		t.Error("key, lineage or input ignored")
	}
	if len(h1.Sum("L", cfg)) != 16 {
		t.Error("length")
	}
}

func TestIdentitySealed(t *testing.T) {
	id, err := NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := id.MarshalSealed([]byte("short")); err == nil {
		t.Error("short passphrase accepted")
	}
	file, err := id.MarshalSealed([]byte("correct horse battery"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(file, []byte(`"seed"`)) || bytes.Contains(file, []byte(`"key"`)) {
		t.Fatal("secret in the clear in a sealed file")
	}
	ask := func(p string) func() ([]byte, error) { return func() ([]byte, error) { return []byte(p), nil } }
	got, err := ParseIdentity(file, ask("correct horse battery"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Reader.Public().Equal(id.Reader.Public()) || !got.SignPub.Equal(id.SignPub) || got.Line() != id.Line() {
		t.Error("identity changed")
	}
	sig, err := got.Sign([]byte("m"))
	if err != nil || !ed25519.Verify(got.SignPub, []byte("m"), sig) {
		t.Error("sign", err)
	}
	if _, err := ParseIdentity(file, ask("wrong horse battery")); err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Errorf("wrong passphrase: %v", err)
	}
	// Changing the cost or the name without the passphrase must fail, not downgrade.
	var f map[string]any
	json.Unmarshal(file, &f)
	f["seal"].(map[string]any)["iter"] = 100_000
	weak, _ := json.Marshal(f)
	if _, err := ParseIdentity(weak, ask("correct horse battery")); err == nil {
		t.Error("tampered iteration count accepted")
	}
	f["seal"].(map[string]any)["iter"] = 10
	weak, _ = json.Marshal(f)
	if _, err := ParseIdentity(weak, ask("correct horse battery")); err == nil {
		t.Error("absurdly weak count accepted")
	}
	if _, err := ParseIdentity(file, nil); err == nil {
		t.Error("no way to ask, no error")
	}
	got.Close()
}

func TestIdentityCIAndSSHReference(t *testing.T) {
	id, _ := NewIdentity("deploy-bot", "")
	file, err := id.MarshalUnsealed()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseIdentity(file, nil) // never asks
	if err != nil || got.Line() != id.Line() {
		t.Fatal(err)
	}
	if _, err := got.Sign([]byte("x")); err != nil {
		t.Error(err)
	}
	// An identity that points at an existing ssh key holds no signing secret.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	ref, err := NewIdentity("ben", "ssh-ed25519 "+SSHBlob(pub)+" ben@laptop")
	if err != nil || ref.SignVia != "ssh-agent" || !ref.SignPub.Equal(pub) {
		t.Fatal(ref, err)
	}
	if _, err := ref.Sign(nil); err == nil {
		t.Error("signed without a key")
	}
	file, _ = ref.MarshalSealed([]byte("a long passphrase"))
	back, err := ParseIdentity(file, func() ([]byte, error) { return []byte("a long passphrase"), nil })
	if err != nil || back.Line() != ref.Line() {
		t.Fatal(err)
	}
	if _, err := NewIdentity("ben", "ssh-rsa AAAA"); err == nil {
		t.Error("rsa accepted")
	}
	if _, err := NewIdentity("../x", ""); err == nil {
		t.Error("bad name")
	}
	// the printed line parses as a readers line
	rd, err := ParseReaderLine(id.Line())
	if err != nil || rd.Name != "deploy-bot" {
		t.Fatal(err)
	}
}

func TestFileModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg", "pravo", "id")
	p := filepath.Join(dir, "ada")
	if err := WritePrivateFile(p, []byte(`{"seal":1}`), false); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFile(p, []byte("x"), false); err == nil {
		t.Error("overwrote without replace")
	}
	if err := WritePrivateFile(p, []byte(`{"seal":2}`), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"seal":2}` {
		t.Error("replace")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("temporary files left behind: %v", ents)
	}
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	st, _ := os.Stat(p)
	sd, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o600 || sd.Mode().Perm() != 0o700 {
		t.Errorf("file %v dir %v", st.Mode().Perm(), sd.Mode().Perm())
	}
	// SaveIdentity makes both ~/.config/pravo and ~/.config/pravo/id private.
	cfg := filepath.Join(t.TempDir(), ".config", "pravo")
	t.Setenv("PRAVO_CONFIG_DIR", cfg)
	t.Setenv("PRAVO_IDENTITY_DIR", "")
	ip, err := SaveIdentity(env.OS(), "dan", []byte("{}"), false)
	if err != nil || ip != filepath.Join(cfg, "id", "dan") {
		t.Fatal(ip, err)
	}
	for _, d := range []string{cfg, filepath.Join(cfg, "id")} {
		if sd, _ := os.Stat(d); sd.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %v", d, sd.Mode().Perm())
		}
	}
	if sd, _ := os.Stat(filepath.Dir(cfg)); sd.Mode().Perm() != 0o755 {
		t.Errorf(".config has mode %v", sd.Mode().Perm())
	}
	// With PRAVO_IDENTITY_DIR set, the identity goes there and the configuration
	// directory is left alone.
	elsewhere := t.TempDir()
	custom := filepath.Join(elsewhere, "keys", "ids")
	cfg2 := filepath.Join(elsewhere, "cfg")
	t.Setenv("PRAVO_CONFIG_DIR", cfg2)
	t.Setenv("PRAVO_IDENTITY_DIR", custom)
	if d, err := IdentityDir(env.OS()); err != nil || d != custom {
		t.Fatalf("IdentityDir = %q, %v; want %q", d, err, custom)
	}
	if ip, err := IdentityPath(env.OS(), "eve"); err != nil || ip != filepath.Join(custom, "eve") {
		t.Fatalf("IdentityPath = %q, %v", ip, err)
	}
	ip, err = SaveIdentity(env.OS(), "eve", []byte("{}"), false)
	if err != nil || ip != filepath.Join(custom, "eve") {
		t.Fatal(ip, err)
	}
	if sd, err := os.Stat(custom); err != nil || sd.Mode().Perm() != 0o700 {
		t.Errorf("%s: %v %v", custom, sd, err)
	}
	if _, err := os.Stat(cfg2); err == nil {
		t.Error("the configuration directory was made")
	}
	if _, err := SaveIdentity(env.OS(), "eve", []byte("{}"), false); err == nil {
		t.Error("an identity that exists was replaced")
	}
	t.Setenv("PRAVO_IDENTITY_DIR", "")
	if d, err := IdentityDir(env.OS()); err != nil || d != filepath.Join(cfg2, "id") {
		t.Fatalf("IdentityDir without the variable = %q, %v", d, err)
	}
	// A loose unsealed identity is refused; a loose sealed one is a warning.
	os.WriteFile(filepath.Join(dir, "ci"), []byte(`{"v":1}`), 0o644)
	if _, _, err := ReadIdentityFile(filepath.Join(dir, "ci")); err == nil {
		t.Error("loose unsealed identity read")
	}
	// The word is not the key: a CI identity named "seal" is still unsealed.
	os.WriteFile(filepath.Join(dir, "named"), []byte(`{"v":1,"name":"seal","reader":{"key":"AAAA"}}`), 0o644)
	if _, _, err := ReadIdentityFile(filepath.Join(dir, "named")); err == nil {
		t.Error(`loose unsealed identity named "seal" read`)
	}
	os.Chmod(p, 0o640)
	if _, w, err := ReadIdentityFile(p); err != nil || w == "" {
		t.Errorf("loose sealed identity: %q %v", w, err)
	}
	// An existing directory keeps its mode (it may be the user's ~/.config).
	other := t.TempDir()
	os.Chmod(other, 0o755)
	if err := EnsurePrivateDir(other); err != nil {
		t.Fatal(err)
	}
	if so, _ := os.Stat(other); so.Mode().Perm() != 0o755 {
		t.Error("changed an existing directory")
	}
}

func TestScrubEnv(t *testing.T) {
	in := []string{"PATH=/bin", "PRAVO_IDENTITY={\"v\":1}", "pravo_identity=x", "PRAVO_IDENTITY_FILE=/x", "HOME=/h", "PRAVO_PASSPHRASE=p"}
	got := ScrubEnv(in)
	if strings.Join(got, ",") != "PATH=/bin,PRAVO_IDENTITY_FILE=/x,HOME=/h" {
		t.Error(got)
	}
}

func TestShort(t *testing.T) {
	k, _ := GenerateReaderKey()
	if s := k.Public().Short(); len(s) != len("x25519:")+6+len("…") || !strings.HasPrefix(k.Public().String(), strings.TrimSuffix(s, "…")) {
		t.Error(s)
	}
}

func TestConfigDir(t *testing.T) {
	t.Setenv("PRAVO_CONFIG_DIR", "/x/y")
	if d, _ := ConfigDir(env.OS()); d != "/x/y" {
		t.Error(d)
	}
	t.Setenv("PRAVO_CONFIG_DIR", "")
	if runtime.GOOS != "windows" {
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		if d, _ := ConfigDir(env.OS()); d != "/xdg/pravo" {
			t.Error(d)
		}
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/u")
		if d, _ := ConfigDir(env.OS()); d != "/home/u/.config/pravo" {
			t.Error(d)
		}
	}
	if _, err := IdentityPath(env.OS(), "../../etc/passwd"); err == nil {
		t.Error("path in identity name")
	}
}

func BenchmarkSeal2KB(b *testing.B) {
	dk := NewDataKey()
	c := Ctx{Lineage: "L", Group: "g1", Kind: KindRecord, Name: "a.b", Revision: 1}
	pt := make([]byte, 2048)
	b.SetBytes(2048)
	for b.Loop() {
		Seal(dk, c, pt)
	}
}

func BenchmarkOpen2KB(b *testing.B) {
	dk := NewDataKey()
	c := Ctx{Lineage: "L", Group: "g1", Kind: KindRecord, Name: "a.b", Revision: 1}
	s := Seal(dk, c, make([]byte, 2048))
	b.SetBytes(2048)
	for b.Loop() {
		Open(dk, c, s)
	}
}

func BenchmarkWrapUnwrap(b *testing.B) {
	k, _ := GenerateReaderKey()
	dk := NewDataKey()
	c := WrapCtx{"L", "g1"}
	for b.Loop() {
		box, _ := Wrap(dk, k.Public(), c)
		Unwrap(box, k, c)
	}
}

// Garbage in every length must come back as an error, never a panic.
func TestGarbageNeverPanics(t *testing.T) {
	_, k := mustReader(t, "x")
	dk := NewDataKey()
	for n := range 201 {
		b := make([]byte, n)
		rand.Read(b)
		if _, err := Unwrap(b, k, WrapCtx{"L", "g1"}); err == nil {
			t.Fatalf("garbage of %d bytes unwrapped", n)
		}
		if _, err := Open(dk, Ctx{Group: "g1"}, Sealed{Alg: Alg, Group: "g1", CT: b}); err == nil {
			t.Fatalf("garbage of %d bytes opened", n)
		}
	}
}

func TestMetaLifecycle(t *testing.T) {
	ada, adaK := mustReader(t, "ada")
	dan, danK := mustReader(t, "dan")
	eve, eveK := mustReader(t, "eve")
	m, keys, err := NewMeta("L1", []Reader{ada, dan})
	if err != nil {
		t.Fatal(err)
	}
	c := Ctx{Lineage: "L1", Kind: KindRecord, Name: "net.vpc", Generation: "tx1", Revision: 1}
	old, err := keys.SealJSON(c, map[string]string{"k": "v1"})
	if err != nil || old.Group != "g1" {
		t.Fatal(old.Group, err)
	}
	if st := m.Status([]Reader{ada, dan}); !st.Matches || len(st.Readers) != 2 {
		t.Fatalf("status %+v", st)
	}
	if st := m.Status(nil); st.Matches {
		t.Error("matches with no readers file")
	}
	st := m.Status([]Reader{ada, dan, eve})
	if st.Matches || len(st.Missing) != 1 || st.Missing[0] != "eve" {
		t.Fatalf("status %+v", st)
	}

	// eve joins: a lock in g1, no new group, no record rewritten.
	ak, _ := m.Unlock("L1", adaK)
	ch, err := m.Rewrap(ak, []Reader{ada, dan, eve})
	if err != nil || ch.NewGroup != "" || len(ch.Added) != 1 || len(m.Groups) != 1 {
		t.Fatal(ch, err)
	}
	ek, err := m.Unlock("L1", eveK)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := ek.OpenJSON(c, old, &got); err != nil || got["k"] != "v1" {
		t.Fatal("eve cannot read what was sealed before she joined", err)
	}

	// dan leaves: g2 for new writes, dan loses his lock in g1 too.
	ch, err = m.Rewrap(ak, []Reader{ada, eve})
	if err != nil || ch.NewGroup != "g2" || len(ch.Removed) != 1 || ch.Removed[0] != "dan" || m.Current != "g2" {
		t.Fatal(ch, err)
	}
	if _, err := m.Unlock("L1", danK); !errors.Is(err, ErrNotReader) {
		t.Errorf("dan still unlocks: %v", err)
	}
	newer, _ := ak.SealJSON(Ctx{Lineage: "L1", Kind: KindRecord, Name: "net.vpc", Generation: "tx1", Revision: 2}, map[string]string{"k": "v2"})
	if newer.Group != "g2" {
		t.Error("new write not in the new group")
	}
	ek, _ = m.Unlock("L1", eveK)
	if err := ek.OpenJSON(c, old, &got); err != nil {
		t.Fatal("eve cannot read g1 records after the rewrap", err)
	}
	// the old copy of the store still opens with dan's old key: nothing in the crypto prevents it.
	// A moved blob fails whatever group it is in.
	c.Name = "other"
	if err := ek.OpenJSON(c, old, &got); !errors.Is(err, ErrAuth) {
		t.Errorf("moved blob: %v", err)
	}
	// after rewrap --all nothing is in g1: prune.
	if dropped := m.Prune(ek, func(g string) bool { return false }); len(dropped) != 1 || dropped[0] != "g1" || len(m.Groups) != 1 {
		t.Fatal(dropped, m.Groups)
	}
	if _, err := ek.Open(c, old); err == nil {
		t.Error("pruned group still opens")
	}
	if _, err := m.Rewrap(ak, nil); err == nil {
		t.Error("removed every reader")
	}
	raw, _ := json.Marshal(m)
	var back Meta
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if _, err := back.Unlock("L1", adaK); err != nil {
		t.Fatal(err)
	}
	if _, err := back.Unlock("L1", danK); !errors.Is(err, ErrNotReader) {
		t.Error("dan after JSON")
	}
	ek.Close()
	if len(ek.keys) != 0 {
		t.Error("Close")
	}
}

func FuzzParsers(f *testing.F) {
	a, _ := GenerateReaderKey()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	line := Reader{Name: "ada", Sign: pub, Key: a.Public()}.String()
	f.Add(line)
	f.Add("\xef\xbb\xbf" + line + "\r\n# c\r\n")
	f.Add("ada ssh-ed25519 AAAA x25519:AAAA")
	f.Fuzz(func(t *testing.T, s string) {
		if rs, err := ParseReaders(strings.NewReader(s)); err == nil {
			// anything accepted must survive a format and parse round trip
			back, err := ParseReaders(bytes.NewReader(FormatReaders(rs)))
			if err != nil || SetID(back) != SetID(rs) {
				t.Fatalf("round trip of %q: %v", s, err)
			}
		}
		ParseSSHPublicLine(s)
		ParsePublic(s)
	})
}

// The readers file lists at most 200 readers, the limit of the specification. The
// error names the line and repeats nothing of it.
func TestReadersFileLimit(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 201; i++ {
		r, _ := mustReader(t, fmt.Sprintf("reader%03d", i))
		if i == 201 {
			rs, err := ParseReaders(strings.NewReader(b.String()))
			if err != nil || len(rs) != 200 {
				t.Fatalf("200 readers = %d, %v", len(rs), err)
			}
			b.WriteString("# a comment\n\n")
		}
		b.WriteString(r.String() + "\n")
	}
	_, err := ParseReaders(strings.NewReader(b.String()))
	if err == nil || !strings.Contains(err.Error(), "line 203") || !strings.Contains(err.Error(), "200") {
		t.Fatalf("201 readers: %v, want an error that names line 203 and the limit", err)
	}
	if strings.Contains(err.Error(), "reader201") || strings.Contains(err.Error(), "ssh-ed25519") {
		t.Errorf("the error repeats the line: %v", err)
	}
}
