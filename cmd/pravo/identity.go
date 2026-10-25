package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"

	"github.com/pravo-systems/pravo/internal/protect"
)

// errNoIdentity is returned, wrapped, when no identity file can be found. A
// caller that can work without an identity (a read-only command on a sealed
// state, the viewer) tests for it with errors.Is.
var errNoIdentity = errors.New("no identity found")

// identityOptions say how to look for the identity of the person running the
// command.
type identityOptions struct {
	// Name selects a file of the identity directory by name. Empty means the
	// lookup order of resolveIdentity.
	Name string
	// Prompt allows asking for the passphrase of a sealed identity on the
	// terminal. A caller with no terminal of its own (pravo mcp) leaves it off,
	// and the passphrase then comes from the environment only.
	Prompt bool
	// Readers are the reader keys that have a lock in the current group of the
	// state being opened, when it is known. Several identity files and none named
	// like the user: the one whose reader key is among them is the identity.
	Readers []protect.ReaderPublic
}

// flagIdentity is the global --identity flag: the name of a file in the
// identity directory, for every command that opens a sealed state. It takes
// precedence over $PRAVO_IDENTITY.
var flagIdentity string

// identityUsage is the help text of --identity.
const identityUsage = "name of the identity to read a sealed state with; overrides $PRAVO_IDENTITY"

// Seams for tests.
var (
	stdinIsTerminal = func() bool {
		fd := os.Stdin.Fd()
		return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
	}
	askPassphrase = func(prompt string) ([]byte, error) { return protect.PassphraseFromTerminal(prompt, false) }
	unsetEnv      = os.Unsetenv
)

// The secret environment variables are removed from the environment once they
// have been read, so that nothing started later inherits them. The values are
// kept here, so that a second resolveIdentity in the same process still finds
// them.
var taken struct {
	mu                   sync.Mutex
	identity, passphrase string
	// loaded caches the identities already opened by this process, so that a
	// command that opens the state twice asks for the passphrase once.
	loaded map[string]*protect.Identity
}

// resolveIdentity finds and loads the identity of the person running the
// command. The order is:
//
//  1. o.Name: that file in the identity directory;
//  2. $PRAVO_IDENTITY: the contents of an identity file if the value starts
//     with '{', otherwise the path of one;
//  3. the files of $PRAVO_IDENTITY_DIR, or of the id directory below the
//     configuration directory (protect.ConfigDir). When there are several, the
//     one named like $USER, else the only one whose reader key is in o.Readers.
//
// No identity file at all is errNoIdentity. A sealed identity is opened with a
// passphrase from the terminal (no echo) if o.Prompt is set and stdin is a
// terminal, otherwise from $PRAVO_PASSPHRASE or $PRAVO_IDENTITY_PASSPHRASE.
// After an identity has been read from the environment the variables that hold
// secrets are removed from it.
func resolveIdentity(o identityOptions) (*protect.Identity, error) {
	taken.mu.Lock()
	defer taken.mu.Unlock()
	src, err := findIdentity(o)
	if err != nil {
		return nil, err
	}
	if id, ok := taken.loaded[src.key]; ok {
		return id, nil
	}
	data := src.data
	if data == nil {
		var warning string
		data, warning, err = protect.ReadIdentityFile(src.path)
		if err != nil {
			// A value that is no file is not repeated: it may be a secret.
			if _, serr := os.Stat(src.path); src.fromEnv && serr != nil {
				return nil, errors.New("PRAVO_IDENTITY is neither the contents of an identity file nor the path of one that can be read")
			}
			return nil, fmt.Errorf("identity %s: %w", src.path, err)
		}
		if warning != "" {
			fmt.Fprintln(os.Stderr, "Warning: "+warning)
		}
	}
	defer clear(data)
	id, err := loadIdentity(data, o.Prompt)
	if err != nil {
		return nil, err
	}
	forgetSecretEnv()
	if taken.loaded == nil {
		taken.loaded = map[string]*protect.Identity{}
	}
	taken.loaded[src.key] = id
	return id, nil
}

// forgetSecretEnv removes the secret variables from the environment and keeps
// what they held, for the rest of the process. Nothing is kept when the
// variables cannot be removed (the values are then still in the environment).
func forgetSecretEnv() {
	ident, pass := os.Getenv("PRAVO_IDENTITY"), taken.passphrase
	if ident == "" {
		ident = taken.identity
	}
	for _, name := range []string{"PRAVO_PASSPHRASE", "PRAVO_IDENTITY_PASSPHRASE"} {
		if v := os.Getenv(name); v != "" && pass == "" {
			pass = v
		}
	}
	for _, name := range protect.SecretEnv {
		unsetEnv(name)
	}
	taken.identity, taken.passphrase = "", ""
	if os.Getenv("PRAVO_IDENTITY") == "" && os.Getenv("PRAVO_PASSPHRASE") == "" && os.Getenv("PRAVO_IDENTITY_PASSPHRASE") == "" {
		taken.identity, taken.passphrase = ident, pass
	}
}

type identitySource struct {
	// data is the contents when they came from the environment; path otherwise.
	data    []byte
	path    string
	fromEnv bool
	// raw is the value of PRAVO_IDENTITY.
	raw string
	// key identifies the source in the cache.
	key string
}

// findIdentity must be called with taken.mu held.
func findIdentity(o identityOptions) (identitySource, error) {
	if name := o.Name; name != "" {
		dir, err := identityDir()
		if err != nil {
			return identitySource{}, err
		}
		if _, err := protect.IdentityPath(environment(), name); err != nil {
			return identitySource{}, err
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return identitySource{}, fmt.Errorf("%w: no identity named %s in %s", errNoIdentity, name, tilde(dir))
			}
			return identitySource{}, err
		}
		return fileSource(p), nil
	}
	v := os.Getenv("PRAVO_IDENTITY")
	if strings.TrimSpace(v) == "" {
		v = taken.identity
	}
	if strings.TrimSpace(v) != "" {
		return envSource(v), nil
	}
	dir, err := identityDir()
	if err != nil {
		return identitySource{}, err
	}
	names, err := identityNames(dir)
	switch {
	case err != nil:
		return identitySource{}, err
	case len(names) == 0:
		return identitySource{}, fmt.Errorf("%w in %s. Make one with: pravo id new --name NAME", errNoIdentity, tilde(dir))
	case len(names) == 1:
		return fileSource(filepath.Join(dir, names[0])), nil
	}
	for _, n := range names {
		if n == osUser() {
			return fileSource(filepath.Join(dir, n)), nil
		}
	}
	// The public part of an identity file is readable without its passphrase, and
	// meta.json says which reader keys the state is locked to.
	var readers []string
	for _, n := range names {
		if key, ok := publicReaderKey(filepath.Join(dir, n)); ok && slices.ContainsFunc(o.Readers, key.Equal) {
			readers = append(readers, n)
		}
	}
	if len(readers) == 1 {
		return fileSource(filepath.Join(dir, readers[0])), nil
	}
	var which string
	if len(readers) > 1 {
		which = fmt.Sprintf(" (%s all read this state)", strings.Join(readers, " "))
	}
	return identitySource{}, fmt.Errorf("%w: %s holds several identities (%s) and none is named like you%s; choose one with --identity NAME (--name NAME for pravo id show), or set PRAVO_IDENTITY to the path of one",
		errNoIdentity, tilde(dir), strings.Join(names, " "), which)
}

// publicReaderKey reads the reader key of an identity file without opening it.
func publicReaderKey(path string) (protect.ReaderPublic, bool) {
	data, err := readSmallFile(path, 64<<10)
	if err != nil {
		return protect.ReaderPublic{}, false
	}
	// The file of an identity for a job holds its private keys in the clear, and this
	// one may not be the one chosen.
	defer clear(data)
	var p publicIdentity
	if json.Unmarshal(data, &p) != nil {
		return protect.ReaderPublic{}, false
	}
	key, err := protect.ParsePublic(p.Reader.Pub)
	return key, err == nil
}

// envSource is an identity given by $PRAVO_IDENTITY: its contents, or a path.
func envSource(v string) identitySource {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") {
		sum := sha256.Sum256([]byte(v))
		return identitySource{data: []byte(v), fromEnv: true, raw: v, key: "env:" + hex.EncodeToString(sum[:])}
	}
	s := fileSource(v)
	s.fromEnv, s.raw = true, v
	return s
}

func fileSource(p string) identitySource {
	key := "file:" + p
	if st, err := os.Stat(p); err == nil {
		key += fmt.Sprintf(":%d:%d", st.Size(), st.ModTime().UnixNano())
	}
	return identitySource{path: p, key: key}
}

// identityDir is where the identity files are: $PRAVO_IDENTITY_DIR, or the id
// directory below the configuration directory.
func identityDir() (string, error) {
	d, err := protect.IdentityDir(environment())
	if err != nil {
		return "", fmt.Errorf("no configuration directory to keep identities in: %w", err)
	}
	return d, nil
}

// identityNames lists the identity files of dir, sorted. A directory that does
// not exist holds none.
func identityNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && validIdentityName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func validIdentityName(s string) bool {
	_, err := protect.IdentityPath(environment(), s)
	return err == nil
}

// osUser is the name of the person running the process, as $USER says.
func osUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return os.Getenv("USERNAME")
}

// tilde writes a path below the home directory with a leading ~.
func tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		if runtime.GOOS == "windows" {
			return p
		}
		return "~/" + filepath.ToSlash(rest)
	}
	return p
}

// loadIdentity parses an identity file's contents and, for a sealed one, asks
// for the passphrase.
func loadIdentity(data []byte, prompt bool) (*protect.Identity, error) {
	var head struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &head)
	terminal := prompt && stdinIsTerminal()
	attempts := 1
	if terminal {
		attempts = 3
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		id, err := protect.ParseIdentity(data, func() ([]byte, error) {
			return passphraseFor(head.Name, prompt, terminal)
		})
		if err == nil {
			return id, nil
		}
		lastErr = err
		if !terminal || !strings.Contains(err.Error(), "wrong passphrase") {
			break
		}
	}
	return nil, lastErr
}

func passphraseFor(name string, prompt, terminal bool) ([]byte, error) {
	if terminal {
		p, err := askPassphrase("Passphrase for " + name + ": ")
		if err != nil {
			return nil, err
		}
		return p, nil
	}
	pass := taken.passphrase
	for _, v := range []string{"PRAVO_PASSPHRASE", "PRAVO_IDENTITY_PASSPHRASE"} {
		if s := os.Getenv(v); s != "" {
			pass = s
			break
		}
	}
	taken.passphrase = pass
	if pass == "" {
		if prompt {
			return nil, fmt.Errorf("the identity %s is sealed with a passphrase and stdin is not a terminal; set PRAVO_PASSPHRASE.", name)
		}
		return nil, fmt.Errorf("the identity %s is sealed with a passphrase and none can be asked for here; set PRAVO_PASSPHRASE.", name)
	}
	return []byte(pass), nil
}
