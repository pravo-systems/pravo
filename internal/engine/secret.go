package engine

import (
	"context"
	"errors"
	"fmt"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/state"
)

// secretTask reads a version of a secret of the sealed state for a pravo.#Secret, which fills in
// its value, or a pravo.#SecretVersion, which fills in the number of the version. Like envTask it
// runs on every plan and apply and records nothing: the value stays in the process.
//
// A run reads one version of a secret. The first node that names a secret without a version
// decides which, and the others of the run follow, so that a value and the number that rotates it
// belong together even when someone sets the secret while the run is going.
func (e *Engine) secretTask(ctx context.Context, r *config.Resource, v cue.Value) error {
	e.cueMu.Lock()
	name, err := v.LookupPath(cue.ParsePath("name")).String()
	asked := v.LookupPath(cue.ParsePath("version"))
	pinned, perr := 0, error(nil)
	if r.Type == "secret" && asked.Exists() {
		var n int64
		if n, perr = asked.Int64(); perr == nil {
			pinned = int(n)
		}
	}
	e.cueMu.Unlock()
	if err != nil || perr != nil {
		return nil // the name or the version depends on something not known yet
	}
	if e.State == nil {
		return fmt.Errorf("%s: a run reads secrets from the state, and there is none", r.Address())
	}
	rules := state.RecordRules{Readers: e.Opts.Readers, Vouched: e.Opts.Vouched, Strict: e.Opts.StrictKeys}
	seq, err := e.secretVersion(ctx, name, pinned, rules)
	if err != nil {
		return e.secretError(r, name, err)
	}
	plog.Debug(ctx, "secret", "addr", r.Address(), "name", name, "version", seq)
	if r.Type == "secret_version" {
		return e.fill(r, map[string]any{"version": seq})
	}
	sv, err := e.State.ReadSecret(ctx, name, seq, rules)
	if err != nil {
		return e.secretError(r, name, err)
	}
	defer clear(sv.Value)
	return e.fill(r, map[string]any{"value": string(sv.Value)})
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
