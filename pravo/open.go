package pravo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// StateMode says whether Open opens the state of the specification, and how.
type StateMode int

const (
	// StateNone does not open the state.
	StateNone StateMode = iota
	// StateRead opens the state for reading: a location that holds none is read as
	// an empty state, and nothing is created or changed.
	StateRead
	// StateWrite opens the state for a run that changes it. A state that does not
	// exist yet is created.
	StateWrite
)

// Config says what Open does and how it reaches the outside.
type Config struct {
	// Dir is the directory of the specification. The default is ".".
	Dir string
	// Tags and SysTags are the values the specification is loaded with (-t, -T).
	Tags    []string
	SysTags bool
	// Env is the environment of the run: the variables the specification reads, the
	// credentials of a remote state, what the providers and the CUE module loader
	// are given. The environment of the process is not consulted; see OSEnv.
	Env Env
	// Root, when set, is used instead of loading the specification.
	Root *Root

	// Install allows downloading providers that are missing.
	Install bool
	// Upgrade ignores the lock file when choosing provider versions.
	Upgrade bool
	// Schemas fetches the schemas of the providers and unifies them into the
	// specification. Planning and applying need them.
	Schemas bool
	// State says whether the state is opened, and how.
	State StateMode
	// RelockModules accepts changed module content (pravo init).
	RelockModules bool

	// Actor and Reason are recorded on transactions, locks and plan files for
	// attribution. They are not verified.
	Actor, Reason string
	// Args are the arguments of the command that runs, which the state shows as the
	// command of a transaction. Empty means "pravo" alone.
	Args []string

	// OpenState opens the state. Nil opens it with OpenStore, and with Identity if
	// the state is sealed. A program with its own rules for who may open which state
	// (the readers of a repository, a vault) gives its own.
	OpenState func(ctx context.Context, root *Root, mode StateMode) (*Store, error)
	// Identity returns the key of this process for a sealed state. readers are the
	// reader keys with a lock in the current group of the state. It is called when a
	// state is found sealed, and by remote_outputs when it reads the state of another
	// root that is. It never has to prompt; a program that can only fail returns the
	// error.
	Identity func(readers []ReaderPublic) (*Identity, error)
	// Log receives the lines about installing providers. Nil drops them.
	Log func(msg string)
	// Progress receives the progress of a plan or an apply, as a printf format. Nil
	// drops it.
	Progress func(format string, args ...any)
	// Warn receives the warnings of the state. Nil drops them.
	Warn func(msg string)
}

// Workspace is a specification opened with its state and providers: what a
// command of pravo works on. Close it when done.
type Workspace struct {
	cfg       Config
	root      *config.Root
	reg       *registry.Client
	providers *engine.Providers
	lock      *engine.Lock
	schemas   map[string]*schema.ProviderSchema
	value     cue.Value
	state     *state.Store
}

// Open loads the specification, reads the lock file, opens the state, resolves
// the providers (installing them when Config.Install says so), brings the lock
// file up to date and, when Config.Schemas says so, fetches the schemas.
func Open(ctx context.Context, cfg Config) (*Workspace, error) {
	root := cfg.Root
	var err error
	began := time.Now()
	if root == nil {
		dir := cfg.Dir
		if dir == "" {
			dir = "."
		}
		lctx, end := telemetry.Span(ctx, "setup load specification")
		root, err = config.Load(dir, config.LoadOptions{Tags: cfg.Tags, SysTags: cfg.SysTags, Context: lctx, Env: cfg.Env})
		end(err)
		if err != nil {
			return nil, engine.WithCode(engine.ExitInvalid, err)
		}
	}
	loaded := []any{"dir", root.Dir, "managed", countMode(root, "managed"), "data", countMode(root, "data"),
		"ephemeral", countMode(root, "ephemeral"), "calls", countMode(root, "call"), "outputs", len(root.Outputs),
		"providers", len(root.Providers), "moves", len(root.Moves), "imports", len(root.Imports)}
	if root.Pruned {
		loaded = append(loaded, "pruned", true)
	}
	plog.Debug(ctx, "loaded", append(loaded, "took", took(began))...)
	w := &Workspace{cfg: cfg, root: root, reg: registry.NewClient(cfg.Env)}
	if err := w.open(ctx); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

func (w *Workspace) open(ctx context.Context) error {
	root, cfg := w.root, w.cfg
	var err error
	_, end := telemetry.Span(ctx, "setup read lock file")
	w.lock, err = engine.ReadLock(root.Dir)
	end(err)
	if err != nil {
		return err
	}
	if cfg.State != StateNone {
		octx, end := telemetry.Span(ctx, "setup open state", attribute.String("pravo.state.backend", BackendName(root.State.Backend)))
		began := time.Now()
		open := cfg.OpenState
		if open == nil {
			open = w.openState
		}
		w.state, err = open(octx, root, cfg.State)
		end(err)
		if err != nil {
			return err
		}
		plog.Debug(ctx, "state opened", "backend", BackendName(root.State.Backend), "location", w.state.Dir(), "nodes", w.state.Len(), "took", took(began))
		w.state.SetContext(ctx)
		// Providers that only exist in state must still be resolvable.
		for _, sr := range w.state.List() {
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
	w.providers = engine.NewProviders(root, w.reg)
	w.providers.Env = cfg.Env
	w.providers.Log = func(msg string) {
		if cfg.Log != nil {
			cfg.Log(msg)
		}
	}
	rctx, end := telemetry.Span(ctx, "setup resolve providers", attribute.Int("pravo.providers", len(root.Providers)))
	err = w.providers.Resolve(rctx, w.lock, cfg.Upgrade, cfg.Install)
	end(err)
	if err != nil {
		return err
	}
	before := lockString(w.lock)
	w.providers.UpdateLock(w.lock)
	var uses []engine.ModuleUse
	for _, m := range root.Modules {
		uses = append(uses, engine.ModuleUse{Path: m.Path, Version: m.Version, Hash: m.Hash})
	}
	if err := w.lock.UpdateModules(uses, cfg.RelockModules); err != nil {
		return engine.WithCode(engine.ExitRefused, err)
	}
	if lockString(w.lock) != before {
		plog.Debug(ctx, "writing the lock file", "file", engine.LockFileName)
		if err := WriteLock(w.lock, root.Dir); err != nil {
			return err
		}
	}
	if cfg.Schemas {
		began := time.Now()
		sctx, end := telemetry.Span(ctx, "setup load schemas", attribute.Int("pravo.providers", len(root.Providers)))
		w.schemas, err = w.providers.Schemas(sctx)
		end(err)
		if err != nil {
			return err
		}
		w.value = root.Value
		plog.Debug(ctx, "schemas ready", "providers", len(w.schemas), "took", took(began))
	}
	return nil
}

// openState is the default Config.OpenState: the store of the root, opened with
// the identity of Config.Identity when the state is sealed.
func (w *Workspace) openState(ctx context.Context, root *Root, mode StateMode) (*Store, error) {
	o := StoreOptions{Write: mode == StateWrite, Env: w.cfg.Env, Warn: w.cfg.Warn}
	st, err := OpenStore(ctx, root, o)
	if err != nil {
		return nil, err
	}
	if st.Locked() && w.cfg.Identity != nil {
		if id, err := w.cfg.Identity(CurrentReaders(st.Protection())); err == nil {
			o.Identity = id
			keyed, err := OpenStore(ctx, root, o)
			if err != nil && !errors.Is(err, state.ErrNotReader) {
				return nil, err
			}
			if err == nil {
				st = keyed
			}
		}
	}
	if st.Locked() && mode == StateWrite {
		return nil, engine.WithCode(engine.ExitRefused, errors.New("the state is sealed, and this process has no identity that is one of its readers"))
	}
	return st, nil
}

// Close stops the provider processes and closes the state.
func (w *Workspace) Close() {
	if w == nil {
		return
	}
	if w.providers != nil {
		w.providers.Close()
	}
	if w.state != nil {
		w.state.Close()
	}
}

// The parts of a workspace, for a program that uses the engine directly. They
// are internal types for now; see the package documentation.

// Root is the specification.
func (w *Workspace) Root() *Root { return w.root }

// State is the state, nil when Config.State was StateNone.
func (w *Workspace) State() *Store { return w.state }

// Providers manages the provider processes.
func (w *Workspace) Providers() *Providers { return w.providers }

// Lock is the lock file as Open left it.
func (w *Workspace) Lock() *Lock { return w.lock }

// Schemas are the schemas of the providers by local name, nil unless Config.Schemas was set.
func (w *Workspace) Schemas() map[string]*ProviderSchema { return w.schemas }

// Registry is the client of the provider registries and the local provider store.
func (w *Workspace) Registry() *registry.Client { return w.reg }

// Env is the environment the workspace was opened with.
func (w *Workspace) Env() Env { return w.cfg.Env }

// Engine returns an engine for the workspace. Options that are left empty are
// filled in: the progress function, the refresh age of the specification, the
// command line the state records, the environment, the actor, the reason, the git
// revision of the specification and the identity of Config.
func (w *Workspace) Engine(opts EngineOptions) *Engine {
	if opts.Progress == nil {
		opts.Progress = w.cfg.Progress
		if opts.Progress == nil {
			opts.Progress = func(string, ...any) {}
		}
	}
	if opts.DefaultRefresh == "" {
		opts.DefaultRefresh = w.root.Refresh
	}
	if opts.Command == "" {
		setCommand(w.state, w.cfg.Args, &opts)
	}
	if len(opts.Env.Environ()) == 0 {
		opts.Env = w.cfg.Env
	}
	if opts.Actor == "" {
		opts.Actor = w.cfg.Actor
	}
	if opts.Reason == "" {
		opts.Reason = w.cfg.Reason
	}
	if opts.GitRevision == "" {
		opts.GitRevision = GitRevision(w.root.Dir)
	}
	if opts.Identity == nil {
		opts.Identity = w.cfg.Identity
	}
	return engine.New(w.root, w.state, w.providers, w.schemas, w.value, opts)
}

// setCommand sets the command line of opts from the arguments of the process.
// What the history of the state and the lock files show of it depends on the
// state: a sealed state shows the values of -t and a few other flags as tags (see
// state.Store.ShowCommand). Args keeps the arguments, so that the plan files and
// the stored plans can keep the line as it was typed.
func setCommand(st *state.Store, args []string, opts *engine.Options) {
	opts.Args = args
	opts.Command = "pravo " + strings.Join(args, " ")
	if st != nil {
		opts.Command, _ = st.ShowCommand(args)
	}
}

// CurrentReaders lists the reader keys with a lock in the current group of a
// sealed state, which is public: meta.json holds it. It is nil for a state that
// is not sealed.
func CurrentReaders(p state.Protection) []ReaderPublic {
	if p.Sealed == nil {
		return nil
	}
	var keys []ReaderPublic
	for _, g := range p.Sealed.Groups {
		if g.ID != p.Sealed.Current {
			continue
		}
		for _, l := range g.Locks {
			if !l.Key.IsZero() && !p.Sealed.IsRecoveryLock(l) {
				keys = append(keys, l.Key)
			}
		}
	}
	return keys
}

// BackendName is the name of the state backend of the settings, "local" when they name none.
func BackendName(b string) string {
	if b == "" {
		return "local"
	}
	return b
}

// GitRevision returns the git revision of dir (with "-dirty" when the tree has
// changes), or "" when it is not in a repository.
func GitRevision(dir string) string {
	rev, err := exec.Command("git", "-C", dir, "rev-parse", "--short=12", "HEAD").Output()
	if err != nil {
		return ""
	}
	out := strings.TrimSpace(string(rev))
	if status, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); err == nil && len(strings.TrimSpace(string(status))) > 0 {
		out += "-dirty"
	}
	return out
}

// WriteLock writes the lock file in the layout that "pravo fmt" leaves it in,
// so that formatting it changes nothing.
func WriteLock(l *Lock, dir string) error {
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
	_, err = WriteIfChanged(path, formatted)
	return err
}

// WriteIfChanged writes data to path unless the file already has exactly that
// content, and reports whether it wrote.
func WriteIfChanged(path string, data []byte) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	return true, os.WriteFile(path, data, 0o644)
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

func countMode(root *config.Root, mode string) int {
	n := 0
	for _, r := range root.Resources {
		if r.Mode == mode {
			n++
		}
	}
	return n
}

// took is the time since began, to the millisecond, as the debug log writes it.
func took(began time.Time) time.Duration { return time.Since(began).Round(time.Millisecond) }
