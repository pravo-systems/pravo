package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// secretWritable is the refusal of a write of a secret that this store cannot make: the state
// has to be sealed, and the store has to hold a key of it.
func (s *Store) secretWritable() error {
	if !s.sealedState() {
		return fmt.Errorf("%w: seal the state with pravo protect init --encrypt", ErrSecretsNeedSeal)
	}
	if err := s.writable(); err != nil {
		return err
	}
	s.recheckSeal()
	if !s.ring.has() {
		return ErrNotReader
	}
	return nil
}

// failSecretTxn ends the transaction of a write that did not happen, and returns err.
func failSecretTxn(t *Txn, err error) error {
	_ = t.FinishError("failed", "no secret was written", err.Error(), true)
	return err
}

// putSecret creates the version at key with the transaction t, and records the change of it.
func (s *Store) putSecret(ctx context.Context, t *Txn, name, action string, before, seq int, f secretFile) error {
	data, err := indentJSON(f)
	if err != nil {
		return err
	}
	// Only the first secret of a state writes meta.json: a writer whose credentials cannot put
	// it can set every later version.
	if s.Format() < FormatSecret {
		if err := s.setFormat(FormatSecret); err != nil {
			return err
		}
	}
	created, err := s.b.PutIfAbsent(ctx, secretKey(encodeAddr(name), seq), data)
	if err != nil {
		return Advise(err)
	}
	if !created {
		return fmt.Errorf("%s: %w: version %d exists", name, ErrSecretChanged, seq)
	}
	t.RecordWith(SecretAddr(name), action, before, seq, f.Commit)
	return nil
}

// sameName finds the secret that has the name of another one, up to case: they would share a
// file on a disk that does not tell the case.
func sameName(names map[string]int, name string) string {
	for enc := range names {
		if other := SecretDisplay(enc); other != name && strings.EqualFold(other, name) {
			return other
		}
	}
	return ""
}

// SetSecret writes a new version of the secret with the value, creating the secret if the
// state has none of that name, and signs it as the transaction it runs in, with command as the
// command line it shows. The value is sealed to the readers of the state. note is kept with the
// version, sealed. A version that another writer put at the same number first is ErrSecretChanged.
// The caller clears value.
func (s *Store) SetSecret(ctx context.Context, name string, value []byte, note, command string) (SecretInfo, error) {
	switch {
	case ValidSecretName(name) != nil:
		return SecretInfo{}, ValidSecretName(name)
	case len(value) == 0:
		return SecretInfo{}, errors.New("a secret cannot be empty; to take a secret away, use pravo secret rm")
	case len(value) > MaxSecretValue:
		return SecretInfo{}, fmt.Errorf("a secret holds at most %d bytes", MaxSecretValue)
	case len(note) > MaxSecretNote || !utf8.ValidString(note):
		return SecretInfo{}, fmt.Errorf("a note is text of at most %d bytes", MaxSecretNote)
	}
	if err := s.secretWritable(); err != nil {
		return SecretInfo{}, err
	}
	names, err := s.secretNames(ctx)
	if err != nil {
		return SecretInfo{}, err
	}
	enc := encodeAddr(name)
	if names[enc] == 0 {
		if other := sameName(names, name); other != "" {
			return SecretInfo{}, fmt.Errorf("the state has a secret named %s, and %s would share its file on a disk that does not tell upper case from lower", other, name)
		}
	}
	head := names[enc]
	seq := head + 1
	lineage := s.Lineage()

	txn := s.Begin("secret", command)
	if err := txn.Start(); err != nil {
		return SecretInfo{}, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return SecretInfo{}, failSecretTxn(txn, err)
	}
	plain, err := marshalBody(secretBody{Value: value, Nonce: nonce, Note: note})
	if err != nil {
		return SecretInfo{}, failSecretTxn(txn, err)
	}
	defer clear(plain)
	box, ok := s.sealBox(secretCtx(lineage, name, seq), plain)
	if !ok {
		return SecretInfo{}, failSecretTxn(txn, ErrNotReader)
	}
	now := time.Now().UTC()
	f := secretFile{V: secretFileVersion, At: now, Txn: txn.ID(), Commit: secretCommit(lineage, name, seq, nonce, value, note), Sealed: &box}
	if err := s.putSecret(ctx, txn, name, "set", head, seq, f); err != nil {
		return SecretInfo{}, failSecretTxn(txn, err)
	}
	info := secretInfoOf(name, seq, f)
	info.Trust = TrustOK
	return info, txn.Finish("applied", fmt.Sprintf("set %s, version %d", name, seq), true)
}

// RemoveSecret ends the secret: it writes a removal as the next version. The values stay in the
// versions until they are purged (PurgeSecret). A run that reads the secret fails from then on.
func (s *Store) RemoveSecret(ctx context.Context, name, command string) (SecretInfo, error) {
	if err := ValidSecretName(name); err != nil {
		return SecretInfo{}, err
	}
	if err := s.secretWritable(); err != nil {
		return SecretInfo{}, err
	}
	names, err := s.secretNames(ctx)
	if err != nil {
		return SecretInfo{}, err
	}
	enc := encodeAddr(name)
	head := names[enc]
	if head == 0 {
		return SecretInfo{}, fmt.Errorf("%s: %w", name, ErrNoSecret)
	}
	cur, err := s.readSecretFile(ctx, enc, head)
	if err != nil {
		return SecretInfo{}, err
	}
	if cur.Removed {
		return SecretInfo{}, fmt.Errorf("%s: %w", name, ErrSecretRemoved)
	}
	seq := head + 1
	txn := s.Begin("secret", command)
	if err := txn.Start(); err != nil {
		return SecretInfo{}, err
	}
	f := secretFile{V: secretFileVersion, At: time.Now().UTC(), Txn: txn.ID(), Commit: removalCommit(s.Lineage(), name, seq), Removed: true}
	if err := s.putSecret(ctx, txn, name, "remove", head, seq, f); err != nil {
		return SecretInfo{}, failSecretTxn(txn, err)
	}
	info := secretInfoOf(name, seq, f)
	info.Trust = TrustOK
	return info, txn.Finish("applied", fmt.Sprintf("removed %s, version %d", name, seq), true)
}

// PurgeSecret forgets the values of old versions of the secret, keeping the newest keep. The
// object of a purged version stays, without its sealed part, and names the transaction that
// purged it, so a purge is as visible as a write. A secret that is live keeps at least its
// latest value; one that was removed may keep none. It returns how many values it purged. The
// versions are written once and not changed, so a bucket that keeps old versions of its objects
// keeps the old values too.
func (s *Store) PurgeSecret(ctx context.Context, name string, keep int, command string) (int, error) {
	if err := ValidSecretName(name); err != nil {
		return 0, err
	}
	if keep < 0 {
		return 0, errors.New("keep is a number of versions, 0 or more")
	}
	if err := s.secretWritable(); err != nil {
		return 0, err
	}
	names, err := s.secretNames(ctx)
	if err != nil {
		return 0, err
	}
	enc := encodeAddr(name)
	n := names[enc]
	if n == 0 {
		return 0, fmt.Errorf("%s: %w", name, ErrNoSecret)
	}
	files := make([]secretFile, n)
	errs := make([]error, n)
	s.each(n, func(i int) { files[i], errs[i] = s.readSecretFile(ctx, enc, i+1) })
	if err := errors.Join(errs...); err != nil {
		return 0, err
	}
	var values []int
	for i, f := range files {
		if k, _ := f.kind(); k == SecretKindValue {
			values = append(values, i+1)
		}
	}
	if !files[n-1].Removed && keep < 1 {
		return 0, fmt.Errorf("%s is live: keep at least its latest value, or remove the secret first with pravo secret rm", name)
	}
	doomed := values[:max(len(values)-keep, 0)]
	if len(doomed) == 0 {
		return 0, nil
	}
	txn := s.Begin("secret", command)
	if err := txn.Start(); err != nil {
		return 0, err
	}
	purged := 0
	for _, seq := range doomed {
		stub := secretFile{V: secretFileVersion, At: files[seq-1].At, Txn: files[seq-1].Txn, Commit: files[seq-1].Commit, Purged: true, PurgedBy: txn.ID()}
		done, err := s.purgeVersion(ctx, enc, seq, stub)
		if err != nil {
			_ = txn.Finish("failed", fmt.Sprintf("purged %d of %d values of %s", purged, len(doomed), name), true)
			return purged, err
		}
		if done {
			purged++
			txn.RecordWith(SecretAddr(name), "purge", seq, 0, stub.Commit)
		}
	}
	return purged, txn.Finish("applied", fmt.Sprintf("purged %d values of %s", purged, name), true)
}

// purgeVersion replaces the value at seq by the stub. A version that is not a value any more,
// because someone purged it first, is left alone. A version that was written again while this
// ran (a rewrap seals it in another group) is read again.
func (s *Store) purgeVersion(ctx context.Context, enc string, seq int, stub secretFile) (bool, error) {
	key := secretKey(enc, seq)
	data, err := indentJSON(stub)
	if err != nil {
		return false, err
	}
	for range replaceAttempts {
		cur, err := s.b.Get(ctx, key)
		if err != nil {
			return false, fmt.Errorf("%s: %w", key, err)
		}
		f, err := parseSecretFile(key, cur)
		if err != nil {
			return false, err
		}
		if k, _ := f.kind(); k != SecretKindValue {
			return false, nil
		}
		if f.Txn != stub.Txn || f.Commit != stub.Commit {
			return false, fmt.Errorf("%w: %s", ErrUntrusted, key)
		}
		ok, err := s.b.ReplaceIf(ctx, key, func(c []byte) ([]byte, bool) { return data, bytes.Equal(c, cur) })
		if err != nil {
			return false, Advise(err)
		}
		if ok {
			return true, nil
		}
	}
	return false, fmt.Errorf("%s keeps changing; run the command again", key)
}
