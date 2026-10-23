package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// sealed is a bucket with two records, sealed to ada and dan by the command.
type sealed struct {
	*s3Env
	ids      string
	ada, dan *protect.Identity
	adaLine  string
	danLine  string
}

const secretA, secretB = "TOPSECRET-alpha-0123456789", "TOPSECRET-bravo-9876543210"

func sealedBucket(t *testing.T) *sealed {
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
	text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
	if code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	return &sealed{e, ids, ada, dan, adaLine, danLine}
}

// asNobody runs with no identity at all.
func (s *sealed) asNobody(t *testing.T) {
	t.Helper()
	resetIdentityState(t)
	t.Setenv("PRAVO_IDENTITY_DIR", t.TempDir())
}

// identityFile is the file of an identity that person made.
func (s *sealed) identityFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.ids, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProtectInitSealsTheState(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	e.record(t, st, "net.db", secretB)
	_, ada := person(t, ids, "ada")
	_, dan := person(t, ids, "dan")
	e.readers(t, ada, dan)
	if !strings.Contains(e.bucketText(), secretA) {
		t.Fatal("the test record is not in the bucket to begin with")
	}

	text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	for _, want := range []string{
		"Readers  .pravo/access/readers.txt  (ada dan)\n",
		"  1  write 2 records as immutable objects  rec/<sha256>\n",
		"  2  encrypt their attributes and private data to 2 readers (ada dan)\n",
		"store check passed\n",
		"2 records sealed in ",
		"outputs sealed · recipients written to meta.json\n",
		"\nThe state is sealed to ada dan.\n",
		"Commit the readers file. Writers read the list from git:\n  .pravo/access/readers.txt\n",
		"No recovery recipient. If every reader loses their key, the state cannot be read.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	for _, secret := range []string{secretA, secretB} {
		if strings.Contains(e.bucketText(), secret) {
			t.Errorf("%q is still in the bucket", secret)
		}
	}
	var meta struct{ Format int }
	if err := json.Unmarshal(e.f.Objects["prod/meta.json"], &meta); err != nil || meta.Format != 3 {
		t.Errorf("meta.json: %s (%v)", e.f.Objects["prod/meta.json"], err)
	}

	t.Run("status", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "protect", "status")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{
			"Sealed       on · format 3 · 2 readers (ada dan) · recovery none\n",
			"Store        s3://northwind-state/prod\n",
			"Readers      .pravo/access/readers.txt matches the store\n",
			"You          ada · reader key x25519:",
			"Roles        off\n",
			"Unsettled    none\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the output lacks %q:\n%s", want, text)
			}
		}
	})
	t.Run("a second init writes nothing", func(t *testing.T) {
		puts, snapshot := e.puts(), e.bucketText()
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 0 || !strings.Contains(text, "Already sealed (format 3). Use pravo protect rewrap to change the readers.") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if e.puts() != puts || e.bucketText() != snapshot {
			t.Error("a second init changed the bucket")
		}
		text, code = runPravo(t, e.dir, "protect", "check")
		if code != 0 || !strings.Contains(text, "Already sealed. Use pravo protect status.") {
			t.Errorf("check: exit %d\n%s", code, text)
		}
	})
	t.Run("a reader opens the values", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "state", "show", "net.vpc")
		if code != 0 || !strings.Contains(text, secretA) {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

func TestSealedStateWithoutAnIdentity(t *testing.T) {
	s := sealedBucket(t)
	s.asNobody(t)
	t.Run("the list commands work", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "state", "list")
		if code != 0 || text != "net.db\nnet.vpc\n" {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, args := range [][]string{{"state", "log"}, {"state", "locks"}, {"state", "graph"}, {"state", "dependents", "net.vpc"}} {
			if text, code := runPravo(t, s.dir, args...); code != 0 {
				t.Errorf("%v: exit %d\n%s", args, code, text)
			}
		}
	})
	t.Run("the commands that need a value are refused", func(t *testing.T) {
		want := "Error: you are not a reader of this state (no identity found).\n  Readers are listed in .pravo/access/readers.txt. Ask one of them to add yours.\n"
		for _, args := range [][]string{{"state", "show", "net.vpc"}, {"state", "query", "len(resources)"}, {"output"}, {"plan"}, {"state", "rm", "net.vpc"}} {
			text, code := runPravo(t, s.dir, args...)
			if code != 5 || !strings.HasSuffix(text, want) {
				t.Errorf("%v: exit %d\n%s", args, code, text)
			}
			if strings.Contains(text, secretA) {
				t.Errorf("%v: the output has a secret", args)
			}
		}
	})
	t.Run("an identity that is not a reader", func(t *testing.T) {
		_, data := newIdentityFile(t, "eve", true)
		d := t.TempDir()
		putIdentity(t, d, "eve", data)
		t.Setenv("PRAVO_IDENTITY_DIR", d)
		resetIdentityState(t)
		text, code := runPravo(t, s.dir, "state", "show", "net.vpc")
		if code != 5 || !strings.Contains(text, "you are not a reader of this state (identity eve is not a reader).") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		text, code = runPravo(t, s.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "You          identity eve is not a reader\n") {
			t.Errorf("status: exit %d\n%s", code, text)
		}
	})
	t.Run("status without an identity", func(t *testing.T) {
		s.asNobody(t)
		text, code := runPravo(t, s.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "You          not a reader of this state (no identity found)\n") || !strings.Contains(text, "Sealed       on · format 3") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("an identity with a passphrase that cannot be asked for", func(t *testing.T) {
		_, data := newIdentityFile(t, "ada", false)
		d := t.TempDir()
		putIdentity(t, d, "ada", data)
		t.Setenv("PRAVO_IDENTITY_DIR", d)
		resetIdentityState(t)
		text, code := runPravo(t, s.dir, "state", "show", "net.vpc")
		if code != 5 || !strings.Contains(text, "set PRAVO_PASSPHRASE") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

func TestSealedStateAndTheReadersFile(t *testing.T) {
	s := sealedBucket(t)
	_, eveLine := person(t, s.ids, "eve")

	t.Run("a writer refuses a file that differs", func(t *testing.T) {
		s.readers(t, s.adaLine, eveLine)
		text, code := runPravo(t, s.dir, "state", "rm", "net.vpc")
		want := "Error: .pravo/access/readers.txt and the state differ (eve added, dan removed). Run pravo protect rewrap."
		if code != 5 || !strings.Contains(text, want) {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if _, ok := s.f.Objects["prod/resources/net.vpc.json"]; !ok {
			t.Error("the refused command removed the record")
		}
	})
	t.Run("a reader only warns", func(t *testing.T) {
		var text string
		var code int
		stderr := withStderr(t, func() { text, code = runPravo(t, s.dir, "state", "list") })
		if code != 0 || text != "net.db\nnet.vpc\n" {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if !strings.Contains(stderr, "Warning: .pravo/access/readers.txt and the state differ (eve added, dan removed). Run pravo protect rewrap.") {
			t.Errorf("standard error: %q", stderr)
		}
	})
	t.Run("status says how they differ", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "Readers      differs: eve is in the file and not in the store; dan is in the store and not in the file\n") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a missing file stops a writer with 5", func(t *testing.T) {
		s.readers(t, s.adaLine, s.danLine)
		moved := filepath.Join(s.dir, ".pravo")
		if err := os.Rename(moved, moved+".away"); err != nil {
			t.Fatal(err)
		}
		defer os.Rename(moved+".away", moved)
		text, code := runPravo(t, s.dir, "state", "rm", "net.vpc")
		if code != 5 || !strings.Contains(text, "looked in .pravo/access") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		var stderr string
		stderr = withStderr(t, func() { text, code = runPravo(t, s.dir, "state", "list") })
		if code != 0 || !strings.Contains(stderr, "Warning: the readers file was not found (looked in .pravo/access)") {
			t.Errorf("a reader: exit %d, standard error %q", code, stderr)
		}
	})
	t.Run("an invalid file stops a writer with 6", func(t *testing.T) {
		s.readers(t, s.adaLine, s.danLine, "SECRETNAME one two")
		text, code := runPravo(t, s.dir, "state", "rm", "net.vpc")
		if code != 6 || !strings.Contains(text, "line 3") || strings.Contains(text, "SECRETNAME") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
}

func TestProtectRewrap(t *testing.T) {
	s := sealedBucket(t)
	bot, botData := newIdentityFile(t, "deploy-bot", true)
	botLine := bot.Line()

	t.Run("nothing to change", func(t *testing.T) {
		puts := s.puts()
		text, code := runPravo(t, s.dir, "protect", "rewrap")
		if code != 0 || !strings.Contains(text, "The store already matches the readers file.") || s.puts() != puts {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("nothing to change, as JSON", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "--json", "protect", "rewrap")
		if code != 0 || !strings.Contains(text, `"resealed": 0`) || strings.Contains(text, "already matches") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a reader who is added", func(t *testing.T) {
		s.readers(t, s.adaLine, s.danLine, botLine)
		text, code := runPravo(t, s.dir, "protect", "rewrap")
		want := "Readers in .pravo/access/readers.txt: ada dan deploy-bot\nWrapped the data keys for deploy-bot (1 reader group, 1 new wrapped key)\nmeta.json updated\n"
		if code != 0 || text != want {
			t.Fatalf("exit %d\n%s", code, text)
		}
		text, _ = runPravo(t, s.dir, "protect", "status")
		if !strings.Contains(text, "3 readers (ada dan deploy-bot)") || !strings.Contains(text, "matches the store") {
			t.Errorf("status:\n%s", text)
		}
		// The job reads with its own identity.
		t.Setenv("PRAVO_IDENTITY", string(botData))
		resetIdentityState(t)
		text, code = runPravo(t, s.dir, "state", "show", "net.vpc")
		if code != 0 || !strings.Contains(text, secretA) {
			t.Errorf("the new reader: exit %d\n%s", code, text)
		}
		t.Setenv("PRAVO_IDENTITY", "")
		resetIdentityState(t)
	})
	t.Run("a reader who is removed", func(t *testing.T) {
		s.readers(t, s.adaLine, botLine)
		text, code := runPravo(t, s.dir, "protect", "rewrap")
		want := "Readers in .pravo/access/readers.txt: ada deploy-bot  (dan removed)\nNew data keys for 1 reader group. Records are sealed again as they are next written.\n"
		if code != 0 || text != want {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("--all seals every record again", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "protect", "rewrap", "--all")
		if code != 0 || !strings.Contains(text, "Sealed 2 records again in ") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, secret := range []string{secretA, secretB} {
			if strings.Contains(s.bucketText(), secret) {
				t.Errorf("%q is in the bucket", secret)
			}
		}
	})
	t.Run("the reader who was removed cannot open what was sealed again", func(t *testing.T) {
		d := t.TempDir()
		putIdentity(t, d, "dan", s.identityFile(t, "dan"))
		t.Setenv("PRAVO_IDENTITY_DIR", d)
		resetIdentityState(t)
		text, code := runPravo(t, s.dir, "state", "show", "net.vpc")
		if code != 5 || !strings.Contains(text, "identity dan is not a reader") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		text, code = runPravo(t, s.dir, "protect", "rewrap")
		if code != 5 {
			t.Errorf("rewrap by a removed reader: exit %d\n%s", code, text)
		}
	})
}

func TestProtectRewrapOfAnInvalidReadersFileIsRefusedWith6(t *testing.T) {
	s := sealedBucket(t)
	s.readers(t, s.adaLine, "SECRETNAME one two")
	text, code := runPravo(t, s.dir, "protect", "rewrap")
	if code != 6 || strings.Contains(text, "SECRETNAME") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	os.RemoveAll(filepath.Join(s.dir, ".pravo"))
	if text, code = runPravo(t, s.dir, "protect", "rewrap"); code != 6 || !strings.Contains(text, "looked in .pravo/access") {
		t.Errorf("a missing file: exit %d\n%s", code, text)
	}
}

func TestProtectStatusFindsATamperedRecord(t *testing.T) {
	s := sealedBucket(t)
	var key string
	for k := range s.f.Objects {
		if strings.Contains(k, "/rec/") {
			key = k
			break
		}
	}
	if key == "" {
		t.Fatal("the sealed bucket holds no rec/ object")
	}
	s.f.Objects[key] = []byte(`{"v":1,"addr":"net.vpc","tampered":true}`)
	text, code := runPravo(t, s.dir, "protect", "status")
	if code != 7 || !strings.Contains(text, "Untrusted: ") || !strings.Contains(text, "rec/") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}

func TestProtectInitSealsTheStoredPlans(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	attrs := fmt.Sprintf(`{"id":"cfg","password":%q}`, secretA)
	if err := st.Set(&state.Resource{Addr: "cfg", Mode: "data", Type: "t_data", Name: "cfg", Attributes: []byte(attrs)}, ""); err != nil {
		t.Fatal(err)
	}
	_, ada := person(t, ids, "ada")
	// The readers file comes after the plan: a repository that lists readers
	// does not write plans into a state that is not sealed.
	text, code := runPravo(t, e.dir, "plan", "--store", "--actor", "ci")
	if code != 0 || !strings.Contains(text, "plan-") {
		t.Fatalf("plan --store exited %d:\n%s", code, text)
	}
	e.readers(t, ada)
	plain := e.bucketText()
	if !strings.Contains(plain, "plans/plan-") {
		t.Fatal("no stored plan in the bucket")
	}

	text, code = runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
	if code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	if strings.Contains(text, "were not sealed") {
		t.Errorf("a plan that nothing uses was left alone:\n%s", text)
	}
	if strings.Contains(e.bucketText(), secretA) {
		t.Error("the password is still in the bucket")
	}
	text, code = runPravo(t, e.dir, "plan", "list")
	if code != 0 || !strings.Contains(text, "plan-") {
		t.Fatalf("a reader lists the plans: exit %d\n%s", code, text)
	}
	t.Run("without an identity", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY_DIR", t.TempDir())
		listed, code := runPravo(t, e.dir, "plan", "list")
		if code != 0 || !strings.Contains(listed, "plan-") {
			t.Fatalf("list: exit %d\n%s", code, listed)
		}
		id := listed[strings.Index(listed, "plan-"):][:13]
		text, code := runPravo(t, e.dir, "plan", "show", id)
		if code != 5 {
			t.Fatalf("show: exit %d\n%s", code, text)
		}
		if strings.Contains(text, secretA) {
			t.Error("the plan was shown")
		}
	})
}

func TestProtectInitOfAnEmptyStateAndOfAnImmutableOne(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	_, ada := person(t, ids, "ada")

	t.Run("a state with no records", func(t *testing.T) {
		e := newS3Env(t, false)
		e.readers(t, ada)
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 0 || !strings.Contains(text, "0 records sealed in ") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		text, code = runPravo(t, e.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "Sealed       on · format 3 · 1 reader (ada)") {
			t.Fatalf("status: exit %d\n%s", code, text)
		}
	})
	t.Run("a state in the immutable layout", func(t *testing.T) {
		e := newS3Env(t, false)
		e.record(t, e.store(t), "net.vpc", secretA)
		text, code := runPravo(t, e.dir, "state", "upgrade")
		if code != 0 || !strings.Contains(text, "The state is in format 2: 1 record as immutable objects") {
			t.Fatalf("state upgrade: exit %d\n%s", code, text)
		}
		text, code = runPravo(t, e.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "Sealed       off · format 2 · immutable records\n") {
			t.Fatalf("status: exit %d\n%s", code, text)
		}
		if !strings.Contains(e.bucketText(), secretA) {
			t.Error("format 2 encrypts nothing, and the value is gone")
		}
		e.readers(t, ada)
		text, code = runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 0 || !strings.Contains(text, "1 record sealed in ") {
			t.Fatalf("init: exit %d\n%s", code, text)
		}
		if strings.Contains(e.bucketText(), secretA) {
			t.Error("the value is still in the bucket")
		}
	})
	t.Run("a hidden command is not listed", func(t *testing.T) {
		text, _ := runPravo(t, t.TempDir(), "state", "--help")
		if strings.Contains(text, "upgrade") {
			t.Errorf("state --help lists the hidden upgrade:\n%s", text)
		}
	})
}

func TestSealedStateInALocalDirectory(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	dir, st := pendingDir(t)
	if err := st.Set(&state.Resource{Addr: "app", Mode: "managed", Type: "t_thing", Name: "app", Attributes: []byte(`{"password":"` + secretA + `"}`)}, ""); err != nil {
		t.Fatal(err)
	}
	_, ada := person(t, ids, "ada")
	writeReaders(t, filepath.Join(dir, ".pravo", "access"), ada)

	text, code := runPravo(t, dir, "protect", "init", "--encrypt", "--yes", "--allow-local")
	if code != 0 || !strings.Contains(text, "2 records sealed in ") {
		t.Fatalf("init: exit %d\n%s", code, text)
	}
	filepath.WalkDir(filepath.Join(dir, "pravo.state"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if data, _ := os.ReadFile(p); strings.Contains(string(data), secretA) {
				t.Errorf("the value is on disk in %s", p)
			}
		}
		return nil
	})
	if text, code := runPravo(t, dir, "protect", "status"); code != 0 || !strings.Contains(text, "Store        ./pravo.state\n") || !strings.Contains(text, "Sealed       on · format 3 · 1 reader (ada)") {
		t.Errorf("status: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "state", "show", "app"); code != 0 || !strings.Contains(text, secretA) {
		t.Errorf("show: exit %d\n%s", code, text)
	}
	t.Setenv("PRAVO_IDENTITY_DIR", t.TempDir())
	resetIdentityState(t)
	if text, code := runPravo(t, dir, "state", "list"); code != 0 || text != "app\ncfg\n" {
		t.Errorf("list: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "state", "show", "app"); code != 5 {
		t.Errorf("show without an identity: exit %d\n%s", code, text)
	}
}

// crashing fails every replace after the first n, as a process that is killed
// in the middle of a seal would stop.
type crashing struct {
	state.Backend
	// left is the number of replaces that still succeed. The upgrade replaces from
	// several goroutines.
	left atomic.Int64
}

func newCrashing(b state.Backend, n int64) *crashing {
	c := &crashing{Backend: b}
	c.left.Store(n)
	return c
}

func (c *crashing) ReplaceIf(ctx context.Context, key string, update func(cur []byte) ([]byte, bool)) (bool, error) {
	if c.left.Add(-1) < 0 {
		return false, errors.New("injected failure")
	}
	return c.Backend.ReplaceIf(ctx, key, update)
}

func TestProtectInitFinishesASealThatWasInterrupted(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	for _, a := range []string{"a", "b", "c", "d"} {
		e.record(t, st, a, secretA+a)
	}
	ada, adaLine := person(t, ids, "ada")
	e.readers(t, adaLine)

	// The seal starts, and the process dies after the first records.
	st, err := state.OpenBackend(context.Background(), newCrashing(e.backend(t), 3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Upgrade(context.Background(), state.UpgradeOptions{To: state.FormatSealed, Readers: []protect.Reader{ada.AsReader()}, Identity: ada, By: "ada"}); err == nil {
		t.Fatal("the injected failure did not stop the upgrade")
	}

	t.Run("status says that it is sealing", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "Sealed       migrating · ") || !strings.Contains(text, "run pravo protect init --encrypt to finish") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a command that writes is refused with 4", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "state", "rm", "a")
		if code != 4 || !strings.Contains(text, "a seal is in progress; run pravo protect init --encrypt to finish") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("commands that read still work", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "state", "list")
		if code != 0 || text != "a\nb\nc\nd\n" {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if text, code := runPravo(t, e.dir, "state", "show", "d"); code != 0 || !strings.Contains(text, secretA+"d") {
			t.Fatalf("show: exit %d\n%s", code, text)
		}
	})
	t.Run("check does not call it ready twice", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "protect", "check")
		if code != 0 || !strings.Contains(text, "sealing began, ") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("init finishes it", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 0 || !strings.Contains(text, "A seal was started by ada and did not finish:") || !strings.Contains(text, "The state is sealed to ada.") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if strings.Contains(e.bucketText(), secretA) {
			t.Error("a value is still in the bucket")
		}
		text, code = runPravo(t, e.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "Sealed       on · format 3 · 1 reader (ada)") {
			t.Fatalf("status: exit %d\n%s", code, text)
		}
	})
}

// A name in meta.json comes from the bucket and may hold anything. The terminal
// never sees a control character that came from there.
func TestProtectStatusPrintsNoControlCharacterFromTheBucket(t *testing.T) {
	s := sealedBucket(t)
	meta := string(s.f.Objects["prod/meta.json"])
	if !strings.Contains(meta, `"name": "dan"`) {
		t.Fatalf("meta.json has no reader named dan:\n%s", meta)
	}
	s.f.Objects["prod/meta.json"] = []byte(strings.ReplaceAll(meta, `"name": "dan"`, `"name": "d\u001b[2Jan"`))
	s.readers(t, s.adaLine)
	text, code := runPravo(t, s.dir, "protect", "status")
	if code != 0 || strings.ContainsRune(text, 0x1b) || !strings.Contains(text, "(invalid name)") {
		t.Fatalf("exit %d\n%q", code, text)
	}
	for _, args := range [][]string{{"state", "list"}, {"protect", "rewrap"}} {
		text, _ := runPravo(t, s.dir, args...)
		if strings.ContainsRune(text, 0x1b) {
			t.Errorf("%v printed an escape sequence:\n%q", args, text)
		}
	}
}

func TestPrintableAndSafeName(t *testing.T) {
	if got := printable("rec/ab\x1b[2J (net.vpc)\r\n"); got != "rec/ab[2J (net.vpc)" {
		t.Errorf("printable = %q", got)
	}
	for in, want := range map[string]string{"ada": "ada", "deploy-bot": "deploy-bot", "a b": "(invalid name)", "": "(invalid name)", "x\x1b": "(invalid name)", "é": "(invalid name)"} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A root that reads the outputs of a sealed root with remote_outputs opens them
// with the identity of the user: the session gives the engine the way to find it.
func TestRemoteOutputsOfASealedRootAreReadWithTheIdentity(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	if err := st.SetOutputs(map[string]json.RawMessage{"url": json.RawMessage(`"http://db.internal"`)}); err != nil {
		t.Fatal(err)
	}
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)
	if text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	consumer := specDir(t, map[string]string{"main.cue": `package app

import "pravo.dev/pravo"

net: pravo.#RemoteOutputs & {
	state: {backend: "s3", bucket: "northwind-state", prefix: "prod", region: "eu-west-1", endpoint: "` + e.url + `", path_style: true, access_key: "test", secret_key: "test"}
}

copy: pravo.#Output & {value: net.outputs.url}
`})
	if text, code := runPravo(t, consumer, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, consumer, "plan")
	if code != 0 || !strings.Contains(text, `copy: "http://db.internal"`) {
		t.Fatalf("a reader plans the consumer: exit %d\n%s", code, text)
	}
	t.Run("without an identity", func(t *testing.T) {
		resetIdentityState(t)
		t.Setenv("PRAVO_IDENTITY_DIR", t.TempDir())
		text, code := runPravo(t, consumer, "plan")
		if code != 5 || strings.Contains(text, "db.internal") {
			t.Fatalf("exit %d, want 5 and no value:\n%s", code, text)
		}
	})
}

// A root that reads the outputs of two sealed roots, each sealed to another of
// the identities in the identity directory, none of them named like the user, gets
// the identity that can read each: the engine gives the session the reader keys of
// the state it is opening, as the open of the root's own state does.
func TestRemoteOutputsOfSeveralSealedRootsChooseTheIdentityByTheirLocks(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	t.Setenv("USER", "nobody-in-the-directory")
	carol, _ := person(t, ids, "carol")
	dan, _ := person(t, ids, "dan")
	eve, err := protect.NewIdentity("eve", "")
	if err != nil {
		t.Fatal(err)
	}
	seal := func(who *protect.Identity, url string) *s3Env {
		t.Helper()
		e := newS3Env(t, false)
		st := e.store(t)
		e.record(t, st, "net.vpc", secretA)
		if err := st.SetOutputs(map[string]json.RawMessage{"url": json.RawMessage(`"` + url + `"`)}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Upgrade(context.Background(), state.UpgradeOptions{To: state.FormatSealed, Readers: []protect.Reader{who.AsReader()}, Identity: who, By: who.Name}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	forCarol, forDan, forEve := seal(carol, "http://carol.internal"), seal(dan, "http://dan.internal"), seal(eve, "http://eve.internal")
	remote := func(name string, e *s3Env) string {
		return name + `: pravo.#RemoteOutputs & {
	state: {backend: "s3", bucket: "northwind-state", prefix: "prod", region: "eu-west-1", endpoint: "` + e.url + `", path_style: true, access_key: "test", secret_key: "test"}
}
`
	}
	consumer := func(files ...string) string {
		spec := "package app\n\nimport \"pravo.dev/pravo\"\n\n"
		for _, f := range files {
			spec += f + "\n"
		}
		dir := specDir(t, map[string]string{"main.cue": spec})
		if text, code := runPravo(t, dir, "init"); code != 0 {
			t.Fatalf("init exited %d:\n%s", code, text)
		}
		return dir
	}

	both := consumer(remote("one", forCarol), remote("two", forDan),
		"copy_one: pravo.#Output & {value: one.outputs.url}", "copy_two: pravo.#Output & {value: two.outputs.url}")
	text, code := runPravo(t, both, "plan")
	if code != 0 || !strings.Contains(text, `copy_one: "http://carol.internal"`) || !strings.Contains(text, `copy_two: "http://dan.internal"`) {
		t.Fatalf("a reader of each plans the consumer: exit %d\n%s", code, text)
	}

	t.Run("a root sealed to nobody in the directory", func(t *testing.T) {
		dir := consumer(remote("one", forCarol), remote("three", forEve),
			"copy_one: pravo.#Output & {value: one.outputs.url}", "copy_three: pravo.#Output & {value: three.outputs.url}")
		text, code := runPravo(t, dir, "plan")
		if code != 5 || strings.Contains(text, "eve.internal") {
			t.Fatalf("exit %d, want 5 and no value:\n%s", code, text)
		}
	})
	t.Run("--identity still decides", func(t *testing.T) {
		defer func() { flagIdentity = "" }()
		dir := consumer(remote("one", forCarol), "copy_one: pravo.#Output & {value: one.outputs.url}")
		text, code := runPravo(t, dir, "--identity", "dan", "plan")
		if code != 5 || strings.Contains(text, "carol.internal") {
			t.Fatalf("an identity that is named but cannot read: exit %d, want 5 and no value:\n%s", code, text)
		}
	})
}

// A plan that was stored before the seal and left alone, because something used
// it, is listed with a warning that says so; a plan that is sealed is not.
func TestPlanListWarnsOfPlansThatAreNotSealed(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	attrs := fmt.Sprintf(`{"id":"cfg","password":%q}`, secretA)
	if err := st.Set(&state.Resource{Addr: "cfg", Mode: "data", Type: "t_data", Name: "cfg", Attributes: []byte(attrs)}, ""); err != nil {
		t.Fatal(err)
	}
	_, ada := person(t, ids, "ada")
	// The readers file comes after the plan: a repository that lists readers
	// does not write plans into a state that is not sealed.
	text, code := runPravo(t, e.dir, "plan", "--store", "--actor", "ci")
	if code != 0 || !strings.Contains(text, "plan-") {
		t.Fatalf("plan --store exited %d:\n%s", code, text)
	}
	e.readers(t, ada)
	id := storedID.FindString(text[strings.Index(text, "Stored the plan as"):])
	if text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	listed, code := runPravo(t, e.dir, "plan", "list", "--all")
	if code != 0 || !strings.Contains(listed, id) || strings.Contains(listed, "Warning") {
		t.Fatalf("a sealed plan is listed without a warning: exit %d\n%s", code, listed)
	}

	// Write the summary as a pravo that did not know the seal would have: no box.
	var key string
	for k := range e.f.Objects {
		if strings.HasSuffix(k, "plans/"+id+".meta.json") {
			key = k
		}
	}
	if key == "" {
		t.Fatalf("no summary of %s in the bucket", id)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(e.f.Objects[key], &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "sealed")
	e.f.Objects[key], _ = json.Marshal(m)

	listed, code = runPravo(t, e.dir, "plan", "list", "--all")
	if code != 0 || !strings.Contains(listed, "Warning: stored before the state was sealed and not sealed: "+id) || !strings.Contains(listed, "pravo protect rewrap --all") {
		t.Fatalf("exit %d, want the warning:\n%s", code, listed)
	}
	listed, code = runPravo(t, e.dir, "plan", "list", "--all", "--json")
	if code != 0 || !strings.Contains(listed, `"unsealed"`) || !strings.Contains(listed, id) {
		t.Fatalf("json: exit %d\n%s", code, listed)
	}

	// An apply holds the plan: rewrap --all leaves it alone, and says that it did.
	claim := fmt.Sprintf(`{"format":1,"id":%q,"applying":[{"by":"ci","at":%q}],"updated":%q}`, id, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	e.f.Objects[strings.Replace(key, ".meta.json", ".progress.json", 1)] = []byte(claim)
	text, code = runPravo(t, e.dir, "protect", "rewrap", "--all")
	if code != 0 || !strings.Contains(text, "Warning: stored before the state was sealed and not sealed: "+id) {
		t.Fatalf("rewrap --all: exit %d, want the warning:\n%s", code, text)
	}
	if !strings.Contains(text, "1 stored plan in use was not sealed again; run pravo protect rewrap --all when it is done.\n") {
		t.Errorf("rewrap --all does not count the plan that it left:\n%s", text)
	}
	text, code = runPravo(t, e.dir, "protect", "rewrap", "--all", "--json")
	if code != 0 || !strings.Contains(text, `"plans_skipped": 1`) {
		t.Errorf("rewrap --all --json: exit %d, want the count of plans left:\n%s", code, text)
	}
}

// rewrap --all deletes the records that nothing names, so it runs only at rest,
// as init does; a rewrap that changes the readers only does not delete anything.
func TestProtectRewrapAllNeedsTheStateAtRest(t *testing.T) {
	s := sealedBucket(t)
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	if err := st.Lock("net.vpc", "pravo apply", txn.ID()); err != nil {
		t.Fatal(err)
	}
	before, snapshot := s.puts(), s.bucketText()
	text, code := runPravo(t, s.dir, "protect", "rewrap", "--all")
	if code != 4 || !strings.Contains(text, "not ready: 1 transaction is running and 1 lock is held. Wait for them") {
		t.Fatalf("rewrap --all with a command running: exit %d\n%s", code, text)
	}
	if s.puts() != before || s.bucketText() != snapshot {
		t.Error("a refusal wrote to the bucket")
	}
	if err := st.Unlock("net.vpc"); err != nil {
		t.Fatal(err)
	}
	if err := txn.Finish("applied", "", false); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, s.dir, "protect", "rewrap", "--all")
	if code != 0 || !strings.Contains(text, " again in ") {
		t.Fatalf("rewrap --all at rest: exit %d\n%s", code, text)
	}
}

func TestReadersLinesListAtMostFiveNames(t *testing.T) {
	names := func(prefix string, n int) []string {
		var out []string
		for i := 1; i <= n; i++ {
			out = append(out, fmt.Sprintf("%s%d", prefix, i))
		}
		return out
	}
	if got, want := diffText(names("a", 2), names("r", 1)), "a1 a2 added, r1 removed"; got != want {
		t.Errorf("diffText = %q, want %q", got, want)
	}
	if got, want := diffText(names("a", 12), names("r", 6)), "a1 a2 a3 a4 a5 and 7 more added, r1 r2 r3 r4 r5 and 1 more removed"; got != want {
		t.Errorf("diffText = %q, want %q", got, want)
	}
	if got, want := differsText(names("a", 1), names("r", 1)), "differs: a1 is in the file and not in the store; r1 is in the store and not in the file"; got != want {
		t.Errorf("differsText = %q, want %q", got, want)
	}
	got := differsText(names("a", 201), nil)
	if strings.Count(got, "is in the file") != 5 || !strings.HasSuffix(got, "; and 196 more") || len(got) > 400 {
		t.Errorf("differsText of 201 readers = %q", got)
	}
}

// A meta.json whose sealed part lists no group is damage, not a missing key: the
// commands say so, and exit with 7 for a reader as for anyone.
func TestProtectStatusOfADamagedSealedPart(t *testing.T) {
	s := sealedBucket(t)
	var key string
	for k := range s.f.Objects {
		if strings.HasSuffix(k, "meta.json") {
			key = k
		}
	}
	var m map[string]any
	if err := json.Unmarshal(s.f.Objects[key], &m); err != nil {
		t.Fatal(err)
	}
	m["sealed"].(map[string]any)["groups"] = []any{}
	data, _ := json.Marshal(m)
	s.f.Objects[key] = data
	for _, args := range [][]string{{"protect", "status"}, {"state", "show", "net.vpc"}} {
		text, code := runPravo(t, s.dir, args...)
		if code != 7 || !strings.Contains(text, "the sealed part of meta.json is damaged") || strings.Contains(text, "not a reader") {
			t.Errorf("pravo %s: exit %d\n%s", strings.Join(args, " "), code, text)
		}
	}
}

// The text of the error of a run that failed is sealed in a transaction: a reader
// sees it in the log, and anyone else sees that it is sealed.
func TestStateLogShowsTheTextOfAnErrorToAReader(t *testing.T) {
	s := sealedBucket(t)
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	const secretError = "the provider said TOPSECRET-error-5550123"
	txn := st.Begin("apply", "pravo apply")
	txn.Record("net.vpc", "update", 1, 2)
	if err := txn.FinishError("failed", "the text of the error is sealed to the readers of the state", secretError, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.bucketText(), "TOPSECRET-error") {
		t.Fatal("the bucket holds the text of the error")
	}
	text, code := runPravo(t, s.dir, "state", "log")
	if code != 0 || !strings.Contains(text, "\n    "+secretError+"\n") {
		t.Errorf("a reader: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "state", "log", "--json")
	if code != 0 || !strings.Contains(text, secretError) || strings.Contains(text, "summary_sealed") {
		t.Errorf("a reader, as JSON: exit %d\n%s", code, text)
	}
	s.asNobody(t)
	text, code = runPravo(t, s.dir, "state", "log")
	if code != 0 || strings.Contains(text, "TOPSECRET-error") || !strings.Contains(text, "sealed to the readers of the state") {
		t.Errorf("no identity: exit %d\n%s", code, text)
	}
}

// A flag that is given its value as the next argument is sealed in the command
// line that a sealed state shows, as it is when it is written with an equals sign;
// a boolean flag is not taken for one that has a value.
func TestASealedStateSealsTheValueAfterAFlagInTheCommandLine(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	attrs := fmt.Sprintf(`{"id":"cfg","password":%q}`, secretA)
	if err := st.Set(&state.Resource{Addr: "cfg", Mode: "data", Type: "t_data", Name: "cfg", Attributes: []byte(attrs)}, ""); err != nil {
		t.Fatal(err)
	}
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)
	if text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "tag.cue"), []byte("package t\n\nk: *\"x\" | string @tag(k)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	was := os.Args
	t.Cleanup(func() { os.Args = was })
	os.Args = []string{"pravo", "--actor", "ci-bot-7", "plan", "--store", "--no-color", "-t", "k=" + secretB}
	text, code := runPravo(t, e.dir, os.Args[1:]...)
	if code != 0 || !strings.Contains(text, "plan-") {
		t.Fatalf("plan --store exited %d:\n%s", code, text)
	}
	listed, code := runPravo(t, e.dir, "plan", "list", "--all", "--json")
	if code != 0 || !strings.Contains(listed, "--actor ci-bot-7 plan --store --no-color -t k="+secretB) {
		t.Errorf("a reader sees the line as it was typed: exit %d\n%s", code, listed)
	}
	s := &sealed{s3Env: e}
	s.asNobody(t)
	listed, code = runPravo(t, e.dir, "plan", "list", "--all", "--json")
	if code != 0 || strings.Contains(listed, "ci-bot-7 plan") || strings.Contains(listed, secretB) || !strings.Contains(listed, "--actor <sealed:") || !strings.Contains(listed, " plan --store --no-color -t k=<sealed:") {
		t.Errorf("without a key: exit %d\n%s", code, listed)
	}
}
