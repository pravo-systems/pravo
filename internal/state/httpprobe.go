package state

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
)

// Prober is a backend that can test whether its store honours the conditional
// requests that the locks need: the S3 and http backends.
type Prober interface {
	Backend
	Probe(ctx context.Context) error
	ProbeSteps(ctx context.Context, report func(check string, err error)) error
	ProbeStrict(ctx context.Context, report func(check string, err error)) error
}

var (
	_ Prober = (*S3Backend)(nil)
	_ Prober = (*HTTPBackend)(nil)
)

// Probe is the check of ProbeChecks, made on scratch objects under probe/; see
// S3Backend.Probe.
func (b *HTTPBackend) Probe(ctx context.Context) error { return b.ProbeSteps(ctx, nil) }

// probePut writes a scratch object with the conditions given, as a lock does. A
// refusal of a request that was sent more than once counts as accepted when the
// object holds what was sent.
func (b *HTTPBackend) probePut(ctx context.Context, key, body, ifNone, ifMatch string) (int, error) {
	h := map[string]string{}
	if ifNone != "" {
		h["If-None-Match"] = ifNone
	}
	if ifMatch != "" {
		h["If-Match"] = ifMatch
	}
	r, err := b.do(ctx, http.MethodPut, b.objectURL(key), []byte(body), h)
	if err != nil {
		return 0, err
	}
	if r.status == http.StatusPreconditionFailed {
		landed, lerr := b.landed(ctx, r.sent, "put", key, []byte(body))
		switch {
		case lerr != nil:
			return 0, lerr
		case landed:
			return http.StatusNoContent, nil
		}
	}
	return r.status, nil
}

func (b *HTTPBackend) probeDelete(ctx context.Context, key, ifMatch string) (int, error) {
	r, err := b.do(ctx, http.MethodDelete, b.objectURL(key), nil, map[string]string{"If-Match": ifMatch})
	return r.status, err
}

func (b *HTTPBackend) probeCleanup(ctx context.Context, keys ...string) {
	for _, k := range keys {
		_ = b.Delete(context.WithoutCancel(ctx), k)
	}
}

// ProbeSteps checks, on scratch objects under probe/, that the server honours
// create only (If-None-Match: *), replace (If-Match on a put) and delete
// (If-Match on a delete), each with a tag that must be refused and one that must
// be accepted, and that eight concurrent creates of one key leave one winner. It
// returns a *ProbeError for a server that ignores or rejects a condition, and
// another error when the server cannot be reached. report is called with the name
// of each check from ProbeChecks as it ends.
func (b *HTTPBackend) ProbeSteps(ctx context.Context, report func(check string, err error)) error {
	id := newID(8)
	one, race := "probe/"+id, "probe/"+id+"-race"
	defer b.probeCleanup(ctx, one, race)
	fail := func(format string, a ...any) error {
		return &ProbeError{Store: b.name, Detail: fmt.Sprintf(format, a...)}
	}
	// accepted and refused sort a status into the answers a conditional request may
	// get; any other status is a fault of the connection or the credentials.
	verdict := func(header string, status int, err error) (accepted bool, e error) {
		switch {
		case err != nil:
			return false, fmt.Errorf("checking %s on %s: %w", header, b.name, err)
		case status/100 == 2:
			return true, nil
		case status == http.StatusPreconditionFailed:
			return false, nil
		case status == http.StatusNotImplemented:
			return false, fail("%s is not implemented", header)
		}
		return false, fmt.Errorf("checking %s on %s: %w", header, b.name, &httpStatusError{op: "probe", key: "probe", status: status})
	}
	put := func(header, key, body, ifNone, ifMatch string) (bool, error) {
		st, err := b.probePut(ctx, key, body, ifNone, ifMatch)
		return verdict(header, st, err)
	}
	read := func(key string) (string, string, error) {
		data, tag, err := b.getTagged(ctx, key)
		return string(data), tag, err
	}

	createOnly := func() error {
		ok, err := put("If-None-Match", one, "one", "*", "")
		if err != nil {
			return err
		}
		if !ok {
			return fail("a create of a new key was refused")
		}
		ok, err = put("If-None-Match", one, "two", "*", "")
		if err != nil {
			return err
		}
		if ok {
			return fail("a second create of the same key succeeded, so If-None-Match is ignored")
		}
		if got, _, err := read(one); err != nil || got != "one" {
			return fail("a refused create changed the object (it holds %q)", got)
		}
		return nil
	}
	replace := func() error {
		ok, err := put("If-Match", one, "two", "", staleTag)
		if err != nil {
			return err
		}
		if ok {
			return fail("a put with a stale entity tag succeeded, so If-Match is ignored on a put")
		}
		got, tag, err := read(one)
		if err != nil || got != "one" {
			return fail("a refused put changed the object (it holds %q)", got)
		}
		if ok, err = put("If-Match", one, "three", "", tag); err != nil {
			return err
		} else if !ok {
			return fail("a put with the current entity tag was refused")
		}
		return nil
	}
	remove := func() error {
		st, err := b.probeDelete(ctx, one, staleTag)
		ok, err := verdict("If-Match", st, err)
		if err != nil {
			return err
		}
		if ok {
			return fail("a delete with a stale entity tag succeeded, so If-Match is ignored on a delete")
		}
		got, tag, err := read(one)
		if err != nil || got != "three" {
			return fail("a refused delete changed the object (it holds %q)", got)
		}
		st, err = b.probeDelete(ctx, one, tag)
		if ok, err = verdict("If-Match", st, err); err != nil {
			return err
		} else if !ok {
			return fail("a delete with the current entity tag was refused")
		}
		if _, _, err := read(one); !errors.Is(err, ErrNotFound) {
			return fail("the object is still there after a delete with the current entity tag")
		}
		return nil
	}
	concurrent := func() error {
		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			won  int
			oops error
		)
		for i := range probeRacers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ok, err := put("If-None-Match", race, strconv.Itoa(i), "*", "")
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err != nil && oops == nil:
					oops = err
				case ok:
					won++
				}
			}(i)
		}
		wg.Wait()
		if oops != nil {
			return oops
		}
		if won != 1 {
			return fail("%d of %d concurrent creates of one key succeeded, want 1", won, probeRacers)
		}
		return nil
	}
	for i, run := range []func() error{createOnly, replace, remove, concurrent} {
		err := run()
		if report != nil {
			report(ProbeChecks[i], err)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ProbeStrict checks what the immutable records of a sealed state need beyond
// ProbeSteps: of eight concurrent replaces with one entity tag exactly one wins,
// and a key that has just been written is in the listing at once.
func (b *HTTPBackend) ProbeStrict(ctx context.Context, report func(check string, err error)) error {
	id := newID(8)
	swap, listed := "probe/"+id+"-swap", "probe/"+id+"-list"
	defer b.probeCleanup(ctx, swap, listed)
	fail := func(format string, a ...any) error {
		return &ProbeError{Store: b.name, Detail: fmt.Sprintf(format, a...)}
	}
	replaces := func() error {
		if st, err := b.probePut(ctx, swap, "start", "*", ""); err != nil || st/100 != 2 {
			return fmt.Errorf("checking concurrent replaces on %s: %w", b.name, errors.Join(err, statusErr(st)))
		}
		_, tag, err := b.getTagged(ctx, swap)
		if err != nil || tag == "" {
			return fmt.Errorf("reading the scratch object on %s: %v", b.name, err)
		}
		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			won  = -1
			wins int
			oops error
		)
		for i := range probeRacers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				st, err := b.probePut(ctx, swap, strconv.Itoa(i), "", tag)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err != nil && oops == nil:
					oops = err
				case st/100 == 2:
					wins++
					won = i
				case st != http.StatusPreconditionFailed && oops == nil:
					oops = statusErr(st)
				}
			}(i)
		}
		wg.Wait()
		if oops != nil {
			return fmt.Errorf("checking concurrent replaces on %s: %w", b.name, oops)
		}
		if wins != 1 {
			return fail("%d of %d concurrent replaces of one object succeeded, want 1", wins, probeRacers)
		}
		if data, _, err := b.getTagged(ctx, swap); err != nil || string(data) != strconv.Itoa(won) {
			return fail("the object does not hold what the one successful replace wrote")
		}
		return nil
	}
	listing := func() error {
		if st, err := b.probePut(ctx, listed, "listed", "", ""); err != nil || st/100 != 2 {
			return fmt.Errorf("checking the listing on %s: %w", b.name, errors.Join(err, statusErr(st)))
		}
		keys, err := b.List(ctx, "probe")
		if err != nil {
			return fmt.Errorf("checking the listing on %s: %w", b.name, err)
		}
		for _, k := range keys {
			if k == listed {
				return nil
			}
		}
		return fail("a listing right after a put did not show the new key")
	}
	for i, run := range []func() error{replaces, listing} {
		err := run()
		if report != nil {
			report(StrictChecks[i], err)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// statusErr is the error for a status that is not a success, nil for one that is.
func statusErr(status int) error {
	if status/100 == 2 || status == 0 {
		return nil
	}
	return &httpStatusError{op: "probe", key: "probe", status: status}
}
