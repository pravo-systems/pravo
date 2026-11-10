package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/pravo-systems/pravo/internal/plugin/netcap"
)

// S3Config describes an S3 or S3-compatible bucket holding a state graph.
type S3Config struct {
	Bucket string
	// Prefix is the key prefix under which the graph lives ("" for the root).
	Prefix string
	Region string
	// Endpoint overrides the service URL for S3-compatible stores
	// (MinIO, Ceph RGW, R2, Garage, ...).
	Endpoint string
	// PathStyle forces path-style addressing (bucket in the path rather than
	// the host), which most compatible stores need.
	PathStyle bool
	// NoSnapshot keeps the state from being read through a snapshot.
	NoSnapshot bool
	// Profile selects a shared-configuration profile; credentials otherwise
	// come from the usual chain (environment, shared files, instance role).
	Profile string
	// AccessKey and SecretKey, when both set, are used directly.
	AccessKey, SecretKey, SessionToken string
}

// idleConns is the number of idle connections the transport keeps per host
// and in all. The upgrade runs up to 32 workers; with the default of ten per
// host every worker beyond the tenth opens, and then closes, a connection per
// request, which leaves a socket in TIME_WAIT for each of them.
const idleConns = 256

// The client sends a request again when the store says that it is busy (500,
// 502, 503 with SlowDown, 504 and 429), when another conditional write to the
// same key is running (409 ConditionalRequestConflict) and when the connection
// fails. The upgrade runs up to 32 writers. The standard retryer gives up after
// three attempts, and after a hundred retries in all (its retry quota), which a
// store that throttles a prefix reaches at once. A request is sent up to
// s3Attempts times, with a random wait that doubles from s3BackoffBase up to
// s3MaxBackoff, and there is no quota.
const (
	s3Attempts   = 8
	s3MaxBackoff = 5 * time.Second
)

// s3BackoffBase is the first upper limit of the random wait before a request is
// sent again. It is a variable so that the tests need not wait for it.
var s3BackoffBase = 100 * time.Millisecond

// s3Backoff waits a random time up to the base doubled for each attempt, the
// "full jitter" of the guidance of the S3 service.
type s3Backoff struct{}

func (s3Backoff) BackoffDelay(attempt int, _ error) (time.Duration, error) {
	limit := s3MaxBackoff
	if attempt < 16 && s3BackoffBase<<attempt < limit {
		limit = s3BackoffBase << attempt
	}
	return time.Duration(rand.Int64N(int64(limit) + 1)), nil
}

// newRetryer is the retryer of the client: the standard one, with more attempts,
// no retry quota, the backoff above, and the two answers of a store under load
// that the standard one does not retry: a conflicting conditional write and
// 429.
func newRetryer() aws.Retryer {
	return retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = s3Attempts
		o.MaxBackoff = s3MaxBackoff
		o.Backoff = s3Backoff{}
		o.RateLimiter = ratelimit.None
		o.Retryables = append(o.Retryables, retry.IsErrorRetryableFunc(func(err error) aws.Ternary {
			if conflicted(err) || statusOf(err) == http.StatusTooManyRequests {
				return aws.TrueTernary
			}
			return aws.UnknownTernary
		}))
	})
}

// tracedHTTP records each request of the client as a network span, under the state request that sent it.
type tracedHTTP struct{ inner aws.HTTPClient }

func (t tracedHTTP) Do(r *http.Request) (*http.Response, error) { return netcap.Trace(r, t.inner.Do) }

// attempts counts the requests that a call sends: one, unless the client sent a
// request again.
type attempts struct {
	inner aws.HTTPClient
	n     atomic.Int32
}

func (a *attempts) Do(r *http.Request) (*http.Response, error) {
	a.n.Add(1)
	return a.inner.Do(r)
}

// counting returns the option that makes a call go through a counter.
func (b *S3Backend) counting() (*attempts, func(*s3.Options)) {
	a := &attempts{inner: tracedHTTP{b.http}}
	return a, func(o *s3.Options) { o.HTTPClient = a }
}

// landed reports whether a write that the client sent more than once was
// applied by an earlier attempt, whose answer was lost: the store refuses the
// condition because the object is the one that was written. Equal contents are
// the evidence. Another command that wrote the same bytes would have done the
// same, and a lock, which holds the nonce of its owner, is not the same bytes.
//
// A read that finds no object says that the write is not there. A read that
// fails says nothing: the refusal may be the answer to a resend of a write that
// landed, and the error is ErrUnknownOutcome, which the callers must not take for
// a refusal. op names the write for the message.
func (b *S3Backend) landed(ctx context.Context, tries *attempts, op, key string, data []byte) (bool, error) {
	if tries.n.Load() < 2 {
		return false, nil
	}
	got, err := b.Get(ctx, key)
	switch {
	case err == nil:
		return bytes.Equal(got, data), nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	}
	return false, fmt.Errorf("s3 conditional %s %s: %w: %w", op, key, ErrUnknownOutcome, err)
}

// S3Backend stores objects in a bucket.
type S3Backend struct {
	client *s3.Client
	http   *awshttp.BuildableClient
	bucket string
	prefix string
	name   string
	// noSnapshot is set when the settings say snapshot: false.
	noSnapshot bool
	// seen holds, by key, the version of each small object that this process last read or
	// wrote (see remember).
	seen sync.Map
}

// seenVersion is one version of an object: its entity tag and its bytes.
type seenVersion struct {
	tag  string
	data []byte
	// wrote is set when this process made the version, and not only read it.
	wrote bool
}

// rememberBelow is the size above which a version is not remembered. Only the locks, with
// the notes beside them, the pointers of the nodes and the outputs are remembered: the first
// are a few hundred bytes each and are what an apply replaces and deletes once for every node,
// and the outputs are read and then replaced on that condition at the end of an apply.
const rememberBelow = 8 << 10

// remembers reports whether versions of key are remembered.
func remembers(key string) bool {
	return strings.HasPrefix(key, "locks/") || strings.HasPrefix(key, "resources/") || key == "outputs.json"
}

// remember keeps the version of an object that a request of this process just read or made,
// so that a conditional write of the same object needs no read first. ReplaceIf and DeleteIf
// are a read and then a write that names the entity tag of what was read. A command mostly
// replaces and deletes what it wrote or read a moment before: the lock it took, which it
// marks, renews and releases, and the pointer whose revision it checked. For those the tag
// and the bytes are known, and the write is sent at once with the tag as its condition. The
// store still decides: when the object is not that version any more the write is refused, and
// the call reads the object and goes on as it always did. Nothing is ever read from here in
// place of the store: a Get is a request.
//
// The function that a caller gives ReplaceIf or DeleteIf can so be called twice in one call,
// first with the remembered contents and then with the ones the store holds. Only what its
// last call returned is acted on.
func (b *S3Backend) remember(key string, tag *string, data []byte, wrote bool) {
	if t := aws.ToString(tag); t != "" && len(data) <= rememberBelow && remembers(key) {
		b.seen.Store(key, seenVersion{tag: t, data: bytes.Clone(data), wrote: wrote})
		return
	}
	b.seen.Delete(key)
}

// remembered returns the version of key that this process last saw.
func (b *S3Backend) remembered(key string) (seenVersion, bool) {
	v, ok := b.seen.Load(key)
	if !ok {
		return seenVersion{}, false
	}
	return v.(seenVersion), true
}

// LastWritten returns the entity tag of the version of key that this process wrote, when its
// last request for the key was that write: the store uses it to record a node in the snapshot
// without reading back what it has just written. The tag is as a listing gives it, without
// the quotes.
func (b *S3Backend) LastWritten(key string) (string, bool) {
	v, ok := b.remembered(key)
	return strings.Trim(v.tag, `"`), ok && v.wrote
}

// NewS3Backend connects to the bucket. It does not create the bucket.
func NewS3Backend(ctx context.Context, cfg S3Config) (*S3Backend, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("$settings.state.bucket is required for the s3 backend")
	}
	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
		if t.MaxIdleConns < idleConns {
			t.MaxIdleConns = idleConns
		}
		if t.MaxIdleConnsPerHost < idleConns {
			t.MaxIdleConnsPerHost = idleConns
		}
	})
	var opts []func(*awsconfig.LoadOptions) error
	// Compatible stores often reject the newer checksum headers and return
	// none; only use checksums where the API requires them, and keep the
	// SDK's own logging out of the terminal.
	opts = append(opts,
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
		awsconfig.WithLogger(logging.Nop{}),
		awsconfig.WithHTTPClient(httpClient),
		awsconfig.WithRetryer(newRetryer),
	)
	if cfg.Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.Region))
	}
	if cfg.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken)))
	}
	ac, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS configuration: %w", err)
	}
	if ac.Region == "" {
		ac.Region = "us-east-1"
	}
	client := s3.NewFromConfig(ac, func(o *s3.Options) {
		o.HTTPClient = tracedHTTP{ac.HTTPClient}
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle || cfg.Endpoint != ""
	})
	prefix := strings.Trim(cfg.Prefix, "/")
	name := "s3://" + cfg.Bucket
	if prefix != "" {
		name += "/" + prefix
	}
	return &S3Backend{client: client, http: httpClient, bucket: cfg.Bucket, prefix: prefix, name: name, noSnapshot: cfg.NoSnapshot}, nil
}

func (b *S3Backend) Name() string { return b.name }
func (b *S3Backend) Local() bool  { return false }

func (b *S3Backend) key(k string) string {
	if b.prefix == "" {
		return k
	}
	return b.prefix + "/" + k
}

func (b *S3Backend) Get(ctx context.Context, key string) ([]byte, error) {
	data, _, err := b.getTagged(ctx, key)
	return data, err
}

// getTagged reads the object and the entity tag of the version it read.
func (b *S3Backend) getTagged(ctx context.Context, key string) ([]byte, *string, error) {
	data, tag, _, err := b.getObject(ctx, key)
	return data, tag, err
}

// GetTimed is Get with the times of the bucket: when it last wrote the object, and its own
// time when it answered. Both are in the answer to the read, so it costs no request.
func (b *S3Backend) GetTimed(ctx context.Context, key string) ([]byte, StoreTime, error) {
	data, _, at, err := b.getObject(ctx, key)
	return data, at, err
}

// getObject reads the object, the entity tag of the version it read and the times of the
// bucket.
func (b *S3Backend) getObject(ctx context.Context, key string) ([]byte, *string, StoreTime, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key))})
	if err != nil {
		if notFound(err) {
			return nil, nil, StoreTime{}, ErrNotFound
		}
		return nil, nil, StoreTime{}, fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err == nil {
		b.remember(key, out.ETag, data, false)
	}
	return data, out.ETag, bucketTime(out), err
}

// bucketTime is what the answer to a read says about the time of the bucket: Last-Modified
// is when the bucket wrote the object and Date is the bucket's time when it answered. An
// answer that carries Age was kept by something between the bucket and this process, and
// its Date is that of an earlier answer, so it tells nothing. S3 sends both headers with
// every object; a compatible store that leaves one out has its locks judged by the clocks
// of the machines, as before.
func bucketTime(out *s3.GetObjectOutput) StoreTime {
	resp, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response)
	if !ok || resp == nil || out.LastModified == nil {
		return StoreTime{}
	}
	return headerTime(resp.Header, *out.LastModified)
}

// headerTime is the StoreTime of an answer with the given headers for an object that the
// store last wrote at modified.
func headerTime(h http.Header, modified time.Time) StoreTime {
	if modified.IsZero() || h.Get("Age") != "" {
		return StoreTime{}
	}
	now, err := http.ParseTime(h.Get("Date"))
	if err != nil {
		return StoreTime{}
	}
	return StoreTime{Modified: modified, Now: now}
}

// statusOf returns the HTTP status of the response that err reports, or 0 if err
// is not the answer of a store.
func statusOf(err error) int {
	var re *awshttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}

// codeOf returns the error code that the store gave in err, or "".
func codeOf(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// anonymous reports whether the store sent no error code that the client can
// tell apart: a response with no body, or one that is not the XML of an error.
func anonymous(err error) bool {
	c := codeOf(err)
	return c == "" || c == "UnknownError"
}

// notFound reports whether err says that the object does not exist. A missing
// bucket is not a missing object.
func notFound(err error) bool {
	var nsk *types.NoSuchKey
	switch codeOf(err) {
	case "NoSuchKey", "NotFound":
		return true
	}
	return errors.As(err, &nsk) || (statusOf(err) == http.StatusNotFound && anonymous(err))
}

// preconditionFailed reports whether err says that a condition of the request
// was not met: 412, which is what S3 and the stores that follow it answer to a
// stale entity tag and to a create of a key that exists. The status is read from
// the response and not from the text of the error, which can hold "412" in a
// port or a request id.
func preconditionFailed(err error) bool {
	return statusOf(err) == http.StatusPreconditionFailed || codeOf(err) == "PreconditionFailed"
}

// conflicted reports whether err says that another conditional write to the same
// key was running, which S3 answers with 409 ConditionalRequestConflict. It is
// neither a refused condition nor a failure: the request may be sent again, and
// then it is accepted or refused with 412. A 409 with no error code that the
// client can tell apart counts too, for the stores that send it bare.
func conflicted(err error) bool {
	return codeOf(err) == "ConditionalRequestConflict" || (statusOf(err) == http.StatusConflict && anonymous(err))
}

func (b *S3Backend) Put(ctx context.Context, key string, data []byte) error {
	b.seen.Delete(key)
	out, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader(data), ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	b.remember(key, out.ETag, data, true)
	return nil
}

// PutIfAbsent relies on the If-None-Match: * conditional write, which S3
// and current compatible stores honour; a store that ignores it would let
// two applies hold the same lock. DeleteIf and ReplaceIf rely on If-Match in
// the same way.
func (b *S3Backend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	tries, opt := b.counting()
	out, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader(data),
		ContentType: aws.String("application/json"), IfNoneMatch: aws.String("*"),
	}, opt)
	if err != nil {
		if preconditionFailed(err) {
			// An attempt that was applied and answered 500, or lost its connection,
			// is sent again and refused because the object it wrote is there.
			return b.landed(ctx, tries, "put", key, data)
		}
		return false, fmt.Errorf("s3 conditional put %s: %w", key, err)
	}
	b.remember(key, out.ETag, data, true)
	return true, nil
}

// noEntityTag is the error for a store that sent no entity tag with an object. The
// request would go without its If-Match and be a write that nothing conditions.
func noEntityTag(op, key string) error {
	return fmt.Errorf("s3 conditional %s %s: the store sent no entity tag, so the write cannot be made conditional", op, key)
}

// DeleteIf reads the object and deletes the version that it read with an
// If-Match condition on its entity tag. A store that ignores the condition
// deletes whatever is there, and two callers that judged the same lock could both
// delete it.
//
// A delete whose answer was lost is sent again by the client and finds nothing:
// the answer is 404, and it is returned as ErrNotFound although this call
// deleted the object. That is not an unknown outcome, and no read is made to
// tell the two apart. Whoever deleted it, the object is gone, which is what the
// callers want of a delete, and they take ErrNotFound as gone: a lock that is
// released, a pointer that is removed, a record that a collection deletes. A
// resend that is refused with 412 means that the object is no longer the one
// that was read: a delete that was made leaves no object to be refused for, so
// the refusal is another writer's and (false, nil) is exact.
//
// When this process holds a version of the object (see remember) that match accepts, the
// delete is sent at once with the tag of that version. A store that still holds the version
// deletes it, which is what a read that found it would have led to. A store that holds
// another version refuses, and the object is read as above.
func (b *S3Backend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	if v, ok := b.remembered(key); ok && match(v.data) {
		b.seen.Delete(key)
		_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), IfMatch: aws.String(v.tag)})
		switch {
		case err == nil:
			return true, nil
		case notFound(err):
			return false, ErrNotFound
		case !preconditionFailed(err):
			return false, fmt.Errorf("s3 conditional delete %s: %w", key, err)
		}
	}
	b.seen.Delete(key)
	data, etag, err := b.getTagged(ctx, key)
	if err != nil {
		return false, err
	}
	if aws.ToString(etag) == "" {
		return false, noEntityTag("delete", key)
	}
	if !match(data) {
		return false, nil
	}
	b.seen.Delete(key)
	_, err = b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), IfMatch: etag})
	switch {
	case err == nil:
		return true, nil
	case notFound(err):
		return false, ErrNotFound
	case preconditionFailed(err):
		return false, nil
	}
	return false, fmt.Errorf("s3 conditional delete %s: %w", key, err)
}

// ReplaceIf reads the object and writes the new contents over the version that
// it read, with an If-Match condition on its entity tag.
//
// When this process holds a version of the object (see remember) that update accepts, the
// write is sent at once with the tag of that version, as DeleteIf does. A refusal, unless an
// earlier attempt of the same write is what made it, leads to the read: update is then
// called a second time, with the contents that the store holds.
func (b *S3Backend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	if v, ok := b.remembered(key); ok {
		if next, ok := update(v.data); ok {
			done, wrote, err := b.replaceVersion(ctx, key, aws.String(v.tag), next)
			if done || wrote {
				return wrote, err
			}
		}
	}
	b.seen.Delete(key)
	data, etag, err := b.getTagged(ctx, key)
	if err != nil {
		return false, err
	}
	if aws.ToString(etag) == "" {
		return false, noEntityTag("put", key)
	}
	next, ok := update(data)
	if !ok {
		return false, nil
	}
	_, wrote, err := b.replaceVersion(ctx, key, etag, next)
	return wrote, err
}

// replaceVersion writes next over the version of key that has the tag. done is false when the
// store refused because it holds another version and no attempt of this write made it: the
// caller that sent a remembered tag then reads the object, and the caller that has just read
// it reports that nothing was written.
func (b *S3Backend) replaceVersion(ctx context.Context, key string, tag *string, next []byte) (done, wrote bool, err error) {
	b.seen.Delete(key)
	tries, opt := b.counting()
	out, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader(next),
		ContentType: aws.String("application/json"), IfMatch: tag,
	}, opt)
	switch {
	case err == nil:
		b.remember(key, out.ETag, next, true)
		return true, true, nil
	case notFound(err):
		return true, false, ErrNotFound
	case preconditionFailed(err):
		wrote, err := b.landed(ctx, tries, "put", key, next)
		return wrote || err != nil, wrote, err
	}
	return true, false, fmt.Errorf("s3 conditional put %s: %w", key, err)
}

func (b *S3Backend) Delete(ctx context.Context, key string) error {
	b.seen.Delete(key)
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key))})
	if err != nil {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	return nil
}

// List pages through the keys under prefix. A page that is cut short carries the
// token of the next one; a store that does not send it is asked again after the
// last key of the page. A listing that does not advance is an error and not an
// endless loop: the next page starts where this one did, or it holds a key that
// an earlier page held, which a store that ignores the token and numbers its
// pages sends. The keys are returned in the byte order that S3 lists them in,
// whatever the store did.
func (b *S3Backend) List(ctx context.Context, prefix string) ([]string, error) {
	objs, err := b.ListVersions(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.Key
	}
	return out, nil
}

// ListsVersions reports whether the settings allow a snapshot (snapshot: false turns it off).
func (b *S3Backend) ListsVersions() bool { return !b.noSnapshot }

// ListVersions is List with the ETag that the listing gives for each key.
func (b *S3Backend) ListVersions(ctx context.Context, prefix string) ([]ObjectVersion, error) {
	full := b.key(strings.TrimSuffix(prefix, "/") + "/")
	var out []ObjectVersion
	var token, startAfter *string
	seen := map[string]bool{}
	for {
		res, err := b.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(b.bucket), Prefix: aws.String(full), ContinuationToken: token, StartAfter: startAfter})
		if err != nil {
			return nil, fmt.Errorf("s3 list %s: %w", prefix, err)
		}
		last := ""
		for _, o := range res.Contents {
			k := aws.ToString(o.Key)
			last = k
			if b.prefix != "" {
				k = strings.TrimPrefix(k, b.prefix+"/")
			}
			if strings.HasSuffix(k, "/") {
				continue
			}
			if seen[k] {
				return nil, fmt.Errorf("s3 list %s: the listing does not advance (the store sent %s again, with a new token)", prefix, k)
			}
			seen[k] = true
			out = append(out, ObjectVersion{Key: k, Tag: strings.Trim(aws.ToString(o.ETag), `"`)})
		}
		if res.IsTruncated == nil || !*res.IsTruncated {
			break
		}
		switch next := aws.ToString(res.NextContinuationToken); {
		case next != "" && next != aws.ToString(token):
			token, startAfter = res.NextContinuationToken, nil
		case next == "" && last != "" && last != aws.ToString(startAfter):
			token, startAfter = nil, aws.String(last)
		default:
			return nil, fmt.Errorf("s3 list %s: the listing does not advance (the store sent a page that starts where the last one did)", prefix)
		}
	}
	if !sort.SliceIsSorted(out, func(i, j int) bool { return out[i].Key < out[j].Key }) {
		sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	}
	return out, nil
}
