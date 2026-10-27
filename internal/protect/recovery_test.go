package protect

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseThreshold(t *testing.T) {
	for _, ok := range []string{"2/2", "3/5", " 2/20 ", "20/20"} {
		if _, _, err := ParseThreshold(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "3", "1/5", "5/3", "3/21", "a/b", "3/", "/5", "-2/5", "3/5/7", "0/0"} {
		if _, _, err := ParseThreshold(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// subsets calls f with every choice of k of the n indexes.
func subsets(n, k int, f func([]int)) {
	idx := make([]int, k)
	var rec func(start, d int)
	rec = func(start, d int) {
		if d == k {
			f(idx)
			return
		}
		for i := start; i < n; i++ {
			idx[d] = i
			rec(i+1, d+1)
		}
	}
	rec(0, 0)
}

func TestRecoveryAnyKSharesRebuildTheKey(t *testing.T) {
	for _, kn := range [][2]int{{2, 2}, {2, 3}, {3, 5}, {4, 7}} {
		k, n := kn[0], kn[1]
		rec, shares, err := NewRecovery(k, n)
		if err != nil {
			t.Fatal(err)
		}
		if len(shares) != n || rec.K != k || rec.N != n {
			t.Fatalf("%d/%d: %d shares, %+v", k, n, len(shares), rec)
		}
		subsets(n, k, func(ix []int) {
			var pick []Share
			for _, i := range ix {
				pick = append(pick, shares[i])
			}
			rk, err := rec.Open(pick)
			if err != nil {
				t.Fatalf("%d/%d subset %v: %v", k, n, ix, err)
			}
			if !rk.Public().Equal(rec.Key) {
				t.Fatalf("%d/%d subset %v: the key is not the recovery key", k, n, ix)
			}
		})
		// More than K shares work as well.
		if k < n {
			if _, err := rec.Open(shares); err != nil {
				t.Errorf("%d/%d: all the shares: %v", k, n, err)
			}
		}
		// Fewer than K do not.
		if k > 2 {
			if _, err := rec.Open(shares[:k-1]); !errors.Is(err, ErrRecovery) {
				t.Errorf("%d/%d: %d shares: %v", k, n, k-1, err)
			}
		}
	}
}

func TestRecoveryRefusesTheWrongShares(t *testing.T) {
	rec, shares, err := NewRecovery(3, 5)
	if err != nil {
		t.Fatal(err)
	}
	other, otherShares, err := NewRecovery(3, 5)
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	t.Run("shares of another recovery", func(t *testing.T) {
		_, err := rec.Open([]Share{shares[0], shares[1], otherShares[2]})
		if !errors.Is(err, ErrRecovery) || !strings.Contains(err.Error(), "another recovery key") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a share given twice", func(t *testing.T) {
		if _, err := rec.Open([]Share{shares[0], shares[0], shares[1]}); !errors.Is(err, ErrRecovery) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a share with a wrong byte that keeps its check", func(t *testing.T) {
		bad := shares[2]
		bad.Data = append([]byte(nil), bad.Data...)
		// Not byte 0 or 31: X25519 clears some of their bits, so a change there gives
		// the same key, which is harmless.
		bad.Data[10] ^= 1
		// ParseShare would refuse the line; Open gets the share as a value and must not
		// hand out a key that is not the recovery key.
		if _, err := rec.Open([]Share{shares[0], shares[1], bad}); !errors.Is(err, ErrRecovery) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("too few", func(t *testing.T) {
		if _, err := rec.Open(shares[:2]); !errors.Is(err, ErrRecovery) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestShareFileRoundTrip(t *testing.T) {
	rec, shares, err := NewRecovery(3, 5)
	if err != nil {
		t.Fatal(err)
	}
	var back []Share
	for _, s := range shares {
		got, err := ParseShare(s.File())
		if err != nil {
			t.Fatal(err)
		}
		if got.KeyID != s.KeyID || got.Index != s.Index || got.K != 3 || got.N != 5 || !bytes.Equal(got.Data, s.Data) {
			t.Fatalf("share %d did not round trip: %+v", s.Index, got)
		}
		back = append(back, got)
	}
	if _, err := rec.Open(back[1:4]); err != nil {
		t.Fatal(err)
	}
	// A mistake in the line is caught by the check, whichever field it is in.
	line := shares[0].String()
	f := strings.Fields(line)
	for i := 1; i < len(f); i++ {
		g := append([]string(nil), f...)
		switch i {
		case 1:
			first := "0"
			if g[i][:1] == "0" {
				first = "1"
			}
			g[i] = first + g[i][1:]
		case 2:
			g[i] = "2"
		case 3:
			g[i] = "2/5"
		case 4:
			// The first character: the last of a base64 text without padding has bits that
			// the decoder ignores, so a change there can leave the bytes as they are.
			c := "A"
			if g[i][0] == 'A' {
				c = "B"
			}
			g[i] = c + g[i][1:]
		case 5:
			g[i] = "00000000"
		}
		if _, err := ParseShare([]byte(strings.Join(g, " "))); err == nil {
			t.Errorf("a change in field %d was accepted: %q", i, strings.Join(g, " "))
		}
	}
	for _, bad := range []string{"", "# only a comment\n", "pravo-share-1 a b c", line + "\n" + line} {
		if _, err := ParseShare([]byte(bad)); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	// A secret never appears in a share file in the clear: the file is the share and a comment.
	if strings.Contains(string(shares[0].File()), "BEGIN") {
		t.Error("a share file looks like a key")
	}
}

func TestRecoveryLine(t *testing.T) {
	rec, _, err := NewRecovery(3, 5)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseRecoveryLine([]byte(rec.Line()))
	if err != nil || !got.Equal(rec) {
		t.Fatalf("%v, %+v", err, got)
	}
	for _, bad := range []string{"", "x25519:AAAA 3/5", rec.Key.String(), rec.Key.String() + " 3", rec.Key.String() + " 3/5\n" + rec.Key.String() + " 3/5", rec.Key.String() + " 1/5"} {
		if _, err := ParseRecoveryLine([]byte(bad)); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if rec.Equal(nil) || (*Recovery)(nil).Equal(rec) || !(*Recovery)(nil).Equal(nil) {
		t.Error("Equal with nil")
	}
}

func TestTheNameRecoveryIsReserved(t *testing.T) {
	r, _ := mustReader(t, "recovery")
	line := r.String()
	if _, err := ParseReaderLine(line); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("err = %v", err)
	}
	r2, _ := mustReader(t, "Recovery")
	if _, err := ParseReaderLine(r2.String()); err == nil {
		t.Error("a different case was accepted")
	}
}

func TestMetaKeepsTheRecoveryThroughRewraps(t *testing.T) {
	ada, adaK := mustReader(t, "ada")
	dan, danK := mustReader(t, "dan")
	eve, _ := mustReader(t, "eve")
	m, keys, err := NewMeta("L1", []Reader{ada, dan})
	if err != nil {
		t.Fatal(err)
	}
	rec, shares, err := NewRecovery(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetRecovery(keys, rec, false); err != nil {
		t.Fatal(err)
	}
	st := m.Status([]Reader{ada, dan})
	if !st.Matches || len(st.Readers) != 2 || st.Recovery == nil || !st.Recovery.Locked || st.Recovery.K != 2 || st.Recovery.N != 3 {
		t.Fatalf("status %+v / %+v", st, st.Recovery)
	}
	c := Ctx{Lineage: "L1", Kind: KindRecord, Name: "net.vpc", Generation: "tx1", Revision: 1}
	sealed, err := keys.SealJSON(c, map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}

	// A new reader and a removed one: the recovery lock is kept in every group.
	ch, err := m.Rewrap(keys, []Reader{ada, eve})
	if err != nil {
		t.Fatal(err)
	}
	if ch.NewGroup != "g2" || len(ch.Removed) != 1 || ch.Removed[0] != "dan" || len(ch.Added) != 1 || ch.Added[0] != "eve" {
		t.Fatalf("change %+v", ch)
	}
	for _, g := range m.Groups {
		n := 0
		for _, l := range g.Locks {
			if m.IsRecoveryLock(l) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("group %s has %d recovery locks", g.ID, n)
		}
	}
	if st := m.Status([]Reader{ada, eve}); !st.Matches || st.Recovery == nil || !st.Recovery.Locked {
		t.Fatalf("status after the rewrap %+v", st)
	}

	// Dan lost access; ada's key and the rebuilt recovery key both open everything.
	if _, err := m.Unlock("L1", danK); !errors.Is(err, ErrNotReader) {
		t.Errorf("dan opens the state: %v", err)
	}
	rk, err := m.Recovery.Open(shares[:2])
	if err != nil {
		t.Fatal(err)
	}
	for who, key := range map[string]*ReaderKey{"ada": adaK, "recovery": rk} {
		k, err := m.Unlock("L1", key)
		if err != nil {
			t.Fatalf("%s: %v", who, err)
		}
		var got map[string]string
		if err := k.OpenJSON(c, sealed, &got); err != nil || got["k"] != "v" {
			t.Errorf("%s: %v %v", who, got, err)
		}
	}
}

func TestSetRecoveryReplacesAndRemoves(t *testing.T) {
	ada, _ := mustReader(t, "ada")
	m, keys, err := NewMeta("L1", []Reader{ada})
	if err != nil {
		t.Fatal(err)
	}
	first, firstShares, _ := NewRecovery(2, 2)
	if err := m.SetRecovery(keys, first, false); err != nil {
		t.Fatal(err)
	}
	second, secondShares, _ := NewRecovery(3, 4)
	if err := m.SetRecovery(keys, second, false); err != nil {
		t.Fatal(err)
	}
	if st := m.Status([]Reader{ada}); !st.Matches || len(st.Readers) != 1 || st.Recovery.K != 3 {
		t.Fatalf("status %+v", st)
	}
	for _, g := range m.Groups {
		if len(g.Locks) != 2 {
			t.Fatalf("group %s has %d locks, want the reader and the new recovery", g.ID, len(g.Locks))
		}
	}
	// The key of the replaced recovery opens nothing any more; the new one does.
	oldKey, err := first.Open(firstShares)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Unlock("L1", oldKey); !errors.Is(err, ErrNotReader) {
		t.Errorf("the replaced recovery key opens the state: %v", err)
	}
	newKey, err := second.Open(secondShares[1:4])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Unlock("L1", newKey); err != nil {
		t.Errorf("the new recovery key: %v", err)
	}
	if err := m.SetRecovery(keys, nil, false); err != nil {
		t.Fatal(err)
	}
	if m.Recovery != nil || len(m.Groups[0].Locks) != 1 {
		t.Fatalf("recovery %+v, %d locks", m.Recovery, len(m.Groups[0].Locks))
	}
	if st := m.Status([]Reader{ada}); !st.Matches || st.Recovery != nil {
		t.Fatalf("status %+v", st)
	}
}

func TestRecoveryLockMissingIsReported(t *testing.T) {
	ada, _ := mustReader(t, "ada")
	m, keys, _ := NewMeta("L1", []Reader{ada})
	rec, _, _ := NewRecovery(2, 3)
	if err := m.SetRecovery(keys, rec, false); err != nil {
		t.Fatal(err)
	}
	// Someone with write access to the bucket takes the recovery lock out.
	g := &m.Groups[0]
	g.Locks = g.Locks[:1]
	st := m.Status([]Reader{ada})
	if st.Recovery == nil || st.Recovery.Locked || len(st.Recovery.Groups) != 1 || st.Recovery.Groups[0] != "g1" {
		t.Fatalf("recovery status %+v", st.Recovery)
	}
	if st.Matches || len(st.Missing) != 1 || st.Missing[0] != RecoveryName {
		t.Errorf("a missing recovery lock is a difference: %+v", st)
	}
	// A rewrap puts it back.
	if _, err := m.Rewrap(keys, []Reader{ada}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status([]Reader{ada}); !st.Matches || !st.Recovery.Locked {
		t.Errorf("after the rewrap %+v", st)
	}
}

func TestRecoveryIdentityOpensAndNeverSigns(t *testing.T) {
	ada, _ := mustReader(t, "ada")
	m, keys, _ := NewMeta("L1", []Reader{ada})
	rec, shares, _ := NewRecovery(2, 3)
	if err := m.SetRecovery(keys, rec, false); err != nil {
		t.Fatal(err)
	}
	rk, err := rec.Open(shares[1:3])
	if err != nil {
		t.Fatal(err)
	}
	id := RecoveryIdentity(rk)
	if id.Name != RecoveryName || id.Reader == nil {
		t.Fatalf("%+v", id)
	}
	if _, err := m.Unlock("L1", id.Reader); err != nil {
		t.Fatal(err)
	}
	if _, err := id.Sign([]byte("x")); err == nil {
		t.Error("the recovery identity signed")
	}
	id.Close()
}

// Holders of the old shares can keep a copy of meta.json from before the change. The data key
// that new records are sealed under must be one that copy cannot open.
func TestReplacingOrRemovingTheRecoveryRotatesTheDataKey(t *testing.T) {
	for _, remove := range []bool{false, true} {
		ada, _ := mustReader(t, "ada")
		m, keys, err := NewMeta("L1", []Reader{ada})
		if err != nil {
			t.Fatal(err)
		}
		first, firstShares, _ := NewRecovery(2, 3)
		if err := m.SetRecovery(keys, first, false); err != nil {
			t.Fatal(err)
		}
		stale := cloneMeta(t, m)
		var next *Recovery
		if !remove {
			if next, _, err = NewRecovery(2, 3); err != nil {
				t.Fatal(err)
			}
		}
		before := m.Current
		if err := m.SetRecovery(keys, next, false); err != nil {
			t.Fatal(err)
		}
		if m.Current == before || len(m.Groups) != 2 {
			t.Fatalf("remove=%v: current %s of %d groups, want a new group", remove, m.Current, len(m.Groups))
		}
		old, err := first.Open(firstShares[:2])
		if err != nil {
			t.Fatal(err)
		}
		oldKeys, err := stale.Unlock("L1", old)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := oldKeys.keys[m.Current]; ok {
			t.Errorf("remove=%v: the old recovery opens the current group", remove)
		}
		// A change that already rotated the data key does not rotate it again, and a change of
		// the threshold alone keeps the key.
		if !remove {
			groups := len(m.Groups)
			if err := m.SetRecovery(keys, next, false); err != nil || len(m.Groups) != groups {
				t.Errorf("same recovery: %v, %d groups, want %d", err, len(m.Groups), groups)
			}
		}
	}
}

func cloneMeta(t *testing.T, m *Meta) *Meta {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var c Meta
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func TestAReaderMayNotHoldTheRecoveryKey(t *testing.T) {
	ada, _ := mustReader(t, "ada")
	m, keys, _ := NewMeta("L1", []Reader{ada})
	rec, _, _ := NewRecovery(2, 3)
	if err := m.SetRecovery(keys, rec, false); err != nil {
		t.Fatal(err)
	}
	bob := Reader{Name: "bob", Key: rec.Key}
	if _, err := m.Rewrap(keys, []Reader{ada, bob}); err == nil {
		t.Fatal("a reader with the recovery key was accepted")
	}
}

func TestRecoveryInMetaMustHaveAValidThreshold(t *testing.T) {
	ada, _ := mustReader(t, "ada")
	m, keys, _ := NewMeta("L1", []Reader{ada})
	rec, _, _ := NewRecovery(2, 3)
	if err := m.SetRecovery(keys, rec, false); err != nil {
		t.Fatal(err)
	}
	for _, kn := range [][2]int{{-1, 3}, {0, 0}, {1, 3}, {4, 3}, {2, 21}, {1 << 60, 1 << 60}} {
		bad := *m.Recovery
		bad.K, bad.N = kn[0], kn[1]
		c := *m
		c.Recovery = &bad
		if err := c.Check(); !errors.Is(err, ErrDamaged) {
			t.Errorf("%v: %v", kn, err)
		}
	}
	if err := m.Check(); err != nil {
		t.Error(err)
	}
}

func TestOpenRefusesIndexesOutsideTheRecovery(t *testing.T) {
	rec, shares, _ := NewRecovery(2, 3)
	for _, idx := range []int{0, 4, 257, -255} {
		bad := shares[0]
		bad.Index = idx
		if _, err := rec.Open([]Share{bad, shares[1]}); !errors.Is(err, ErrRecovery) {
			t.Errorf("index %d: %v", idx, err)
		}
	}
}

func TestTheRecoveryLockCarriesACheckThatNeedsTheDataKey(t *testing.T) {
	ada, _ := mustReader(t, "ada")
	m, keys, _ := NewMeta("L1", []Reader{ada})
	rec, _, _ := NewRecovery(2, 3)
	if err := m.SetRecovery(keys, rec, false); err != nil {
		t.Fatal(err)
	}
	if bad := keys.VerifyRecovery(m); len(bad) != 0 {
		t.Fatalf("%v", bad)
	}
	g := &m.Groups[0]
	idx := -1
	for i, l := range g.Locks {
		if m.IsRecoveryLock(l) {
			idx = i
		}
	}
	good := append([]byte(nil), g.Locks[idx].Box...)
	for name, tamper := range map[string]func(){
		"box":     func() { g.Locks[idx].Box[0] ^= 1 },
		"check":   func() { g.Locks[idx].Check[0] ^= 1 },
		"dropped": func() { g.Locks[idx].Check = nil },
	} {
		save := append([]byte(nil), g.Locks[idx].Check...)
		tamper()
		if bad := keys.VerifyRecovery(m); len(bad) != 1 {
			t.Errorf("%s: not seen", name)
		}
		g.Locks[idx].Box, g.Locks[idx].Check = append([]byte(nil), good...), save
	}
	// A box and a check that someone made without the data key do not pass.
	other := NewDataKey()
	box, _ := Wrap(other, rec.Key, WrapCtx{"L1", g.ID})
	forged := Lock{Name: RecoveryName, Key: rec.Key, Box: box}
	forged.Check = forged.check(other, "L1", g.ID)
	g.Locks[idx] = forged
	if bad := keys.VerifyRecovery(m); len(bad) != 1 {
		t.Error("a check made with another data key passes")
	}
	// A rewrap makes the lock again.
	if _, err := m.Rewrap(keys, []Reader{ada}); err != nil {
		t.Fatal(err)
	}
	if bad := keys.VerifyRecovery(m); len(bad) != 0 {
		t.Errorf("not mended: %v", bad)
	}
}
