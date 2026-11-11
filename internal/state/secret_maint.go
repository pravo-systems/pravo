package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// This file is what the rest of the sealed state does for secrets: the rewrap seals their
// values again, the collection of reader groups counts them, an unseal refuses while the
// state holds one, a copy carries them, and the vouching of an admin covers their signers.

// secretObjects lists the keys under secret/ that are versions, in order.
func (s *Store) secretObjects(ctx context.Context) ([]string, error) {
	keys, err := s.b.List(ctx, secretDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range keys {
		if secretKeyRe.MatchString(k) {
			out = append(out, k)
		}
	}
	return out, nil
}

// eachSecret reads every version under secret/ and calls fn with it. A version that is not
// valid is skipped, as the sealed text of a damaged transaction is: the checks say so.
func (s *Store) eachSecret(ctx context.Context, fn func(key string, raw []byte, f secretFile)) error {
	keys, err := s.secretObjects(ctx)
	if err != nil {
		return err
	}
	var (
		mu       sync.Mutex
		firstErr error
	)
	s.each(len(keys), func(i int) {
		data, err := s.b.Get(ctx, keys[i])
		if errors.Is(err, ErrNotFound) {
			return
		}
		if err != nil {
			mu.Lock()
			firstErr = errors.Join(firstErr, err)
			mu.Unlock()
			return
		}
		f, err := parseSecretFile(keys[i], data)
		if err != nil {
			return
		}
		fn(keys[i], data, f)
	})
	return firstErr
}

// resealSecrets seals the value of every version that is sealed in another group than current
// under current, in place. The commit does not change. A value that this key cannot open is
// left as it is, and a collection of groups keeps the group it is sealed in.
func (s *Store) resealSecrets(ctx context.Context, current string) error {
	var (
		mu       sync.Mutex
		firstErr error
	)
	fail := func(err error) {
		mu.Lock()
		firstErr = errors.Join(firstErr, err)
		mu.Unlock()
	}
	err := s.eachSecret(ctx, func(key string, raw []byte, f secretFile) {
		if f.Sealed == nil || f.Sealed.Group == current {
			return
		}
		m := secretKeyRe.FindStringSubmatch(key)
		seq, _ := strconv.Atoi(m[2])
		c := secretCtx(s.Lineage(), SecretDisplay(m[1]), seq)
		plain, err := s.openBox(c, *f.Sealed)
		if err != nil {
			return
		}
		box, ok := s.sealBox(c, plain)
		clear(plain)
		if !ok {
			fail(ErrNotReader)
			return
		}
		f.Sealed = &box
		next, err := indentJSON(f)
		if err != nil {
			fail(err)
			return
		}
		if _, err := s.b.ReplaceIf(ctx, key, func(cur []byte) ([]byte, bool) { return next, bytes.Equal(cur, raw) }); err != nil && !errors.Is(err, ErrNotFound) {
			fail(Advise(err))
		}
	})
	return errors.Join(err, firstErr)
}

// sealedSecrets counts the values that are sealed in each group.
func (s *Store) sealedSecrets(ctx context.Context) (map[string]int, error) {
	var mu sync.Mutex
	counts := map[string]int{}
	err := s.eachSecret(ctx, func(_ string, _ []byte, f secretFile) {
		if f.Sealed != nil {
			mu.Lock()
			counts[f.Sealed.Group]++
			mu.Unlock()
		}
	})
	return counts, err
}

// SecretValues counts the versions that hold a value, which an unseal would leave in the open.
func (s *Store) SecretValues(ctx context.Context) (int, error) {
	counts, err := s.sealedSecrets(ctx)
	n := 0
	for _, c := range counts {
		n += c
	}
	return n, err
}

// SecretsHeld is the refusal of an unseal while the state holds the value of a secret, or nil.
// Unseal makes it too; a command that asks before it unseals asks this first.
func (s *Store) SecretsHeld(ctx context.Context) error { return s.refuseSecrets(ctx) }

// refuseSecrets is the refusal of an unseal while the state holds the value of a secret.
func (s *Store) refuseSecrets(ctx context.Context) error {
	n, err := s.SecretValues(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	held, them := fmt.Sprintf("%d values of secrets are", n), "them"
	if n == 1 {
		held, them = "1 value of a secret is", "it"
	}
	return fmt.Errorf("%w: %s sealed in it, and unsealing would write %s in the open. Remove each secret with pravo secret rm NAME and forget its values with pravo secret purge NAME --keep 0 first (pravo secret list shows them)", ErrSecretsHeld, held, them)
}

// secretTxnIDsAll lists the ids of the transactions that wrote or purged a version of a secret.
func (s *Store) secretTxnIDsAll(ctx context.Context) ([]string, error) {
	var (
		mu  sync.Mutex
		ids = map[string]bool{}
	)
	err := s.eachSecret(ctx, func(_ string, _ []byte, f secretFile) {
		mu.Lock()
		for _, id := range secretTxnIDs(f) {
			ids[id] = true
		}
		mu.Unlock()
	})
	out := make([]string, 0, len(ids))
	for id := range ids {
		if txnIDRe.MatchString(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, err
}

// copiedSecretDiffers is the rule of Copy (see copyVersions) for a version of a secret that
// both states hold with other bytes. A version is written once, but a rewrap writes its
// ciphertext again and a purge drops it, so the same version may have other bytes on the two
// sides: it is the same when its transaction and commit agree, and the source wins. A value
// that the destination purged and the source still holds is refused.
func copiedSecretDiffers(k string, cur, data []byte) (bool, string) {
	a, errA := parseSecretFile(k, cur)
	b, errB := parseSecretFile(k, data)
	if errA != nil || errB != nil || a.Txn != b.Txn || a.Commit != b.Commit {
		return false, "holds another version at " + k
	}
	if ka, _ := a.kind(); ka == SecretKindPurged {
		if kb, _ := b.kind(); kb == SecretKindValue {
			return false, "purged the value at " + k + ", and the source still holds it"
		}
	}
	return true, ""
}
