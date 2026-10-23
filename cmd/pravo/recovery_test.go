package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// recoveryEnv is a bucket sealed to ada and dan with a recovery of 2 of 3.
type recoveryEnv struct {
	*sealed
	shares []string
}

func (r *recoveryEnv) sharePath(i int) string {
	return filepath.Join(r.dir, "recovery-shares", "share-"+string(rune('0'+i))+"-of-3.txt")
}

func recoveredBucket(t *testing.T) *recoveryEnv {
	t.Helper()
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	e.record(t, st, "net.db", secretB)
	ada, adaLine := person(t, ids, "ada")
	dan, danLine := person(t, ids, "dan")
	e.readers(t, adaLine, danLine)
	text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes", "--recovery", "2/3")
	if code != 0 {
		t.Fatalf("protect init --recovery exited %d:\n%s", code, text)
	}
	s := &sealed{e, ids, ada, dan, adaLine, danLine}
	r := &recoveryEnv{sealed: s}
	for i := 1; i <= 3; i++ {
		r.shares = append(r.shares, r.sharePath(i))
	}
	return r
}

func TestProtectInitWithARecovery(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	_, adaLine := person(t, ids, "ada")
	e.readers(t, adaLine)

	text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes", "--recovery", "2/3")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	for _, want := range []string{
		"  4  add a recovery recipient: 3 shares, any 2 rebuild the key\n",
		"  5  set the state to format 3.",
		"Recovery: 3 shares written (any 2 rebuild the recovery key):\n",
		"  ./recovery-shares/share-1-of-3.txt  …  share-3-of-3.txt\n",
		"Do not commit them. Give each to a different person, offline.",
		"Commit the recovery recipient with the readers file:\n  .pravo/access/recovery.txt\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "No recovery recipient") {
		t.Errorf("the output warns of a missing recovery:\n%s", text)
	}
	// The shares: three files of mode 0600 that rebuild the key of the state.
	var shares []protect.Share
	for i := 1; i <= 3; i++ {
		p := filepath.Join(e.dir, "recovery-shares", "share-"+string(rune('0'+i))+"-of-3.txt")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" {
			if m := mode(t, p); m != 0o600 {
				t.Errorf("%s has mode %v, want 0600", p, m)
			}
		}
		s, err := protect.ParseShare(data)
		if err != nil {
			t.Fatal(err)
		}
		shares = append(shares, s)
	}
	file, err := os.ReadFile(filepath.Join(e.dir, ".pravo", "access", "recovery.txt"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := protect.ParseRecoveryLine(file)
	if err != nil || rec.K != 2 || rec.N != 3 {
		t.Fatalf("recovery.txt: %v %+v", err, rec)
	}
	if _, err := rec.Open(shares[:2]); err != nil {
		t.Errorf("two shares do not rebuild the key: %v", err)
	}
	// Nothing of the secret or of a share is in the bucket.
	for _, secret := range []string{secretA, shares[0].String(), string(shares[0].Data)} {
		if strings.Contains(e.bucketText(), secret) {
			t.Errorf("%q is in the bucket", secret)
		}
	}
	t.Run("status", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "protect", "status")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{
			"Sealed       on · format 3 · 1 reader (ada) · recovery 2 of 3\n",
			"Recovery     2 of 3 shares · key x25519:",
			".pravo/access/recovery.txt matches the state\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("status lacks %q:\n%s", want, text)
			}
		}
		text, code = runPravo(t, e.dir, "--json", "protect", "status")
		if code != 0 || !strings.Contains(text, `"recovery": {`) || !strings.Contains(text, `"file": "match"`) {
			t.Errorf("JSON: exit %d\n%s", code, text)
		}
	})
	t.Run("a second set of shares is not mixed with the first", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "protect", "rewrap", "--recovery", "2/3", "--yes")
		if code == 0 || !strings.Contains(text, "recovery-shares already holds shares") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

func TestARecoveryIsKeptByTheRepository(t *testing.T) {
	r := recoveredBucket(t)
	file := filepath.Join(r.dir, ".pravo", "access", "recovery.txt")
	good, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("a missing file stops a writer with 5", func(t *testing.T) {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		defer os.WriteFile(file, good, 0o644)
		text, code := runPravo(t, r.dir, "state", "rm", "net.vpc")
		if code != 5 || !strings.Contains(text, "the state has a recovery recipient, and .pravo/access/recovery.txt was not found") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		var out string
		stderr := withStderr(t, func() { out, code = runPravo(t, r.dir, "state", "list") })
		if code != 0 || !strings.Contains(stderr, "Warning: the state has a recovery recipient") {
			t.Errorf("a reader: exit %d, %q, %q", code, out, stderr)
		}
	})
	t.Run("a file that names another key stops a writer with 5", func(t *testing.T) {
		other, _, err := protect.NewRecovery(2, 3)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(other.Line()), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.WriteFile(file, good, 0o644)
		text, code := runPravo(t, r.dir, "state", "rm", "net.vpc")
		if code != 5 || !strings.Contains(text, "name different recovery keys") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		// rewrap makes the state follow the repository, and does not touch the readers.
		text, code = runPravo(t, r.dir, "protect", "rewrap")
		if code != 0 || !strings.Contains(text, "Recovery recipient set from .pravo/access/recovery.txt: any 2 of 2") && !strings.Contains(text, "any 2 of 3 shares rebuild its key") {
			t.Fatalf("rewrap: exit %d\n%s", code, text)
		}
		text, code = runPravo(t, r.dir, "state", "list")
		if code != 0 {
			t.Errorf("the state does not follow the file: exit %d\n%s", code, text)
		}
	})
	t.Run("an invalid file stops a writer with 6 and shows no line", func(t *testing.T) {
		if err := os.WriteFile(file, []byte("SECRETWORD not a recovery\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.WriteFile(file, good, 0o644)
		text, code := runPravo(t, r.dir, "state", "rm", "net.vpc")
		if code != 6 || strings.Contains(text, "SECRETWORD") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

func TestProtectRecover(t *testing.T) {
	r := recoveredBucket(t)

	// Every reader is lost: only a new identity, and the shares.
	d := t.TempDir()
	eve, eveData := newIdentityFile(t, "eve", true)
	putIdentity(t, d, "eve", eveData)
	t.Setenv("PRAVO_IDENTITY_DIR", d)
	resetIdentityState(t)
	r.readers(t, eve.Line())

	t.Run("nobody can read", func(t *testing.T) {
		text, code := runPravo(t, r.dir, "state", "show", "net.vpc")
		if code != 5 {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("too few shares", func(t *testing.T) {
		text, code := runPravo(t, r.dir, "protect", "recover", "--yes", "--share", r.shares[0])
		if code != 5 || !strings.Contains(text, "it takes 2 shares and 1 were given") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a share that was mistyped", func(t *testing.T) {
		data, _ := os.ReadFile(r.shares[1])
		bad := filepath.Join(t.TempDir(), "bad.txt")
		changed := strings.Replace(string(data), "pravo-share-1 ", "pravo-share-1 X", 1)
		if err := os.WriteFile(bad, []byte(changed), 0o600); err != nil {
			t.Fatal(err)
		}
		text, code := runPravo(t, r.dir, "protect", "recover", "--yes", "--share", r.shares[0], "--share", bad)
		if code != 5 || !strings.Contains(text, bad) {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a share of another state", func(t *testing.T) {
		_, other, err := protect.NewRecovery(2, 3)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), "other.txt")
		os.WriteFile(p, other[0].File(), 0o600)
		text, code := runPravo(t, r.dir, "protect", "recover", "--yes", "--share", r.shares[0], "--share", p)
		if code != 5 || !strings.Contains(text, "another recovery key") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("nothing was written by the refusals", func(t *testing.T) {
		text, code := runPravo(t, r.dir, "state", "show", "net.vpc")
		if code != 5 {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("two shares open the state for a new reader", func(t *testing.T) {
		text, code := runPravo(t, r.dir, "protect", "recover", "--yes", "--all", "--share", r.shares[0], "--share", r.shares[2])
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{
			"Recovery key rebuilt from 2 shares.\n",
			"  1  lock the data keys for eve\n",
			"  2  remove ada dan, and make a new data key\n",
			"The recovery key was rebuilt on this machine. Replace it: pravo protect rewrap --recovery K/N",
			"meta.json updated\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the output lacks %q:\n%s", want, text)
			}
		}
		text, code = runPravo(t, r.dir, "state", "show", "net.vpc")
		if code != 0 || !strings.Contains(text, secretA) {
			t.Fatalf("eve reads: exit %d\n%s", code, text)
		}
		for _, secret := range []string{secretA, secretB} {
			if strings.Contains(r.bucketText(), secret) {
				t.Errorf("%q is in the bucket", secret)
			}
		}
		text, _ = runPravo(t, r.dir, "protect", "status")
		if !strings.Contains(text, "1 reader (eve) · recovery 2 of 3") {
			t.Errorf("status:\n%s", text)
		}
	})
	t.Run("the old readers read nothing", func(t *testing.T) {
		old := t.TempDir()
		putIdentity(t, old, "ada", r.identityFile(t, "ada"))
		t.Setenv("PRAVO_IDENTITY_DIR", old)
		resetIdentityState(t)
		text, code := runPravo(t, r.dir, "state", "show", "net.vpc")
		if code != 5 {
			t.Fatalf("ada: exit %d\n%s", code, text)
		}
		t.Setenv("PRAVO_IDENTITY_DIR", d)
		resetIdentityState(t)
	})
	t.Run("the recovery is replaced and the old shares open nothing", func(t *testing.T) {
		old := filepath.Join(r.dir, "recovery-shares-old")
		if err := os.Rename(filepath.Join(r.dir, "recovery-shares"), old); err != nil {
			t.Fatal(err)
		}
		text, code := runPravo(t, r.dir, "protect", "rewrap", "--recovery", "3/4", "--yes")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{"Recovery: 4 shares written (any 3 rebuild the recovery key):", "The old shares open nothing that is written from now on."} {
			if !strings.Contains(text, want) {
				t.Errorf("the output lacks %q:\n%s", want, text)
			}
		}
		text, code = runPravo(t, r.dir, "protect", "recover", "--yes",
			"--share", filepath.Join(old, "share-1-of-3.txt"), "--share", filepath.Join(old, "share-2-of-3.txt"))
		if code != 5 || !strings.Contains(text, "another recovery key") {
			t.Errorf("the old shares: exit %d\n%s", code, text)
		}
		text, _ = runPravo(t, r.dir, "protect", "status")
		if !strings.Contains(text, "recovery 3 of 4") || !strings.Contains(text, "matches the state") {
			t.Errorf("status:\n%s", text)
		}
	})
	t.Run("the recovery is removed", func(t *testing.T) {
		text, code := runPravo(t, r.dir, "protect", "rewrap", "--remove-recovery", "--yes")
		if code != 0 || !strings.Contains(text, "Recovery removed. Commit the deletion of .pravo/access/recovery.txt") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if _, err := os.Stat(filepath.Join(r.dir, ".pravo", "access", "recovery.txt")); err == nil {
			t.Error("recovery.txt is still there")
		}
		text, _ = runPravo(t, r.dir, "protect", "status")
		if !strings.Contains(text, "recovery none") || strings.Contains(text, "Recovery ") {
			t.Errorf("status:\n%s", text)
		}
		text, code = runPravo(t, r.dir, "protect", "recover", "--yes", "--share", r.shares[0], "--share", r.shares[1])
		if code == 0 || !strings.Contains(text, "no recovery recipient") {
			t.Errorf("recover without a recovery: exit %d\n%s", code, text)
		}
	})
}

func TestARecoveryRefusesWithoutThreshold(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	e.store(t)
	_, adaLine := person(t, ids, "ada")
	e.readers(t, adaLine)
	for _, bad := range []string{"1/3", "3/2", "5", "x/y", "2/21"} {
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes", "--recovery", bad)
		if code == 0 || !strings.Contains(text, "recovery") {
			t.Errorf("--recovery %s: exit %d\n%s", bad, code, text)
		}
		if _, err := os.Stat(filepath.Join(e.dir, "recovery-shares")); err == nil {
			t.Errorf("--recovery %s wrote shares", bad)
		}
	}
}

// Recovering and replacing the recovery key in one command seals the records again
// first and replaces the key as a second step: the key that opens the state is the
// one that is replaced.
func TestProtectRecoverWithANewRecovery(t *testing.T) {
	r := recoveredBucket(t)
	old := filepath.Join(r.dir, "recovery-shares-old")
	if err := os.Rename(filepath.Join(r.dir, "recovery-shares"), old); err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	eve, eveData := newIdentityFile(t, "eve", true)
	putIdentity(t, d, "eve", eveData)
	t.Setenv("PRAVO_IDENTITY_DIR", d)
	resetIdentityState(t)
	r.readers(t, eve.Line())

	text, code := runPravo(t, r.dir, "protect", "recover", "--yes", "--all", "--recovery", "3/4",
		"--share", filepath.Join(old, "share-2-of-3.txt"), "--share", filepath.Join(old, "share-3-of-3.txt"))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	for _, want := range []string{
		"  4  replace the recovery key: 4 new shares, any 3 rebuild it, and the old shares open nothing written from now on\n",
		"Recovery: 4 shares written (any 3 rebuild the recovery key):\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "was rebuilt on this machine") {
		t.Errorf("it tells to replace a recovery that it replaced:\n%s", text)
	}
	text, code = runPravo(t, r.dir, "state", "show", "net.vpc")
	if code != 0 || !strings.Contains(text, secretA) {
		t.Fatalf("eve reads: exit %d\n%s", code, text)
	}
	text, _ = runPravo(t, r.dir, "protect", "status")
	if !strings.Contains(text, "1 reader (eve) · recovery 3 of 4") || !strings.Contains(text, "matches the state") {
		t.Errorf("status:\n%s", text)
	}
	// The old shares open nothing.
	text, code = runPravo(t, r.dir, "protect", "recover", "--yes",
		"--share", filepath.Join(old, "share-1-of-3.txt"), "--share", filepath.Join(old, "share-2-of-3.txt"), "--share", filepath.Join(old, "share-3-of-3.txt"))
	if code != 5 || !strings.Contains(text, "another recovery key") {
		t.Errorf("the old shares: exit %d\n%s", code, text)
	}
}

func TestReplacingTheRecoveryAsksFirst(t *testing.T) {
	r := recoveredBucket(t)
	file := filepath.Join(r.dir, ".pravo", "access", "recovery.txt")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--recovery", "3/4"}, {"--remove-recovery"}} {
		text, code := runPravo(t, r.dir, append([]string{"protect", "rewrap"}, args...)...)
		if code == 0 || !strings.Contains(text, "pass --yes") {
			t.Fatalf("%v: exit %d\n%s", args, code, text)
		}
		after, _ := os.ReadFile(file)
		if string(after) != string(before) {
			t.Errorf("%v: the recovery file changed", args)
		}
		if _, err := os.Stat(filepath.Join(r.dir, "recovery-shares", "share-4-of-4.txt")); err == nil {
			t.Errorf("%v: new shares were written", args)
		}
	}
}

func TestRecoveryFilesAreSettledBeforeJSON(t *testing.T) {
	r := recoveredBucket(t)
	file := filepath.Join(r.dir, ".pravo", "access", "recovery.txt")
	before, _ := os.ReadFile(file)
	if err := os.RemoveAll(filepath.Join(r.dir, "recovery-shares")); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, r.dir, "--json", "protect", "rewrap", "--recovery", "3/4", "--yes")
	if code != 0 || !strings.Contains(text, `"action": "replaced"`) || !strings.Contains(text, `"shares": "recovery-shares"`) {
		t.Fatalf("exit %d\n%s", code, text)
	}
	after, _ := os.ReadFile(file)
	if string(after) == string(before) || !strings.Contains(string(after), " 3/4") {
		t.Errorf("recovery.txt was not rewritten:\n%s", after)
	}
	if text, code = runPravo(t, r.dir, "state", "list"); code != 0 {
		t.Errorf("a writer is refused after --json: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "--json", "protect", "rewrap", "--remove-recovery", "--yes")
	if code != 0 || !strings.Contains(text, `"action": "removed"`) {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("recovery.txt stayed after --remove-recovery --json")
	}
	if text, code = runPravo(t, r.dir, "state", "list"); code != 0 {
		t.Errorf("a writer is refused after the removal: exit %d\n%s", code, text)
	}
}

func TestRemoveRecoveryDeletesAStaleFile(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	_, adaLine := person(t, ids, "ada")
	e.readers(t, adaLine)
	if text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	other, _, _ := protect.NewRecovery(2, 3)
	file := filepath.Join(e.dir, ".pravo", "access", "recovery.txt")
	if err := os.WriteFile(file, []byte(other.Line()), 0o644); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, e.dir, "protect", "rewrap", "--remove-recovery")
	if code != 0 || !strings.Contains(text, "Removed .pravo/access/recovery.txt") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("the stale file stayed")
	}
	if text, code = runPravo(t, e.dir, "protect", "rewrap", "--remove-recovery"); code != 0 || !strings.Contains(text, "no recovery recipient") {
		t.Errorf("exit %d\n%s", code, text)
	}
}

func TestTheRecoveryFileIsNotWrittenThroughALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	_, adaLine := person(t, ids, "ada")
	e.readers(t, adaLine)
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.dir, ".pravo", "access", "recovery.txt")
	if err := os.Symlink(victim, file); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes", "--recovery", "2/3")
	if code == 0 && !strings.Contains(text, "is not a regular file") {
		t.Logf("exit %d\n%s", code, text)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep\n" {
		t.Errorf("the target of the link was written:\n%s", b)
	}
}

func TestSharesDirectoryIsIgnoredByGit(t *testing.T) {
	r := recoveredBucket(t)
	b, err := os.ReadFile(filepath.Join(r.dir, "recovery-shares", ".gitignore"))
	if err != nil || string(b) != "*\n" {
		t.Errorf("%q %v", b, err)
	}
}

func TestInitWithARecoveryOnASealedStateSaysItWasNotApplied(t *testing.T) {
	r := recoveredBucket(t)
	text, code := runPravo(t, r.dir, "protect", "init", "--encrypt", "--yes", "--recovery", "2/3")
	if code != 0 || !strings.Contains(text, "--recovery was not applied") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}
