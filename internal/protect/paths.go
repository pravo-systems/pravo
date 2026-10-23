package protect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ConfigDir is where pravo keeps per-user files that are not a cache:
//
//	$PRAVO_CONFIG_DIR, else $XDG_CONFIG_HOME/pravo, else ~/.config/pravo
//
// On every Unix, macOS included (os.UserConfigDir would say ~/Library/Application
// Support there, which the design does not use). On Windows it is %AppData%\pravo.
func ConfigDir() (string, error) {
	if d := os.Getenv("PRAVO_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		d, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, "pravo"), nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "pravo"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "pravo"), nil
}

// IdentityDir is where identity files are kept and looked for:
//
//	$PRAVO_IDENTITY_DIR, else the id directory of ConfigDir
//
// The commands that read identities and the one that makes them use this one
// directory.
func IdentityDir() (string, error) {
	if d := os.Getenv("PRAVO_IDENTITY_DIR"); d != "" {
		return d, nil
	}
	c, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "id"), nil
}

// IdentityPath is the file NAME in IdentityDir, ~/.config/pravo/id/NAME unless
// $PRAVO_IDENTITY_DIR or $PRAVO_CONFIG_DIR say otherwise. NAME is checked, so it
// cannot name a path.
func IdentityPath(name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("identity name %q: use letters, digits, . _ - (at most 64)", name)
	}
	d, err := IdentityDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// EnsurePrivateDir creates dir (and its parents with 0755, as MkdirAll would
// under a normal umask) and makes dir itself, only if it had to create it, mode
// 0700. A directory that exists is left alone, because it may be the user's own
// ~/.config.
func EnsurePrivateDir(dir string) error {
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return chmodPrivate(dir, 0o700) // umask can only remove bits, but be explicit
}

// WritePrivateFile writes data to path with mode 0600, atomically: a temporary
// file in the same directory, created 0600 with O_EXCL, synced, then renamed. It
// refuses to replace an existing file unless replace is set. The directory is
// created 0700 if missing (only the last element).
func WritePrivateFile(path string, data []byte, replace bool) error {
	dir := filepath.Dir(path)
	if err := EnsurePrivateDir(dir); err != nil {
		return err
	}
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+".tmp"+hexSuffix())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	if !replace {
		// A hard link fails if path exists, where rename would overwrite it. Where
		// links are not supported, fall through to the rename after the check above.
		switch err := os.Link(tmp, path); {
		case err == nil:
			return os.Remove(tmp)
		case errors.Is(err, os.ErrExist):
			os.Remove(tmp)
			return fmt.Errorf("%s already exists", path)
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func hexSuffix() string {
	const hex = "0123456789abcdef"
	b := randBytes(6)
	for i := range b {
		b[i] = hex[b[i]&15]
	}
	return string(b)
}

// ReadIdentityFile reads an identity file and, on Unix, refuses to read an
// unsealed one that others can read (as ssh does for private keys). A sealed one
// only draws a warning, returned to the caller to print.
func ReadIdentityFile(path string) (data []byte, warning string, err error) {
	// Reading a named pipe waits for a writer: look before opening.
	if st, serr := os.Stat(path); serr == nil && !st.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%s is not a regular file", path)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if loose, mode := looseMode(path); loose {
		msg := fmt.Sprintf("%s has mode %04o: it should be readable by you alone (chmod 600)", path, mode)
		if hasPassphraseSeal(data) {
			return data, msg, nil
		}
		clear(data)
		return nil, "", errors.New(msg + "; refusing to use an identity with no passphrase")
	}
	return data, "", nil
}

// hasPassphraseSeal reports whether data is an identity file with a "seal" key,
// that is, one whose secrets are under a passphrase. It looks at the key and not at
// the text, so that a CI identity whose name is "seal" is not taken for one.
func hasPassphraseSeal(data []byte) bool {
	var probe struct {
		Seal json.RawMessage `json:"seal"`
	}
	return json.Unmarshal(data, &probe) == nil && len(probe.Seal) > 0 && string(probe.Seal) != "null"
}

// SaveIdentity writes the file NAME of the identity directory (see IdentityDir).
// It creates the directory with mode 0700 when it is missing, and, when the
// directory is the default one, ~/.config/pravo above it the same way.
func SaveIdentity(name string, data []byte, replace bool) (string, error) {
	path, err := IdentityPath(name)
	if err != nil {
		return "", err
	}
	idDir := filepath.Dir(path)
	if os.Getenv("PRAVO_IDENTITY_DIR") == "" {
		if err := EnsurePrivateDir(filepath.Dir(idDir)); err != nil {
			return "", err
		}
	}
	if err := EnsurePrivateDir(idDir); err != nil {
		return "", err
	}
	return path, WritePrivateFile(path, data, replace)
}
