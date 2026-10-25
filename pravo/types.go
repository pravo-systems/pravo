package pravo

import (
	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// Types of the engine, under the names this package gives them. See the package
// documentation: they are aliases for now.
type (
	// Env is the environment a run sees. See NewEnv.
	Env = env.Env
	// Root is a specification as loaded: its resources, providers, outputs, state settings.
	Root = config.Root
	// Store is the state.
	Store = state.Store
	// Backend is where a state is kept.
	Backend = state.Backend
	// Engine plans and applies.
	Engine = engine.Engine
	// EngineOptions are the options of an Engine.
	EngineOptions = engine.Options
	// Plan is what an Engine computed: the changes to make.
	Plan = engine.Plan
	// ApplyResult is what an apply did.
	ApplyResult = engine.ApplyResult
	// Event is a step of a run, for a program that follows it.
	Event = engine.Event
	// View is the document of a plan, the one "pravo plan --json" prints.
	View = engine.View
	// Lock is the lock file: the providers and modules the specification was resolved to.
	Lock = engine.Lock
	// Providers manages the provider processes of a run.
	Providers = engine.Providers
	// ProviderSchema is the schema a provider declares.
	ProviderSchema = schema.ProviderSchema
	// Identity is the key of a reader of a sealed state.
	Identity = protect.Identity
	// ReaderPublic is the public part of the key of a reader of a sealed state.
	ReaderPublic = protect.ReaderPublic
)

// NewEnv returns the environment of the given NAME=value entries, as
// os.Environ lists them.
func NewEnv(environ []string) Env { return env.New(environ) }

// EnvMap returns the environment of a map of names to values.
func EnvMap(vars map[string]string) Env { return env.Map(vars) }

// OSEnv is the environment of this process. A program that wants the run to see
// the environment it runs in says so with it; this package never reads it on its
// own.
func OSEnv() Env { return env.OS() }
