package shamir

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"testing"
)

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

func TestSplitCombine(t *testing.T) {
	secret := make([]byte, 32) // an Ed25519 seed / X25519 key
	rand.Read(secret)
	total, wrongBelowK := 0, 0
	for _, kn := range [][2]int{{2, 2}, {2, 3}, {3, 5}, {4, 7}, {5, 9}, {7, 12}, {3, 255}} {
		k, n := kn[0], kn[1]
		sh, err := Split(secret, k, n)
		if err != nil {
			t.Fatal(err)
		}
		cnt := 0
		lim := n
		if n > 20 {
			lim = 20 // do not enumerate C(255,3) fully
		}
		subsets(lim, k, func(ix []int) {
			var pick [][]byte
			for _, i := range ix {
				pick = append(pick, sh[i])
			}
			got, err := Combine(pick)
			if err != nil || !bytes.Equal(got, secret) {
				t.Fatalf("k=%d n=%d subset %v failed", k, n, ix)
			}
			cnt++
		})
		total += cnt
		// k-1 shares must not give the secret
		got, _ := Combine(sh[:k-1+boolInt(k == 2)])
		if k > 2 && bytes.Equal(got, secret) {
			t.Fatalf("k-1 shares recovered the secret k=%d", k)
		}
		if k > 2 {
			wrongBelowK++
		}
		t.Logf("k=%d n=%d: %d subsets of size k recover the secret", k, n, cnt)
	}
	t.Logf("total subsets verified: %d", total)
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The limits: one corrupted share silently gives a wrong secret, with no error.
func TestNoVerifiability(t *testing.T) {
	secret := make([]byte, 32)
	rand.Read(secret)
	sh, _ := Split(secret, 3, 5)
	sh[1][5] ^= 0x01 // a single flipped bit in one share
	got, err := Combine([][]byte{sh[0], sh[1], sh[2]})
	t.Logf("corrupted share: err=%v, recovered==secret: %v (no error is raised; only a separate hash of the secret can tell)", err, bytes.Equal(got, secret))
	if bytes.Equal(got, secret) {
		t.Fatal("expected a wrong secret")
	}
	h := sha256.Sum256(secret)
	if sha256.Sum256(got) == h {
		t.Fatal("hash check should catch it")
	}
	// a dealer sees the secret and all shares: Split ran on one machine with the whole secret.
}
