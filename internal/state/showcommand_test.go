package state

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// testKeys makes the keys of a sealed state with one reader.
func testKeys(t *testing.T) *protect.Keys {
	t.Helper()
	rk, err := protect.GenerateReaderKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, keys, err := protect.NewMeta("lineage-for-tests", []protect.Reader{{Name: "ada", Sign: pub, Key: rk.Public()}})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestRedactArgs(t *testing.T) {
	keys := testKeys(t)
	tok := func(canon, value string) string { return "<sealed:" + keys.Tag(canon+"\x00"+value) + ">" }
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"nothing to hide", []string{"plan", "--store", "--ttl", "2h"}, "pravo plan --store --ttl 2h"},
		{"tag, separate", []string{"apply", "-t", "env=prod", "--auto-approve", "plan-2f9c1a4e"},
			"pravo apply -t env=" + tok("tag", "env=prod") + " --auto-approve plan-2f9c1a4e"},
		{"tag, attached", []string{"plan", "-tenv=prod"}, "pravo plan -tenv=" + tok("tag", "env=prod")},
		{"tag, equals", []string{"plan", "-t=env=prod"}, "pravo plan -t=env=" + tok("tag", "env=prod")},
		{"tag, long", []string{"plan", "--tag", "env=prod", "--tag=db=s3cret"},
			"pravo plan --tag env=" + tok("tag", "env=prod") + " --tag=db=" + tok("tag", "db=s3cret")},
		{"tag, in a cluster", []string{"plan", "-Tt", "env=prod"}, "pravo plan -Tt env=" + tok("tag", "env=prod")},
		{"tag without a key", []string{"plan", "-t", "justavalue"}, "pravo plan -t " + tok("tag", "justavalue")},
		{"tag at the end", []string{"plan", "-t"}, "pravo plan -t"},
		{"webhook", []string{"drift", "--webhook", "https://hooks.example.com/T0/secret"}, "pravo drift --webhook " + tok("webhook", "https://hooks.example.com/T0/secret")},
		{"expression", []string{"eval", "-e", "password"}, "pravo eval -e " + tok("expression", "password")},
		{"expression, long equals", []string{"eval", "--expression=password"}, "pravo eval --expression=" + tok("expression", "password")},
		{"public flags keep their value", []string{"apply", "--only", "a.b,c.d", "--replace=x.y", "-C", "/srv/w"}, "pravo apply --only a.b,c.d --replace=x.y -C /srv/w"},
		{"unknown flag with equals", []string{"apply", "--actor=ada@example.com"}, "pravo apply --actor=" + tok("actor", "ada@example.com")},
		{"boolean with equals", []string{"apply", "--json=true"}, "pravo apply --json=true"},
		{"after the double dash", []string{"state", "mv", "--", "-t", "x=y"}, "pravo state mv -- -t x=y"},
		{"same value, other flag", []string{"eval", "-e", "v", "--webhook", "v"}, "pravo eval -e " + tok("expression", "v") + " --webhook " + tok("webhook", "v")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactArgs(tt.args, keys.Tag)
			if got != tt.want {
				t.Errorf("RedactArgs(%q)\n got %s\nwant %s", tt.args, got, tt.want)
			}
		})
	}
}

func TestRedactArgsLeaksNothing(t *testing.T) {
	keys := testKeys(t)
	secrets := []string{"hunter2-planted", "sk-live-planted", "planted.example.com"}
	args := []string{"apply", "-t", "pw=" + secrets[0], "-tkey=" + secrets[1], "--tag=x=" + secrets[0],
		"--webhook", "https://" + secrets[2] + "/hook", "-e", secrets[1], "--other=" + secrets[0]}
	got := RedactArgs(args, keys.Tag)
	for _, s := range secrets {
		if strings.Contains(got, s) {
			t.Errorf("the redacted line %q holds %q", got, s)
		}
	}
	for _, k := range []string{"pw=", "key=", "x="} {
		if !strings.Contains(got, k) {
			t.Errorf("the redacted line %q lost the key %q", got, k)
		}
	}
}

func TestRedactArgsWithoutAKey(t *testing.T) {
	got := RedactArgs([]string{"plan", "-t", "a=b"}, func(string) string { return "" })
	if got != "pravo plan -t a=<sealed>" {
		t.Errorf("got %q", got)
	}
}

func TestRedactLine(t *testing.T) {
	keys := testKeys(t)
	line := "pravo apply plan-2f9c1a4e -t env=prod -t note=has a space --auto-approve --store"
	got := RedactLine(line, keys.Tag)
	for _, leak := range []string{"prod", "has", "space", "a space"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q holds %q", got, leak)
		}
	}
	if !strings.HasPrefix(got, "pravo apply plan-2f9c1a4e -t ") || !strings.HasSuffix(got, "--auto-approve --store") {
		t.Errorf("the words and the flags should stay: %q", got)
	}
}

func TestRedactTag(t *testing.T) {
	keys := testKeys(t)
	got := RedactTag("env=prod", keys.Tag)
	if !strings.HasPrefix(got, "env=<sealed:") || strings.Contains(got, "prod") {
		t.Errorf("got %q", got)
	}
	if RedactTag("env=prod", keys.Tag) != got {
		t.Error("equal tags must be redacted alike")
	}
}

// unsealedStart skips a test that needs a store that is not sealed when it is
// made, when the suite is run over sealed stores (PRAVO_TEST_FORMAT=3).
func unsealedStart(t *testing.T) {
	t.Helper()
	if NewStoreFormat == FormatSealed {
		t.Skip("the test needs a state that is not sealed to begin with")
	}
}

func TestShowCommandUnsealed(t *testing.T) {
	unsealedStart(t)
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shown, box := st.ShowCommand([]string{"apply", "-t", "env=prod", "--auto-approve"})
	if shown != "pravo apply -t env=prod --auto-approve" || box != nil {
		t.Errorf("an unsealed state shows the line as it is: %q %v", shown, box)
	}
	var none *Store
	if shown, _ := none.ShowCommand([]string{"plan"}); shown != "pravo plan" {
		t.Errorf("no store: %q", shown)
	}
}

// Whatever the order of the arguments, a value given to -t, --tag, --webhook or
// -e is not in the line that the history shows, and what is not a secret is.
func TestRedactArgsNeverShowsTheValueOfASealedFlag(t *testing.T) {
	keys := testKeys(t)
	const secret = "s3cr3t-value-planted"
	plain := [][]string{{"plan"}, {"apply"}, {"--store"}, {"-T"}, {"--ttl", "2h"}, {"-C", "dir"}, {"a.b"}, {"plan-2f9c1a4e"}, {"--only=a.b,c.d"}, {"--"}}
	secrets := [][]string{
		{"-t", "k=" + secret}, {"-tk=" + secret}, {"-t=k=" + secret}, {"--tag", "k=" + secret}, {"--tag=k=" + secret},
		{"-t", secret}, {"--webhook", "https://" + secret}, {"--webhook=" + secret}, {"-e", secret}, {"-e" + secret},
		{"--expression", secret}, {"--expression=" + secret}, {"-Tt", "k=" + secret}, {"--other=" + secret},
	}
	seed := uint64(1)
	next := func(n int) int { // a small xorshift: the test needs no more
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(n))
	}
	for i := 0; i < 2000; i++ {
		var args []string
		for j, n := 0, 1+next(6); j < n; j++ {
			if next(2) == 0 {
				args = append(args, plain[next(len(plain))]...)
			} else {
				args = append(args, secrets[next(len(secrets))]...)
			}
		}
		// Everything after a double dash is positional, and shown as it is.
		got := RedactArgs(args, keys.Tag)
		if before, _, _ := strings.Cut(strings.Join(args, " "), " -- "); !strings.Contains(before, secret) {
			continue
		}
		head := got
		if k := strings.Index(got, " -- "); k >= 0 {
			head = got[:k]
		}
		if strings.Contains(head, secret) {
			t.Fatalf("args %q give %q", args, got)
		}
	}
}

// plantedTagValue is a value given to -t in the tests that follow.
const plantedTagValue = "tag-value-planted-9f3c"

func TestUpgradeRedactsTheCommandsOfEarlierTransactions(t *testing.T) {
	unsealedStart(t)
	ctx := context.Background()
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			st, err := OpenBackend(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			seedSecrets(t, st)
			begin := func(command string) string {
				txn := st.Begin("apply", command)
				txn.Record("db.main", "update", 1, 2)
				if err := txn.Finish("applied", "1 changed", true); err != nil {
					t.Fatal(err)
				}
				return txn.ID()
			}
			past := time.Now().Add(-48 * time.Hour).UTC()
			stale, _ := json.Marshal(Lock{Addr: "old.thing", PID: 1, Host: "h", Command: "pravo plan -t pw=" + plantedTagValue + " --reserve 15m", Kind: "reservation", Since: past, RenewedAt: past, Expires: &past})
			if err := b.Put(ctx, lockKey("old.thing"), stale); err != nil {
				t.Fatal(err)
			}
			secret := begin("pravo apply -t pw=" + plantedTagValue + " -t note=two words --auto-approve plan-0abc1234")
			bare := begin("pravo state mv a.b c.d")
			id := testIdentity(t, "ada")
			if _, err := st.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(id), Identity: id}); err != nil {
				t.Fatal(err)
			}
			for key, data := range allObjects(t, b) {
				for _, n := range []string{plantedTagValue, "two words"} {
					if bytes.Contains(data, []byte(n)) {
						t.Errorf("%s still holds %q", key, n)
					}
				}
			}
			sealed, err := OpenBackendWith(ctx, b, Options{Identity: id})
			if err != nil {
				t.Fatal(err)
			}
			txns, err := sealed.Transactions()
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, tx := range txns {
				got[tx.ID] = tx.Command
			}
			if c := got[secret]; !strings.HasPrefix(c, "pravo apply -t pw=<sealed:") || !strings.HasSuffix(c, "--auto-approve plan-0abc1234") {
				t.Errorf("the command of the earlier apply is %q", c)
			}
			if got[bare] != "pravo state mv a.b c.d" {
				t.Errorf("a command with nothing to hide is changed: %q", got[bare])
			}
			var left Lock
			if err := json.Unmarshal(mustGet(t, b, lockKey("old.thing")), &left); err != nil || !strings.HasPrefix(left.Command, "pravo plan -t pw=<sealed:") || !strings.Contains(left.Command, " --reserve ") || left.Kind != "reservation" || left.Expires == nil {
				t.Errorf("the lock that was left behind: %+v, %v", left, err)
			}
			// Sealing again writes nothing, and a transaction already redacted is not redacted again.
			before := string(mustGet(t, b, "txn/"+secret+".json"))
			if err := sealed.redactHistory(ctx); err != nil {
				t.Fatal(err)
			}
			if string(mustGet(t, b, "txn/"+secret+".json")) != before {
				t.Error("redacting twice must not change the transaction again")
			}
		})
	}
}

func mustGet(t *testing.T, b Backend, key string) []byte {
	t.Helper()
	data, err := b.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestShowCommandInASealedState(t *testing.T) {
	unsealedStart(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	st, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, st)
	id := testIdentity(t, "ada")
	if _, err := st.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(id), Identity: id}); err != nil {
		t.Fatal(err)
	}
	sealed, err := OpenBackendWith(ctx, b, Options{Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"apply", "-t", "pw=" + plantedTagValue, "--webhook", "https://hooks.example.com/" + plantedTagValue, "--auto-approve", "plan-2f9c1a4e"}
	shown, box := sealed.ShowCommand(args)
	if strings.Contains(shown, plantedTagValue) || !strings.HasPrefix(shown, "pravo apply -t pw=<sealed:") || !strings.HasSuffix(shown, " --auto-approve plan-2f9c1a4e") {
		t.Errorf("shown %q", shown)
	}
	if box == nil {
		t.Fatal("the original line should come back sealed")
	}
	line, err := sealed.Sealer().Open(protect.KindCommand, "", *box)
	if err != nil || string(line) != "pravo "+strings.Join(args, " ") {
		t.Errorf("the box holds %q, %v", line, err)
	}
	if bytes.Contains(mustJSON(t, box), []byte(plantedTagValue)) {
		t.Error("the box shows the value")
	}
	again, _ := sealed.ShowCommand(args)
	if again != shown {
		t.Errorf("the same line shows two ways: %q and %q", again, shown)
	}
	other, _ := sealed.ShowCommand([]string{"apply", "-t", "pw=another-value"})
	if strings.Split(other, "pw=")[1] == strings.Split(shown, "pw=")[1][:len("<sealed:00000000>")] {
		t.Error("two values have one tag")
	}

	// Without a key the values are still hidden, and there is no box to read.
	locked, err := OpenBackendWith(ctx, b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !locked.Locked() {
		t.Fatal("the state should open locked")
	}
	shown, box = locked.ShowCommand(args)
	if strings.Contains(shown, plantedTagValue) || !strings.Contains(shown, "pw=<sealed>") || box != nil {
		t.Errorf("locked: %q %v", shown, box)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// testFlags is a command tree with the flags that the tests below use: the long
// name of each flag with its shorthand, and whether it takes a value.
var testFlags = []struct {
	long, short string
	takes       bool
}{
	{"tag", "t", true}, {"expression", "e", true}, {"webhook", "", true},
	{"actor", "", true}, {"reason", "", true}, {"profile", "P", true},
	{"only", "", true}, {"chdir", "C", true},
	{"inject-vars", "T", false}, {"auto-approve", "", false}, {"store", "", false},
	{"json", "", false}, {"verbose", "v", false},
}

// withFlags sets LookupFlag to a command tree that has testFlags for the length of
// the test.
func withFlags(t *testing.T) {
	t.Helper()
	was := LookupFlag
	t.Cleanup(func() { LookupFlag = was })
	LookupFlag = func(name string, short bool) (FlagKind, bool) {
		for _, f := range testFlags {
			if (short && f.short == name) || (!short && f.long == name) {
				return FlagKind{Long: f.long, TakesValue: f.takes}, true
			}
		}
		return FlagKind{}, false
	}
}

func TestRedactArgsSealsTheValueOfAFlagThatTakesOneInTheSpaceForm(t *testing.T) {
	withFlags(t)
	keys := testKeys(t)
	tok := func(canon, value string) string { return "<sealed:" + keys.Tag(canon+"\x00"+value) + ">" }
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"a flag and its value", []string{"apply", "--actor", "ada@example.com", "plan-2f9c1a4e"},
			"pravo apply --actor " + tok("actor", "ada@example.com") + " plan-2f9c1a4e"},
		{"two flags, repeated", []string{"apply", "--actor", "ada", "--reason", "fix the db", "--actor", "bob"},
			"pravo apply --actor " + tok("actor", "ada") + " --reason " + tok("reason", "fix the db") + " --actor " + tok("actor", "bob")},
		{"the same value in the other form has the same tag", []string{"apply", "--actor=ada"}, "pravo apply --actor=" + tok("actor", "ada")},
		{"a boolean flag leaves the next word alone", []string{"apply", "--auto-approve", "plan-2f9c1a4e", "--store", "ada"},
			"pravo apply --auto-approve plan-2f9c1a4e --store ada"},
		{"a boolean flag with a value", []string{"apply", "--json=false", "ada"}, "pravo apply --json=false ada"},
		{"a value that looks like a flag", []string{"apply", "--actor", "-t", "x"}, "pravo apply --actor " + tok("actor", "-t") + " x"},
		{"a flag at the end", []string{"apply", "--actor"}, "pravo apply --actor"},
		{"public flags keep their value", []string{"apply", "--only", "a.b,c.d", "--chdir", "/srv/w", "-C", "/srv/w"},
			"pravo apply --only a.b,c.d --chdir /srv/w -C /srv/w"},
		{"a short flag, separate", []string{"plan", "-P", "prod-admin", "x"}, "pravo plan -P " + tok("profile", "prod-admin") + " x"},
		{"a short flag, attached", []string{"plan", "-Pprod-admin"}, "pravo plan -P" + tok("profile", "prod-admin")},
		{"a short flag, attached with an equals sign", []string{"plan", "-P=prod-admin"}, "pravo plan -P=" + tok("profile", "prod-admin")},
		{"a short flag in a cluster", []string{"plan", "-vTP", "prod-admin", "x"}, "pravo plan -vTP " + tok("profile", "prod-admin") + " x"},
		{"a cluster of boolean flags", []string{"plan", "-vT", "ada"}, "pravo plan -vT ada"},
		{"tag, in every spelling", []string{"plan", "-t", "a=1", "-tb=2", "-t=c=3", "--tag", "d=4", "--tag=e=5"},
			"pravo plan -t a=" + tok("tag", "a=1") + " -tb=" + tok("tag", "b=2") + " -t=c=" + tok("tag", "c=3") + " --tag d=" + tok("tag", "d=4") + " --tag=e=" + tok("tag", "e=5")},
		{"a flag that the tree does not know", []string{"plan", "--unknown", "value"}, "pravo plan --unknown value"},
		{"after the double dash", []string{"state", "mv", "--actor", "ada", "--", "--actor", "ada"},
			"pravo state mv --actor " + tok("actor", "ada") + " -- --actor ada"},
		{"a positional word that is a secret stays", []string{"state", "mv", "hunter2", "--actor", "ada"},
			"pravo state mv hunter2 --actor " + tok("actor", "ada")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactArgs(tt.args, keys.Tag); got != tt.want {
				t.Errorf("RedactArgs(%q)\n got %s\nwant %s", tt.args, got, tt.want)
			}
		})
	}
	t.Run("without the tree", func(t *testing.T) {
		LookupFlag = nil
		args := []string{"apply", "--actor", "ada", "-P", "prod-admin"}
		if got, want := RedactArgs(args, keys.Tag), "pravo "+strings.Join(args, " "); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
}

func TestRedactArgsInTheSpaceFormLeaksNothing(t *testing.T) {
	withFlags(t)
	keys := testKeys(t)
	secrets := []string{"hunter2-planted", "sk-live-planted", "planted reason with spaces"}
	args := []string{"apply", "--actor", secrets[0], "-P", secrets[1], "--reason", secrets[2], "--auto-approve", "plan-2f9c1a4e"}
	got := RedactArgs(args, keys.Tag)
	for _, s := range secrets {
		if strings.Contains(got, s) {
			t.Errorf("the redacted line %q holds %q", got, s)
		}
	}
	if !strings.HasSuffix(got, " --auto-approve plan-2f9c1a4e") {
		t.Errorf("the boolean flag and the plan id should stay: %q", got)
	}
}
