package state

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// ProbeError says that the store does not honour a conditional request that
// the locks of the S3 or http backend rest on.
type ProbeError struct {
	Store  string
	Detail string
}

func (e *ProbeError) Error() string {
	return fmt.Sprintf("%s cannot hold a state that more than one command writes: %s", e.Store, e.Detail)
}

// staleTag is an entity tag that no object has.
const staleTag = `"00000000000000000000000000000000"`

// probeRacers is the number of concurrent writes of one key.
const probeRacers = 8

// ProbeChecks are the checks of Probe, in the order it runs them.
var ProbeChecks = []string{"create only (If-None-Match)", "replace (If-Match on a put)", "delete (If-Match on a delete)", "concurrent creates"}

// StrictChecks are the checks of ProbeStrict, in the order it runs them.
var StrictChecks = []string{"concurrent replaces", "listing after a put"}

// errProbeUnsupported is what a transport returns for a conditional request that the
// store answers as not implemented.
var errProbeUnsupported = errors.New("not implemented")

// probeAnswer is how a store answered a conditional request. A request ends in one of
// three ways: it is accepted, the store refuses the condition, or the store does not
// implement it (err is errProbeUnsupported). Any other error is a fault of the
// connection, the credentials or the bucket.
type probeAnswer struct {
	accepted bool
	// refusal is the store's own words for a refused condition, when it gave any.
	refusal error
	err     error
}

// probeIO is what the probes need of a store: the requests they make, in the
// terms of conditions and tags. The S3 and http backends implement it, and the
// checks themselves are written once, in runProbe and runStrict.
type probeIO interface {
	storeName() string
	// put writes a scratch object under the conditions given. A refusal of a
	// request that was sent more than once counts as accepted when the object holds
	// what was sent.
	put(ctx context.Context, key, body, ifNone, ifMatch string) probeAnswer
	del(ctx context.Context, key, ifMatch string) probeAnswer
	// get reads an object and its entity tag; a missing object is ErrNotFound.
	get(ctx context.Context, key string) (body, tag string, err error)
	list(ctx context.Context, prefix string) ([]string, error)
	// cleanup deletes scratch objects, ignoring failures: an object that stays under
	// probe/ is read by nothing.
	cleanup(ctx context.Context, keys ...string)
}

// probeReports runs the checks in order, reporting each outcome, and stops at the
// first that fails.
func probeReports(names []string, report func(check string, err error), checks ...func() error) error {
	for i, run := range checks {
		err := run()
		if report != nil {
			report(names[i], err)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runProbe is the check of ProbeChecks, made on scratch objects under probe/: the
// store honours create only (If-None-Match: *), replace (If-Match on a put) and
// delete (If-Match on a delete), each with a tag that must be refused and one that
// must be accepted, and the contents are read back after a refused request. Eight
// concurrent creates of one key must leave one winner.
func runProbe(ctx context.Context, p probeIO, report func(check string, err error)) error {
	id := newID(8)
	r := &probeRun{ctx: ctx, p: p, name: p.storeName(), one: "probe/" + id, race: "probe/" + id + "-race"}
	defer p.cleanup(ctx, r.one, r.race)
	return probeReports(ProbeChecks, report, r.createOnly, r.replace, r.remove, r.concurrent)
}

// probeRun is one run of the checks of ProbeChecks: the store, and the two scratch
// keys it writes.
type probeRun struct {
	ctx       context.Context
	p         probeIO
	name      string
	one, race string
}

func (r *probeRun) fail(format string, a ...any) error {
	return &ProbeError{Store: r.name, Detail: fmt.Sprintf(format, a...)}
}

// verdict reads an answer: accepted or refused, or the error that the answer is.
func (r *probeRun) verdict(header string, a probeAnswer) (bool, error) {
	switch {
	case errors.Is(a.err, errProbeUnsupported):
		return false, r.fail("%s is not implemented", header)
	case a.err != nil:
		return false, fmt.Errorf("checking %s on %s: %w", header, r.name, a.err)
	}
	return a.accepted, nil
}

// refusedBy is the words a store gave for a refusal, to append to a message.
func refusedBy(a probeAnswer) string {
	if a.refusal == nil {
		return ""
	}
	return ": " + a.refusal.Error()
}

func (r *probeRun) put(header, key, body, ifNone, ifMatch string) (bool, probeAnswer, error) {
	a := r.p.put(r.ctx, key, body, ifNone, ifMatch)
	ok, err := r.verdict(header, a)
	return ok, a, err
}

func (r *probeRun) createOnly() error {
	ok, a, err := r.put("If-None-Match", r.one, "one", "*", "")
	if err != nil {
		return err
	}
	if !ok {
		return r.fail("a create of a new key was refused%s", refusedBy(a))
	}
	ok, _, err = r.put("If-None-Match", r.one, "two", "*", "")
	if err != nil {
		return err
	}
	if ok {
		return r.fail("a second create of the same key succeeded, so If-None-Match is ignored")
	}
	if got, _, err := r.p.get(r.ctx, r.one); err != nil || got != "one" {
		return r.fail("a refused create changed the object (it holds %q)", got)
	}
	return nil
}

func (r *probeRun) replace() error {
	ok, _, err := r.put("If-Match", r.one, "two", "", staleTag)
	if err != nil {
		return err
	}
	if ok {
		return r.fail("a put with a stale entity tag succeeded, so If-Match is ignored on a put")
	}
	got, tag, err := r.p.get(r.ctx, r.one)
	if err != nil || got != "one" {
		return r.fail("a refused put changed the object (it holds %q)", got)
	}
	ok, a, err := r.put("If-Match", r.one, "three", "", tag)
	if err != nil {
		return err
	}
	if !ok {
		return r.fail("a put with the current entity tag was refused%s", refusedBy(a))
	}
	return nil
}

func (r *probeRun) remove() error {
	ok, err := r.verdict("If-Match", r.p.del(r.ctx, r.one, staleTag))
	if err != nil {
		return err
	}
	if ok {
		return r.fail("a delete with a stale entity tag succeeded, so If-Match is ignored on a delete")
	}
	got, tag, err := r.p.get(r.ctx, r.one)
	if err != nil || got != "three" {
		return r.fail("a refused delete changed the object (it holds %q)", got)
	}
	a := r.p.del(r.ctx, r.one, tag)
	if ok, err = r.verdict("If-Match", a); err != nil {
		return err
	} else if !ok {
		return r.fail("a delete with the current entity tag was refused%s", refusedBy(a))
	}
	if _, _, err := r.p.get(r.ctx, r.one); !errors.Is(err, ErrNotFound) {
		return r.fail("the object is still there after a delete with the current entity tag")
	}
	return nil
}

func (r *probeRun) concurrent() error {
	won, err := probeRace(r.p, "concurrent creates", func(i int) probeAnswer {
		return r.p.put(r.ctx, r.race, strconv.Itoa(i), "*", "")
	})
	if err != nil {
		return err
	}
	if len(won) != 1 {
		return r.fail("%d of %d concurrent creates of one key succeeded, want 1", len(won), probeRacers)
	}
	return nil
}

// probeRace makes probeRacers requests at once and returns which of them were
// accepted. A fault of any is an error; a refusal is the answer of a store that
// holds the condition.
func probeRace(p probeIO, what string, request func(i int) probeAnswer) (accepted []int, _ error) {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		oops error
	)
	for i := range probeRacers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := request(i)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case a.err != nil:
				if oops == nil {
					oops = a.err
				}
			case a.accepted:
				accepted = append(accepted, i)
			}
		}(i)
	}
	wg.Wait()
	if oops != nil {
		return nil, fmt.Errorf("checking %s on %s: %w", what, p.storeName(), oops)
	}
	return accepted, nil
}

// runStrict is the check of StrictChecks, what the immutable records of a sealed
// state need beyond runProbe: eight writers replace one object at once, each with
// the entity tag they all read, and exactly one must win (a pointer to a record is
// replaced only by compare-and-swap); a key that has just been written must be in
// the listing of its prefix at once.
func runStrict(ctx context.Context, p probeIO, report func(check string, err error)) error {
	id := newID(8)
	swap, listed := "probe/"+id+"-swap", "probe/"+id+"-list"
	defer p.cleanup(ctx, swap, listed)
	name := p.storeName()
	fail := func(format string, a ...any) error {
		return &ProbeError{Store: name, Detail: fmt.Sprintf(format, a...)}
	}
	replaces := func() error {
		if a := p.put(ctx, swap, "start", "*", ""); a.err != nil || !a.accepted {
			return fmt.Errorf("checking concurrent replaces on %s: %w", name, errors.Join(a.err, a.refusal, errors.New("the create was refused")))
		}
		_, tag, err := p.get(ctx, swap)
		if err != nil || tag == "" {
			return fmt.Errorf("reading the scratch object on %s: %v", name, err)
		}
		won, err := probeRace(p, "concurrent replaces", func(i int) probeAnswer {
			return p.put(ctx, swap, strconv.Itoa(i), "", tag)
		})
		if err != nil {
			return err
		}
		if len(won) != 1 {
			return fail("%d of %d concurrent replaces of one object succeeded, want 1", len(won), probeRacers)
		}
		if data, _, err := p.get(ctx, swap); err != nil || data != strconv.Itoa(won[0]) {
			return fail("the object does not hold what the one successful replace wrote")
		}
		return nil
	}
	listing := func() error {
		if a := p.put(ctx, listed, "listed", "", ""); a.err != nil || !a.accepted {
			return fmt.Errorf("checking the listing on %s: %w", name, errors.Join(a.err, a.refusal, errors.New("the write was refused")))
		}
		keys, err := p.list(ctx, "probe")
		if err != nil {
			return fmt.Errorf("checking the listing on %s: %w", name, err)
		}
		for _, k := range keys {
			if k == listed {
				return nil
			}
		}
		return fail("a listing right after a put did not show the new key")
	}
	return probeReports(StrictChecks, report, replaces, listing)
}
