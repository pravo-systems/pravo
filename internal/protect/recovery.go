package protect

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pravo-systems/pravo/internal/protect/shamir"
)

// A recovery recipient is one more reader of a sealed state, whose private key
// nobody holds whole. The key is made when the recovery is set, split into N shares
// (Shamir secret sharing: any K of them rebuild it, fewer give nothing) and
// forgotten. Every group of the state carries a lock for its public key, so that K
// holders of shares can open the state when every reader has lost their key.
//
// Nothing here is a signature or a delay: whoever holds K shares and a copy of the
// bucket can read it at once. K is a decision about whom to trust together.

// RecoveryName is the name of the lock of the recovery recipient. It is reserved:
// the readers file may not use it.
const RecoveryName = "recovery"

// Limits of a recovery: at least two shares are needed to rebuild the key (a
// single holder is a reader), and a state does not need more than a few dozen.
const (
	MinRecoveryK = 2
	MaxRecoveryN = 20
)

// Recovery is what meta.json holds of a recovery recipient. It holds no secret.
type Recovery struct {
	Key ReaderPublic `json:"key"`
	K   int          `json:"k"`
	N   int          `json:"n"`
}

// ErrRecovery is returned for shares that do not rebuild the recovery key.
var ErrRecovery = errors.New("the shares do not rebuild the recovery key of this state")

// ParseThreshold parses "3/5" into K and N.
func ParseThreshold(s string) (k, n int, err error) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "/")
	if ok {
		k, err = strconv.Atoi(a)
		if err == nil {
			n, err = strconv.Atoi(b)
		}
	}
	if !ok || err != nil {
		return 0, 0, fmt.Errorf("recovery %q: write it as K/N, for example 3/5", s)
	}
	if k < MinRecoveryK || n < k || n > MaxRecoveryN {
		return 0, 0, fmt.Errorf("recovery %q: K must be at least %d, N at least K and at most %d", s, MinRecoveryK, MaxRecoveryN)
	}
	return k, n, nil
}

// Share is one share of a recovery key.
type Share struct {
	KeyID string // ReaderPublic.ID of the recovery key it belongs to
	Index int    // 1 to N
	K, N  int
	Data  []byte
}

const shareTag = "pravo-share-1"

func (s Share) check() string {
	h := sha256.New()
	h.Write([]byte("pravo/share/v1\x00"))
	fmt.Fprintf(h, "%s\x00%d\x00%d/%d\x00", s.KeyID, s.Index, s.K, s.N)
	h.Write(s.Data)
	return hex.EncodeToString(h.Sum(nil)[:4])
}

// String is the line of the share: the tag, the id of the key, the index, K/N, the
// share in base64url and a short check that catches a typed or pasted mistake.
func (s Share) String() string {
	return fmt.Sprintf("%s %s %d %d/%d %s %s", shareTag, s.KeyID, s.Index, s.K, s.N,
		base64.RawURLEncoding.EncodeToString(s.Data), s.check())
}

// File is what is written to a share file: the line, with a comment that says what
// it is and how to keep it.
func (s Share) File() []byte {
	return fmt.Appendf(nil, "# pravo recovery share %d of %d. Any %d of the %d shares rebuild the recovery key of one state.\n"+
		"# Keep it offline, with one person. Do not commit it. Anyone who holds %d shares can read the state.\n%s\n",
		s.Index, s.N, s.K, s.N, s.K, s.String())
}

// ParseShare parses a share file: one share line, with comments and blank lines.
func ParseShare(data []byte) (Share, error) {
	var line string
	for _, l := range strings.Split(string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l[0] == '#' {
			continue
		}
		if line != "" {
			return Share{}, errors.New("the file holds more than one share")
		}
		line = l
	}
	f := strings.Fields(line)
	if len(f) != 6 || f[0] != shareTag {
		return Share{}, errors.New("not a pravo recovery share")
	}
	var s Share
	s.KeyID = f[1]
	var err error
	if s.Index, err = strconv.Atoi(f[2]); err != nil || s.Index < 1 || s.Index > MaxRecoveryN {
		return Share{}, errors.New("the index of the share is not valid")
	}
	if s.K, s.N, err = ParseThreshold(f[3]); err != nil {
		return Share{}, errors.New("the threshold of the share is not valid")
	}
	if s.Index > s.N {
		return Share{}, errors.New("the index of the share is above N")
	}
	if s.Data, err = base64.RawURLEncoding.DecodeString(f[4]); err != nil || len(s.Data) != 32 {
		return Share{}, errors.New("the share is damaged")
	}
	if f[5] != s.check() {
		return Share{}, errors.New("the check of the share does not match: it was changed or mistyped")
	}
	return s, nil
}

// NewRecovery makes a recovery key, splits it into n shares of which k rebuild it,
// and returns what meta.json holds with the shares. The private key is wiped.
func NewRecovery(k, n int) (*Recovery, []Share, error) {
	if k < MinRecoveryK || n < k || n > MaxRecoveryN {
		return nil, nil, fmt.Errorf("recovery %d/%d: K must be at least %d, N at least K and at most %d", k, n, MinRecoveryK, MaxRecoveryN)
	}
	rk, err := GenerateReaderKey()
	if err != nil {
		return nil, nil, err
	}
	secret := rk.Bytes()
	defer clear(secret)
	raw, err := shamir.Split(secret, k, n)
	if err != nil {
		return nil, nil, err
	}
	rec := &Recovery{Key: rk.Public(), K: k, N: n}
	shares := make([]Share, n)
	for i, r := range raw {
		shares[i] = Share{KeyID: rec.Key.ID(), Index: int(r[0]), K: k, N: n, Data: slices.Clone(r[1:])}
		clear(r)
	}
	return rec, shares, nil
}

// Open rebuilds the recovery key from shares, and checks it against the public
// key of the recovery. Fewer than K shares, shares of another key, and a wrong
// share all give ErrRecovery (a wrong share cannot be told from a wrong set: only
// the public key says whether the key came out right). The caller closes the key.
func (r *Recovery) Open(shares []Share) (*ReaderKey, error) {
	seen := map[int]bool{}
	pick := make([][]byte, 0, r.K)
	for i, s := range shares {
		switch {
		case s.KeyID != r.Key.ID():
			return nil, fmt.Errorf("%w: share %d is of another recovery key (%s, this state has %s)", ErrRecovery, i+1, s.KeyID, r.Key.ID())
		case seen[s.Index]:
			return nil, fmt.Errorf("%w: share %d of %d is given twice", ErrRecovery, s.Index, s.N)
		}
		seen[s.Index] = true
		if len(pick) < r.K {
			pick = append(pick, append([]byte{byte(s.Index)}, s.Data...))
		}
	}
	if len(shares) < r.K {
		return nil, fmt.Errorf("%w: it takes %d shares and %d were given", ErrRecovery, r.K, len(shares))
	}
	secret, err := shamir.Combine(pick)
	for _, p := range pick {
		clear(p)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRecovery, err)
	}
	defer clear(secret)
	rk, err := ReaderKeyFromBytes(secret)
	if err != nil || !rk.Public().Equal(r.Key) {
		return nil, fmt.Errorf("%w: a share is wrong, or the shares are of another state", ErrRecovery)
	}
	return rk, nil
}

// RecoveryIdentity is the identity under which a rebuilt recovery key opens a
// state: it can read and rewrap, and it has no signing key.
func RecoveryIdentity(rk *ReaderKey) *Identity {
	return &Identity{Name: RecoveryName, Reader: rk}
}

// ParseRecoveryLine parses the line of .pravo/access/recovery.txt:
//
//	x25519:BASE64URL K/N
//
// Comments (#), blank lines, CRLF and a byte order mark are accepted.
func ParseRecoveryLine(data []byte) (*Recovery, error) {
	var line string
	for _, l := range strings.Split(string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), "\n") {
		if i := strings.Index(l, "#"); i >= 0 {
			l = l[:i]
		}
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if line != "" {
			return nil, errors.New("recovery file: more than one line")
		}
		line = l
	}
	f := strings.Fields(line)
	if len(f) != 2 {
		return nil, fmt.Errorf("recovery file: want 2 fields: x25519:BASE64URL K/N (got %d)", len(f))
	}
	key, err := ParsePublic(f[0])
	if err != nil {
		return nil, fmt.Errorf("recovery file: %w", err)
	}
	k, n, err := ParseThreshold(f[1])
	if err != nil {
		return nil, fmt.Errorf("recovery file: %w", err)
	}
	return &Recovery{Key: key, K: k, N: n}, nil
}

// Line is the content of .pravo/access/recovery.txt.
func (r *Recovery) Line() string {
	return fmt.Sprintf("# The recovery recipient of the state: any %d of %d shares rebuild its key.\n%s %d/%d\n", r.K, r.N, r.Key.String(), r.K, r.N)
}

// Equal says whether two recoveries are the same key with the same threshold.
func (r *Recovery) Equal(o *Recovery) bool {
	if r == nil || o == nil {
		return r == o
	}
	return r.Key.Equal(o.Key) && r.K == o.K && r.N == o.N
}

// recipients is the readers with the recovery recipient added, if the state has one.
func (m *Meta) recipients(readers []Reader) []Reader {
	if m.Recovery == nil {
		return readers
	}
	out := slices.Clone(readers)
	return append(out, Reader{Name: RecoveryName, Key: m.Recovery.Key})
}

// IsRecoveryLock says whether a lock is the one of the recovery recipient.
func (m *Meta) IsRecoveryLock(l Lock) bool {
	return m.Recovery != nil && l.Key.Equal(m.Recovery.Key)
}

// SetRecovery sets the recovery recipient of the state, or replaces it, or removes
// it when rec is nil: every group gets a lock for the new key, and the lock of the
// old key goes. The keys must open every group.
func (m *Meta) SetRecovery(k *Keys, rec *Recovery) error {
	for _, g := range m.Groups {
		if _, ok := k.keys[g.ID]; !ok {
			return fmt.Errorf("protect: you cannot open group %s, so you cannot change its recovery", g.ID)
		}
	}
	old := m.Recovery
	for i := range m.Groups {
		g := &m.Groups[i]
		var rs []Reader
		for _, l := range g.Locks {
			if old != nil && l.Key.Equal(old.Key) {
				continue
			}
			rs = append(rs, Reader{Name: l.Name, Key: l.Key})
		}
		if rec != nil {
			rs = append(rs, Reader{Name: RecoveryName, Key: rec.Key})
		}
		if err := g.Rewrap(k.lineage, k.keys[g.ID], rs); err != nil {
			return err
		}
	}
	m.Recovery = rec
	return nil
}

// RecoveryStatus says how a state's recovery stands. It needs no key.
type RecoveryStatus struct {
	K, N   int
	Key    ReaderPublic
	Locked bool     // every group has a lock for the recovery key
	Groups []string // the groups that have none
}

// recoveryStatus is the status of the recovery of m, nil when it has none.
func (m *Meta) recoveryStatus() *RecoveryStatus {
	if m.Recovery == nil {
		return nil
	}
	st := &RecoveryStatus{K: m.Recovery.K, N: m.Recovery.N, Key: m.Recovery.Key, Locked: true}
	for _, g := range m.Groups {
		if !slices.ContainsFunc(g.Locks, m.IsRecoveryLock) {
			st.Locked = false
			st.Groups = append(st.Groups, g.ID)
		}
	}
	return st
}
