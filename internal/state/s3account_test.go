package state

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	acctKey    = "ASIAACCT0003"
	acctSecret = "FAKEACCTSECRET0003"
	acctToken  = "FAKEACCTTOKEN0003"
)

// accountNoSecrets fails when an error text holds a key, a secret or a token.
func accountNoSecrets(t *testing.T, text string) {
	t.Helper()
	for _, w := range []string{acctKey, acctSecret, acctToken} {
		if strings.Contains(text, w) {
			t.Errorf("the error holds the credential %q: %s", w, text)
		}
	}
}

// accountRig stands in for STS and S3. The caller identity answers with the
// account, or with a refusal when account is empty. s3Calls counts the
// requests that reach the bucket.
func accountRig(t *testing.T, account string) (s3URL string, s3Calls, stsCalls *atomic.Int32) {
	t.Helper()
	s3Calls, stsCalls = new(atomic.Int32), new(atomic.Int32)
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stsCalls.Add(1)
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "Action=GetCallerIdentity") {
			t.Errorf("unexpected STS request %q", b)
		}
		w.Header().Set("Content-Type", "text/xml")
		if account == "" {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>not allowed</Message></Error><RequestId>r</RequestId></ErrorResponse>`)
			return
		}
		io.WriteString(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult>`+
			`<Arn>arn:aws:iam::`+account+`:user/u</Arn><UserId>AIDAFAKE</UserId><Account>`+account+`</Account>`+
			`</GetCallerIdentityResult><ResponseMetadata><RequestId>r</RequestId></ResponseMetadata></GetCallerIdentityResponse>`)
	}))
	t.Cleanup(sts.Close)
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Calls.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
	}))
	t.Cleanup(s3.Close)
	t.Setenv("AWS_ENDPOINT_URL_STS", sts.URL)
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	return s3.URL, s3Calls, stsCalls
}

func accountConfig(s3URL, account string) S3Config {
	return S3Config{Bucket: "state", Region: "us-east-1", Endpoint: s3URL, PathStyle: true,
		AccessKey: acctKey, SecretKey: acctSecret, SessionToken: acctToken, Account: account}
}

// The right account opens the state.
func TestS3AccountMatches(t *testing.T) {
	url, s3Calls, stsCalls := accountRig(t, "111111111111")
	b, err := NewS3Backend(context.Background(), accountConfig(url, "111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.List(context.Background(), "rec"); err != nil {
		t.Fatal(err)
	}
	if stsCalls.Load() != 1 || s3Calls.Load() == 0 {
		t.Errorf("sts calls %d, s3 calls %d", stsCalls.Load(), s3Calls.Load())
	}
}

// Without an account no identity call is made.
func TestS3NoAccountNoIdentityCall(t *testing.T) {
	url, _, stsCalls := accountRig(t, "111111111111")
	if _, err := NewS3Backend(context.Background(), accountConfig(url, "")); err != nil {
		t.Fatal(err)
	}
	if stsCalls.Load() != 0 {
		t.Errorf("%d sts calls", stsCalls.Load())
	}
}

// Another account refuses, names both, and nothing reaches the bucket.
func TestS3AccountMismatchRefuses(t *testing.T) {
	url, s3Calls, _ := accountRig(t, "999999999999")
	_, err := NewS3Backend(context.Background(), accountConfig(url, "111111111111"))
	if err == nil {
		t.Fatal("a different account was accepted")
	}
	for _, want := range []string{"111111111111", "999999999999"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %s: %v", want, err)
		}
	}
	accountNoSecrets(t, err.Error())
	if n := s3Calls.Load(); n != 0 {
		t.Errorf("%d requests reached the bucket before the refusal", n)
	}
}

// A failed identity call refuses as well.
func TestS3AccountLookupFailureRefuses(t *testing.T) {
	url, s3Calls, _ := accountRig(t, "")
	_, err := NewS3Backend(context.Background(), accountConfig(url, "111111111111"))
	if err == nil {
		t.Fatal("a failed GetCallerIdentity was accepted")
	}
	if !strings.Contains(err.Error(), "GetCallerIdentity") || !strings.Contains(err.Error(), "111111111111") {
		t.Errorf("error %v", err)
	}
	accountNoSecrets(t, err.Error())
	if n := s3Calls.Load(); n != 0 {
		t.Errorf("%d requests reached the bucket", n)
	}
}

// An account that is not 12 digits is refused before anything is called.
func TestS3AccountMalformed(t *testing.T) {
	url, _, stsCalls := accountRig(t, "111111111111")
	for _, a := range []string{"1234", "12345678901a", "1111111111111"} {
		if _, err := NewS3Backend(context.Background(), accountConfig(url, a)); err == nil || !strings.Contains(err.Error(), "12-digit") {
			t.Errorf("account %q: %v", a, err)
		}
	}
	if stsCalls.Load() != 0 {
		t.Errorf("%d sts calls", stsCalls.Load())
	}
}
