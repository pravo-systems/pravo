package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

const (
	loginARN    = "arn:aws:sts::111111111111:assumed-role/SomeRole/someone"
	orgRole     = "arn:aws:iam::222222222222:role/OrganizationAccountAccessRole"
	fakeKey     = "ASIAFAKE0001"
	fakeSecret  = "FAKESECRETVALUE0001"
	fakeToken   = "FAKESESSIONTOKEN0001"
	stsRoleKey  = "ASIAROLE0002"
	stsRoleSec  = "FAKEROLESECRET0002"
	stsRoleTok  = "FAKEROLETOKEN0002"
	secretWords = fakeKey + "|" + fakeSecret + "|" + fakeToken + "|" + stsRoleKey + "|" + stsRoleSec + "|" + stsRoleTok
)

// awsHome gives a temp HOME and profile files, and keeps every AWS lookup away
// from the machine that runs the tests.
func awsHome(t *testing.T, config string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, k := range []string{"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"} {
		t.Setenv(k, "")
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, "none"))
	cfg := filepath.Join(home, "config")
	if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", cfg)
	return home
}

// loginCache writes the cached session that `aws login` leaves for a profile.
func loginCache(t *testing.T, home string) {
	t.Helper()
	sum := sha256.Sum256([]byte(loginARN))
	dir := filepath.Join(home, ".aws", "login", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"accessToken": {"accessKeyId": "` + fakeKey + `", "secretAccessKey": "` + fakeSecret + `", "sessionToken": "` + fakeToken + `", "accountId": "111111111111", "expiresAt": "2099-01-01T00:00:00Z"},` +
		` "tokenType": "aws_sigv4", "refreshToken": "x", "identityToken": "x", "clientId": "arn:aws:signin:::devtools/same-device", "dpopKey": "x"}`
	if err := os.WriteFile(filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func noSecrets(t *testing.T, what, text string) {
	t.Helper()
	for _, w := range strings.Split(secretWords, "|") {
		if strings.Contains(text, w) {
			t.Errorf("%s holds the credential %q: %s", what, w, text)
		}
	}
}

// A role profile whose source_profile is an `aws login` profile is refused by
// the AWS library with no reason. Pravo says why and what to do, still fails,
// and still wraps the library's error. When this stops failing after a bump of
// the library, AWS has fixed the refusal and the hint can go.
func TestS3LoginSessionAsSourceProfileIsRefusedWithAHint(t *testing.T) {
	home := awsHome(t, "[profile org]\nlogin_session = "+loginARN+"\nregion = us-east-1\n\n"+
		"[profile prod]\nrole_arn = "+orgRole+"\nsource_profile = org\nregion = us-east-1\n")
	loginCache(t, home)
	// The library alone still refuses it: this is the premise of the hint.
	_, libErr := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithSharedConfigProfile("prod"))
	var lib awsconfig.SharedConfigAssumeRoleError
	if !errors.As(libErr, &lib) || lib.Err != nil {
		t.Fatalf("the AWS library no longer refuses a login session as a source_profile (%v): remove the hint", libErr)
	}
	_, err := NewS3Backend(context.Background(), S3Config{Bucket: "state", Profile: "prod"})
	if err == nil {
		t.Fatal("a login session as source_profile was accepted")
	}
	var e awsconfig.SharedConfigAssumeRoleError
	if !errors.As(err, &e) || e.RoleARN != orgRole || e.Profile != "org" {
		t.Errorf("the library's error is not wrapped: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{orgRole, `"org"`, "aws login", "credential_process = aws configure export-credentials --profile org --format process", awsLoginDocs, "failed to load assume role"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error lacks %q: %s", want, msg)
		}
	}
	noSecrets(t, "the error", msg)
}

// A source profile that does not exist is another error, and reads as it did.
func TestS3MissingSourceProfileReadsAsBefore(t *testing.T) {
	awsHome(t, "[profile prod]\nrole_arn = "+orgRole+"\nsource_profile = nowhere\nregion = us-east-1\n")
	_, err := NewS3Backend(context.Background(), S3Config{Bucket: "state", Profile: "prod"})
	if err == nil {
		t.Fatal("a missing source profile was accepted")
	}
	if strings.Contains(err.Error(), "credential_process") || strings.Contains(err.Error(), awsLoginDocs) {
		t.Errorf("the hint was added to an unrelated error: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "loading AWS configuration: ") || !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("error %q", err)
	}
}

// The way around the refusal: a source profile that takes its keys from a
// process, as `aws configure export-credentials --format process` gives them.
func TestS3CredentialProcessSourceProfileAssumesTheRole(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for the process is a shell script")
	}
	home := awsHome(t, "")
	script := filepath.Join(home, "export-credentials.sh")
	out := `{"Version": 1, "AccessKeyId": "` + fakeKey + `", "SecretAccessKey": "` + fakeSecret + `", "SessionToken": "` + fakeToken + `", "Expiration": "2099-01-01T00:00:00Z"}`
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '"+out+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The path is quoted for sh, with exec in front: the AWS library strips quotes that surround the whole value.
	cfg := "[profile org-sdk]\ncredential_process = exec '" + script + "'\nregion = us-east-1\n\n" +
		"[profile prod]\nrole_arn = " + orgRole + "\nsource_profile = org-sdk\nregion = us-east-1\n"
	if err := os.WriteFile(os.Getenv("AWS_CONFIG_FILE"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var stsAuth, stsBody, s3Auth string
	sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		stsAuth, stsBody = r.Header.Get("Authorization"), string(b)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/xml")
		io.WriteString(w, `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials>`+
			`<AccessKeyId>`+stsRoleKey+`</AccessKeyId><SecretAccessKey>`+stsRoleSec+`</SecretAccessKey><SessionToken>`+stsRoleTok+`</SessionToken>`+
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
	b, err := NewS3Backend(context.Background(), S3Config{Bucket: "state", Profile: "prod", Endpoint: s3.URL, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.List(context.Background(), "rec"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(stsBody, "Action=AssumeRole") || !strings.Contains(stsBody, "OrganizationAccountAccessRole") {
		t.Errorf("assume-role request %q", stsBody)
	}
	if !strings.Contains(stsAuth, "Credential="+fakeKey+"/") {
		t.Errorf("the role was assumed with %q, want the keys of the process", stsAuth)
	}
	if !strings.Contains(s3Auth, "Credential="+stsRoleKey+"/") {
		t.Errorf("the bucket was opened with %q, want the credentials of the role", s3Auth)
	}
}

// The text of a provider's error is matched whole, and only when the cause is nil.
func TestAWSSourceProfileHintFromText(t *testing.T) {
	msg := "configuring provider aws: failed to load assume role " + orgRole + ", of profile org, <nil>"
	h := AWSSourceProfileHintFromText(msg)
	for _, want := range []string{orgRole, `"org"`, "aws login", "--profile org", awsLoginDocs} {
		if !strings.Contains(h, want) {
			t.Errorf("the hint lacks %q: %s", want, h)
		}
	}
	for _, other := range []string{
		"failed to load assume role " + orgRole + ", of profile org, some other cause",
		"failed to get shared config profile, nowhere",
		"",
	} {
		if h := AWSSourceProfileHintFromText(other); h != "" {
			t.Errorf("%q got the hint %q", other, h)
		}
	}
	if h := AWSSourceProfileHint(awsconfig.SharedConfigAssumeRoleError{Profile: "org", RoleARN: orgRole, Err: errors.New("denied")}); h != "" {
		t.Errorf("an error with a cause got the hint %q", h)
	}
}
