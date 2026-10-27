package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"go.opentelemetry.io/otel/attribute"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/telemetry"
	"github.com/pravo-systems/pravo/pravo"
)

// session bundles everything a command needs after loading the root. It is a
// pravo.Workspace with the parts of it that the commands use by name.
type session struct {
	ws        *pravo.Workspace
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
	root, err := config.Load(".", config.LoadOptions{Tags: flags.Tags, SysTags: flags.SysTags, Context: ctx, Env: environment()})
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
	mode := pravo.StateNone
	switch {
	case opts.loadState && opts.readOnlyState:
		mode = pravo.StateRead
	case opts.loadState:
		mode = pravo.StateWrite
	}
	ws, err := pravo.Open(ctx, pravo.Config{
		Tags: flags.Tags, SysTags: flags.SysTags, Env: environment(), Root: opts.root,
		Install: opts.install, Upgrade: opts.upgrade, Schemas: opts.schemas, State: mode, RelockModules: opts.relockModules,
		Actor: flags.Actor, Reason: flags.Reason, Args: os.Args[1:],
		OpenState: func(ctx context.Context, root *config.Root, mode pravo.StateMode) (*state.Store, error) {
			if mode == pravo.StateRead {
				return openStateValues(ctx, root)
			}
			return openState(ctx, root)
		},
		// Looked for only when remote_outputs meets a sealed state, so that a command
		// that never needs a key never asks for a passphrase.
		// The readers of the other state are given: with several identity files and
		// none named like the user, the one whose reader key has a lock in it is
		// chosen, as it is for the state of this root.
		Identity: func(readers []protect.ReaderPublic) (*protect.Identity, error) {
			return resolveIdentity(identityOptions{Name: flags.Identity, Prompt: identityPrompt, Readers: readers})
		},
		Log:      func(msg string) { out.Println(msg) },
		Progress: func(format string, args ...any) { out.Printf(format+"\n", args...) },
	})
	if err != nil {
		return nil, err
	}
	s := &session{ws: ws, root: ws.Root(), reg: ws.Registry(), providers: ws.Providers(), lock: ws.Lock(), schemas: ws.Schemas(), state: ws.State()}
	if opts.schemas {
		s.value = s.root.Value
	}
	if !opts.regenerates {
		s.warnStaleSchemas()
	}
	return s, nil
}

// took is the time since began, to the millisecond, as the debug log writes it.
func took(began time.Time) time.Duration { return time.Since(began).Round(time.Millisecond) }

func (s *session) close() {
	if s == nil {
		return
	}
	s.ws.Close()
}

func (s *session) engine(opts engine.Options) *engine.Engine {
	opts.Actor, opts.Reason = flags.Actor, flags.Reason
	opts.Readers = loadReadersFile(s.root.Dir, flags.AccessDir).Readers
	if flags.JSON && streamEvents && opts.Event == nil {
		// Only commands that change things stream events; plan-like
		// commands print a single document.
		opts.Event = func(ev engine.Event) { emitLine(ev) }
	}
	return s.ws.Engine(opts)
}

// openState opens the configured state backend.
func openState(ctx context.Context, root *config.Root) (*state.Store, error) {
	return openStateStore(ctx, root)
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
	open := func(id *protect.Identity) (*state.Store, error) {
		// The end of a transaction that could not be signed is shown, not failed on.
		return pravo.OpenStore(ctx, root, pravo.StoreOptions{
			Write: write, Identity: id, Env: environment(),
			CheckConditions: func(ctx context.Context, b state.Backend) error { return checkStoreConditions(ctx, root, b) },
			Warn:            func(msg string) { out.Warning(msg) },
		})
	}
	// A repository that lists readers expects a sealed state, and a state that does
	// not exist yet is not one. Whether it exists is looked at before the open, which
	// would make it, in format 1.
	missing := false
	if rf := loadReadersFile(root.Dir, flags.AccessDir); rf.Found && stateMissing(ctx, root) {
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
	id, idErr := resolveIdentity(identityOptions{Name: flags.Identity, Prompt: identityPrompt, Readers: pravo.CurrentReaders(st.Protection())})
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
	return notReaderError(loadReadersFile(root.Dir, flags.AccessDir), k.id, k.err)
}

// remoteBackend opens the bucket or the server that the root keeps its state on.
func remoteBackend(ctx context.Context, root *config.Root) (state.Backend, error) {
	return pravo.RemoteBackend(ctx, root, environment())
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
		plog.Debug(ctx, "state opened for reading", "backend", pravo.BackendName(root.State.Backend), "location", st.Dir(), "nodes", st.Len(), "took", took(began))
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

const metaKey = "meta.json"

// parseRefresh maps the -refresh flag to a mode.
func parseRefresh(v string) (engine.RefreshMode, error) { return pravo.ParseRefresh(v) }

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
		changed, err := pravo.WriteIfChanged(filepath.Join(dir, pkgName+"_gen.cue"), schema.GenerateCUE(ps, pkgName, opts))
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
	_, err := pravo.WriteIfChanged(filepath.Join(pdir, "settings_gen.cue"), []byte(schema.SettingsPackage))
	return err
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
