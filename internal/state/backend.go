package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pravo-systems/pravo/internal/filelock"
	"github.com/pravo-systems/pravo/internal/protect"
)

// ErrNotFound is returned by Backend.Get for a missing key.
var ErrNotFound = errors.New("not found")

// ErrUnknownOutcome is returned by PutIfAbsent and ReplaceIf for a write that the
// client sent more than once, that the store refused, and that the client could
// not check: the refusal may be the answer to a resend of a write that the first
// attempt had made, and the read that would tell failed. The object may hold the
// bytes that were sent or the bytes of another writer. A caller that takes a lock
// reads it again, as after a refusal; any other returns an error that tells the
// user to run the command again, and never reports the write as refused, which is
// ErrChanged and says that another command wrote the object.
var ErrUnknownOutcome = errors.New("the store did not say whether the write was made")

// Backend is the storage a Store keeps its objects in. Keys are slash
// separated relative paths such as "resources/random_pet.a.json".
type Backend interface {
	// Name describes the backend for messages, e.g. "pravo.state" or
	// "s3://bucket/prefix".
	Name() string
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, data []byte) error
	// PutIfAbsent creates the object only if no object exists at key and
	// reports whether it did. It must be atomic: two concurrent callers
	// cannot both succeed. A backend that can lose the answer to a write returns
	// ErrUnknownOutcome, with the cause, when it cannot tell whether the write it
	// refused is its own (see ErrUnknownOutcome); ReplaceIf does the same.
	PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error)
	// DeleteIf deletes the object at key when match reports true for its
	// current contents, and reports whether it did. A missing object is
	// ErrNotFound. Between the read and the delete the object is not replaced or
	// deleted by another DeleteIf or ReplaceIf, so two callers that judge the same
	// object cannot both delete it, and a caller that judged an object that has
	// been replaced since deletes nothing. The locks are deleted this way.
	DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error)
	// ReplaceIf writes the contents that update returns for the current ones,
	// unless update reports false, and reports whether it wrote them. A missing
	// object is ErrNotFound. It is atomic against DeleteIf and ReplaceIf as
	// DeleteIf is. The locks are renewed this way.
	ReplaceIf(ctx context.Context, key string, update func(current []byte) (data []byte, ok bool)) (bool, error)
	Delete(ctx context.Context, key string) error
	// List returns the keys under prefix, sorted.
	List(ctx context.Context, prefix string) ([]string, error)
	// Local reports whether the backend is on this machine, in which case
	// lock holders can be checked by process id.
	Local() bool
}

// ObjectVersion names an object and the version of it that a listing saw. The tag is
// opaque: two listings that give the same tag for a key saw the same bytes.
type ObjectVersion struct{ Key, Tag string }

// VersionLister is implemented by a backend that says, in the listing itself, which
// version of each object it holds. The store uses it to trust a snapshot of the
// nodes (see snapshot.go) instead of reading each of them.
type VersionLister interface {
	ListVersions(ctx context.Context, prefix string) ([]ObjectVersion, error)
}

// LocalBackend keeps objects as files under a directory.
type LocalBackend struct {
	Dir string
}

// NewLocalBackend creates the directory layout under dir.
func NewLocalBackend(dir string) (*LocalBackend, error) {
	for _, sub := range []string{"", "resources", "txn", "locks"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	return &LocalBackend{Dir: dir}, nil
}

func (b *LocalBackend) Name() string { return b.Dir }
func (b *LocalBackend) Local() bool  { return true }

func (b *LocalBackend) path(key string) string {
	return filepath.Join(b.Dir, filepath.FromSlash(key))
}

func (b *LocalBackend) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := filelock.ReadFile(b.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

// Put writes the object to a temporary file of its own and renames it over the
// old one, so that a reader sees the old object or the new one and two writers
// of the same key do not share a file.
func (b *LocalBackend) Put(ctx context.Context, key string, data []byte) error {
	p := b.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := writeTemp(p, data)
	if err != nil {
		return err
	}
	if err := filelock.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// writeTemp writes data to a new temporary file next to p, which List skips,
// and returns its name.
func writeTemp(p string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// PutIfAbsent writes the object to a temporary file and renames the file to the
// key, which fails if the key exists, or links it when the filesystem cannot do
// that. An object that exists has all of its contents: a reader never finds a lock
// that is empty because its creator has not written it yet. A filesystem that has
// neither gets a file that is created and then written; a reader of a lock reads
// it again if it cannot be parsed.
func (b *LocalBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	p := b.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return false, err
	}
	tmp, err := writeTemp(p, data)
	if err != nil {
		return false, err
	}
	err = renameExcl(tmp, p)
	if err != nil && !errors.Is(err, os.ErrExist) {
		// The filesystem may not know exclusive renames.
		err = os.Link(tmp, p)
		_ = os.Remove(tmp)
	} else if err != nil {
		_ = os.Remove(tmp)
	}
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrExist):
		return false, nil
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return false, werr
	}
	return true, cerr
}

// DeleteIf reads the object and deletes it if match accepts what it read, while
// no other DeleteIf or ReplaceIf of an object in the same directory runs, in
// this process or in another.
func (b *LocalBackend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	p := b.path(key)
	defer filelock.Exclusive(filepath.Dir(p))()
	data, err := filelock.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !match(data) {
		return false, nil
	}
	if err := filelock.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, ErrNotFound
		}
		return false, err
	}
	return true, nil
}

// ReplaceIf reads the object and writes what update makes of it, under the
// same lock as DeleteIf.
func (b *LocalBackend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	p := b.path(key)
	defer filelock.Exclusive(filepath.Dir(p))()
	data, err := filelock.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	next, ok := update(data)
	if !ok {
		return false, nil
	}
	if err := b.Put(ctx, key, next); err != nil {
		return false, err
	}
	return true, nil
}

func (b *LocalBackend) Delete(ctx context.Context, key string) error {
	err := filelock.Remove(b.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (b *LocalBackend) List(ctx context.Context, prefix string) ([]string, error) {
	dir := b.path(prefix)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		out = append(out, strings.TrimSuffix(prefix, "/")+"/"+e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// ErrCopyRefused is returned by Copy for a copy that would replace the meta.json
// of a state that holds data with one that does not belong to it.
var ErrCopyRefused = errors.New("the copy would replace the state with another")

// Copy copies every object from src to dst and returns the number copied. The
// stored plans are copied with the records, claims on them included, and so are
// the immutable records of a state of format 2 or 3.
//
// A sealed state is bound to its lineage: its records open only under the
// lineage they were sealed with. Copy refuses to replace the meta.json of a
// destination that holds records with one of another lineage when either is
// sealed, and to replace a sealed meta.json with an unsealed one or the reverse.
// A destination that holds no records takes any state.
func Copy(ctx context.Context, src, dst Backend) (int, error) {
	srcMeta, err := readMeta(ctx, src)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	if err := checkCopy(ctx, srcMeta, dst); err != nil {
		return 0, err
	}
	n := 0
	copyKey := func(k string, optional bool) error {
		data, err := src.Get(ctx, k)
		if errors.Is(err, ErrNotFound) && optional {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
		if err := dst.Put(ctx, k, data); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
		n++
		return nil
	}
	// A state of format 2 or 3 changes the format first, so that a pravo that does
	// not know the layout refuses the destination before any pointer is in it. Its
	// records come before the pointers that name them. The marks of the destination's
	// collections are not carried and are dropped: the copy may put a record that
	// they mark, and it is unnamed until the pointers come.
	if srcMeta != nil && srcMeta.Format > Format {
		if err := dst.Delete(ctx, gcMarksKey); err != nil {
			return n, fmt.Errorf("%s: %w", gcMarksKey, err)
		}
		if err := copyKey("meta.json", false); err != nil {
			return n, err
		}
	}
	for _, prefix := range []string{"rec", "resources", "txn", "plans", DiscoveryRunDir} {
		keys, err := src.List(ctx, prefix)
		if err != nil {
			return n, err
		}
		for _, k := range keys {
			// A stored plan is removed on its own schedule by a collection: one
			// that went since the listing is not part of the copy. The same goes for
			// a record that was superseded, and for a discovery object that someone
			// withdrew.
			if err := copyKey(k, prefix != "resources" && prefix != "txn"); err != nil {
				return n, err
			}
		}
	}
	for _, dir := range []string{policyDir, rolesDir} {
		copied, err := copyVersions(ctx, src, dst, dir)
		n += copied
		if err != nil {
			return n, err
		}
	}
	copied, err := copySecrets(ctx, src, dst)
	n += copied
	if err != nil {
		return n, err
	}
	for _, k := range []string{"meta.json", "outputs.json"} {
		if k == "meta.json" && srcMeta != nil && srcMeta.Format > Format {
			continue
		}
		if err := copyKey(k, true); err != nil {
			return n, err
		}
	}
	return n, nil
}

// copyVersions puts the versions of the policies or of the roles under dir of src into dst without replacing any: a version
// that dst holds with other bytes, and a version of dst that src lacks, would rewrite
// the history of a policy, and are refused. A copy that skipped the prefix would
// leave the destination without its policies, which judges nothing.
func copyVersions(ctx context.Context, src, dst Backend, dir string) (int, error) {
	have, err := src.List(ctx, dir)
	if err != nil {
		return 0, err
	}
	held, err := dst.List(ctx, dir)
	if err != nil {
		return 0, err
	}
	for _, k := range held {
		if !slices.Contains(have, k) {
			return 0, fmt.Errorf("%w: %s holds the version %s, which the source lacks", ErrCopyRefused, dst.Name(), k)
		}
	}
	n := 0
	for _, k := range have {
		data, err := src.Get(ctx, k)
		if err != nil {
			return n, fmt.Errorf("%s: %w", k, err)
		}
		created, err := dst.PutIfAbsent(ctx, k, data)
		if err != nil {
			return n, fmt.Errorf("%s: %w", k, Advise(err))
		}
		if created {
			n++
			continue
		}
		cur, err := dst.Get(ctx, k)
		if err != nil {
			return n, fmt.Errorf("%s: %w", k, err)
		}
		if !bytes.Equal(cur, data) {
			return n, fmt.Errorf("%w: %s holds another version at %s", ErrCopyRefused, dst.Name(), k)
		}
	}
	return n, nil
}

// readMeta reads meta.json of the state kept in b.
func readMeta(ctx context.Context, b Backend) (*meta, error) {
	data, err := b.Get(ctx, "meta.json")
	if err != nil {
		return nil, err
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("reading %s/meta.json: %w", b.Name(), err)
	}
	return &m, nil
}

// checkCopy applies the rule of Copy to the destination.
func checkCopy(ctx context.Context, src *meta, dst Backend) error {
	cur, err := readMeta(ctx, dst)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if src == nil {
		src = &meta{}
	}
	// A copy of format 1 would put whole records over some of the pointers of a
	// state of format 2 and leave the rest, under a meta.json that says format 1,
	// which a pravo that does not know the pointers reads as records.
	if src.Format < FormatImmutable && cur.Format >= FormatImmutable {
		if empty, err := holdsNothing(ctx, dst); err != nil || empty {
			return err
		}
		return fmt.Errorf("%w: %s is a state of format %d and the source is format 1; upgrade the source first", ErrCopyRefused, dst.Name(), cur.Format)
	}
	srcSealed, dstSealed := src.Sealed != nil, cur.Sealed != nil
	if !srcSealed && !dstSealed {
		return nil
	}
	if srcSealed == dstSealed && src.Lineage == cur.Lineage {
		// A copy that lacks a group of the destination is older than a removal of a
		// reader: it would give the reader her lock back, and leave the records
		// sealed in the group without the lock that opens them.
		if srcSealed {
			for _, g := range cur.Sealed.Groups {
				if !slices.ContainsFunc(src.Sealed.Groups, func(h protect.Group) bool { return h.ID == g.ID }) {
					return fmt.Errorf("%w: %s has been rewrapped since the source was copied (it has the group %s, which the source lacks); copy it again", ErrCopyRefused, dst.Name(), g.ID)
				}
			}
		}
		return nil
	}
	if empty, err := holdsNothing(ctx, dst); err != nil || empty {
		return err
	}
	switch {
	case srcSealed && !dstSealed:
		return fmt.Errorf("%w: %s holds an unsealed state and %s is sealed", ErrCopyRefused, dst.Name(), "the source")
	case !srcSealed && dstSealed:
		return fmt.Errorf("%w: %s is sealed and the source is not", ErrCopyRefused, dst.Name())
	}
	return fmt.Errorf("%w: %s is a state of another lineage, and sealed records open only in their own", ErrCopyRefused, dst.Name())
}

// holdsNothing reports whether b holds no record, output, stored plan or
// discovery object, which is what a store that was opened and never written holds.
func holdsNothing(ctx context.Context, b Backend) (bool, error) {
	for _, prefix := range []string{"resources", "rec", "plans", DiscoveryRunDir, policyDir, rolesDir, secretDir} {
		keys, err := b.List(ctx, prefix)
		if err != nil {
			return false, err
		}
		if len(keys) > 0 {
			return false, nil
		}
	}
	if _, err := b.Get(ctx, "outputs.json"); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return true, nil
}
