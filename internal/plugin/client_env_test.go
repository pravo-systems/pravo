package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/env"
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

// What providerEnv withholds must not reach the process: go-plugin adds the environment of the
// host unless it is told not to. The "provider" here is a script that writes its environment
// and fails the handshake.
func TestStartedProviderDoesNotReceiveWhatPravoWithholds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script is the provider")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "terraform-provider-spy")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv > "+out+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRAVO_PASSPHRASE", "hunter22")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("PRAVO_CHAT_API_KEY", "chat-key")
	t.Setenv("AWS_ACCESS_KEY_ID", "cloud-key")
	if p, err := Start(script, nil, env.OS()); err == nil {
		p.Close()
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, secret := range []string{"PRAVO_PASSPHRASE=", "SSH_AUTH_SOCK=", "PRAVO_CHAT_API_KEY="} {
		if strings.Contains(got, secret) {
			t.Errorf("%s reaches a provider", secret)
		}
	}
	if !strings.Contains(got, "AWS_ACCESS_KEY_ID=cloud-key") {
		t.Errorf("the credentials of a cloud must reach the provider: %s", got)
	}
}
