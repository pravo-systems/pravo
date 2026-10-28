package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

const (
	pw1 = "correct-horse-battery-staple-1111"
	pw2 = "correct-horse-battery-staple-2222"
	pw3 = "correct-horse-battery-staple-3333"
)

// rolesFor gives the readers of the identities the roles, in order.
func rolesFor(ids []*protect.Identity, roles ...protect.Role) []protect.Reader {
	out := readersOf(ids...)
	for i := range out {
		out[i].Role = roles[i]
	}
	return out
}

func noPlans(context.Context) (map[string]int, error) { return nil, nil }

func secretTxns(t *testing.T, s *Store) []Transaction {
	t.Helper()
	all, err := s.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	var out []Transaction
	for _, tx := range all {
		if tx.Kind == "secret" {
			out = append(out, tx)
		}
	}
	return out
}

func mustSetSecret(t *testing.T, s *Store, name, value, note string) SecretInfo {
	t.Helper()
	info, err := s.SetSecret(context.Background(), name, []byte(value), note, "pravo secret set "+name)
	if err != nil {
		t.Fatalf("setting %s: %v", name, err)
	}
	return info
}

func mustReadSecret(t *testing.T, s *Store, name string, version int, rules RecordRules) *SecretValue {
	t.Helper()
	v, err := s.ReadSecret(context.Background(), name, version, rules)
	if err != nil {
		t.Fatalf("reading %s version %d: %v", name, version, err)
	}
	return v
}

func TestSecretsSetReadAndRotate(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			skipWhenSealed(t)
			ctx := context.Background()
			b := bk.make(t)
			ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
			s := sealedStore(t, b, ada, dan)
			rules := RecordRules{Readers: readersOf(ada, dan)}

			if got, err := s.Secrets(ctx, rules); err != nil || len(got) != 0 {
				t.Fatalf("a state without secrets: %v %v", got, err)
			}
			info := mustSetSecret(t, s, "prod/db_password", pw1, "first")
			if info.Seq != 1 || info.Kind != SecretKindValue || s.Format() != FormatSecret {
				t.Fatalf("the first version: %+v, format %d", info, s.Format())
			}
			other := openIdentity(t, b, dan)
			if v := mustSetSecret(t, other, "prod/db_password", pw2, "rotated"); v.Seq != 2 {
				t.Fatalf("the second version: %+v", v)
			}
			mustSetSecret(t, s, "api_key", pw3, "")

			v := mustReadSecret(t, s, "prod/db_password", 0, rules)
			if string(v.Value) != pw2 || v.Note != "rotated" || v.Seq != 2 || v.By != "dan" || v.Trust != TrustOK {
				t.Errorf("the latest: %q %+v", v.Value, v.SecretInfo)
			}
			old := mustReadSecret(t, s, "prod/db_password", 1, rules)
			if string(old.Value) != pw1 || old.By != "ada" || old.Note != "first" {
				t.Errorf("version 1: %q %+v", old.Value, old.SecretInfo)
			}
			list, err := s.Secrets(ctx, rules)
			if err != nil || len(list) != 2 || list[0].Name != "api_key" || list[1].Name != "prod/db_password" || list[1].Versions != 2 || list[1].Head.Seq != 2 || !list[1].Live() {
				t.Fatalf("the list: %+v %v", list, err)
			}
			hist, err := s.SecretHistory(ctx, "prod/db_password", rules)
			if err != nil || len(hist) != 2 || hist[0].By != "ada" || hist[1].By != "dan" || hist[0].Trust != TrustOK || hist[1].Trust != TrustOK {
				t.Fatalf("the history: %+v %v", hist, err)
			}
			notes, err := s.SecretNotes(ctx, "prod/db_password")
			if err != nil || notes[1] != "first" || notes[2] != "rotated" {
				t.Errorf("the notes: %v %v", notes, err)
			}
			if _, err := s.ReadSecret(ctx, "nothing", 0, rules); !errors.Is(err, ErrNoSecret) {
				t.Errorf("an unknown name: %v", err)
			}
			if _, err := s.ReadSecret(ctx, "api_key", 3, rules); err == nil || !strings.Contains(err.Error(), "versions 1 to 1") {
				t.Errorf("a version that does not exist: %v", err)
			}
			for _, pw := range []string{pw1, pw2, pw3, "rotated"} {
				assertNoSecret(t, b, pw)
			}
			// Every change is a transaction that names the secret and the version.
			var seen []string
			for _, tx := range secretTxns(t, s) {
				for _, c := range tx.Changes {
					seen = append(seen, fmt.Sprintf("%s %s %d>%d", c.Addr, c.Action, c.Before, c.After))
					if c.Record == "" {
						t.Errorf("%s: a change without the commit", tx.ID)
					}
				}
				if tx.Status != "applied" || tx.StartSig == nil || tx.FinishSig == nil {
					t.Errorf("%s is not a finished, signed transaction: %+v", tx.ID, tx)
				}
				if strings.Contains(tx.Command, pw1) || strings.Contains(tx.Summary, pw1) {
					t.Errorf("the value is in a transaction")
				}
			}
			if len(seen) != 3 {
				t.Errorf("changes: %v", seen)
			}
		})
	}
}

func TestSecretNamesAreChecked(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	s := sealedStore(t, testBackends()[0].make(t), ada)
	for _, bad := range []string{"", "a b", "../x", "/x", "x/", "a//b", "a@1", ".hidden", "a/.b", strings.Repeat("a", 201), "naïve"} {
		if _, err := s.SetSecret(ctx, bad, []byte("v"), "", "c"); err == nil {
			t.Errorf("the name %q was accepted", bad)
		}
	}
	for _, good := range []string{"db", "prod/db_password", "a.b-c_d", "A1/B2/c3"} {
		if err := ValidSecretName(good); err != nil {
			t.Errorf("the name %q: %v", good, err)
		}
	}
	mustSetSecret(t, s, "DB", pw1, "")
	if _, err := s.SetSecret(ctx, "db", []byte(pw2), "", "c"); err == nil || !strings.Contains(err.Error(), "DB") {
		t.Errorf("a name that differs by case: %v", err)
	}
	if _, err := s.SetSecret(ctx, "DB", []byte(pw2), "", "c"); err != nil {
		t.Errorf("the same name again: %v", err)
	}
	if _, err := s.SetSecret(ctx, "empty", nil, "", "c"); err == nil {
		t.Error("an empty value was accepted")
	}
	if _, err := s.SetSecret(ctx, "big", make([]byte, MaxSecretValue+1), "", "c"); err == nil {
		t.Error("a value over the limit was accepted")
	}
	if _, err := s.SetSecret(ctx, "noted", []byte("v"), strings.Repeat("n", MaxSecretNote+1), "c"); err == nil {
		t.Error("a note over the limit was accepted")
	}
	// A value may be binary, and keeps every byte.
	raw := []byte{0, 1, 2, 0xff, 0xfe, '\n', 0}
	if _, err := s.SetSecret(ctx, "blob", raw, "", "c"); err != nil {
		t.Fatal(err)
	}
	if v := mustReadSecret(t, s, "blob", 0, RecordRules{Readers: readersOf(ada)}); !bytes.Equal(v.Value, raw) {
		t.Errorf("binary value: %v", v.Value)
	}
}

func TestSecretsNeedASealedStateAndAKey(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	plain, err := OpenBackend(ctx, testBackends()[0].make(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.SetSecret(ctx, "db", []byte(pw1), "", "c"); !errors.Is(err, ErrSecretsNeedSeal) {
		t.Errorf("a set in a state that is not sealed: %v", err)
	}
	if plain.Format() >= FormatSecret {
		t.Error("a refused set raised the format")
	}
	if _, err := plain.ReadSecret(ctx, "db", 0, RecordRules{}); !errors.Is(err, ErrSecretsNeedSeal) {
		t.Errorf("a read in a state that is not sealed: %v", err)
	}

	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	mustSetSecret(t, s, "db", pw1, "")
	locked, err := OpenBackendWith(ctx, b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !locked.Locked() {
		t.Fatal("the store should be locked")
	}
	if _, err := locked.ReadSecret(ctx, "db", 0, RecordRules{Readers: readersOf(ada)}); !errors.Is(err, ErrNotReader) {
		t.Errorf("a read without a key: %v", err)
	}
	if _, err := locked.SetSecret(ctx, "db", []byte(pw2), "", "c"); !errors.Is(err, ErrNotReader) {
		t.Errorf("a set without a key: %v", err)
	}
	// What a store without a key lists is in the clear: the names, the numbers, who and when.
	list, err := locked.Secrets(ctx, RecordRules{Readers: readersOf(ada)})
	if err != nil || len(list) != 1 || list[0].Head.By != "ada" || list[0].Head.Trust != TrustOK {
		t.Errorf("a list without a key: %+v %v", list, err)
	}
	if head, err := locked.SecretHead(ctx, "db", 0, RecordRules{Readers: readersOf(ada)}); err != nil || head.Seq != 1 {
		t.Errorf("the head without a key: %+v %v", head, err)
	}
}

func TestTwoWritersOfOneVersionAreOrdered(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	mustSetSecret(t, s, "db", pw1, "")
	const writers = 6
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins []int
		lost int
	)
	for i := range writers {
		w := openIdentity(t, b, ada)
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := w.SetSecret(ctx, "db", fmt.Appendf(nil, "%s-%d", pw2, i), "", "c")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins = append(wins, info.Seq)
			case errors.Is(err, ErrSecretChanged):
				lost++
			default:
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	if len(wins)+lost != writers || len(wins) == 0 {
		t.Fatalf("wins %v, lost %d", wins, lost)
	}
	hist, err := s.SecretHistory(ctx, "db", RecordRules{Readers: readersOf(ada)})
	if err != nil || len(hist) != 1+len(wins) {
		t.Fatalf("the versions are not the wins: %v %v", hist, err)
	}
	for _, h := range hist {
		if h.Trust != TrustOK {
			t.Errorf("version %d: %s %s", h.Seq, h.Trust, h.Problem)
		}
	}
	// A writer that lost left a finished transaction that says so and holds no change.
	failed := 0
	for _, tx := range secretTxns(t, s) {
		if tx.Status == "failed" {
			failed++
			if len(tx.Changes) != 0 {
				t.Errorf("a failed write with changes: %+v", tx)
			}
		}
	}
	if failed != lost {
		t.Errorf("failed transactions %d, lost %d", failed, lost)
	}
}

func TestRolesDecideWhoMayWriteASecret(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, dan, eve := testIdentity(t, "ada"), testIdentity(t, "dan"), testIdentity(t, "eve")
	ids := []*protect.Identity{ada, dan, eve}
	s := sealedStore(t, b, ids...)
	// Commands open the state after the first secret has raised its format, so these do.
	if err := s.setFormat(FormatSecret); err != nil {
		t.Fatal(err)
	}
	adas, dans, eves := s, openIdentity(t, b, dan), openIdentity(t, b, eve)
	roles := RecordRules{Readers: rolesFor(ids, protect.RoleAdmin, protect.RoleWriter, protect.RoleReader)}
	noRoles := RecordRules{Readers: readersOf(ids...)}

	mustSetSecret(t, dans, "db", pw1, "")
	if v := mustReadSecret(t, adas, "db", 0, roles); v.Trust != TrustOK || v.By != "dan" {
		t.Fatalf("a writer's version: %+v", v.SecretInfo)
	}
	// A reader holds the data key and the credentials of the bucket, so the store lets it write.
	// The signatures find it.
	mustSetSecret(t, eves, "db", "evil", "")
	_, err := adas.ReadSecret(ctx, "db", 0, roles)
	if !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "eve") || !strings.Contains(err.Error(), "a reader") {
		t.Fatalf("a reader's version is read: %v", err)
	}
	if _, err := adas.SecretHead(ctx, "db", 0, roles); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a reader's version is the head of a run: %v", err)
	}
	if v := mustReadSecret(t, adas, "db", 1, roles); string(v.Value) != pw1 {
		t.Errorf("the writer's version is still there: %q", v.Value)
	}
	list, _ := adas.Secrets(ctx, roles)
	if len(list) != 1 || list[0].Head.Trust != TrustProblem || list[0].Head.Problem == "" {
		t.Errorf("the list: %+v", list)
	}
	verdict, err := adas.VerifySecrets(ctx, mustTxns(t, adas), roles)
	if err != nil || len(verdict.Problems) != 1 || verdict.Problems[0].Version != 2 || verdict.Bound != 1 {
		t.Errorf("the verdict: %+v %v", verdict, err)
	}
	// Without roles every key is an admin, and the same version reads.
	if v := mustReadSecret(t, adas, "db", 0, noRoles); string(v.Value) != "evil" {
		t.Errorf("without roles: %q", v.Value)
	}

	// A writer can add a version and a reader's mistake is replaced by it.
	mustSetSecret(t, dans, "db", pw2, "")
	if v := mustReadSecret(t, adas, "db", 0, roles); string(v.Value) != pw2 {
		t.Errorf("the writer's replacement: %q", v.Value)
	}

	// Only an admin removes. A writer's removal does not take effect for a run.
	if _, err := dans.RemoveSecret(ctx, "db", "pravo secret rm db"); err != nil {
		t.Fatal(err)
	}
	if _, err := adas.ReadSecret(ctx, "db", 0, roles); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "admin") {
		t.Errorf("a writer's removal: %v", err)
	}
	mustSetSecret(t, dans, "db", pw3, "")
	if _, err := adas.RemoveSecret(ctx, "db", "pravo secret rm db"); err != nil {
		t.Fatal(err)
	}
	if _, err := adas.ReadSecret(ctx, "db", 0, roles); !errors.Is(err, ErrSecretRemoved) {
		t.Errorf("an admin's removal: %v", err)
	}
}

func mustTxns(t *testing.T, s *Store) []Transaction {
	t.Helper()
	txns, err := s.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	return txns
}

func TestAKeyOutsideTheRolesNeedsAVouch(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	dans := openIdentity(t, b, dan)
	mustSetSecret(t, dans, "db", pw1, "")

	// dan left: the readers file lists ada only.
	left := RecordRules{Readers: rolesFor([]*protect.Identity{ada}, protect.RoleAdmin)}
	list, _ := s.Secrets(ctx, left)
	if list[0].Head.Trust != TrustUnchecked {
		t.Errorf("a key that is not listed, rules not strict: %+v", list[0].Head)
	}
	if v := mustReadSecret(t, s, "db", 0, left); string(v.Value) != pw1 {
		t.Errorf("not strict: %q", v.Value)
	}
	left.Strict = true
	if _, err := s.ReadSecret(ctx, "db", 0, left); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "not in the roles") {
		t.Errorf("strict, no vouch: %v", err)
	}

	// The admin vouches for the transaction, as protect roles does for the ones that wrote records.
	signers, err := s.LiveSigners(ctx)
	if err != nil || len(signers) == 0 {
		t.Fatalf("the signers: %v %v", signers, err)
	}
	left.Vouched = map[string]protect.Vouch{}
	for _, v := range signers {
		left.Vouched[v.Txn] = v
	}
	v := mustReadSecret(t, s, "db", 0, left)
	if string(v.Value) != pw1 || v.Trust != TrustVouched {
		t.Errorf("vouched: %q %+v", v.Value, v.SecretInfo)
	}
	// A vouch is for the transaction as it was signed. One that was rewritten is another.
	for id, vo := range left.Vouched {
		vo.Digest = strings.Repeat("0", 64)
		left.Vouched[id] = vo
	}
	if _, err := s.ReadSecret(ctx, "db", 0, left); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a vouch for another transaction: %v", err)
	}
}

func TestATamperedSecretIsFound(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")
	s := sealedStore(t, b, ada, eve)
	ids := []*protect.Identity{ada, eve}
	roles := RecordRules{Readers: rolesFor(ids, protect.RoleAdmin, protect.RoleReader)}
	mustSetSecret(t, s, "db", pw1, "")
	mustSetSecret(t, s, "db", pw2, "")
	enc := encodeAddr("db")
	k1, k2, k3 := secretKey(enc, 1), secretKey(enc, 2), secretKey(enc, 3)
	v1, _ := b.Get(ctx, k1)
	v2, _ := b.Get(ctx, k2)
	check := func(what string) {
		t.Helper()
		verdict, err := s.VerifySecrets(ctx, mustTxns(t, s), roles)
		if err != nil {
			t.Errorf("%s: %v", what, err)
			return
		}
		if len(verdict.Problems) == 0 {
			t.Errorf("%s: no problem found: %+v", what, verdict)
		}
	}

	// The value of version 2 under the name of version 1: the place is part of what is sealed.
	if err := b.Put(ctx, k1, v2); err != nil {
		t.Fatal(err)
	}
	check("a version moved")
	noRoles := RecordRules{Readers: readersOf(ids...)}
	if _, err := s.ReadSecret(ctx, "db", 1, noRoles); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a version moved, read without roles: %v", err)
	}
	if err := b.Put(ctx, k1, v1); err != nil {
		t.Fatal(err)
	}

	// Another commit in the object: the value does not match it.
	var f secretFile
	_ = json.Unmarshal(v2, &f)
	f.Commit = strings.Repeat("ab", 32)
	forged, _ := json.Marshal(f)
	if err := b.Put(ctx, k2, forged); err != nil {
		t.Fatal(err)
	}
	check("another commit")
	if _, err := s.ReadSecret(ctx, "db", 2, noRoles); !errors.Is(err, ErrUntrusted) {
		t.Errorf("another commit, read without roles: %v", err)
	}
	if err := b.Put(ctx, k2, v2); err != nil {
		t.Fatal(err)
	}
	if verdict, _ := s.VerifySecrets(ctx, mustTxns(t, s), roles); len(verdict.Problems) != 0 {
		t.Fatalf("the restored state has problems: %+v", verdict)
	}

	// A reader who holds the data key seals a value of their own and a commit for it, and
	// names a transaction that is not there.
	eves := openIdentity(t, b, eve)
	plain, _ := marshalBody(secretBody{Value: []byte("evil"), Nonce: bytes.Repeat([]byte{7}, 32)})
	box, _ := eves.sealBox(secretCtx(eves.Lineage(), "db", 3), plain)
	bogus := secretFile{V: 1, Txn: "20260101T000000Z-aaaaaa", Commit: secretCommit(eves.Lineage(), "db", 3, bytes.Repeat([]byte{7}, 32), []byte("evil"), ""), Sealed: &box}
	data, _ := json.Marshal(bogus)
	if _, err := b.PutIfAbsent(ctx, k3, data); err != nil {
		t.Fatal(err)
	}
	check("a version no transaction names")
	if _, err := s.ReadSecret(ctx, "db", 0, roles); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "not in the state") {
		t.Errorf("a version no transaction names: %v", err)
	}
	// With the transaction of a reader that names it, it is a reader's work.
	tx := eves.Begin("secret", "pravo secret set db")
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	tx.RecordWith(SecretAddr("db"), "set", 2, 3, bogus.Commit)
	if err := tx.Finish("applied", "", true); err != nil {
		t.Fatal(err)
	}
	bogus.Txn = tx.ID()
	data, _ = json.Marshal(bogus)
	_ = b.Delete(ctx, k3)
	if _, err := b.PutIfAbsent(ctx, k3, data); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadSecret(ctx, "db", 0, roles); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "reader") {
		t.Errorf("a reader's transaction: %v", err)
	}
	if err := b.Delete(ctx, k3); err != nil {
		t.Fatal(err)
	}
	// The transaction of the reader names a version that is not there now.
	verdict, err := s.VerifySecrets(ctx, mustTxns(t, s), roles)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdict.Problems) != 1 || !strings.Contains(verdict.Problems[0].Why, "deleted") {
		t.Errorf("a deleted version: %+v", verdict)
	}

	// A gap, and something that is no version.
	_ = b.Delete(ctx, k1)
	if _, err := s.Secrets(ctx, roles); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a gap: %v", err)
	}
	_ = b.Put(ctx, k1, v1)
	_ = b.Put(ctx, "secret/junk.json", []byte("{}"))
	if _, err := s.Secrets(ctx, roles); !errors.Is(err, ErrUntrusted) {
		t.Errorf("junk: %v", err)
	}
	_ = b.Delete(ctx, "secret/junk.json")
	// A shape that is none of the three.
	var odd secretFile
	_ = json.Unmarshal(v1, &odd)
	odd.Removed = true
	data, _ = json.Marshal(odd)
	_ = b.Put(ctx, k1, data)
	if _, err := s.SecretHistory(ctx, "db", roles); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a value that is also a removal: %v", err)
	}
	if _, err := s.VerifySecrets(ctx, mustTxns(t, s), roles); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a value that is also a removal, verified: %v", err)
	}
}

func TestANewestVersionThatWasDeletedIsFound(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	rules := RecordRules{Readers: readersOf(ada)}
	mustSetSecret(t, s, "db", pw1, "")
	mustSetSecret(t, s, "db", pw2, "")
	if err := b.Delete(ctx, secretKey(encodeAddr("db"), 2)); err != nil {
		t.Fatal(err)
	}
	// The old value is what a run reads, and the check says it should not be.
	if v := mustReadSecret(t, s, "db", 0, rules); string(v.Value) != pw1 {
		t.Fatalf("the bucket now holds version 1 only: %q", v.Value)
	}
	verdict, err := s.VerifySecrets(ctx, mustTxns(t, s), rules)
	if err != nil || len(verdict.Problems) != 1 || verdict.Problems[0].Version != 2 {
		t.Errorf("the verdict: %+v %v", verdict, err)
	}
	// A whole secret deleted is found as well.
	_ = b.Delete(ctx, secretKey(encodeAddr("db"), 1))
	verdict, _ = s.VerifySecrets(ctx, mustTxns(t, s), rules)
	if len(verdict.Problems) != 1 || verdict.Problems[0].Name != "db" {
		t.Errorf("a secret that is gone: %+v", verdict)
	}
}

func TestRemoveAndCreateAgain(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	rules := RecordRules{Readers: readersOf(ada)}
	mustSetSecret(t, s, "db", pw1, "")
	if _, err := s.RemoveSecret(ctx, "nothing", "c"); !errors.Is(err, ErrNoSecret) {
		t.Errorf("removing what is not there: %v", err)
	}
	rm, err := s.RemoveSecret(ctx, "db", "pravo secret rm db")
	if err != nil || rm.Seq != 2 || rm.Kind != SecretKindRemoved {
		t.Fatalf("removal: %+v %v", rm, err)
	}
	if _, err := s.RemoveSecret(ctx, "db", "c"); !errors.Is(err, ErrSecretRemoved) {
		t.Errorf("removing twice: %v", err)
	}
	if _, err := s.ReadSecret(ctx, "db", 0, rules); !errors.Is(err, ErrSecretRemoved) {
		t.Errorf("reading a removed secret: %v", err)
	}
	if _, err := s.SecretHead(ctx, "db", 0, rules); !errors.Is(err, ErrSecretRemoved) {
		t.Errorf("the head of a removed secret: %v", err)
	}
	// Its values are in the history until they are purged.
	if v := mustReadSecret(t, s, "db", 1, rules); string(v.Value) != pw1 {
		t.Errorf("an old version of a removed secret: %q", v.Value)
	}
	if _, err := s.ReadSecret(ctx, "db", 2, rules); err == nil || !strings.Contains(err.Error(), "removal") {
		t.Errorf("the removal as a value: %v", err)
	}
	list, _ := s.Secrets(ctx, rules)
	if len(list) != 1 || list[0].Live() || list[0].Head.Kind != SecretKindRemoved || list[0].Head.Trust != TrustOK {
		t.Errorf("the list: %+v", list)
	}
	// The numbering goes on.
	if v := mustSetSecret(t, s, "db", pw2, ""); v.Seq != 3 {
		t.Errorf("created again: %+v", v)
	}
	if v := mustReadSecret(t, s, "db", 0, rules); string(v.Value) != pw2 || v.Seq != 3 {
		t.Errorf("read again: %q", v.Value)
	}
}

func TestPurgeForgetsOldValuesAndLeavesAnAuditTrail(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	ids := []*protect.Identity{ada, dan}
	roles := RecordRules{Readers: rolesFor(ids, protect.RoleAdmin, protect.RoleWriter)}
	dans := openIdentity(t, b, dan)
	mustSetSecret(t, dans, "db", pw1, "one")
	mustSetSecret(t, dans, "db", pw2, "two")
	mustSetSecret(t, dans, "db", pw3, "three")

	if _, err := s.PurgeSecret(ctx, "db", 0, "c"); err == nil || !strings.Contains(err.Error(), "live") {
		t.Errorf("purging every value of a live secret: %v", err)
	}
	if _, err := s.PurgeSecret(ctx, "nothing", 1, "c"); !errors.Is(err, ErrNoSecret) {
		t.Errorf("purging what is not there: %v", err)
	}
	n, err := s.PurgeSecret(ctx, "db", 1, "pravo secret purge db")
	if err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if n, err := s.PurgeSecret(ctx, "db", 1, "c"); err != nil || n != 0 {
		t.Errorf("purging again: %d %v", n, err)
	}
	if _, err := s.ReadSecret(ctx, "db", 1, roles); !errors.Is(err, ErrSecretPurged) {
		t.Errorf("a purged value: %v", err)
	}
	if v := mustReadSecret(t, s, "db", 0, roles); string(v.Value) != pw3 {
		t.Errorf("the newest value: %q", v.Value)
	}
	hist, err := s.SecretHistory(ctx, "db", roles)
	if err != nil || len(hist) != 3 {
		t.Fatal(hist, err)
	}
	for i, h := range hist {
		want := SecretKindPurged
		if i == 2 {
			want = SecretKindValue
		}
		if h.Kind != want || h.Trust != TrustOK || h.By != "dan" {
			t.Errorf("version %d: %+v", h.Seq, h)
		}
	}
	// The stub keeps no ciphertext.
	raw, _ := b.Get(ctx, secretKey(encodeAddr("db"), 1))
	if bytes.Contains(raw, []byte(`"sealed"`)) || !bytes.Contains(raw, []byte(`"purged": true`)) {
		t.Errorf("the stub: %s", raw)
	}
	// The purge is a transaction of its own, signed by an admin, that names each version and its commit.
	var purges int
	for _, tx := range secretTxns(t, s) {
		for _, c := range tx.Changes {
			if c.Action == "purge" {
				purges++
				if c.Record == "" || c.After != 0 || tx.FinishSig == nil {
					t.Errorf("purge change: %+v", c)
				}
			}
		}
	}
	if purges != 2 {
		t.Errorf("purge changes: %d", purges)
	}
	// A writer cannot purge by the rules: the signature of the purge is an admin's, and a purge
	// by a writer is a problem.
	if _, err := dans.PurgeSecret(ctx, "db", 1, "c"); err != nil {
		t.Fatal(err) // nothing left to purge
	}
	mustSetSecret(t, dans, "db", "four", "")
	if n, err := dans.PurgeSecret(ctx, "db", 1, "pravo secret purge db"); err != nil || n != 1 {
		t.Fatalf("a writer's purge: %d %v", n, err)
	}
	verdict, _ := s.VerifySecrets(ctx, mustTxns(t, s), roles)
	if len(verdict.Problems) != 1 || verdict.Problems[0].Version != 3 || !strings.Contains(verdict.Problems[0].Why, "purge") {
		t.Errorf("a writer's purge: %+v", verdict)
	}

	// A stub that no transaction of an admin accounts for: someone replaced a value by it.
	mustSetSecret(t, s, "other", pw1, "")
	key := secretKey(encodeAddr("other"), 1)
	cur, _ := b.Get(ctx, key)
	var f secretFile
	_ = json.Unmarshal(cur, &f)
	stub := secretFile{V: 1, At: f.At, Txn: f.Txn, Commit: f.Commit, Purged: true, PurgedBy: "20260101T000000Z-bbbbbb"}
	data, _ := json.Marshal(stub)
	_ = b.Put(ctx, key, data)
	hist, _ = s.SecretHistory(ctx, "other", roles)
	if hist[0].Trust != TrustProblem || !strings.Contains(hist[0].Problem, "purged it is not in the state") {
		t.Errorf("a stub without a purge: %+v", hist[0])
	}
	// A stub that names the transaction that wrote the version is not a purge either.
	stub.PurgedBy = f.Txn
	data, _ = json.Marshal(stub)
	_ = b.Put(ctx, key, data)
	hist, _ = s.SecretHistory(ctx, "other", roles)
	if hist[0].Trust != TrustProblem {
		t.Errorf("a stub that names the writing transaction: %+v", hist[0])
	}
}

func TestPurgeAfterRemovalLeavesNoValue(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	rules := RecordRules{Readers: readersOf(ada)}
	mustSetSecret(t, s, "db", pw1, "")
	mustSetSecret(t, s, "db", pw2, "")
	if n, _ := s.SecretValues(ctx); n != 2 {
		t.Fatalf("values: %d", n)
	}
	// An unseal would write the values in the open.
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); !errors.Is(err, ErrSecretsHeld) {
		t.Fatalf("unseal with secrets: %v", err)
	}
	if !s.sealedState() {
		t.Fatal("the refused unseal changed the state")
	}
	if _, err := s.RemoveSecret(ctx, "db", "c"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PurgeSecret(ctx, "db", 0, "c"); err != nil || n != 2 {
		t.Fatalf("purge all: %d %v", n, err)
	}
	if n, _ := s.SecretValues(ctx); n != 0 {
		t.Fatalf("values: %d", n)
	}
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
		t.Fatalf("unseal with only removals and stubs: %v", err)
	}
	// What is left is the trace: the secret is listed as removed, with every version accounted for.
	plain, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	list, err := plain.Secrets(ctx, rules)
	if err != nil || len(list) != 1 || list[0].Live() || list[0].Versions != 3 {
		t.Errorf("after the unseal: %+v %v", list, err)
	}
}

func TestRewrapSealsSecretsAgainAndGCCountsThem(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	rules := RecordRules{Readers: readersOf(ada)}
	mustSetSecret(t, s, "db", pw1, "kept")

	// dan is dropped: a new group, and the secret is still sealed in the old one.
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
		t.Fatal(err)
	}
	counts, err := s.sealedSecrets(ctx)
	if err != nil || counts["g1"] != 1 || len(counts) != 1 {
		t.Fatalf("sealed in %v %v", counts, err)
	}
	later := ahead(2*sweepGrace() + time.Minute)
	res, err := s.GC(ctx, GCOptions{Groups: true, Plans: noPlans, Now: later})
	if err != nil || len(res.Groups) != 1 || res.Groups[0].Dropped || res.Groups[0].Secrets != 1 {
		t.Fatalf("GC with a secret in the old group: %+v %v", res, err)
	}
	if !res.Groups[0].Referenced() {
		t.Error("a group with a secret in it is referenced")
	}
	// A secret written after the rewrap is in the new group.
	mustSetSecret(t, s, "api", pw2, "")
	counts, _ = s.sealedSecrets(ctx)
	if counts["g1"] != 1 || counts["g2"] != 1 {
		t.Errorf("sealed in %v", counts)
	}

	rw, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true})
	if err != nil {
		t.Fatal(err)
	}
	counts, _ = s.sealedSecrets(ctx)
	if counts["g1"] != 0 || counts["g2"] != 2 {
		t.Errorf("after rewrap --all: %v (%+v)", counts, rw)
	}
	// The commit did not change, and the signatures still account for the version.
	v := mustReadSecret(t, s, "db", 0, rules)
	if string(v.Value) != pw1 || v.Note != "kept" || v.Trust != TrustOK {
		t.Errorf("after the reseal: %q %+v", v.Value, v.SecretInfo)
	}
	res, err = s.GC(ctx, GCOptions{Groups: true, Plans: noPlans, Now: ahead(3*sweepGrace() + time.Minute)})
	if err != nil || len(res.Groups) != 1 || !res.Groups[0].Dropped {
		t.Fatalf("GC after the reseal: %+v %v", res, err)
	}
	// dan opens nothing that is left.
	gone, err := OpenBackendWith(ctx, b, Options{Identity: dan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gone.ReadSecret(ctx, "db", 0, rules); !errors.Is(err, ErrNotReader) {
		t.Errorf("a member who left reads: %v", err)
	}
	assertNoSecret(t, b, pw1)
}

func TestCopyCarriesSecrets(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	src := testBackends()[0].make(t)
	s := sealedStore(t, src, ada)
	rules := RecordRules{Readers: readersOf(ada)}
	mustSetSecret(t, s, "db", pw1, "")
	mustSetSecret(t, s, "db", pw2, "")
	dst := testBackends()[0].make(t)
	if _, err := Copy(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	if keys, _ := dst.List(ctx, secretDir); len(keys) != 2 {
		t.Fatalf("copied %v", keys)
	}
	copyOf, err := OpenBackendWith(ctx, dst, Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	if v := mustReadSecret(t, copyOf, "db", 0, rules); string(v.Value) != pw2 || v.Trust != TrustOK {
		t.Errorf("read from the copy: %q %+v", v.Value, v.SecretInfo)
	}
	if empty, _ := holdsNothing(ctx, dst); empty {
		t.Error("a backend with secrets holds something")
	}
	if _, err := Copy(ctx, src, dst); err != nil {
		t.Errorf("a second copy: %v", err)
	}
	// The source is rewrapped, which writes the ciphertext again, and copied over the old copy.
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(ctx, src, dst); err != nil {
		t.Errorf("a copy after a rewrap: %v", err)
	}
	// A version the source lacks, other bytes under a name, and a value the destination purged.
	if _, err := s.PurgeSecret(ctx, "db", 1, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(ctx, src, dst); err != nil {
		t.Errorf("a purge is carried: %v", err)
	}
	if raw, _ := dst.Get(ctx, secretKey(encodeAddr("db"), 1)); !bytes.Contains(raw, []byte("purged")) {
		t.Errorf("the destination did not take the purge: %s", raw)
	}
	older := testBackends()[0].make(t)
	if _, err := Copy(ctx, src, older); err != nil {
		t.Fatal(err)
	}
	// older holds the purged state; a source that still holds the value cannot be copied over it.
	pre := testBackends()[0].make(t)
	ps := sealedStore(t, pre, ada)
	mustSetSecret(t, ps, "db", pw1, "")
	other := testBackends()[0].make(t)
	if _, err := Copy(ctx, pre, other); err != nil {
		t.Fatal(err)
	}
	_, _ = dst.PutIfAbsent(ctx, secretKey("zzz", 1), []byte(`{}`))
	if _, err := Copy(ctx, src, dst); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("a version the source lacks: %v", err)
	}
	_ = dst.Delete(ctx, secretKey("zzz", 1))
	_ = dst.Put(ctx, secretKey(encodeAddr("db"), 2), []byte(`{"v":1,"txn":"x","commit":"y","removed":true}`))
	if _, err := Copy(ctx, src, dst); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("another version at the same name: %v", err)
	}
}

func TestSecretsCountForTheVouchesOfAnAdmin(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	dans := openIdentity(t, b, dan)
	mustSetSecret(t, dans, "db", pw1, "")
	mustSetSecret(t, s, "db", pw2, "")
	if _, err := s.PurgeSecret(ctx, "db", 1, "c"); err != nil {
		t.Fatal(err)
	}
	signers, err := s.LiveSigners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, v := range signers {
		ids[v.Txn] = true
		if v.Digest == "" || v.Key == "" {
			t.Errorf("a vouch without a digest or a key: %+v", v)
		}
	}
	for _, tx := range secretTxns(t, s) {
		if !ids[tx.ID] {
			t.Errorf("the transaction %s (%s) cannot be vouched for", tx.ID, tx.Command)
		}
	}
}
