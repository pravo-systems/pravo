package state

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// writeCounter counts the calls that change the backend.
type writeCounter struct {
	Backend
	writes atomic.Int64
}

func (b *writeCounter) Put(ctx context.Context, key string, data []byte) error {
	b.writes.Add(1)
	return b.Backend.Put(ctx, key, data)
}

func (b *writeCounter) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	b.writes.Add(1)
	return b.Backend.PutIfAbsent(ctx, key, data)
}

func (b *writeCounter) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	wrote := false
	ok, err := b.Backend.ReplaceIf(ctx, key, func(cur []byte) ([]byte, bool) {
		data, ok := update(cur)
		wrote = ok
		return data, ok
	})
	if wrote {
		b.writes.Add(1)
	}
	return ok, err
}

func (b *writeCounter) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	ok, err := b.Backend.DeleteIf(ctx, key, match)
	if ok {
		b.writes.Add(1)
	}
	return ok, err
}

func (b *writeCounter) Delete(ctx context.Context, key string) error {
	b.writes.Add(1)
	return b.Backend.Delete(ctx, key)
}

// secretValue is planted in the attributes, the private data and the outputs of
// the stores in these tests, and searched for in every object of the backend.
const secretValue = "hunter2-correct-horse-battery-staple-0451"

func testIdentity(t *testing.T, name string) *protect.Identity {
	t.Helper()
	id, err := protect.NewIdentity(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(id.Close)
	return id
}

// outputsJSON is outputs in a form that two stores can be compared by.
func outputsJSON(m map[string]json.RawMessage) string {
	data, _ := json.Marshal(m)
	return string(data)
}

func readersOf(ids ...*protect.Identity) []protect.Reader {
	out := make([]protect.Reader, len(ids))
	for i, id := range ids {
		out[i] = id.AsReader()
	}
	return out
}

// seedSecrets writes records and outputs that carry secretValue where a value
// can be: the attributes, the private data, the configuration digests.
func seedSecrets(t *testing.T, s *Store) {
	t.Helper()
	when := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	mustSet(t, s,
		&Resource{Mode: "managed", Type: "db", Name: "main", Provider: "p", ProviderKey: "p/db", ProviderVersion: "1.2.3", SchemaVersion: 3,
			Attributes: json.RawMessage(`{"id":"db-1","password":"` + secretValue + `","note":"<&>"}`), Private: []byte("private:" + secretValue),
			ConfigHash: "c0ffee00c0ffee00", ProviderConfig: "feedbeef", RefreshedAt: &when, AppliedAt: &when},
		&Resource{Mode: "managed", Type: "net", Name: "vpc", Attributes: json.RawMessage(`{"id":"vpc-1"}`), Dependencies: []string{"db.main"}, DriftedAt: &when},
		&Resource{Mode: "data", Type: "dns", Name: "zone", Attributes: json.RawMessage(`{"name":"example.com","token":"` + secretValue + `"}`)},
		&Resource{Mode: "managed", Type: "db", Name: "gone", Attributes: json.RawMessage(`{"password":"` + secretValue + `"}`), Gone: true},
	)
	// A second revision, so that the first one is a record of its own in format 2.
	again := *s.Get("db.main")
	again.Attributes = json.RawMessage(`{"id":"db-1","password":"` + secretValue + `","rev":2}`)
	mustSet(t, s, &again)
	if err := s.SetOutputs(map[string]json.RawMessage{
		"password": json.RawMessage(`{"$sensitive":"` + secretValue + `"}`),
		"url":      json.RawMessage(`"https://example.com"`),
	}); err != nil {
		t.Fatal(err)
	}
}

// needles returns the forms in which secretValue could appear in a stored object:
// as it is, in hex, and in every base64 alphabet and alignment.
func needles(secret string) []string {
	out := []string{secret, hex.EncodeToString([]byte(secret)), strings.ToUpper(hex.EncodeToString([]byte(secret)))}
	for skip := range 3 {
		rest := []byte(secret)[skip:]
		rest = rest[:len(rest)/3*3]
		out = append(out,
			base64.StdEncoding.EncodeToString(rest),
			base64.URLEncoding.EncodeToString(rest))
	}
	return out
}

// allObjects reads every object of the state that a thief with the bucket sees.
func allObjects(t *testing.T, b Backend) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	out := map[string][]byte{}
	for _, prefix := range []string{"resources", "rec", "txn", "plans", "locks"} {
		for _, k := range listKeys(t, b, prefix) {
			data, err := b.Get(ctx, k)
			if err != nil {
				continue
			}
			out[k] = data
		}
	}
	for _, k := range []string{"meta.json", "outputs.json"} {
		if data, err := b.Get(ctx, k); err == nil {
			out[k] = data
		}
	}
	return out
}

// assertNoSecret fails if a needle is in any object, or in any string of an object
// that is base64.
func assertNoSecret(t *testing.T, b Backend, secret string) {
	t.Helper()
	objs := allObjects(t, b)
	if len(objs) == 0 {
		t.Fatal("no objects to search")
	}
	for k, data := range objs {
		for _, n := range needles(secret) {
			if bytes.Contains(data, []byte(n)) {
				t.Errorf("%s holds the secret (as %q...)", k, n[:min(len(n), 12)])
			}
		}
	}
}

// sealedStore makes a store of format 1 with secrets, seals it to the identities
// and returns the store that did it, opened by the first.
func sealedStore(t *testing.T, b Backend, ids ...*protect.Identity) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format, Identity: ids[0]})
	if err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, s)
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ids...), Identity: ids[0]}); err != nil {
		t.Fatal(err)
	}
	return s
}

func openIdentity(t *testing.T, b Backend, id *protect.Identity) *Store {
	t.Helper()
	s, err := OpenBackendWith(context.Background(), b, Options{Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A store of format 1 or 2 is sealed to its readers; the readers see all of it, and
// the records are pointers.
func TestUpgradeToSealed(t *testing.T) {
	for _, bk := range testBackends() {
		for _, from := range []int{Format, FormatImmutable} {
			t.Run(fmt.Sprintf("%s from format %d", bk.name, from), func(t *testing.T) {
				b := bk.make(t)
				ctx := context.Background()
				ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
				s, err := OpenBackendWith(ctx, b, Options{NewFormat: from, Identity: ada})
				if err != nil {
					t.Fatal(err)
				}
				seedSecrets(t, s)
				before := listJSON(t, s)
				beforeOut, _, _ := s.LoadOutputs()
				txns, _ := s.Transactions()

				var calls atomic.Int64
				res, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada, dan), Identity: ada,
					Progress: func(done, total int) { calls.Add(1) },
					Plans:    func(context.Context) (int, int, error) { return 2, 1, nil }})
				if err != nil {
					t.Fatal(err)
				}
				if res.From != from || res.To != FormatSealed || res.Records != s.Len() || res.Resumed || res.PlansSealed != 2 || res.PlansSkipped != 1 || calls.Load() != int64(s.Len()) {
					t.Errorf("result %+v, %d progress calls", res, calls.Load())
				}
				if formatOf(t, b) != FormatSealed || s.Format() != FormatSealed {
					t.Errorf("format %d (in memory %d)", formatOf(t, b), s.Format())
				}
				if got := listJSON(t, s); got != before {
					t.Errorf("the store that was sealed lists other records:\n%s\n%s", got, before)
				}
				assertNoSecret(t, b, secretValue)

				for _, id := range []*protect.Identity{ada, dan} {
					r := openIdentity(t, b, id)
					if r.Locked() || listJSON(t, r) != before {
						t.Errorf("%s: locked %v, or the records differ", id.Name, r.Locked())
					}
					if out, _, err := r.LoadOutputs(); err != nil || outputsJSON(out) != outputsJSON(beforeOut) {
						t.Errorf("%s: outputs %v, %v", id.Name, out, err)
					}
					if got, _ := r.Transactions(); len(got) != len(txns) {
						t.Errorf("%s: %d transactions", id.Name, len(got))
					}
					if v, err := r.VerifyAll(ctx); err != nil || len(v.Bad) != 0 || v.Checked != r.Len() {
						t.Errorf("%s: VerifyAll = %+v, %v", id.Name, v, err)
					}
				}

				p := s.Protection()
				if p.State != "sealed" || p.Format != 3 || p.Records != s.Len() || p.Plain != 0 || p.Unreadable != 0 || p.Migration != nil ||
					p.ByGroup["g1"] != s.Len() || p.Sealed == nil || p.Sealed.Current != "g1" || p.You.State != "reader" || p.You.Name != "ada" ||
					p.You.Key != ada.Reader.Public().String() || len(p.History) != 1 || p.History[0].What != "sealed" || p.History[0].By != "ada" ||
					fmt.Sprint(p.History[0].Added) != "[ada dan]" {
					t.Errorf("Protection = %+v", p)
				}

				// A store that is sealed is written like any other, and stays sealed.
				r := openIdentity(t, b, dan)
				changed := *r.Get("db.main")
				changed.Attributes = json.RawMessage(`{"password":"` + secretValue + `","rev":3}`)
				mustSet(t, r, &changed)
				if err := r.SetOutputs(map[string]json.RawMessage{"k": json.RawMessage(`"` + secretValue + `"`)}); err != nil {
					t.Fatal(err)
				}
				assertNoSecret(t, b, secretValue)
				if got := openIdentity(t, b, ada).Get("db.main"); got == nil || got.Revision != 3 || !strings.Contains(string(got.Attributes), `"rev":3`) {
					t.Errorf("the record written after the seal reads back as %+v", got)
				}

				// Running it again writes nothing.
				counted := &writeCounter{Backend: b}
				again := openIdentity(t, counted, ada)
				counted.writes.Store(0)
				if res, err := again.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada, dan), Identity: ada}); err != nil || res.Records != 0 {
					t.Errorf("second upgrade = %+v, %v", res, err)
				}
				if n := counted.writes.Load(); n != 0 {
					t.Errorf("a store that is sealed was written by a second upgrade: %d writes", n)
				}
			})
		}
	}
}

// What a stranger with the bucket can do: list the graph, and nothing else.
func TestSealedStateOpensLockedWithoutAKey(t *testing.T) {
	skipWhenSealed(t)
	b, _ := NewLocalBackend(t.TempDir())
	ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")
	sealed := sealedStore(t, b, ada)
	want := map[string]Resource{}
	for _, r := range sealed.List() {
		want[r.Address()] = *r
	}

	for name, id := range map[string]*protect.Identity{"no identity": nil, "an identity that is not a reader": eve} {
		t.Run(name, func(t *testing.T) {
			s := openIdentity(t, b, id)
			if !s.Locked() {
				t.Fatal("the store is not locked")
			}
			if s.Len() != len(want) {
				t.Fatalf("%d records listed, want %d", s.Len(), len(want))
			}
			for _, r := range s.List() {
				w := want[r.Address()]
				if r.Attributes != nil || r.Private != nil || r.ConfigHash != "" || r.ProviderConfig != "" {
					t.Errorf("%s: a value is readable without a key: %+v", r.Address(), r)
				}
				if r.Type != w.Type || r.Name != w.Name || r.Mode != w.Mode || r.Revision != w.Revision || r.Generation != w.Generation ||
					fmt.Sprint(r.Dependencies) != fmt.Sprint(w.Dependencies) || r.Gone != w.Gone || (r.DriftedAt == nil) != (w.DriftedAt == nil) ||
					r.Provider != w.Provider || r.ProviderKey != w.ProviderKey || r.ProviderVersion != w.ProviderVersion || r.SchemaVersion != w.SchemaVersion {
					t.Errorf("%s: the readable part differs: %+v, want %+v", r.Address(), r, w)
				}
			}
			if got := s.Dependents("db.main"); fmt.Sprint(got) != "[net.vpc]" {
				t.Errorf("Dependents = %v", got)
			}
			if len(s.Outputs()) != 0 {
				t.Errorf("outputs are readable: %v", s.Outputs())
			}
			if _, _, err := s.LoadOutputs(); !errors.Is(err, ErrNotReader) {
				t.Errorf("LoadOutputs = %v, want ErrNotReader", err)
			}
			r2 := want["net.vpc"]
			if err := s.Set(&r2, ""); !errors.Is(err, ErrNotReader) {
				t.Errorf("Set = %v, want ErrNotReader", err)
			}
			if err := s.Refresh(&r2, ""); !errors.Is(err, ErrNotReader) {
				t.Errorf("Refresh = %v, want ErrNotReader", err)
			}
			if _, _, _, err := s.Invalidate("net.vpc", ""); !errors.Is(err, ErrNotReader) {
				t.Errorf("Invalidate = %v, want ErrNotReader", err)
			}
			if _, err := s.Remove("net.vpc"); !errors.Is(err, ErrNotReader) {
				t.Errorf("Remove = %v, want ErrNotReader", err)
			}
			if err := s.SetOutputs(map[string]json.RawMessage{"x": json.RawMessage(`1`)}); !errors.Is(err, ErrNotReader) {
				t.Errorf("SetOutputs = %v, want ErrNotReader", err)
			}
			if err := s.UpdateOutputs(func(m map[string]json.RawMessage, _ bool) (map[string]json.RawMessage, bool, error) {
				return m, true, nil
			}); !errors.Is(err, ErrNotReader) {
				t.Errorf("UpdateOutputs = %v, want ErrNotReader", err)
			}
			if err := s.Begin("apply", "pravo apply").Start(); !errors.Is(err, ErrNotReader) {
				t.Errorf("Txn.Start = %v, want ErrNotReader", err)
			}
			if sl := s.Sealer(); sl == nil {
				t.Error("a locked store has no sealer")
			} else if _, err := sl.Seal(protect.KindPlan, "plan-1", []byte("x")); !errors.Is(err, ErrNotReader) {
				t.Errorf("Seal = %v, want ErrNotReader", err)
			}
			if _, err := s.Rewrap(context.Background(), RewrapOptions{Readers: readersOf(ada)}); !errors.Is(err, ErrNotReader) {
				t.Errorf("Rewrap = %v, want ErrNotReader", err)
			}
			if _, err := s.Upgrade(context.Background(), UpgradeOptions{To: FormatSealed}); !errors.Is(err, ErrNotReader) {
				t.Errorf("Upgrade = %v, want ErrNotReader", err)
			}
			p := s.Protection()
			wantYou := "no-identity"
			if id != nil {
				wantYou = "not-reader"
			}
			if p.You.State != wantYou || p.Unreadable != s.Len() || p.State != "sealed" {
				t.Errorf("Protection = %+v", p)
			}
			if v, err := s.VerifyAll(context.Background()); err != nil || len(v.Bad) != 0 || v.Checked != s.Len() {
				t.Errorf("VerifyAll without a key = %+v, %v", v, err)
			}
		})
	}
}

// A sealed part does not open anywhere but where it was sealed.
func TestSealedRecordsAreBoundToTheirPlace(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")

	// edit applies f to the object of the record at addr and puts it back under the
	// name of its new bytes, with a pointer that names it.
	edit := func(t *testing.T, b Backend, addr string, f func(rec map[string]any, p *pointer)) {
		t.Helper()
		p := pointerAt(t, b, addr)
		data, err := b.Get(ctx, p.Record)
		if err != nil {
			t.Fatal(err)
		}
		var rec map[string]any
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		f(rec, &p)
		out := mustIndent(t, rec)
		p.Record = recKey(out)
		if err := b.Put(ctx, p.Record, out); err != nil {
			t.Fatal(err)
		}
		if err := b.Put(ctx, nodeKey(addr), mustIndent(t, p)); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a pointer at the record of another address", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		stranger := openIdentity(t, b, nil)
		other := pointerAt(t, b, "net.vpc")
		p := pointerAt(t, b, "db.main")
		p.Record = other.Record
		if err := b.Put(ctx, nodeKey("db.main"), mustIndent(t, p)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []*protect.Identity{ada, nil} {
			if _, err := OpenBackendWith(ctx, b, Options{Identity: id}); !errors.Is(err, ErrUntrusted) {
				t.Errorf("Open = %v, want ErrUntrusted", err)
			}
		}
		if v, err := stranger.VerifyAll(ctx); err != nil || len(v.Bad) != 1 || !strings.Contains(v.Bad[0], "db.main") {
			t.Errorf("VerifyAll without a key = %+v, %v, want db.main to be bad", v, err)
		}
	})
	t.Run("a pointer whose revision is changed", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		p := pointerAt(t, b, "db.main")
		p.Revision++
		if err := b.Put(ctx, nodeKey("db.main"), mustIndent(t, p)); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenBackendWith(ctx, b, Options{Identity: ada}); !errors.Is(err, ErrUntrusted) {
			t.Errorf("Open = %v, want ErrUntrusted", err)
		}
	})
	t.Run("a record whose sealed part is removed", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		edit(t, b, "db.main", func(rec map[string]any, p *pointer) {
			delete(rec, "sealed")
			rec["attributes"] = map[string]any{"password": "forged"}
		})
		for _, id := range []*protect.Identity{ada, nil} {
			if _, err := OpenBackendWith(ctx, b, Options{Identity: id}); !errors.Is(err, ErrUntrusted) {
				t.Errorf("Open = %v, want ErrUntrusted", err)
			}
		}
	})
	t.Run("a whole record where a pointer should be", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		whole := map[string]any{"addr": "db.main", "mode": "managed", "type": "t", "name": "main", "revision": 1, "attributes": map[string]any{"password": "forged"}}
		if err := b.Put(ctx, nodeKey("db.main"), mustIndent(t, whole)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []*protect.Identity{ada, nil} {
			if _, err := OpenBackendWith(ctx, b, Options{Identity: id}); !errors.Is(err, ErrUntrusted) {
				t.Errorf("Open = %v, want ErrUntrusted", err)
			}
		}
	})
	t.Run("a sealed part moved to another address", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		// The readable part names the address of vpc, the sealed part is db.main's, and
		// the pointer and the record agree: only the sealed part knows where it is from.
		secret := func(addr string) any {
			p := pointerAt(t, b, addr)
			data, _ := b.Get(ctx, p.Record)
			var rec map[string]any
			_ = json.Unmarshal(data, &rec)
			return rec["sealed"]
		}
		moved := secret("db.main")
		edit(t, b, "net.vpc", func(rec map[string]any, p *pointer) { rec["sealed"] = moved })
		if _, err := OpenBackendWith(ctx, b, Options{Identity: ada}); !errors.Is(err, ErrUntrusted) {
			t.Errorf("Open = %v, want ErrUntrusted", err)
		}
		// Without a key nothing can be told, and the graph still lists.
		if s := openIdentity(t, b, nil); s.Len() != 4 {
			t.Errorf("a locked store lists %d records", s.Len())
		}
	})
	t.Run("a record whose readable revision is raised in both places", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		edit(t, b, "db.main", func(rec map[string]any, p *pointer) {
			rec["revision"] = float64(p.Revision + 5)
			p.Revision += 5
		})
		if _, err := OpenBackendWith(ctx, b, Options{Identity: ada}); !errors.Is(err, ErrUntrusted) {
			t.Errorf("Open = %v, want ErrUntrusted", err)
		}
	})
	t.Run("a record of another state", func(t *testing.T) {
		b1, _ := NewLocalBackend(t.TempDir())
		b2, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b1, ada)
		// The same identity seals a second state; its lineage is its own.
		s2, err := OpenBackendWith(ctx, b2, Options{NewFormat: Format, Identity: ada})
		if err != nil {
			t.Fatal(err)
		}
		seedSecrets(t, s2)
		if _, err := s2.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
			t.Fatal(err)
		}
		p := pointerAt(t, b1, "db.main")
		data, _ := b1.Get(ctx, p.Record)
		p2 := pointerAt(t, b2, "db.main")
		// Put the record of the first state into the second under its own name.
		if err := b2.Put(ctx, p.Record, data); err != nil {
			t.Fatal(err)
		}
		p.Record = recKey(data)
		if err := b2.Put(ctx, nodeKey("db.main"), mustIndent(t, p)); err != nil {
			t.Fatal(err)
		}
		_ = p2
		if _, err := OpenBackendWith(ctx, b2, Options{Identity: ada}); !errors.Is(err, ErrUntrusted) {
			t.Errorf("Open = %v, want ErrUntrusted", err)
		}
	})
}

// Readers come and go: a new reader opens everything, and a reader that is removed
// opens nothing that is written after.
func TestRewrapAddsAndRemovesReaders(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			ada, fay, dan := testIdentity(t, "ada"), testIdentity(t, "fay"), testIdentity(t, "dan")
			s := sealedStore(t, b, ada, fay)
			g1Meta, _ := b.Get(ctx, "meta.json") // what fay can keep

			// Nothing to do.
			counted := &writeCounter{Backend: b}
			cs := openIdentity(t, counted, ada)
			counted.writes.Store(0)
			if res, err := cs.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, fay)}); err != nil || len(res.Change.Added) != 0 || len(res.Change.Removed) != 0 || counted.writes.Load() != 0 {
				t.Errorf("rewrap of the same readers = %+v, %v, %d writes", res, err, counted.writes.Load())
			}

			// Dan joins: he opens the records that were sealed before he was a reader.
			res, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, fay, dan)})
			if err != nil || fmt.Sprint(res.Change.Added) != "[dan]" || res.Change.NewGroup != "" {
				t.Fatalf("rewrap to add = %+v, %v", res, err)
			}
			want := listJSON(t, s)
			if got := openIdentity(t, b, dan); got.Locked() || listJSON(t, got) != want {
				t.Errorf("dan, a new reader, sees %v locked", got.Locked())
			}
			if m := formatMeta(t, b); len(m.History) != 2 || m.History[1].What != "rewrap" || fmt.Sprint(m.History[1].Added) != "[dan]" {
				t.Errorf("history = %+v", m.History)
			}

			// Fay leaves: a new group, and what is written after is sealed in it.
			res, err = s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan)})
			if err != nil || fmt.Sprint(res.Change.Removed) != "[fay]" || res.Change.NewGroup != "g2" {
				t.Fatalf("rewrap to remove = %+v, %v", res, err)
			}
			if got := openIdentity(t, b, fay); !got.Locked() || got.Protection().You.State != "not-reader" {
				t.Error("fay, who was removed, opens the store")
			}
			if p := s.Protection(); p.Sealed.Current != "g2" || len(p.Sealed.Groups) != 2 {
				t.Errorf("groups after the removal: %+v", p.Sealed)
			}
			changed := *s.Get("db.main")
			changed.Attributes = json.RawMessage(`{"password":"` + secretValue + `","after":"fay"}`)
			mustSet(t, s, &changed)
			if got := pointerAt(t, b, "db.main"); recGroup(t, b, got.Record) != "g2" {
				t.Errorf("a record written after the removal is sealed in %s", recGroup(t, b, got.Record))
			}
			if got := pointerAt(t, b, "net.vpc"); recGroup(t, b, got.Record) != "g1" {
				t.Errorf("a record that was not written stays in g1, and is in %s", recGroup(t, b, got.Record))
			}
			p := openIdentity(t, b, dan).Protection()
			if p.ByGroup["g1"] != 3 || p.ByGroup["g2"] != 1 || p.Unreadable != 0 {
				t.Errorf("records by group = %v, unreadable %d", p.ByGroup, p.Unreadable)
			}
			for _, id := range []*protect.Identity{ada, dan} {
				if got := openIdentity(t, b, id); got.Locked() || got.Get("db.main") == nil || !strings.Contains(string(got.Get("db.main").Attributes), "after") {
					t.Errorf("%s does not read the record written after the removal", id.Name)
				}
			}

			// Fay keeps the meta.json she had. It opens what is in g1; a record in g2 is
			// in a group that this meta.json does not list, which is no state that a
			// pravo wrote, so the bucket fails verification. A copy of the bucket from
			// before the removal is hers, and no more.
			old, _ := NewLocalBackend(t.TempDir())
			for k, data := range allObjects(t, b) {
				if err := old.Put(ctx, k, data); err != nil {
					t.Fatal(err)
				}
			}
			if err := old.Put(ctx, "meta.json", g1Meta); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenBackendWith(ctx, old, Options{Identity: fay}); !errors.Is(err, ErrUntrusted) {
				t.Errorf("fay with her old meta.json opens a store that has a record in g2: %v", err)
			}
			_ = old.Delete(ctx, pointerAt(t, b, "db.main").Record)
			_ = old.Delete(ctx, nodeKey("db.main"))
			if got, err := OpenBackendWith(ctx, old, Options{Identity: fay}); err != nil || got.Locked() || got.Get("net.vpc") == nil {
				t.Errorf("fay cannot open what was sealed before she left: %v", err)
			}

			// rewrap --all seals everything again in g2, and fay's old key opens nothing.
			// The record that the write above replaced is old enough for the sweep.
			defer func(was func() time.Duration) { sweepGrace = was }(sweepGrace)
			sweepGrace = func() time.Duration { return 0 }
			res, err = s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan), All: true, Plans: func(context.Context) (int, int, error) { return 1, 2, nil }})
			if err != nil || res.Resealed != 3 || res.Change.NewGroup != "" || res.PlansSealed != 1 || res.PlansSkipped != 2 {
				t.Fatalf("rewrap --all = %+v, %v", res, err)
			}
			for _, r := range s.List() {
				if g := recGroup(t, b, pointerAt(t, b, r.Address()).Record); g != "g2" {
					t.Errorf("%s is in %s after rewrap --all", r.Address(), g)
				}
			}
			if keys := listKeys(t, b, "rec"); len(keys) != s.Len() {
				t.Errorf("%d records for %d nodes after rewrap --all: the old ones are kept", len(keys), s.Len())
			}
			out, _, err := openIdentity(t, b, dan).LoadOutputs()
			if err != nil || len(out) != 2 || !strings.Contains(string(out["password"]), secretValue) {
				t.Errorf("outputs after rewrap --all: %v, %v", outputsJSON(out), err)
			}
			if env, ok := asSealedOutputs(allObjects(t, b)["outputs.json"]); !ok || env.Sealed.Group != "g2" {
				t.Errorf("outputs after rewrap --all are sealed in %q", env.Sealed.Group)
			}
			assertNoSecret(t, b, secretValue)
			if got := openIdentity(t, b, dan); listJSON(t, got) != listJSON(t, s) {
				t.Error("rewrap --all changed the records")
			}
			if p := openIdentity(t, b, ada).Protection(); p.ByGroup["g2"] != s.Len() || len(p.ByGroup) != 1 {
				t.Errorf("records by group = %v", p.ByGroup)
			}
			if m := formatMeta(t, b); m.History[len(m.History)-1].What != "rewrap --all" {
				t.Errorf("history = %+v", m.History)
			}

			// A rewrap that would leave no reader is refused, and so is one by a reader
			// who has gone.
			if _, err := s.Rewrap(ctx, RewrapOptions{}); err == nil {
				t.Error("a rewrap to no readers was accepted")
			}
		})
	}
}

func formatMeta(t *testing.T, b Backend) meta {
	t.Helper()
	m, err := readMeta(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return *m
}

// recGroup is the group the record at key is sealed in.
func recGroup(t *testing.T, b Backend, key string) string {
	t.Helper()
	data, err := b.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Sealed *protect.Sealed `json:"sealed"`
	}
	if err := json.Unmarshal(data, &rec); err != nil || rec.Sealed == nil {
		t.Fatalf("%s is not sealed: %v", key, err)
	}
	return rec.Sealed.Group
}

// A rewrap that races another is not lost: it is told to run again, or it retries.
func TestRewrapAgainstAChangedMeta(t *testing.T) {
	b, _ := NewLocalBackend(t.TempDir())
	ctx := context.Background()
	ada, dan, eve := testIdentity(t, "ada"), testIdentity(t, "dan"), testIdentity(t, "eve")
	s1 := sealedStore(t, b, ada)
	s2 := openIdentity(t, b, ada)
	if _, err := s1.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan)}); err != nil {
		t.Fatal(err)
	}
	// s2 holds the meta from before, and the rewrap works on what is there now.
	res, err := s2.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan, eve)})
	if err != nil || fmt.Sprint(res.Change.Added) != "[eve]" {
		t.Fatalf("rewrap from an older view = %+v, %v", res, err)
	}
	if got := openIdentity(t, b, dan); got.Locked() {
		t.Error("dan, added by the first rewrap, lost his lock to the second")
	}
}

// The seal is refused when the state is in use, when the identity is not a reader,
// and when a rewrap or a write is tried while it is under way.
func TestUpgradeToSealedRefusals(t *testing.T) {
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")
	s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format})
	if err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, s)

	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada)}); !errors.Is(err, ErrNotReader) {
		t.Errorf("seal with no identity = %v", err)
	}
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: eve}); !errors.Is(err, ErrNotReader) {
		t.Errorf("seal with an identity that is not in the list = %v", err)
	}
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Identity: ada}); err == nil {
		t.Error("seal to no readers was accepted")
	}
	if formatOf(t, b) != Format {
		t.Error("a refused seal changed the format")
	}
	other := openIdentity(t, b, nil)
	if err := other.Lock("db.main", "pravo apply", "txn"); err != nil {
		t.Fatal(err)
	}
	var locked *ErrLocked
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); !errors.As(err, &locked) {
		t.Errorf("seal with a lock held = %v, want ErrLocked", err)
	}
	if formatOf(t, b) != Format {
		t.Error("a refused seal changed the format")
	}
	if err := other.Unlock("db.main"); err != nil {
		t.Fatal(err)
	}
	// The outputs lock counts as well.
	l := s.newLock("outputs.json", "update outputs", "")
	data, _ := indentJSON(l)
	if _, err := b.PutIfAbsent(ctx, outputsLockKey, data); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); !errors.As(err, &locked) {
		t.Errorf("seal with the outputs locked = %v, want ErrLocked", err)
	}
	_ = b.Delete(ctx, outputsLockKey)
}

// A machine that goes down at any point of a seal leaves a store that the readers
// open whole and that the seal finishes when it is run again; until then the
// format is 3, so that a pravo that does not know it refuses the store, and
// writes are refused.
func TestUpgradeToSealedSurvivesACrash(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	build := func(t *testing.T, from int) (*Store, *failAfter, Backend) {
		lb, _ := NewLocalBackend(t.TempDir())
		s, err := OpenBackendWith(ctx, lb, Options{NewFormat: from, Identity: ada})
		if err != nil {
			t.Fatal(err)
		}
		seedSecrets(t, s)
		fb := &failAfter{Backend: lb}
		fb.left.Store(1 << 30)
		s, err = OpenBackendWith(ctx, fb, Options{Identity: ada})
		if err != nil {
			t.Fatal(err)
		}
		return s, fb, lb
	}
	up := UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}
	// A lock on the outputs that a crash leaves is stale after a minute; the process
	// of these tests is alive, so the minute is shortened.
	defer func(d time.Duration) { outputsLockStale = d }(outputsLockStale)
	for _, from := range []int{Format, FormatImmutable} {
		s, fb, _ := build(t, from)
		if _, err := s.Upgrade(ctx, up); err != nil {
			t.Fatal(err)
		}
		total := int(fb.seen.Load())
		if total < 2*s.Len() {
			t.Fatalf("a seal of %d records made %d writes", s.Len(), total)
		}
		for _, n := range []int{0, 1, 2, total / 2, total - 3, total - 1} {
			t.Run(fmt.Sprintf("from format %d after %d of %d writes", from, n, total), func(t *testing.T) {
				outputsLockStale = time.Hour
				s, fb, lb := build(t, from)
				want := listJSON(t, s)
				wantOut, _, _ := s.LoadOutputs()
				fb.left.Store(int64(n))
				if _, err := s.Upgrade(ctx, up); !errors.Is(err, errCrash) {
					t.Fatalf("seal that crashed: %v", err)
				}
				m := formatMeta(t, lb)
				switch n {
				case 0:
					if m.Format != from || m.Sealed != nil {
						t.Errorf("a seal that wrote nothing changed meta.json: %+v", m)
					}
				default:
					if m.Format != FormatSealed || m.Migration == nil {
						t.Errorf("a seal that stopped halfway left meta.json at format %d, migration %v", m.Format, m.Migration)
					}
				}
				mid, err := OpenBackendWith(ctx, lb, Options{Identity: ada})
				if err != nil {
					t.Fatalf("open after the crash: %v", err)
				}
				if listJSON(t, mid) != want {
					t.Errorf("the reader's view after the crash differs")
				}
				if out, _, _ := mid.LoadOutputs(); outputsJSON(out) != outputsJSON(wantOut) {
					t.Errorf("outputs after the crash: %v", out)
				}
				if locked := openIdentity(t, lb, nil); locked.Len() != mid.Len() {
					t.Errorf("a stranger lists %d of %d records", locked.Len(), mid.Len())
				}
				if v, err := mid.VerifyAll(ctx); err != nil || len(v.Bad) != 0 {
					t.Errorf("VerifyAll after the crash = %+v, %v", v, err)
				}
				if n > 0 {
					p := mid.Protection()
					if p.State != "migrating" || p.Migration == nil || p.Migration.By != "ada" || p.Migration.Total != mid.Len() {
						t.Errorf("Protection after the crash = %+v", p)
					}
					r := *mid.Get("net.vpc")
					if err := mid.Set(&r, ""); !errors.Is(err, ErrSealing) {
						t.Errorf("Set while a seal is under way = %v, want ErrSealing", err)
					}
					if err := mid.SetOutputs(nil); !errors.Is(err, ErrSealing) {
						t.Errorf("SetOutputs while a seal is under way = %v, want ErrSealing", err)
					}
					if _, err := mid.Remove("net.vpc"); !errors.Is(err, ErrSealing) {
						t.Errorf("Remove while a seal is under way = %v, want ErrSealing", err)
					}
					if err := mid.Begin("apply", "x").Start(); !errors.Is(err, ErrSealing) {
						t.Errorf("Txn.Start while a seal is under way = %v, want ErrSealing", err)
					}
					if _, err := mid.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); !errors.Is(err, ErrSealing) {
						t.Errorf("Rewrap while a seal is under way = %v, want ErrSealing", err)
					}
				}

				outputsLockStale = time.Nanosecond // the lock the crash left is gone by now
				res, err := mid.Upgrade(ctx, up)
				if err != nil {
					t.Fatalf("second seal: %v", err)
				}
				if n > 0 && !res.Resumed {
					t.Errorf("second seal did not say it resumed: %+v", res)
				}
				done := openIdentity(t, lb, ada)
				if listJSON(t, done) != want {
					t.Errorf("the finished store lists other records")
				}
				if out, _, _ := done.LoadOutputs(); outputsJSON(out) != outputsJSON(wantOut) {
					t.Errorf("outputs after the seal: %v", out)
				}
				if p := done.Protection(); p.State != "sealed" || p.Plain != 0 || p.Migration != nil {
					t.Errorf("Protection after the seal = %+v", p)
				}
				if v, err := done.VerifyAll(ctx); err != nil || len(v.Bad) != 0 || v.Checked != done.Len() {
					t.Errorf("VerifyAll after the seal = %+v, %v", v, err)
				}
				assertNoSecret(t, lb, secretValue)
				// Another identity resumes a seal it is not a reader of: it cannot.
			})
		}
	}
}

// A store opened with another identity cannot finish a seal that is not its own.
func TestResumingASealNeedsAReader(t *testing.T) {
	ctx := context.Background()
	ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")
	lb, _ := NewLocalBackend(t.TempDir())
	s, _ := OpenBackendWith(ctx, lb, Options{NewFormat: Format})
	seedSecrets(t, s)
	fb := &failAfter{Backend: lb}
	fb.left.Store(4)
	s, _ = OpenBackendWith(ctx, fb, Options{})
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); !errors.Is(err, errCrash) {
		t.Fatalf("seal that crashed: %v", err)
	}
	stranger := openIdentity(t, lb, eve)
	if _, err := stranger.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(eve), Identity: eve}); !errors.Is(err, ErrNotReader) {
		t.Errorf("resume by an identity that is not a reader = %v, want ErrNotReader", err)
	}
	// A store opened without a key resumes with the identity it is given.
	locked := openIdentity(t, lb, nil)
	if _, err := locked.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
		t.Errorf("resume by a store opened without a key: %v", err)
	}
	if p := openIdentity(t, lb, ada).Protection(); p.State != "sealed" {
		t.Errorf("state after the resume: %+v", p)
	}
}

// The outputs of another state are read with an identity, or not at all.
func TestSealedOutputsOfAnotherRoot(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")
	sealedStore(t, b, ada)
	if _, err := ReadOutputs(ctx, b); !errors.Is(err, ErrNotReader) || !strings.Contains(err.Error(), "sealed") {
		t.Errorf("ReadOutputs of sealed outputs = %v, want an error that says they are sealed", err)
	}
	out, err := OpenOutputs(ctx, b, ada)
	if err != nil || string(out["url"]) != `"https://example.com"` || !strings.Contains(string(out["password"]), secretValue) {
		t.Errorf("OpenOutputs = %v, %v", out, err)
	}
	if _, err := OpenOutputs(ctx, b, eve); !errors.Is(err, ErrNotReader) {
		t.Errorf("OpenOutputs with a stranger = %v, want ErrNotReader", err)
	}
	if _, err := OpenOutputs(ctx, b, nil); !errors.Is(err, ErrNotReader) {
		t.Errorf("OpenOutputs with no identity = %v, want ErrNotReader", err)
	}
	// Outputs that are not sealed are read as before.
	plain, _ := NewLocalBackend(t.TempDir())
	ps, _ := OpenBackend(ctx, plain)
	_ = ps.SetOutputs(map[string]json.RawMessage{"a": json.RawMessage(`1`)})
	if got, err := OpenOutputs(ctx, plain, nil); err != nil || string(got["a"]) != "1" {
		t.Errorf("OpenOutputs of plain outputs = %v, %v", got, err)
	}
	// An output that is named like the envelope is not taken for one.
	odd := map[string]json.RawMessage{"v": json.RawMessage(`1`), "sealed": json.RawMessage(`{"alg":"x","group":"g1","ct":"AAAA"}`)}
	if _, ok := asSealedOutputs(mustIndent(t, odd)); ok {
		t.Log("outputs named v and sealed look like an envelope; they are told apart by the state being sealed")
	}
}

// The sealer seals what is not a record, bound to what it is.
func TestSealer(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")
	plain, _ := OpenBackend(ctx, mustLocal2(t))
	if plain.Sealer() != nil {
		t.Error("a state that is not sealed has a sealer")
	}
	b, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, b, ada)
	sl := openIdentity(t, b, ada).Sealer()
	box, err := sl.Seal(protect.KindPlan, "plan-1", []byte("a plan"))
	if err != nil || box.Group != "g1" {
		t.Fatalf("Seal = %+v, %v", box, err)
	}
	if got, err := sl.Open(protect.KindPlan, "plan-1", box); err != nil || string(got) != "a plan" {
		t.Errorf("Open = %q, %v", got, err)
	}
	for _, c := range []struct{ kind, name string }{{protect.KindPlan, "plan-2"}, {protect.KindPlanCmd, "plan-1"}, {protect.KindOutputs, ""}} {
		if _, err := sl.Open(c.kind, c.name, box); !errors.Is(err, ErrUntrusted) {
			t.Errorf("Open as %s %q = %v, want ErrUntrusted", c.kind, c.name, err)
		}
	}
	// Another state does not open it.
	b2, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, b2, ada)
	if _, err := openIdentity(t, b2, ada).Sealer().Open(protect.KindPlan, "plan-1", box); !errors.Is(err, ErrUntrusted) {
		t.Errorf("Open in another state = %v, want ErrUntrusted", err)
	}
	// A group the key cannot open is not a reader's error of authentication.
	if _, err := sl.Open(protect.KindPlan, "plan-1", protect.Sealed{Alg: box.Alg, Group: "g9", CT: box.CT}); !errors.Is(err, ErrNotReader) {
		t.Errorf("Open in a group the key has no lock in = %v, want ErrNotReader", err)
	}
	if _, err := openIdentity(t, b, eve).Sealer().Open(protect.KindPlan, "plan-1", box); !errors.Is(err, ErrNotReader) {
		t.Errorf("Open by a stranger = %v, want ErrNotReader", err)
	}
}

func mustLocal2(t *testing.T) Backend {
	t.Helper()
	b, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A long apply that began before a reader was removed seals what it writes after
// in the new group, within the recheck time.
func TestWritesSwitchToTheCurrentGroup(t *testing.T) {
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	long := sealedStore(t, b, ada, fay)
	admin := openIdentity(t, b, ada)
	if _, err := admin.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
		t.Fatal(err)
	}
	was := sealRecheck
	defer func() { sealRecheck = was }()

	// Within the recheck time the long command goes on in the group it opened with.
	sealRecheck = time.Hour
	r := *long.Get("net.vpc")
	r.Attributes = json.RawMessage(`{"id":"vpc-2"}`)
	mustSet(t, long, &r)
	if g := recGroup(t, b, pointerAt(t, b, "net.vpc").Record); g != "g1" {
		t.Errorf("a write inside the recheck time is sealed in %s, want g1", g)
	}
	// After it, it switches.
	sealRecheck = 0
	r2 := *long.Get("net.vpc")
	r2.Attributes = json.RawMessage(`{"id":"vpc-3"}`)
	mustSet(t, long, &r2)
	if g := recGroup(t, b, pointerAt(t, b, "net.vpc").Record); g != "g2" {
		t.Errorf("a write after the recheck time is sealed in %s, want g2", g)
	}
	if got := openIdentity(t, b, ada).Get("net.vpc"); got == nil || !strings.Contains(string(got.Attributes), "vpc-3") {
		t.Errorf("ada reads %+v", got)
	}
	if p := long.Protection(); p.Sealed.Current != "g2" {
		t.Errorf("the long command still thinks the current group is %s", p.Sealed.Current)
	}
}

// A copy does not turn a sealed state into one that is not, or the reverse, or
// one lineage into another.
func TestCopyKeepsASealedStateSealed(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	sealedBackend, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, sealedBackend, ada)

	plainBackend, _ := NewLocalBackend(t.TempDir())
	ps, _ := OpenBackendWith(ctx, plainBackend, Options{NewFormat: Format})
	seedSecrets(t, ps)

	// A sealed state into a state that holds records and is not sealed, and the reverse.
	if _, err := Copy(ctx, sealedBackend, plainBackend); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("sealed over unsealed = %v, want ErrCopyRefused", err)
	}
	if _, err := Copy(ctx, plainBackend, sealedBackend); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("unsealed over sealed = %v, want ErrCopyRefused", err)
	}
	if formatOf(t, plainBackend) != Format {
		t.Error("a refused copy changed the destination")
	}
	// Another sealed state: another lineage.
	other, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, other, ada)
	if _, err := Copy(ctx, sealedBackend, other); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("sealed over another sealed state = %v, want ErrCopyRefused", err)
	}
	// The same state, copied again, and into nothing and into a state that was
	// opened and never written.
	_, remote := newFakeS3(t)
	if _, err := Copy(ctx, sealedBackend, remote); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(ctx, sealedBackend, remote); err != nil {
		t.Errorf("copy of a state over its own copy: %v", err)
	}
	opened, _ := NewLocalBackend(t.TempDir())
	if _, err := OpenBackend(ctx, opened); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(ctx, sealedBackend, opened); err != nil {
		t.Errorf("copy of a sealed state into a state that holds nothing: %v", err)
	}
	for _, b := range []Backend{remote, opened} {
		if got := openIdentity(t, b, ada); got.Locked() || got.Get("db.main") == nil || !strings.Contains(string(got.Get("db.main").Attributes), secretValue) {
			t.Error("the copy of a sealed state does not open")
		}
	}
}

// A seal of more records than it notes its progress after (200) works, and the
// notes, which are written from the goroutines that convert, race with nothing.
func TestUpgradeToSealedNotesItsProgress(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format, Identity: ada})
			if err != nil {
				t.Fatal(err)
			}
			const n = 450
			for i := range n {
				mustSet(t, s, &Resource{Mode: "managed", Type: "t", Name: fmt.Sprintf("r%03d", i), Attributes: json.RawMessage(`{"password":"` + secretValue + `"}`)})
			}
			var (
				calls, noted atomic.Int64
				sawNote      atomic.Bool
			)
			res, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada, Progress: func(done, total int) {
				calls.Add(1)
				if done > 220 && !sawNote.Load() {
					if m, err := readMeta(ctx, b); err == nil && m.Migration != nil && m.Migration.Done >= 200 && m.Migration.Total == n {
						sawNote.Store(true)
					}
				}
				noted.Store(int64(total))
			}})
			if err != nil || res.Records != n || calls.Load() != n || noted.Load() != n {
				t.Fatalf("Upgrade = %+v, %v, %d progress calls", res, err, calls.Load())
			}
			if !sawNote.Load() {
				t.Error("meta.json did not carry a note of the progress after 200 records")
			}
			if v, err := openIdentity(t, b, ada).VerifyAll(ctx); err != nil || len(v.Bad) != 0 || v.Checked != n {
				t.Errorf("VerifyAll = %+v, %v", v, err)
			}
			assertNoSecret(t, b, secretValue)
		})
	}
}

// A transaction that is recorded as running blocks an upgrade while its command
// holds a lock, and does not when the command is gone.
func TestUpgradeAndARunningTransaction(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	b, _ := NewLocalBackend(t.TempDir())
	s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format, Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, s)
	other := openIdentity(t, b, ada)
	txn := other.Begin("apply", "pravo apply")
	if err := other.Lock("db.main", "pravo apply", txn.ID()); err != nil {
		t.Fatal(err)
	}
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	up := UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}
	var locked *ErrLocked
	if _, err := s.Upgrade(ctx, up); !errors.As(err, &locked) || locked.Lock.Txn != txn.ID() {
		t.Fatalf("seal while a transaction runs and holds a lock = %v, want ErrLocked naming it", err)
	}
	// The command is killed: its lock is stale and its transaction still says running.
	if _, err := other.ForceUnlock("db.main"); err != nil {
		t.Fatal(err)
	}
	writeDeadLock(t, b, "net.vpc")
	if txns, _ := s.Transactions(); len(txns) == 0 || txns[0].Status != "running" {
		t.Fatalf("transactions = %+v", txns)
	}
	if _, err := s.Upgrade(ctx, up); err != nil {
		t.Fatalf("seal after the command that ran the transaction was killed: %v", err)
	}
}

// Damage to the sealed part of meta.json, or to the group of a record, is a
// failure of verification for everyone, a reader included. ErrNotReader stays
// for a group that exists and holds no lock for the key.
func TestDamageToTheSealedPartIsNotAMissingKey(t *testing.T) {
	ctx := context.Background()
	ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")

	editMeta := func(t *testing.T, b Backend, f func(sealed map[string]any)) {
		t.Helper()
		data, err := b.Get(ctx, "meta.json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		f(m["sealed"].(map[string]any))
		if err := b.Put(ctx, "meta.json", mustIndent(t, m)); err != nil {
			t.Fatal(err)
		}
	}
	editRecord := func(t *testing.T, b Backend, f func(sealed map[string]any)) {
		t.Helper()
		p := pointerAt(t, b, "db.main")
		data, err := b.Get(ctx, p.Record)
		if err != nil {
			t.Fatal(err)
		}
		var rec map[string]any
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		f(rec["sealed"].(map[string]any))
		out := mustIndent(t, rec)
		p.Record = recKey(out)
		if err := b.Put(ctx, p.Record, out); err != nil {
			t.Fatal(err)
		}
		if err := b.Put(ctx, nodeKey("db.main"), mustIndent(t, p)); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		edit func(t *testing.T, b Backend)
	}{
		{"an empty list of groups", func(t *testing.T, b Backend) {
			editMeta(t, b, func(s map[string]any) { s["groups"] = []any{} })
		}},
		{"a current group that is not in the list", func(t *testing.T, b Backend) {
			editMeta(t, b, func(s map[string]any) { s["current"] = "g9" })
		}},
		{"a record in a group that is not in the list", func(t *testing.T, b Backend) {
			editRecord(t, b, func(s map[string]any) { s["group"] = "g9" })
		}},
		{"a record in no group", func(t *testing.T, b Backend) {
			editRecord(t, b, func(s map[string]any) { s["group"] = "" })
		}},
		{"a sealed value whose ct is not base64", func(t *testing.T, b Backend) {
			editRecord(t, b, func(s map[string]any) { s["ct"] = "!!not base64!!" })
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, _ := NewLocalBackend(t.TempDir())
			sealedStore(t, b, ada)
			c.edit(t, b)
			for _, id := range []*protect.Identity{ada, eve, nil} {
				_, err := OpenBackendWith(ctx, b, Options{Identity: id})
				if !errors.Is(err, ErrUntrusted) || errors.Is(err, ErrNotReader) {
					t.Errorf("Open as %v = %v, want ErrUntrusted and not ErrNotReader", id != nil, err)
				}
			}
		})
	}

	t.Run("a group that exists and holds no lock for the key", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		s := openIdentity(t, b, eve)
		if _, _, err := s.LoadOutputs(); !errors.Is(err, ErrNotReader) || errors.Is(err, ErrUntrusted) {
			t.Errorf("LoadOutputs as a stranger = %v, want ErrNotReader", err)
		}
	})
}

// A sealed state holds no outputs in plain text: a plain outputs.json was written
// by someone who holds no key, and neither the store nor a root that reads the
// outputs of the state takes it. In a state that is not sealed, or that is being
// sealed, plain outputs are what they are.
func TestSealedStateRefusesPlainOutputs(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	b, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, b, ada)
	forged := []byte(`{"url": "https://evil.example"}`)
	if err := b.Put(ctx, "outputs.json", forged); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenOutputs(ctx, b, ada); !errors.Is(err, ErrUntrusted) {
		t.Errorf("OpenOutputs of plain outputs in a sealed state = %v, want ErrUntrusted", err)
	}
	if _, err := OpenOutputs(ctx, b, nil); !errors.Is(err, ErrUntrusted) {
		t.Errorf("OpenOutputs without an identity = %v, want ErrUntrusted", err)
	}
	for _, id := range []*protect.Identity{ada, nil} {
		if _, err := OpenBackendWith(ctx, b, Options{Identity: id}); !errors.Is(err, ErrUntrusted) {
			t.Errorf("Open with plain outputs in a sealed state = %v, want ErrUntrusted", err)
		}
	}

	// Not sealed: as before.
	plain, _ := NewLocalBackend(t.TempDir())
	ps, _ := OpenBackend(ctx, plain)
	_ = ps.SetOutputs(map[string]json.RawMessage{"a": json.RawMessage(`1`)})
	if got, err := OpenOutputs(ctx, plain, nil); err != nil || string(got["a"]) != "1" {
		t.Errorf("OpenOutputs of plain outputs in a state that is not sealed = %v, %v", got, err)
	}
	// A state of format 1 has no meta.json at all.
	bare, _ := NewLocalBackend(t.TempDir())
	_ = bare.Put(ctx, "outputs.json", forged)
	if got, err := OpenOutputs(ctx, bare, nil); err != nil || string(got["url"]) != `"https://evil.example"` {
		t.Errorf("OpenOutputs of outputs with no meta.json = %v, %v", got, err)
	}
}

// The sweep of rewrap --all leaves a young record that nothing names alone: it may
// be the record of a writer that opened the store before the rewrap, has put it and
// has not yet pointed at it. An old one goes.
func TestSweepLeavesAYoungUnnamedRecordAlone(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)

	// A record in g1 that nothing names, as an old and as a young one.
	p := pointerAt(t, b, "net.vpc")
	data, err := b.Get(ctx, p.Record)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	put := func(updated time.Time, revision int) string {
		rec["updated_at"], rec["revision"] = updated, revision
		out := mustIndent(t, rec)
		if err := b.Put(ctx, recKey(out), out); err != nil {
			t.Fatal(err)
		}
		return recKey(out)
	}
	young := put(time.Now().UTC(), 90)
	old := put(time.Now().UTC().Add(-time.Hour), 91)

	keepNothing := func(string) bool { return false }
	if err := s.sweepRecords(keepNothing); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, young); err != nil {
		t.Errorf("the young record was deleted: %v", err)
	}
	if _, err := b.Get(ctx, old); !errors.Is(err, ErrNotFound) {
		t.Errorf("the old record is still there: %v", err)
	}
	if _, err := b.Get(ctx, p.Record); err != nil {
		t.Errorf("the record that a pointer names was deleted: %v", err)
	}
}

// The text of an error that a transaction keeps sealed is sealed again by rewrap
// --all, so that a reader who was removed does not read it in the bucket.
func TestRewrapAllSealsTheTextOfAnErrorAgain(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
			s := sealedStore(t, b, ada, fay)
			const text = "the provider said planted-diagnostic-0042"
			txn := s.Begin("apply", "pravo apply")
			txn.Record("db.main", "update", 1, 2)
			if err := txn.FinishError("failed", "the text of the error is sealed", text, true); err != nil {
				t.Fatal(err)
			}
			summary := func(st *Store) string {
				t.Helper()
				txns, err := st.Transactions()
				if err != nil || len(txns) != 1 {
					t.Fatalf("%v %v", txns, err)
				}
				return st.SummaryText(txns[0])
			}
			if got := summary(openIdentity(t, b, fay)); got != text {
				t.Fatalf("fay reads %q before she leaves", got)
			}
			assertNoSecret(t, b, "planted-diagnostic-0042")

			if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
				t.Fatal(err)
			}
			txns, _ := s.Transactions()
			if txns[0].SummarySealed.Group != "g1" {
				t.Fatalf("a rewrap that removes a reader seals the text again: %s", txns[0].SummarySealed.Group)
			}
			if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
				t.Fatal(err)
			}
			txns, _ = s.Transactions()
			if g := txns[0].SummarySealed.Group; g != "g2" {
				t.Errorf("rewrap --all leaves the text in %s", g)
			}
			if got := summary(openIdentity(t, b, ada)); got != text {
				t.Errorf("ada reads %q after rewrap --all", got)
			}
			assertNoSecret(t, b, "planted-diagnostic-0042")
			// The key of g1 that fay holds opens nothing of what is in g2.
			if got := summary(openIdentity(t, b, fay)); got == text {
				t.Error("fay reads the text after rewrap --all")
			}
		})
	}
}

// The text of the error of a transaction that failed before the seal moves into a
// sealed box when the state is sealed, and the readers keep it.
func TestUpgradeToSealedSealsTheTextOfEarlierErrors(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			ada := testIdentity(t, "ada")
			s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format, Identity: ada})
			if err != nil {
				t.Fatal(err)
			}
			seedSecrets(t, s)
			const planted = "planted-diagnostic-0042"
			finish := func(status, summary string) string {
				t.Helper()
				txn := s.Begin("apply", "pravo apply")
				txn.Record("db.main", "update", 1, 2)
				if err := txn.Finish(status, summary, true); err != nil {
					t.Fatal(err)
				}
				return txn.ID()
			}
			failed := finish("failed", "the provider said "+planted)
			rejected := finish("rejected", "policy: "+planted)
			interrupted := finish("failed", "interrupted; update of db.main was in flight; check the provider")
			applied := finish("applied", "3 added, 0 changed, 0 destroyed")

			if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
				t.Fatal(err)
			}
			assertNoSecret(t, b, planted)
			r := openIdentity(t, b, ada)
			txns, err := r.Transactions()
			if err != nil || len(txns) != 4 {
				t.Fatalf("%v %v", txns, err)
			}
			byID := map[string]Transaction{}
			for _, tx := range txns {
				byID[tx.ID] = tx
			}
			for id, want := range map[string]string{failed: "the provider said " + planted, rejected: "policy: " + planted} {
				tx := byID[id]
				if tx.Summary != SealedErrorText || tx.SummarySealed == nil || r.SummaryText(tx) != want {
					t.Errorf("%s: summary %q, text %q", id, tx.Summary, r.SummaryText(tx))
				}
			}
			for id, want := range map[string]string{interrupted: "interrupted; update of db.main was in flight; check the provider", applied: "3 added, 0 changed, 0 destroyed"} {
				if tx := byID[id]; tx.Summary != want || tx.SummarySealed != nil {
					t.Errorf("%s: summary %q is changed", id, tx.Summary)
				}
			}
		})
	}
}
