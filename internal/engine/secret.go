package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/state"
)

// checkSecrets asks the state about every secret that a node reads, before a task starts, in the
// order of the addresses, and stops the run at the first thing it has to say: the state cannot
// keep secrets, has none of that name, or does not accept the version. The tasks would race to say
// so, and the message would name whichever of them lost; this names the first node by address, so
// the same specification gives the same message, and no provider has started when the run stops.
// A node whose name or version depends on something not known yet is left to its task.
func (e *Engine) checkSecrets(ctx context.Context, nodes map[string]*node) error {
	var asking []*config.Resource
	for _, n := range nodes {
		if r := n.res; r.Builtin && (r.Type == "secret" || r.Type == "secret_version") {
			asking = append(asking, r)
		}
	}
	sort.Slice(asking, func(i, j int) bool { return asking[i].Address() < asking[j].Address() })
	for _, r := range asking {
		e.cueMu.Lock()
		name, pinned, known := secretAsked(r, e.current.LookupPath(r.Path))
		e.cueMu.Unlock()
		if !known {
			continue
		}
		if _, err := e.secretHead(ctx, r, name, pinned); err != nil {
			return err
		}
	}
	return nil
}

// secretTask reads a version of a secret of the sealed state for a pravo.#Secret, which fills in
// its value, or a pravo.#SecretVersion, which fills in the number of the version. Like envTask it
// runs on every plan and apply and records nothing: the value stays in the process.
//
// A run reads one version of a secret. The first node that names a secret without a version
// decides which, and the others of the run follow, so that a value and the number that rotates it
// belong together even when someone sets the secret while the run is going.
func (e *Engine) secretTask(ctx context.Context, r *config.Resource, v cue.Value) error {
	e.cueMu.Lock()
	name, pinned, known := secretAsked(r, v)
	e.cueMu.Unlock()
	if !known {
		return nil // the name or the version depends on something not known yet
	}
	seq, err := e.secretHead(ctx, r, name, pinned)
	if err != nil {
		return err
	}
	plog.Debug(ctx, "secret", "addr", r.Address(), "name", name, "version", seq)
	if r.Type == "secret_version" {
		return e.fill(r, map[string]any{"version": seq})
	}
	sv, err := e.State.ReadSecret(ctx, name, seq, e.secretRules())
	if err != nil {
		return e.secretError(r, name, err)
	}
	defer clear(sv.Value)
	return e.fill(r, map[string]any{"value": string(sv.Value)})
}

// secretAsked is the name of the secret that a node reads and the version it asks for, 0 for the
// latest. Only a pravo.#Secret asks for one: the version of a pravo.#SecretVersion is what it
// reads. known is false when the name or the version depends on something not known yet. The
// caller holds cueMu.
func secretAsked(r *config.Resource, v cue.Value) (name string, version int, known bool) {
	name, err := v.LookupPath(cue.ParsePath("name")).String()
	if err != nil {
		return "", 0, false
	}
	if asked := v.LookupPath(cue.ParsePath("version")); r.Type == "secret" && asked.Exists() {
		n, err := asked.Int64()
		if err != nil {
			return "", 0, false
		}
		version = int(n)
	}
	return name, version, true
}

func (e *Engine) secretRules() state.RecordRules {
	return state.RecordRules{Readers: e.Opts.Readers, Vouched: e.Opts.Vouched, Strict: e.Opts.StrictKeys}
}

// secretHead is what the state has to say about the secret that a node reads: the number of the
// version that the node reads, judged, without opening the value.
func (e *Engine) secretHead(ctx context.Context, r *config.Resource, name string, pinned int) (int, error) {
	if e.State == nil {
		return 0, fmt.Errorf("%s: a run reads secrets from the state, and there is none", r.Address())
	}
	seq, err := e.secretVersion(ctx, name, pinned, e.secretRules())
	if err != nil {
		return 0, e.secretError(r, name, err)
	}
	return seq, nil
}

// secretVersion is the number of the version of the secret that the run reads: pinned when the
// node asks for one, else the latest, which the first node of the run settles. It judges the
// version, so a secret that the roles do not accept stops the run here.
func (e *Engine) secretVersion(ctx context.Context, name string, pinned int, rules state.RecordRules) (int, error) {
	if pinned > 0 {
		info, err := e.State.SecretHead(ctx, name, pinned, rules)
		return info.Seq, err
	}
	e.secretMu.Lock()
	defer e.secretMu.Unlock()
	if seq, ok := e.secretPins[name]; ok {
		return seq, nil
	}
	info, err := e.State.SecretHead(ctx, name, 0, rules)
	if err != nil {
		return 0, err
	}
	if e.secretPins == nil {
		e.secretPins = map[string]int{}
	}
	e.secretPins[name] = info.Seq
	return info.Seq, nil
}

// secretError says what is wrong with a secret that a node reads, and what to do about it.
func (e *Engine) secretError(r *config.Resource, name string, err error) error {
	addr := r.Address()
	switch {
	case errors.Is(err, state.ErrNoSecret):
		return fmt.Errorf("%s: the state has no secret named %s. Set it with: pravo secret set %s", addr, name, name)
	case errors.Is(err, state.ErrSecretRemoved):
		return fmt.Errorf("%s: the secret %s was removed. Set it again with: pravo secret set %s", addr, name, name)
	case errors.Is(err, state.ErrSecretPurged):
		return fmt.Errorf("%s: %s. Use a later version, or leave the version out", addr, err)
	case errors.Is(err, state.ErrSecretsNeedSeal):
		return WithCode(ExitRefused, fmt.Errorf("%s: %w", addr, err))
	case errors.Is(err, state.ErrUntrusted):
		return WithCode(ExitUntrusted, fmt.Errorf("%s: %w", addr, err))
	}
	return fmt.Errorf("%s: %w", addr, err)
}
