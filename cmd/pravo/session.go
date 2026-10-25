package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"
	"go.opentelemetry.io/otel/attribute"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// session bundles everything a command needs after loading the root.
type session struct {
	root      *config.Root
	reg       *registry.Client
	providers *engine.Providers
	lock      *engine.Lock
	schemas   map[string]*schema.ProviderSchema
	value     cue.Value
	state     *state.Store
}

type sessionOptions struct {
	// install allows downloading providers that are missing.
	install bool
	// upgrade ignores the lock file when choosing versions.
	upgrade bool
	// schemas fetches schemas and unifies them into the configuration.
	schemas bool
	// loadState reads the state file.
	loadState bool
	// readOnlyState reads the state without creating or changing it: a
	// location that holds none is read as an empty state. For the commands that
	// only look.
	readOnlyState bool
	// regenerates is set by the commands that write the generated provider
	// packages in this run, which therefore do not warn that they are old.
	regenerates bool
	// root, when set, is used instead of loading the configuration.
	root *config.Root
	// relockModules accepts changed module content (pravo init).
	relockModules bool
}

// loadRoot loads and evaluates the specification in the working directory. An
// error from loading or evaluating it exits with ExitInvalid, in every
// command.
func loadRoot() (*config.Root, error) {
	ctx, end := setupSpan("load specification")
	root, err := config.Load(".", config.LoadOptions{Tags: flagTags, SysTags: flagSysTags, Context: ctx, Env: environment()})
	end(err)
	if err != nil {
		return nil, engine.WithCode(engine.ExitInvalid, err)
	}
	return root, nil
}

// setupSpan starts the span of a stage of readying a command, under the
// command's span.
func setupSpan(name string, attrs ...attribute.KeyValue) (context.Context, func(error)) {
	ctx := context.Background()
	if runningCommand != nil && runningCommand.Context() != nil {
		ctx = runningCommand.Context()
	}
	return telemetry.Span(ctx, "setup "+name, attrs...)
}

func newSession(ctx context.Context, opts sessionOptions) (*session, error) {
	root := opts.root
	var err error
	began := time.Now()
	if root == nil {
		root, err = loadRoot()
		if err != nil {
			return nil, err
		}
	}
	loaded := []any{"dir", root.Dir, "managed", countMode(root, "managed"), "data", countMode(root, "data"),
		"ephemeral", countMode(root, "ephemeral"), "calls", countMode(root, "call"), "outputs", len(root.Outputs),
		"providers", len(root.Providers), "moves", len(root.Moves), "imports", len(root.Imports)}
	if root.Pruned {
		loaded = append(loaded, "pruned", true)
	}
	plog.Debug(ctx, "loaded", append(loaded, "took", took(began))...)
	s := &session{root: root, reg: registry.NewClient(environment())}
	_, end := telemetry.Span(ctx, "setup read lock file")
	s.lock, err = engine.ReadLock(root.Dir)
	end(err)
	if err != nil {
		return nil, err
	}
	if opts.loadState {
		octx, end := telemetry.Span(ctx, "setup open state", attribute.String("pravo.state.backend", backendName(root.State.Backend)))
		if opts.readOnlyState {
			s.state, err = openStateValues(octx, root)
		} else {
			s.state, err = openState(octx, root)
		}
		end(err)
		if err != nil {
			return nil, err
		}
		s.state.SetContext(ctx)
		// Providers that only exist in state must still be resolvable.
		for _, sr := range s.state.List() {
			local := config.LocalName(sr.ProviderKey)
			if _, ok := root.Providers[local]; !ok && sr.ProviderProtocol == "" {
				// A provider reached by a non-default protocol cannot be
				// rediscovered from the state: its address is not recorded.
				src, err := registry.ParseSource(sr.Provider)
				if err == nil {
					root.Providers[local] = &config.ProviderReq{LocalName: local, Source: src, Implied: true, Protocol: config.ProtocolTfplugin}
				}
			}
		}
	}
	s.providers = engine.NewProviders(root, s.reg)
	s.providers.Env = environment()
	s.providers.Log = func(msg string) { out.Println(msg) }
	rctx, end := telemetry.Span(ctx, "setup resolve providers", attribute.Int("pravo.providers", len(root.Providers)))
	err = s.providers.Resolve(rctx, s.lock, opts.upgrade, opts.install)
	end(err)
	if err != nil {
		return nil, err
	}
	before := lockString(s.lock)
	s.providers.UpdateLock(s.lock)
	var uses []engine.ModuleUse
	for _, m := range root.Modules {
		uses = append(uses, engine.ModuleUse{Path: m.Path, Version: m.Version, Hash: m.Hash})
	}
	if err := s.lock.UpdateModules(uses, opts.relockModules); err != nil {
		return nil, engine.WithCode(engine.ExitRefused, err)
	}
	if lockString(s.lock) != before {
		plog.Debug(ctx, "writing the lock file", "file", engine.LockFileName)
		if err := writeLock(s.lock, root.Dir); err != nil {
			return nil, err
		}
	}
	if !opts.regenerates {
		s.warnStaleSchemas()
	}
	if opts.schemas {
		began := time.Now()
		sctx, end := telemetry.Span(ctx, "setup load schemas", attribute.Int("pravo.providers", len(root.Providers)))
		s.schemas, err = s.providers.Schemas(sctx)
		end(err)
		if err != nil {
			return nil, err
		}
		s.value = root.Value
		plog.Debug(ctx, "schemas ready", "providers", len(s.schemas), "took", took(began))
	}
	return s, nil
}

// took is the time since began, to the millisecond, as the debug log writes it.
func took(began time.Time) time.Duration { return time.Since(began).Round(time.Millisecond) }

// writeLock writes the lock file in the layout that "pravo fmt" leaves it in,
// so that formatting it changes nothing.
func writeLock(l *engine.Lock, dir string) error {
	if err := l.Write(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, engine.LockFileName)
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	formatted, err := format.Source(src)
	if err != nil {
		return fmt.Errorf("%s: %w", engine.LockFileName, err)
	}
	_, err = writeIfChanged(path, formatted)
	return err
}

func lockString(l *engine.Lock) string {
	var b strings.Builder
	for k, e := range l.Providers {
		fmt.Fprintf(&b, "%s=%s;%s;%v\n", k, e.Version, e.Constraints, e.Hashes)
	}
	var mk []string
	for k, e := range l.Modules {
		mk = append(mk, fmt.Sprintf("%s=%s;%s", k, e.Version, e.Hash))
	}
	sort.Strings(mk)
	fmt.Fprintf(&b, "modules:%v", mk)
	return b.String()
}

func (s *session) close() {
	if s == nil {
		return
	}
	if s.providers != nil {
		s.providers.Close()
	}
	if s.state != nil {
		s.state.Close()
	}
}

func (s *session) engine(opts engine.Options) *engine.Engine {
	if opts.Progress == nil {
		opts.Progress = func(format string, args ...any) { out.Printf(format+"\n", args...) }
	}
	if opts.DefaultRefresh == "" {
		opts.DefaultRefresh = s.root.Refresh
	}
	if opts.Command == "" {
		setCommand(s.state, os.Args[1:], &opts)
	}
	opts.Env = environment()
	opts.Actor, opts.Reason, opts.GitRevision = flagActor, flagReason, gitRevision(s.root.Dir)
	opts.Readers = loadReadersFile(s.root.Dir, flagAccessDir).Readers
	if opts.Identity == nil {
		// Looked for only when remote_outputs meets a sealed state, so that a command
		// that never needs a key never asks for a passphrase.
		// The readers of the other state are given: with several identity files and
		// none named like the user, the one whose reader key has a lock in it is
		// chosen, as it is for the state of this root.
		opts.Identity = func(readers []protect.ReaderPublic) (*protect.Identity, error) {
			return resolveIdentity(identityOptions{Name: flagIdentity, Prompt: identityPrompt, Readers: readers})
		}
	}
	if flagJSON && streamEvents && opts.Event == nil {
		// Only commands that change things stream events; plan-like
		// commands print a single document.
		opts.Event = func(ev engine.Event) { emitLine(ev) }
	}
	return engine.New(s.root, s.state, s.providers, s.schemas, s.value, opts)
}

// openState opens the configured state backend.
func openState(ctx context.Context, root *config.Root) (*state.Store, error) {
	began := time.Now()
	st, err := openStateStore(ctx, root)
	if err == nil {
		plog.Debug(ctx, "state opened", "backend", backendName(root.State.Backend), "location", st.Dir(), "nodes", st.Len(), "took", took(began))
	}
	return st, err
}

// openStateStore opens the state of the root for a command that writes it. A
// sealed state needs the identity of a reader and a readers file that matches
// the state.
func openStateStore(ctx context.Context, root *config.Root) (*state.Store, error) {
	st, _, err := openStateAs(ctx, root, true, false)
	return st, err
}

// openStateAs opens the state, to write it or not. A state that is sealed is
// opened with the identity of the user, which is looked for only then, so that
// a state that is not sealed never asks for a passphrase. Without a usable
// identity the store is locked: it lists addresses and revisions and returns no
// value, and why says why there is no identity (nil when there is one). A
// command that needs values gets the refusal of a store that is not read, unless
// allowLocked is set. A command that writes also needs a readers
// file that matches the state; one that reads only warns.
func openStateAs(ctx context.Context, root *config.Root, write, allowLocked bool) (st *state.Store, why *noKey, err error) {
	var openStore func(id *protect.Identity) (*state.Store, error)
	open := func(id *protect.Identity) (*state.Store, error) {
		st, err := openStore(id)
		if st != nil {
			// The end of a transaction that could not be signed is shown, not failed on.
			st.SetWarn(func(msg string) { out.Warning(msg) })
		}
		return st, err
	}
	openStore = func(id *protect.Identity) (*state.Store, error) {
		o := state.Options{Identity: id}
		switch {
		case root.State.Remote():
			b, err := remoteBackend(ctx, root)
			if err != nil {
				return nil, err
			}
			if write {
				if err := checkStoreConditions(ctx, root, b); err != nil {
					return nil, err
				}
			} else {
				b = &readOnlyBackend{Backend: b}
			}
			return state.OpenBackendWith(ctx, b, o)
		case write:
			return state.OpenWithContext(ctx, root.StateFile(), o)
		}
		path := root.StateFile()
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return state.Open(path) // says that the path is a file
		}
		return state.OpenBackendWith(ctx, &readOnlyBackend{Backend: &state.LocalBackend{Dir: path}}, o)
	}
	// A repository that lists readers expects a sealed state, and a state that does
	// not exist yet is not one. Whether it exists is looked at before the open, which
	// would make it, in format 1.
	missing := false
	if rf := loadReadersFile(root.Dir, flagAccessDir); rf.Found && stateMissing(ctx, root) {
		problem := missingStateProblem(rf)
		if write {
			return nil, nil, engine.WithCode(engine.ExitRefused, errors.New(problem))
		}
		fmt.Fprintln(os.Stderr, "Warning: "+problem)
		missing = true
	}
	if st, err = open(nil); err != nil {
		return nil, nil, err
	}
	if missing {
		return st, nil, nil // an empty state, read and not created
	}
	if !st.Locked() {
		return st, nil, checkReadersFile(root, st, write)
	}
	// The state is sealed.
	id, idErr := resolveIdentity(identityOptions{Name: flagIdentity, Prompt: identityPrompt, Readers: currentReaders(st.Protection())})
	if idErr == nil {
		keyed, err := open(id)
		if err != nil && !errors.Is(err, state.ErrNotReader) {
			return nil, nil, err
		}
		if err == nil {
			st = keyed
		}
	}
	if st.Locked() {
		why = &noKey{id: id, err: idErr}
		if !allowLocked {
			return nil, why, engine.WithCode(engine.ExitRefused, why.error(root))
		}
		if idErr != nil && !errors.Is(idErr, errNoIdentity) {
			fmt.Fprintln(os.Stderr, "Warning: "+idErr.Error())
		}
	}
	return st, why, checkReadersFile(root, st, write && !st.Locked())
}

// stateMissing reports whether the state of the root does not exist yet: its
// backend holds no meta.json. Nothing is created. A backend that cannot be read
// is not reported as missing; the open says why.
func stateMissing(ctx context.Context, root *config.Root) bool {
	var b state.Backend
	if root.State.Remote() {
		var err error
		if b, err = remoteBackend(ctx, root); err != nil {
			return false
		}
	} else {
		path := root.StateFile()
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return false // a file: the open says so
		}
		b = &state.LocalBackend{Dir: path}
	}
	_, err := b.Get(ctx, metaKey)
	return errors.Is(err, state.ErrNotFound)
}

// noKey says why a sealed state could not be opened: there is no identity, it
// cannot be opened, or it is not a reader.
type noKey struct {
	id  *protect.Identity
	err error
}

func (k *noKey) error(root *config.Root) error {
	return notReaderError(loadReadersFile(root.Dir, flagAccessDir), k.id, k.err)
}

// remoteBackend opens the bucket or the server that the root keeps its state on.
func remoteBackend(ctx context.Context, root *config.Root) (state.Backend, error) {
	return engine.RemoteBackend(ctx, root.State, environment())
}

// openStateReadOnly opens the state for a command that only looks at it. A
// state that does not exist yet is read as an empty one, and nothing is
// created: opening a store otherwise makes the directory and writes its
// metadata. A sealed state that cannot be opened with an identity is returned
// locked (see state.Store.Locked); commands that need values check for that.
func openStateReadOnly(ctx context.Context, root *config.Root) (*state.Store, error) {
	st, _, err := openStateReadOnlyWhy(ctx, root)
	return st, err
}

func openStateReadOnlyWhy(ctx context.Context, root *config.Root) (*state.Store, *noKey, error) {
	began := time.Now()
	st, why, err := openStateAs(ctx, root, false, true)
	if err == nil {
		plog.Debug(ctx, "state opened for reading", "backend", backendName(root.State.Backend), "location", st.Dir(), "nodes", st.Len(), "took", took(began))
	}
	return st, why, err
}

// openStateValues is openStateReadOnly for a command that prints values: a
// locked store is refused.
func openStateValues(ctx context.Context, root *config.Root) (*state.Store, error) {
	st, why, err := openStateReadOnlyWhy(ctx, root)
	if err != nil {
		return nil, err
	}
	if st.Locked() {
		return nil, engine.WithCode(engine.ExitRefused, why.error(root))
	}
	return st, nil
}

// readOnlyBackend refuses every write but one. The store writes its metadata
// when the location has none; that write is kept in memory, so that an empty
// location reads as an empty state.
type readOnlyBackend struct {
	state.Backend
	meta []byte
}

const metaKey = "meta.json"

func (b *readOnlyBackend) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := b.Backend.Get(ctx, key)
	if key == metaKey && errors.Is(err, state.ErrNotFound) && b.meta != nil {
		return b.meta, nil
	}
	return data, err
}

func (b *readOnlyBackend) Put(ctx context.Context, key string, data []byte) error {
	if key == metaKey {
		b.meta = data
		return nil
	}
	return b.refuse()
}

func (b *readOnlyBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	return false, b.refuse()
}

func (b *readOnlyBackend) Delete(ctx context.Context, key string) error { return b.refuse() }

func (b *readOnlyBackend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	return false, b.refuse()
}

func (b *readOnlyBackend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	return false, b.refuse()
}

func (b *readOnlyBackend) refuse() error {
	return fmt.Errorf("the state at %s is open for reading and is not changed", b.Name())
}

// parseRefresh maps the -refresh flag to a mode.
func parseRefresh(v string) (engine.RefreshMode, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "age":
		return engine.RefreshAge, nil
	case "false", "none", "no", "off":
		return engine.RefreshNone, nil
	case "true", "all", "force", "yes":
		return engine.RefreshAll, nil
	case "expired":
		return engine.RefreshExpired, nil
	}
	return 0, fmt.Errorf("invalid -refresh value %q: want age, expired, false or all", v)
}

// genPackageDir returns the import path and directory for a provider's
// generated CUE package.
func genPackage(src registry.Source) (importPath, pkgName string) {
	parts := []string{"pravo.dev", "providers"}
	switch {
	case src.OCI:
		parts = append(parts, "oci", strings.ReplaceAll(src.Host, ":", "_"))
	case src.Host != registry.DefaultHost:
		parts = append(parts, src.Host)
	}
	parts = append(parts, src.Namespace, src.Type)
	pkgName = strings.NewReplacer("-", "_", ".", "_").Replace(src.Type)
	switch pkgName {
	case "null", "true", "false", "package", "import", "for", "in", "if", "let":
		pkgName += "_"
	}
	if pkgName[0] >= '0' && pkgName[0] <= '9' {
		pkgName = "p" + pkgName
	}
	return strings.Join(parts, "/"), pkgName
}

// writeGenerated writes the generated CUE packages under cue.mod/gen: one
// per provider, holding every definition the provider offers, and
// pravo.dev/pravo with the settings schema. Specifications import them.
func (s *session) writeGenerated(verbose bool) error {
	root := s.root
	modDir, ok := root.ModuleDir()
	if !ok {
		if err := os.MkdirAll(modDir, 0o755); err != nil {
			return err
		}
		name := strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(filepath.Base(root.Dir)))
		content := fmt.Sprintf("module: %q\nlanguage: version: \"v0.14.0\"\n", "pravo.local/"+name)
		if err := os.WriteFile(filepath.Join(modDir, "module.cue"), []byte(content), 0o644); err != nil {
			return err
		}
		out.Printf("Created %s\n", filepath.Join("cue.mod", "module.cue"))
		root.ModRoot = root.Dir
	} else if root.SharedModule() && verbose {
		rel, _ := filepath.Rel(root.Dir, root.ModRoot)
		out.Printf("Using the module at %s\n", rel)
	}
	locals := make([]string, 0, len(s.schemas))
	for l := range s.schemas {
		locals = append(locals, l)
	}
	sortStrings(locals)
	for _, local := range locals {
		rp := s.providers.Resolved[local]
		ps := s.schemas[local]
		importPath, pkgName := genPackage(rp.Req.Source)
		opts := schema.GenOptions{Header: fmt.Sprintf("Provider %s v%s", rp.Req.Source.String(), rp.Version), Source: rp.Req.Source.String()}
		dir := filepath.Join(modDir, "gen", filepath.FromSlash(importPath))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		changed, err := writeIfChanged(filepath.Join(dir, pkgName+"_gen.cue"), schema.GenerateCUE(ps, pkgName, opts))
		if err != nil {
			return err
		}
		if verbose {
			verb := "Generated"
			if !changed {
				verb = "Up to date"
			}
			out.Printf("%s %s (%d resource types, %d data sources, %d ephemeral, %d functions)\n", verb, filepath.Join("cue.mod", "gen", filepath.FromSlash(importPath)), len(ps.SortedResourceNames()), len(ps.SortedDataSourceNames()), len(ps.SortedEphemeralNames()), len(ps.SortedFunctionNames()))
		}
	}
	pdir := filepath.Join(modDir, "gen", "pravo.dev", "pravo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		return err
	}
	_, err := writeIfChanged(filepath.Join(pdir, "settings_gen.cue"), []byte(schema.SettingsPackage))
	return err
}

// writeIfChanged writes data to path unless the file already has exactly that
// content, and reports whether it wrote.
func writeIfChanged(path string, data []byte) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	return true, os.WriteFile(path, data, 0o644)
}

func quoteLabel(s string) string {
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return fmt.Sprintf("%q", s)
	}
	return s
}

// warnStaleSchemas tells the user when a generated provider package was made
// from a different provider version than the one now selected: the
// definitions a specification is checked against would be out of date.
func (s *session) warnStaleSchemas() {
	modDir, ok := s.root.ModuleDir()
	if !ok {
		return
	}
	for local, rp := range s.providers.Resolved {
		if rp.Path == "(replay)" || rp.Version == "" {
			continue
		}
		importPath, pkgName := genPackage(rp.Req.Source)
		b, err := os.ReadFile(filepath.Join(modDir, "gen", filepath.FromSlash(importPath), pkgName+"_gen.cue"))
		if err != nil {
			continue
		}
		first, _, _ := strings.Cut(string(b), "\n")
		want := fmt.Sprintf("// Provider %s v%s", rp.Req.Source.String(), rp.Version)
		if first != want {
			out.Warning(fmt.Sprintf("the generated schema for provider %s was made from a different version than the selected v%s; run \"pravo init\" to refresh it", local, rp.Version))
		}
	}
}

func backendName(b string) string {
	if b == "" {
		return "local"
	}
	return b
}
