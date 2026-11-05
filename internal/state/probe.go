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
	one, race := "probe/"+id, "probe/"+id+"-race"
	defer p.cleanup(ctx, one, race)
	name := p.storeName()
	fail := func(format string, a ...any) error {
		return &ProbeError{Store: name, Detail: fmt.Sprintf(format, a...)}
	}
	// verdict reads an answer: accepted or refused, or the error that the answer is.
	verdict := func(header string, a probeAnswer) (bool, error) {
		switch {
		case errors.Is(a.err, errProbeUnsupported):
			return false, fail("%s is not implemented", header)
		case a.err != nil:
			return false, fmt.Errorf("checking %s on %s: %w", header, name, a.err)
		}
		return a.accepted, nil
	}
	// refusedBy is the words a store gave for a refusal, to append to a message.
	refusedBy := func(a probeAnswer) string {
		if a.refusal == nil {
			return ""
		}
		return ": " + a.refusal.Error()
	}
	put := func(header, key, body, ifNone, ifMatch string) (bool, probeAnswer, error) {
		a := p.put(ctx, key, body, ifNone, ifMatch)
		ok, err := verdict(header, a)
		return ok, a, err
	}

	createOnly := func() error {
		ok, a, err := put("If-None-Match", one, "one", "*", "")
		if err != nil {
			return err
		}
		if !ok {
			return fail("a create of a new key was refused%s", refusedBy(a))
		}
		ok, _, err = put("If-None-Match", one, "two", "*", "")
		if err != nil {
			return err
		}
		if ok {
			return fail("a second create of the same key succeeded, so If-None-Match is ignored")
		}
		if got, _, err := p.get(ctx, one); err != nil || got != "one" {
			return fail("a refused create changed the object (it holds %q)", got)
		}
		return nil
	}
	replace := func() error {
		ok, _, err := put("If-Match", one, "two", "", staleTag)
		if err != nil {
			return err
		}
		if ok {
			return fail("a put with a stale entity tag succeeded, so If-Match is ignored on a put")
		}
		got, tag, err := p.get(ctx, one)
		if err != nil || got != "one" {
			return fail("a refused put changed the object (it holds %q)", got)
		}
		ok, a, err := put("If-Match", one, "three", "", tag)
		if err != nil {
			return err
		}
		if !ok {
			return fail("a put with the current entity tag was refused%s", refusedBy(a))
		}
		return nil
	}
	remove := func() error {
		ok, err := verdict("If-Match", p.del(ctx, one, staleTag))
		if err != nil {
			return err
		}
		if ok {
			return fail("a delete with a stale entity tag succeeded, so If-Match is ignored on a delete")
		}
		got, tag, err := p.get(ctx, one)
		if err != nil || got != "three" {
			return fail("a refused delete changed the object (it holds %q)", got)
		}
		a := p.del(ctx, one, tag)
		if ok, err = verdict("If-Match", a); err != nil {
			return err
		} else if !ok {
			return fail("a delete with the current entity tag was refused%s", refusedBy(a))
		}
		if _, _, err := p.get(ctx, one); !errors.Is(err, ErrNotFound) {
			return fail("the object is still there after a delete with the current entity tag")
		}
		return nil
	}
	concurrent := func() error {
		won, err := probeRace(p, "concurrent creates", func(i int) probeAnswer {
			return p.put(ctx, race, strconv.Itoa(i), "*", "")
		})
		if err != nil {
			return err
		}
		if len(won) != 1 {
			return fail("%d of %d concurrent creates of one key succeeded, want 1", len(won), probeRacers)
		}
		return nil
	}
	return probeReports(ProbeChecks, report, createOnly, replace, remove, concurrent)
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
