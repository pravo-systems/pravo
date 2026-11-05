package state

import (
	"bytes"
	"context"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

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
	return runProbe(ctx, s3Probe{b}, report)
}

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
	return runStrict(ctx, s3Probe{b}, report)
}

// s3Probe is the requests of the probes, made against a bucket.
type s3Probe struct{ b *S3Backend }

func (p s3Probe) storeName() string { return p.b.name }

// answer sorts the error of a request into the three answers a conditional request
// may get; anything else is a fault.
func (p s3Probe) answer(err error) probeAnswer {
	switch {
	case err == nil:
		return probeAnswer{accepted: true}
	case preconditionFailed(err):
		return probeAnswer{refusal: err}
	case codeOf(err) == "NotImplemented" || statusOf(err) == http.StatusNotImplemented:
		return probeAnswer{err: errProbeUnsupported}
	}
	return probeAnswer{err: err}
}

func (p s3Probe) put(ctx context.Context, key, body, ifNone, ifMatch string) probeAnswer {
	b := p.b
	in := &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader([]byte(body))}
	if ifNone != "" {
		in.IfNoneMatch = aws.String(ifNone)
	}
	if ifMatch != "" {
		in.IfMatch = aws.String(ifMatch)
	}
	tries, opt := b.counting()
	_, err := b.client.PutObject(ctx, in, opt)
	if err != nil && preconditionFailed(err) {
		// The store may have applied the request and lost its answer; the request
		// that was sent again was refused because the object is the one that was
		// written. It would be counted as a store that refuses every create. A
		// check that cannot tell is an error, not a verdict on the store.
		landed, lerr := b.landed(ctx, tries, "put", key, []byte(body))
		switch {
		case lerr != nil:
			return probeAnswer{err: lerr}
		case landed:
			return probeAnswer{accepted: true}
		}
	}
	return p.answer(err)
}

func (p s3Probe) del(ctx context.Context, key, ifMatch string) probeAnswer {
	b := p.b
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), IfMatch: aws.String(ifMatch)})
	return p.answer(err)
}

func (p s3Probe) get(ctx context.Context, key string) (string, string, error) {
	data, tag, err := p.b.getTagged(ctx, key)
	return string(data), aws.ToString(tag), err
}

func (p s3Probe) list(ctx context.Context, prefix string) ([]string, error) {
	return p.b.List(ctx, prefix)
}

func (p s3Probe) cleanup(ctx context.Context, keys ...string) {
	for _, k := range keys {
		_, _ = p.b.client.DeleteObject(context.WithoutCancel(ctx), &s3.DeleteObjectInput{Bucket: aws.String(p.b.bucket), Key: aws.String(p.b.key(k))})
	}
}
