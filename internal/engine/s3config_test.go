package engine

import (
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/env"
)

// The credentials the settings leave out come from the environment of the run, and
// the settings win.
func TestS3ConfigComesFromTheEnvironmentOfTheRun(t *testing.T) {
	st := config.StateConfig{Bucket: "b"}
	e := env.Map(map[string]string{"AWS_ACCESS_KEY_ID": "id", "AWS_SECRET_ACCESS_KEY": "secret", "AWS_SESSION_TOKEN": "tok", "AWS_REGION": "eu-west-1", "AWS_PROFILE": "ignored"})
	c := s3ConfigFor(st, e)
	if c.AccessKey != "id" || c.SecretKey != "secret" || c.SessionToken != "tok" || c.Region != "eu-west-1" || c.Profile != "" {
		t.Errorf("config = %+v", c)
	}

	// Without keys the profile of the environment is used, and the default region.
	c = s3ConfigFor(st, env.Map(map[string]string{"AWS_PROFILE": "dev", "AWS_DEFAULT_REGION": "us-west-2"}))
	if c.Profile != "dev" || c.Region != "us-west-2" || c.AccessKey != "" {
		t.Errorf("config = %+v", c)
	}

	// The settings win over the environment.
	st = config.StateConfig{Bucket: "b", Region: "ap-south-1", Profile: "prod"}
	c = s3ConfigFor(st, e)
	if c.Region != "ap-south-1" || c.Profile != "prod" || c.AccessKey != "" {
		t.Errorf("config = %+v", c)
	}

	// An empty environment leaves everything to the SDK.
	c = s3ConfigFor(config.StateConfig{Bucket: "b"}, env.Env{})
	if c.AccessKey != "" || c.Profile != "" || c.Region != "" || c.SessionToken != "" {
		t.Errorf("config = %+v", c)
	}
}
