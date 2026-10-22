package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
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

// notFound reports whether err says that the object does not exist.
func notFound(err error) bool {
	var nsk *types.NoSuchKey
	var apiErr smithy.APIError
	return errors.As(err, &nsk) || (errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound"))
}

// preconditionFailed reports whether err says that a condition of the request
// was not met, or that another conditional request on the object was running.
func preconditionFailed(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "PreconditionFailed", "ConditionalRequestConflict":
			return true
		}
	}
	return strings.Contains(err.Error(), "412")
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
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader(data),
		ContentType: aws.String("application/json"), IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		if preconditionFailed(err) {
			return false, nil
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
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.key(key)), Body: bytes.NewReader(next),
		ContentType: aws.String("application/json"), IfMatch: etag,
	})
	switch {
	case err == nil:
		return true, nil
	case notFound(err):
		return false, ErrNotFound
	case preconditionFailed(err):
		return false, nil
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

func (b *S3Backend) List(ctx context.Context, prefix string) ([]string, error) {
	full := b.key(strings.TrimSuffix(prefix, "/") + "/")
	var out []string
	var token *string
	for {
		res, err := b.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(b.bucket), Prefix: aws.String(full), ContinuationToken: token})
		if err != nil {
			return nil, fmt.Errorf("s3 list %s: %w", prefix, err)
		}
		for _, o := range res.Contents {
			k := aws.ToString(o.Key)
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
		token = res.NextContinuationToken
	}
	return out, nil
}
