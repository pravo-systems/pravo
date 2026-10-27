// Package env holds the environment a run sees, as a value.
//
// No package under internal/ reads the environment of the process. The command
// line reads it once, with OS, and hands the value down; a program that embeds
// pravo hands down whatever it wants the run to see. TestNoProcessEnvironment
// holds the rule.
package env

import (
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Env is a set of environment variables. The zero value is an empty environment.
// It is a value: With and Without return a changed copy.
type Env struct {
	environ []string // NAME=value, in order; the last of a name wins
}

// New returns the environment of the given NAME=value entries, as os.Environ
// lists them.
func New(environ []string) Env {
	return Env{environ: append([]string(nil), environ...)}
}

// Map returns the environment of a map of names to values, in the order of the names.
func Map(vars map[string]string) Env {
	var e Env
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		e.environ = append(e.environ, k+"="+vars[k])
	}
	return e
}

// Lookup returns the value of a variable and whether it is set. A name is
// compared without regard to case on Windows, as the system does.
func (e Env) Lookup(name string) (string, bool) {
	for i := len(e.environ) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(e.environ[i], "=")
		if ok && sameName(k, name) {
			return v, true
		}
	}
	return "", false
}

// Get returns the value of a variable, "" when it is not set.
func (e Env) Get(name string) string {
	v, _ := e.Lookup(name)
	return v
}

// Environ returns the environment as NAME=value entries, for a child process.
func (e Env) Environ() []string { return append([]string(nil), e.environ...) }

// With returns the environment with the variable set.
func (e Env) With(name, value string) Env {
	return Env{environ: append(e.Without(name).environ, name+"="+value)}
}

// Without returns the environment without the variable.
func (e Env) Without(name string) Env {
	out := make([]string, 0, len(e.environ))
	for _, kv := range e.environ {
		if k, _, _ := strings.Cut(kv, "="); !sameName(k, name) {
			out = append(out, kv)
		}
	}
	return Env{environ: out}
}

func sameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// The directories of the user, from the variables of this environment. They
// follow os.UserHomeDir, os.UserConfigDir and os.UserCacheDir, which read the
// environment of the process.

// UserHomeDir is the home directory of the user: $HOME, on Windows %USERPROFILE%.
func (e Env) UserHomeDir() (string, bool) {
	name := "HOME"
	if runtime.GOOS == "windows" {
		name = "USERPROFILE"
	}
	h := e.Get(name)
	return h, h != ""
}

// UserCacheDir is the directory for cached files of the user.
func (e Env) UserCacheDir() (string, bool) {
	switch runtime.GOOS {
	case "windows":
		d := e.Get("LocalAppData")
		return d, d != ""
	case "darwin", "ios":
		if h, ok := e.UserHomeDir(); ok {
			return filepath.Join(h, "Library", "Caches"), true
		}
	default:
		if d := e.Get("XDG_CACHE_HOME"); filepath.IsAbs(d) {
			return d, true
		}
		if h, ok := e.UserHomeDir(); ok {
			return filepath.Join(h, ".cache"), true
		}
	}
	return "", false
}

// UserConfigDir is the directory for configuration files of the user.
func (e Env) UserConfigDir() (string, bool) {
	switch runtime.GOOS {
	case "windows":
		d := e.Get("AppData")
		return d, d != ""
	case "darwin", "ios":
		if h, ok := e.UserHomeDir(); ok {
			return filepath.Join(h, "Library", "Application Support"), true
		}
	default:
		if d := e.Get("XDG_CONFIG_HOME"); filepath.IsAbs(d) {
			return d, true
		}
		if h, ok := e.UserHomeDir(); ok {
			return filepath.Join(h, ".config"), true
		}
	}
	return "", false
}
