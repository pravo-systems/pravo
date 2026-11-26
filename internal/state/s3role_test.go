package state

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// With a role_arn, the bucket is opened with the credentials that
// sts:AssumeRole returns, for the role, the external ID and the session name.
func TestS3AssumesTheRole(t *testing.T) {
	var mu sync.Mutex
	var form url.Values
	var s3Auth string
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		form, _ = url.ParseQuery(string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/xml")
		io.WriteString(w, `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials>`+
			`<AccessKeyId>ASIAROLE</AccessKeyId><SecretAccessKey>secret</SecretAccessKey><SessionToken>tok</SessionToken>`+
			`<Expiration>2099-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>`)
	}))
	t.Cleanup(sts.Close)
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		s3Auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
	}))
	t.Cleanup(s3.Close)
	t.Setenv("AWS_ENDPOINT_URL_STS", sts.URL)
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	b, err := NewS3Backend(context.Background(), S3Config{Bucket: "state", Region: "us-east-1", Endpoint: s3.URL, PathStyle: true,
		AccessKey: "base", SecretKey: "base", RoleARN: "arn:aws:iam::1:role/r", ExternalID: "ext", SessionName: "sess"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.List(context.Background(), "rec"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if form.Get("Action") != "AssumeRole" || form.Get("RoleArn") != "arn:aws:iam::1:role/r" || form.Get("ExternalId") != "ext" || form.Get("RoleSessionName") != "sess" {
		t.Errorf("assume-role request %v", form)
	}
	if !strings.Contains(s3Auth, "Credential=ASIAROLE/") {
		t.Errorf("the bucket was opened with %q, want the credentials of the role", s3Auth)
	}
}
