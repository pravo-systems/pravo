package plugin

import (
	"strings"
	"testing"
)

func TestProviderEnvKeepsTheEnvironmentButNotTheSecretsOfTheIdentity(t *testing.T) {
	env := providerEnv([]string{
		"PATH=/usr/bin", "AWS_ACCESS_KEY_ID=key",
		"PRAVO_IDENTITY={\"v\":1}", "PRAVO_PASSPHRASE=hunter22", "pravo_identity_passphrase=hunter22",
		"PRAVO_IDENTITY_DIR=/home/ada/ids", "PRAVO_ACCESS_DIR=access",
	})
	have := map[string]bool{}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		have[strings.ToUpper(name)] = true
	}
	for _, want := range []string{"PATH", "AWS_ACCESS_KEY_ID", "PRAVO_IDENTITY_DIR", "PRAVO_ACCESS_DIR", "TF_PLUGIN_MAGIC_COOKIE"} {
		if !have[want] {
			t.Errorf("%s is missing from the environment of a provider", want)
		}
	}
	for _, secret := range []string{"PRAVO_IDENTITY", "PRAVO_PASSPHRASE", "PRAVO_IDENTITY_PASSPHRASE"} {
		if have[secret] {
			t.Errorf("%s reaches a provider", secret)
		}
	}
}
