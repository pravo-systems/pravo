package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ProbeError says that the store does not honour a conditional request that
// the locks of the S3 backend rest on.
type ProbeError struct {
	Store  string
	Detail string
}

func (e *ProbeError) Error() string {
	return fmt.Sprintf("%s cannot hold a state that more than one command writes: %s", e.Store, e.Detail)
}

// staleTag is an entity tag that no object has.
const staleTag = `"00000000000000000000000000000000"`

// probeRacers is the number of concurrent creates of one key.
const probeRacers = 8

// ProbeChecks are the checks of Probe, in the order it runs them.
var ProbeChecks = []string{"create only (If-None-Match)", "replace (If-Match on a put)", "delete (If-Match on a delete)", "concurrent creates"}

// Probe checks, on scratch objects under the prefix of the store, that the
// store honours the three conditional requests that the locks need: create
// only (If-None-Match: *), replace (If-Match on a put) and delete (If-Match on
// a delete). Each condition is tried with a tag that must be refused and with
// one that must be accepted, and the contents are read back after a refused
// request. Eight concurrent creates of one key must leave one winner. It
// returns a *ProbeError for a store that ignores or rejects a condition, and
// another error when the store cannot be reached or written. The scratch
// objects are deleted at the end.
func (b *S3Backend) Probe(ctx context.Context) error { return b.ProbeSteps(ctx, nil) }

// ProbeSteps is Probe, and calls report with the name of each check from
// ProbeChecks and its outcome as the check ends. It stops at the first check
// that fails.
func (b *S3Backend) ProbeSteps(ctx context.Context, report func(check string, err error)) error {
	id := newID(8)
	one, race := "probe/"+id, "probe/"+id+"-race"
	defer func() {
		// A cleanup that fails leaves an object under probe/, which nothing reads.
		for _, k := range []string{one, race} {
			_, _ = b.client.DeleteObject(context.WithoutCancel(ctx), &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(k))})
		}
	}()
	fail := func(format string, a ...any) error {
		return &ProbeError{Store: b.name, Detail: fmt.Sprintf(format, a...)}
	}
	put := func(key, body, ifNone, ifMatch string) error {
		in := &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader([]byte(body))}
		if ifNone != "" {
			in.IfNoneMatch = aws.String(ifNone)
		}
		if ifMatch != "" {
			in.IfMatch = aws.String(ifMatch)
		}
		tries, opt := b.counting()
		_, err := b.client.PutObject(ctx, in, opt)
		if err != nil && preconditionFailed(err) && b.landed(ctx, tries, key, []byte(body)) {
			// The store applied the request and its answer was lost; the request that
			// was sent again was refused because the object is the one that was
			// written. It would be counted as a store that refuses every create.
			return nil
		}
		return err
	}
	del := func(key, ifMatch string) error {
		_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), IfMatch: aws.String(ifMatch)})
		return err
	}
	read := func(key string) (string, *string, error) {
		data, tag, err := b.getTagged(ctx, key)
		return string(data), tag, err
	}
	// A request must end in one of three ways: it succeeds, the store refuses
	// the condition, or the store does not implement it. Anything else is a
	// fault of the connection, the credentials or the bucket.
	unsupported := func(err error) bool {
		return codeOf(err) == "NotImplemented" || statusOf(err) == http.StatusNotImplemented
	}
	check := func(what, header string, err error) error {
		if err == nil || preconditionFailed(err) {
			return nil
		}
		if unsupported(err) {
			return fail("%s is not implemented (%s)", header, what)
		}
		return fmt.Errorf("checking %s on %s: %w", header, b.name, err)
	}

	createOnly := func() error {
		if err := put(one, "one", "*", ""); err != nil {
			if e := check("create", "If-None-Match", err); e != nil {
				return e
			}
			return fail("a create of a new key was refused: %v", err)
		}
		err := put(one, "two", "*", "")
		if err == nil {
			return fail("a second create of the same key succeeded, so If-None-Match is ignored")
		}
		if e := check("create", "If-None-Match", err); e != nil {
			return e
		}
		if got, _, err := read(one); err != nil || got != "one" {
			return fail("a refused create changed the object (it holds %q)", got)
		}
		return nil
	}
	replace := func() error {
		err := put(one, "two", "", staleTag)
		if err == nil {
			return fail("a put with a stale entity tag succeeded, so If-Match is ignored on a put")
		}
		if e := check("replace", "If-Match", err); e != nil {
			return e
		}
		if got, _, err := read(one); err != nil || got != "one" {
			return fail("a refused put changed the object (it holds %q)", got)
		}
		_, tag, err := read(one)
		if err != nil {
			return fmt.Errorf("reading the scratch object on %s: %w", b.name, err)
		}
		if err := put(one, "three", "", aws.ToString(tag)); err != nil {
			return fail("a put with the current entity tag was refused: %v", err)
		}
		return nil
	}
	remove := func() error {
		err := del(one, staleTag)
		if err == nil {
			return fail("a delete with a stale entity tag succeeded, so If-Match is ignored on a delete")
		}
		if e := check("delete", "If-Match", err); e != nil {
			return e
		}
		got, tag, err := read(one)
		if err != nil || got != "three" {
			return fail("a refused delete changed the object (it holds %q)", got)
		}
		if err := del(one, aws.ToString(tag)); err != nil {
			return fail("a delete with the current entity tag was refused: %v", err)
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
		for i := 0; i < probeRacers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				err := put(race, fmt.Sprint(i), "*", "")
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					won++
				case preconditionFailed(err):
				case oops == nil:
					oops = err
				}
			}(i)
		}
		wg.Wait()
		if oops != nil {
			return fmt.Errorf("checking concurrent creates on %s: %w", b.name, oops)
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

// StrictChecks are the checks of ProbeStrict, in the order it runs them.
var StrictChecks = []string{"concurrent replaces", "listing after a put"}

// ProbeStrict checks what the immutable records of a sealed state need beyond
// Probe. Eight writers replace one object at once, each with the entity tag
// they all read, and exactly one must win: a pointer to a record is replaced
// only by compare-and-swap. A key that has just been written must be in the
// listing of its prefix at once. It returns a *ProbeError for a store that
// fails a check, and another error when the store cannot be reached or written.
// It calls report, if it is not nil, with the name of each check from
// StrictChecks and its outcome, and stops at the first that fails. The scratch
// objects are deleted at the end.
func (b *S3Backend) ProbeStrict(ctx context.Context, report func(check string, err error)) error {
	id := newID(8)
	swap, listed := "probe/"+id+"-swap", "probe/"+id+"-list"
	defer func() {
		for _, k := range []string{swap, listed} {
			_, _ = b.client.DeleteObject(context.WithoutCancel(ctx), &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(k))})
		}
	}()
	fail := func(format string, a ...any) error {
		return &ProbeError{Store: b.name, Detail: fmt.Sprintf(format, a...)}
	}
	put := func(key, body, ifNone, ifMatch string) error {
		in := &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader([]byte(body))}
		if ifNone != "" {
			in.IfNoneMatch = aws.String(ifNone)
		}
		if ifMatch != "" {
			in.IfMatch = aws.String(ifMatch)
		}
		tries, opt := b.counting()
		_, err := b.client.PutObject(ctx, in, opt)
		if err != nil && preconditionFailed(err) && b.landed(ctx, tries, key, []byte(body)) {
			return nil // see Probe: the answer to a write that was applied was lost
		}
		return err
	}
	replaces := func() error {
		if err := put(swap, "start", "*", ""); err != nil {
			return fmt.Errorf("checking concurrent replaces on %s: %w", b.name, err)
		}
		_, tag, err := b.getTagged(ctx, swap)
		if err != nil || tag == nil {
			return fmt.Errorf("reading the scratch object on %s: %v", b.name, err)
		}
		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			won  = -1
			wins int
			oops error
		)
		for i := 0; i < probeRacers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				err := put(swap, fmt.Sprint(i), "", aws.ToString(tag))
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					wins++
					won = i
				case preconditionFailed(err):
				case oops == nil:
					oops = err
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
		if data, _, err := b.getTagged(ctx, swap); err != nil || string(data) != fmt.Sprint(won) {
			return fail("the object does not hold what the one successful replace wrote")
		}
		return nil
	}
	listing := func() error {
		if err := put(listed, "listed", "", ""); err != nil {
			return fmt.Errorf("checking the listing on %s: %w", b.name, err)
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
