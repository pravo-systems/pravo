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
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
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
const idleConns = 64

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
	a := &attempts{inner: b.http}
	return a, func(o *s3.Options) { o.HTTPClient = a }
}

// landed reports whether a write that the client sent more than once was
// applied by an earlier attempt, whose answer was lost: the store refuses the
// condition because the object is the one that was written. Equal contents are
// the evidence. Another command that wrote the same bytes would have done the
// same, and a lock, which holds the nonce of its owner, is not the same bytes.
func (b *S3Backend) landed(ctx context.Context, tries *attempts, key string, data []byte) bool {
	if tries.n.Load() < 2 {
		return false
	}
	got, err := b.Get(ctx, key)
	return err == nil && bytes.Equal(got, data)
}

// S3Backend stores objects in a bucket.
type S3Backend struct {
	client *s3.Client
	http   *awshttp.BuildableClient
	bucket string
	prefix string
	name   string
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
	return &S3Backend{client: client, http: httpClient, bucket: cfg.Bucket, prefix: prefix, name: name}, nil
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
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key))})
	if err != nil {
		if notFound(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	return data, out.ETag, err
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
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader(data), ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

// PutIfAbsent relies on the If-None-Match: * conditional write, which S3
// and current compatible stores honour; a store that ignores it would let
// two applies hold the same lock. DeleteIf and ReplaceIf rely on If-Match in
// the same way.
func (b *S3Backend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	tries, opt := b.counting()
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader(data),
		ContentType: aws.String("application/json"), IfNoneMatch: aws.String("*"),
	}, opt)
	if err != nil {
		if preconditionFailed(err) {
			// An attempt that was applied and answered 500, or lost its connection,
			// is sent again and refused because the object it wrote is there.
			return b.landed(ctx, tries, key, data), nil
		}
		return false, fmt.Errorf("s3 conditional put %s: %w", key, err)
	}
	return true, nil
}

// DeleteIf reads the object and deletes the version that it read with an
// If-Match condition on its entity tag. A store that ignores the condition
// deletes whatever is there, and two callers that judged the same lock could both
// delete it.
func (b *S3Backend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	data, etag, err := b.getTagged(ctx, key)
	if err != nil {
		return false, err
	}
	if !match(data) {
		return false, nil
	}
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
func (b *S3Backend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	data, etag, err := b.getTagged(ctx, key)
	if err != nil {
		return false, err
	}
	next, ok := update(data)
	if !ok {
		return false, nil
	}
	tries, opt := b.counting()
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader(next),
		ContentType: aws.String("application/json"), IfMatch: etag,
	}, opt)
	switch {
	case err == nil:
		return true, nil
	case notFound(err):
		return false, ErrNotFound
	case preconditionFailed(err):
		return b.landed(ctx, tries, key, next), nil
	}
	return false, fmt.Errorf("s3 conditional put %s: %w", key, err)
}

func (b *S3Backend) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key(key))})
	if err != nil {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	return nil
}

// List pages through the keys under prefix. A page that is cut short carries the
// token of the next one; a store that does not send it is asked again after the
// last key of the page, and a listing that does not advance, because the next
// page starts where this one did, is an error and not an endless loop. The keys
// are returned in the byte order that S3 lists them in, whatever the store did.
func (b *S3Backend) List(ctx context.Context, prefix string) ([]string, error) {
	full := b.key(strings.TrimSuffix(prefix, "/") + "/")
	var out []string
	var token, startAfter *string
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
			out = append(out, k)
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
	if !sort.StringsAreSorted(out) {
		sort.Strings(out)
	}
	return out, nil
}
