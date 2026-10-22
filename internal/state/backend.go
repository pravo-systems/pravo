package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pravo-systems/pravo/internal/filelock"
)

// ErrNotFound is returned by Backend.Get for a missing key.
var ErrNotFound = errors.New("not found")

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
	// cannot both succeed.
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

// Copy copies every object from src to dst and returns the number copied. The
// stored plans are copied with the records, claims on them included.
func Copy(ctx context.Context, src, dst Backend) (int, error) {
	n := 0
	for _, prefix := range []string{"resources", "txn", "plans"} {
		keys, err := src.List(ctx, prefix)
		if err != nil {
			return n, err
		}
		for _, k := range keys {
			data, err := src.Get(ctx, k)
			if errors.Is(err, ErrNotFound) && prefix == "plans" {
				// A stored plan is removed on its own schedule by a collection: one
				// that went since the listing is not part of the copy.
				continue
			}
			if err != nil {
				return n, fmt.Errorf("%s: %w", k, err)
			}
			if err := dst.Put(ctx, k, data); err != nil {
				return n, fmt.Errorf("%s: %w", k, err)
			}
			n++
		}
	}
	for _, k := range []string{"meta.json", "outputs.json"} {
		data, err := src.Get(ctx, k)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return n, fmt.Errorf("%s: %w", k, err)
		}
		if err := dst.Put(ctx, k, data); err != nil {
			return n, fmt.Errorf("%s: %w", k, err)
		}
		n++
	}
	return n, nil
}
