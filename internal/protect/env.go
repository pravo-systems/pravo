package protect

import "strings"

// SecretEnv lists the environment variables that can hold an identity or the
// passphrase of one. Provider plugins run with pravo's environment
// (internal/plugin/client.go: cmd.Env = append(os.Environ(), …)), so these must
// not reach them.
var SecretEnv = []string{"PRAVO_IDENTITY", "PRAVO_IDENTITY_PASSPHRASE", "PRAVO_PASSPHRASE"}

// ScrubEnv returns env without the variables in SecretEnv. Names are compared
// without regard to case, as Windows does. Apply it in plugin.Start, and call
// os.Unsetenv for each name once the identity has been read.
func ScrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		for _, s := range SecretEnv {
			if strings.EqualFold(name, s) {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}
