package plugin

import (
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/env"
)

func TestParseAddress(t *testing.T) {
	ok := map[string]Reattachment{
		"unix:///tmp/p.sock":   {Network: "unix", Address: "/tmp/p.sock"},
		"tcp://127.0.0.1:5000": {Network: "tcp", Address: "127.0.0.1:5000"},
		"tcp://localhost:5000": {Network: "tcp", Address: "localhost:5000"},
		"tcp://[::1]:5000":     {Network: "tcp", Address: "[::1]:5000"},
	}
	for in, want := range ok {
		got, err := ParseAddress(in)
		if err != nil || got != want {
			t.Errorf("ParseAddress(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	bad := map[string]string{
		"tcp://10.0.0.5:5000":    "loopback",
		"tcp://example.com:5000": "loopback",
		"tcp://127.0.0.1":        "host:port",
		"http://127.0.0.1:5000":  "not supported",
		"/tmp/p.sock":            "invalid provider address",
		"unix://":                "no socket path",
	}
	for in, frag := range bad {
		if _, err := ParseAddress(in); err == nil || !strings.Contains(err.Error(), frag) {
			t.Errorf("ParseAddress(%q): err = %v, want it to mention %q", in, err, frag)
		}
	}
}

func TestReattachFromEnv(t *testing.T) {
	const entry = `{"Protocol":"grpc","ProtocolVersion":6,"Test":true,"Addr":{"Network":"unix","String":"/tmp/x"}}`
	t.Setenv("TF_REATTACH_PROVIDERS", `{"registry.terraform.io/hashicorp/random":`+entry+`}`)

	// The key names the registry the provider was built for, which need not
	// be the one pravo uses; namespace/type is enough to match.
	r, err := ReattachFromEnv(env.OS(), "registry.opentofu.org/hashicorp/random", "hashicorp/random")
	if err != nil || r == nil || r.Address != "/tmp/x" || r.ProtocolVersion != 6 {
		t.Fatalf("match by namespace/type: %+v, %v", r, err)
	}
	if r, err := ReattachFromEnv(env.OS(), "registry.opentofu.org/hashicorp/aws", "hashicorp/aws"); r != nil || err != nil {
		t.Fatalf("another provider must not match: %+v, %v", r, err)
	}
	if r, err := ReattachFromEnv(env.OS(), "registry.terraform.io/hashicorp/random"); err != nil || r == nil {
		t.Fatalf("exact key: %+v, %v", r, err)
	}

	t.Setenv("TF_REATTACH_PROVIDERS", `{"hashicorp/random":{"Protocol":"grpc","Addr":{"Network":"tcp","String":"10.1.2.3:99"}}}`)
	if _, err := ReattachFromEnv(env.OS(), "hashicorp/random"); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback tcp must be refused: %v", err)
	}
	t.Setenv("TF_REATTACH_PROVIDERS", `{"hashicorp/random":{"Protocol":"netrpc","Addr":{"Network":"unix","String":"/x"}}}`)
	if _, err := ReattachFromEnv(env.OS(), "hashicorp/random"); err == nil {
		t.Fatal("only grpc is supported")
	}
	t.Setenv("TF_REATTACH_PROVIDERS", `not json`)
	if _, err := ReattachFromEnv(env.OS(), "hashicorp/random"); err == nil {
		t.Fatal("invalid JSON must be reported")
	}
	t.Setenv("TF_REATTACH_PROVIDERS", "")
	if r, err := ReattachFromEnv(env.OS(), "hashicorp/random"); r != nil || err != nil {
		t.Fatal("unset means no reattachment")
	}
}
