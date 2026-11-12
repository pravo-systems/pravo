package main

import (
	"fmt"
	"runtime/debug"
	"strconv"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/filelock"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// runSet holds the variables the flags set for the running command (-vv sets
// PRAVO_LOG). Nothing below cmd/pravo reads the environment of the process; the
// commands hand the value of environment() down.
var runSet = map[string]string{}

// environment returns the environment of the running command: the one of the
// process, with what the flags set. It is read where a command is set up and
// handed down, never by the packages below.
func environment() env.Env {
	e := env.OS()
	for k, v := range runSet {
		e = e.With(k, v)
	}
	return e
}

// setEnvironment records the variables of e that differ from the process, for
// environment() to return.
func setEnvironment(e env.Env) {
	runSet = map[string]string{}
	if v := e.Get("PRAVO_LOG"); v != env.OS().Get("PRAVO_LOG") {
		runSet["PRAVO_LOG"] = v
	}
	if v := e.Get(registry.MirrorVar); v != env.OS().Get(registry.MirrorVar) {
		runSet[registry.MirrorVar] = v
	}
}

// colourOff reports whether the output is not to be coloured: a flag or NO_COLOR.
func colourOff(flag bool) bool { return flag || environment().Get("NO_COLOR") != "" }

// applyProcessSettings hands the settings that are process-wide to the packages
// that keep them: telemetry is configured by the environment, the ssh-agent is
// found by it, the lock files are kept in the cache directory of the user, and
// the garbage collector is set (applyCollector). They are reached from places
// that carry no environment of their own.
func applyProcessSettings(e env.Env) {
	telemetry.Configure(e)
	protect.SetAgentEnv(e)
	if d, ok := e.UserCacheDir(); ok {
		filelock.SetDir(d)
	}
	applyCollector(e)
}

// collectorPercent is the GOGC that a command runs with when the environment
// sets none: the collector starts when the heap has grown to five times what
// was live after the last collection, where Go's own default is twice. A
// command evaluates a specification once and ends. Most of what it allocates
// is live until it does, and with the default about four tenths of the time of
// a plan of thousands of resources went to collections that freed little.
const collectorPercent = 400

// applyCollector sets collectorPercent unless GOGC is set, which Go's runtime
// has read by now and which wins.
func applyCollector(e env.Env) {
	if e.Get("GOGC") == "" {
		debug.SetGCPercent(collectorPercent)
	}
}

// maxParallelism is the most PRAVO_PARALLELISM may ask for: past it the providers
// and the backend, not pravo, are the limit.
const maxParallelism = 256

// applyParallelism sets how many resources a command works on at once from
// PRAVO_PARALLELISM. Unset or empty keeps the default.
func applyParallelism(e env.Env) error {
	v := e.Get("PRAVO_PARALLELISM")
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxParallelism {
		return fmt.Errorf("PRAVO_PARALLELISM=%q: want a whole number from 1 to %d", v, maxParallelism)
	}
	engine.Parallelism = n
	return nil
}

// applyDependencies sets where the dependencies between resources are read from.
// PRAVO_DEPENDENCIES=syntax reads them from the syntax of the specification
// alone; unset, empty or "evaluator" also reads CUE's evaluator, which knows the
// element that a loop over a collection of resources reads.
func applyDependencies(e env.Env) error {
	switch v := e.Get("PRAVO_DEPENDENCIES"); v {
	case "", "evaluator":
		engine.DependenciesFromSyntax = false
	case "syntax":
		engine.DependenciesFromSyntax = true
	default:
		return fmt.Errorf("PRAVO_DEPENDENCIES=%q: want syntax or evaluator", v)
	}
	return nil
}
