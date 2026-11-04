package protect

import "strings"

// SecretEnv lists the environment variables that can hold an identity or the
// passphrase of one. Provider plugins run with pravo's environment
// (internal/plugin/client.go: cmd.Env = append(os.Environ(), …)), so these must
// not reach them.
var SecretEnv = []string{"PRAVO_IDENTITY", "PRAVO_IDENTITY_PASSPHRASE", "PRAVO_PASSPHRASE"}

// PrivateEnv lists variables that belong to pravo or to its operator and that no provider
// needs: the key of the viewer's assistant, the directory of the identities, the headers
// of the telemetry export, and the socket of the ssh-agent that can sign as the operator.
// They are withheld from providers unless PRAVO_PROVIDER_ENV names them (a comma-separated
// list), for a provider that really does talk to the agent.
var PrivateEnv = []string{"PRAVO_CHAT_API_KEY", "PRAVO_CONFIG_DIR", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS", "SSH_AUTH_SOCK", "PRAVO_SSH_AUTH_SOCK"}

// PassEnv is the variable that lists the PrivateEnv names a provider may receive.
const PassEnv = "PRAVO_PROVIDER_ENV"

// ScrubEnv returns env without the variables in SecretEnv. Names are compared
// without regard to case, as Windows does. Apply it in plugin.Start, and call
// os.Unsetenv for each name once the identity has been read.
func ScrubEnv(env []string) []string {
	pass := map[string]bool{}
	for _, kv := range env {
		if name, v, _ := strings.Cut(kv, "="); strings.EqualFold(name, PassEnv) {
			for _, n := range strings.Split(v, ",") {
				pass[strings.ToUpper(strings.TrimSpace(n))] = true
			}
		}
	}
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		for _, s := range SecretEnv {
			if strings.EqualFold(name, s) {
				continue next
			}
		}
		for _, s := range PrivateEnv {
			if strings.EqualFold(name, s) && !pass[strings.ToUpper(s)] {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}
