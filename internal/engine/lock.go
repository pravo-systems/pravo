package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// LockFileName is the provider lock file, written in CUE.
const LockFileName = "pravo.lock.cue"

// LockEntry pins one provider.
type LockEntry struct {
	Version     string
	Constraints string
	// Hashes maps a platform, such as "darwin_arm64", to the "sha256:<hex>" of
	// the provider archive for it.
	Hashes map[string]string
}

// ModuleEntry pins one dependency module: the version it resolved to and a
// hash of the files that were loaded from it.
type ModuleEntry struct {
	Version string
	Hash    string
}

// Lock is the parsed lock file.
type Lock struct {
	Providers map[string]*LockEntry   // keyed by source address
	Modules   map[string]*ModuleEntry // keyed by module path with major version
}

// ErrModuleChanged is returned when a locked module version now has
// different content, which means a republished tag or tampering.
type ErrModuleChanged struct {
	Path, Version, Locked, Now string
}

func (e *ErrModuleChanged) Error() string {
	return fmt.Sprintf("module %s %s has different content than when it was locked (%s, now %s); a published version must not change. If this is expected, review the change and run \"pravo init\" to relock", e.Path, e.Version, e.Locked, e.Now)
}

// UpdateModules records the modules in use. A module whose version is
// unchanged but whose hash differs is an error unless relock is set; a new
// version replaces the old entry.
func (l *Lock) UpdateModules(uses []ModuleUse, relock bool) error {
	if l.Modules == nil {
		l.Modules = map[string]*ModuleEntry{}
	}
	seen := map[string]bool{}
	for _, m := range uses {
		seen[m.Path] = true
		if old, ok := l.Modules[m.Path]; ok && old.Version == m.Version && old.Hash != m.Hash && !relock {
			return &ErrModuleChanged{Path: m.Path, Version: m.Version, Locked: old.Hash, Now: m.Hash}
		}
		l.Modules[m.Path] = &ModuleEntry{Version: m.Version, Hash: m.Hash}
	}
	for p := range l.Modules {
		if !seen[p] {
			delete(l.Modules, p) // no longer used
		}
	}
	return nil
}

// ModuleUse mirrors config.ModuleUse without importing it here.
type ModuleUse struct{ Path, Version, Hash string }

// ReadLock parses dir/pravo.lock.cue; a missing file yields an empty lock.
func ReadLock(dir string) (*Lock, error) {
	l := &Lock{Providers: map[string]*LockEntry{}, Modules: map[string]*ModuleEntry{}}
	data, err := os.ReadFile(filepath.Join(dir, LockFileName))
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	v := cuecontext.New().CompileBytes(data, cue.Filename(LockFileName))
	if err := v.Err(); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", LockFileName, err)
	}
	if mi, err := v.LookupPath(cue.ParsePath("modules")).Fields(); err == nil {
		for mi.Next() {
			e := &ModuleEntry{}
			e.Version, _ = mi.Value().LookupPath(cue.ParsePath("version")).String()
			e.Hash, _ = mi.Value().LookupPath(cue.ParsePath("hash")).String()
			l.Modules[mi.Selector().Unquoted()] = e
		}
	}
	it, err := v.LookupPath(cue.ParsePath("providers")).Fields()
	if err != nil {
		return l, nil
	}
	for it.Next() {
		e := &LockEntry{}
		body := it.Value()
		e.Version, _ = body.LookupPath(cue.ParsePath("version")).String()
		e.Constraints, _ = body.LookupPath(cue.ParsePath("constraints")).String()
		if hi, err := body.LookupPath(cue.ParsePath("hashes")).Fields(); err == nil {
			e.Hashes = map[string]string{}
			for hi.Next() {
				if s, err := hi.Value().String(); err == nil {
					e.Hashes[hi.Selector().Unquoted()] = s
				}
			}
		}
		if e.Version != "" {
			l.Providers[it.Selector().Unquoted()] = e
		}
	}
	return l, nil
}

// Write renders the lock file.
func (l *Lock) Write(dir string) error {
	keys := make([]string, 0, len(l.Providers))
	for k := range l.Providers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("// This file is maintained by \"pravo init\". Commit it to version control.\n\n")
	for _, k := range keys {
		e := l.Providers[k]
		fmt.Fprintf(&b, "providers: %s: {\n", strconv.Quote(k))
		fmt.Fprintf(&b, "\tversion: %s\n", strconv.Quote(e.Version))
		if e.Constraints != "" {
			fmt.Fprintf(&b, "\tconstraints: %s\n", strconv.Quote(e.Constraints))
		}
		if len(e.Hashes) > 0 {
			plats := make([]string, 0, len(e.Hashes))
			for p := range e.Hashes {
				plats = append(plats, p)
			}
			sort.Strings(plats)
			b.WriteString("\thashes: {\n")
			for _, p := range plats {
				fmt.Fprintf(&b, "\t\t%s: %s\n", strconv.Quote(p), strconv.Quote(e.Hashes[p]))
			}
			b.WriteString("\t}\n")
		}
		b.WriteString("}\n")
	}
	mkeys := make([]string, 0, len(l.Modules))
	for k := range l.Modules {
		mkeys = append(mkeys, k)
	}
	sort.Strings(mkeys)
	for _, k := range mkeys {
		e := l.Modules[k]
		fmt.Fprintf(&b, "modules: %s: {\n\tversion: %s\n\thash:    %s\n}\n", strconv.Quote(k), strconv.Quote(e.Version), strconv.Quote(e.Hash))
	}
	return os.WriteFile(filepath.Join(dir, LockFileName), []byte(b.String()), 0o644)
}
