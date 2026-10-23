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

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
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
	root, err := config.Load(".", config.LoadOptions{Tags: flagTags, SysTags: flagSysTags})
	if err != nil {
		return nil, engine.WithCode(engine.ExitInvalid, err)
	}
	return root, nil
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
	s := &session{root: root, reg: registry.NewClient()}
	s.lock, err = engine.ReadLock(root.Dir)
	if err != nil {
		return nil, err
	}
	if opts.loadState {
		if opts.readOnlyState {
			s.state, err = openStateReadOnly(ctx, root)
		} else {
			s.state, err = openState(ctx, root)
		}
		if err != nil {
			return nil, err
		}
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
	s.providers.Log = func(msg string) { out.Println(msg) }
	if err := s.providers.Resolve(ctx, s.lock, opts.upgrade, opts.install); err != nil {
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
		s.schemas, err = s.providers.Schemas(ctx)
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
	if s != nil && s.providers != nil {
		s.providers.Close()
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
		opts.Command = "pravo " + strings.Join(os.Args[1:], " ")
	}
	opts.Actor, opts.Reason, opts.GitRevision = flagActor, flagReason, gitRevision(s.root.Dir)
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

func openStateStore(ctx context.Context, root *config.Root) (*state.Store, error) {
	switch root.State.Backend {
	case "s3":
		b, err := s3Backend(ctx, root)
		if err != nil {
			return nil, err
		}
		if err := checkS3Store(ctx, root, b); err != nil {
			return nil, err
		}
		return state.OpenBackend(ctx, b)
	default:
		return state.Open(root.StateFile())
	}
}

func s3Backend(ctx context.Context, root *config.Root) (state.Backend, error) {
	return state.NewS3Backend(ctx, state.S3Config{
		Bucket: root.State.Bucket, Prefix: root.State.Prefix, Region: root.State.Region, Endpoint: root.State.Endpoint,
		PathStyle: root.State.PathStyle, Profile: root.State.Profile, AccessKey: root.State.AccessKey, SecretKey: root.State.SecretKey,
		SessionToken: os.Getenv("AWS_SESSION_TOKEN"),
	})
}

// openStateReadOnly opens the state for a command that only looks at it. A
// state that does not exist yet is read as an empty one, and nothing is
// created: opening a store otherwise makes the directory and writes its
// metadata.
func openStateReadOnly(ctx context.Context, root *config.Root) (*state.Store, error) {
	var b state.Backend
	switch root.State.Backend {
	case "s3":
		var err error
		if b, err = s3Backend(ctx, root); err != nil {
			return nil, err
		}
	default:
		path := root.StateFile()
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return state.Open(path) // says that the path is a file
		}
		b = &state.LocalBackend{Dir: path}
	}
	began := time.Now()
	st, err := state.OpenBackend(ctx, &readOnlyBackend{Backend: b})
	if err == nil {
		plog.Debug(ctx, "state opened for reading", "backend", backendName(root.State.Backend), "location", st.Dir(), "nodes", st.Len(), "took", took(began))
	}
	return st, err
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
