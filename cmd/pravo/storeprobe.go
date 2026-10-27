package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/state"
)

// s3ProbeAge is how long the verdict on a store holds before pravo asks again.
const s3ProbeAge = 30 * 24 * time.Hour

// checkStoreConditions makes sure that the store behind b honours the conditional
// requests that the locks rest on, before a command that writes the state uses
// it. A store that passed is remembered under the pravo home for s3ProbeAge,
// by endpoint and bucket or by URL, so that the check costs a few requests once.
// PRAVO_S3_PROBE=off skips it.
func checkStoreConditions(ctx context.Context, root *config.Root, b state.Backend) error {
	sb, ok := b.(state.Prober)
	if !ok || os.Getenv("PRAVO_S3_PROBE") == "off" {
		return nil
	}
	if fi, err := os.Stat(s3ProbeMark(root)); err == nil && time.Since(fi.ModTime()) < s3ProbeAge {
		return nil
	}
	began := time.Now()
	if err := sb.Probe(ctx); err != nil {
		var pe *state.ProbeError
		if errors.As(err, &pe) {
			return fmt.Errorf("%w\npravo takes its locks with conditional requests. Run \"pravo state probe\" to see which check fails; the reference page \"S3-compatible stores\" describes them.\nTo skip this check, set PRAVO_S3_PROBE=off", err)
		}
		return err
	}
	plog.Debug(ctx, "s3 store probed", "store", sb.Name(), "took", took(began))
	rememberS3Store(root)
	return nil
}

// s3ProbeMark is the file whose age is the age of the verdict on the store.
func s3ProbeMark(root *config.Root) string {
	id := root.State.Endpoint + "|" + root.State.Bucket
	if root.State.Backend == "http" {
		id = "http|" + root.State.URL
	}
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(registry.Home(environment()), "s3-probe", hex.EncodeToString(sum[:8]))
}

// rememberS3Store records that the store passed. A failure to write the file
// only means that the next command probes again.
func rememberS3Store(root *config.Root) {
	mark := s3ProbeMark(root)
	if err := os.MkdirAll(filepath.Dir(mark), 0o755); err == nil {
		_ = os.WriteFile(mark, nil, 0o644)
	}
}

// probeS3 runs the probe of the store that the root names, writes one line for
// each check and remembers a store that passes. It ignores the verdict that is
// remembered and PRAVO_S3_PROBE.
func probeS3(ctx context.Context, root *config.Root) error {
	if !root.State.Remote() {
		return errors.New("the state is not on a remote store: $settings.state.backend is neither \"s3\" nor \"http\"")
	}
	b, err := remoteBackend(ctx, root)
	if err != nil {
		return err
	}
	sb, ok := b.(state.Prober)
	if !ok {
		return errors.New("this state backend cannot be probed")
	}
	type result struct {
		Check string `json:"check"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	var results []result
	err = sb.ProbeSteps(ctx, func(check string, err error) {
		r := result{Check: check, OK: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
		if !flagJSON {
			status := "ok"
			if err != nil {
				status = "FAILED"
			}
			out.Printf("%-32s %s\n", check, status)
		}
	})
	if err == nil {
		rememberS3Store(root)
	}
	if flagJSON {
		if e := emitJSON(map[string]any{"store": sb.Name(), "ok": err == nil, "checks": results}); e != nil {
			return e
		}
		if err != nil {
			return errSilent
		}
		return nil
	}
	if err == nil {
		out.Printf("%s honours the conditional requests that the locks need.\n", sb.Name())
	}
	return err
}
