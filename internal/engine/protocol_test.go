package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pravo.dev/pravo/internal/config"
)

func loadSettings(t *testing.T, cue string) (*config.Root, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.cue"), []byte("package t\n\n"+cue), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.LoadSettings(dir)
}

// providerSpec is a specification that declares one provider of source
// ns/type with the given meta fields.
func providerSpec(ns, typ, meta string) string {
	return "import x \"pravo.dev/providers/" + ns + "/" + typ + "\"\n\nprov: x.#Provider & {" + meta + "}\n"
}

func TestProviderProtocolValidation(t *testing.T) {
	bad := map[string]string{
		`$protocol: "carrier-pigeon"`: "unknown protocol",
		`$address: "unix:///x"`:       "address applies to protocol",
		`$protocol: "tfplugin+reattach", $address: "unix:///x", $path: "/bin/x"`: "already running",
		`$protocol: "tfplugin+reattach", $address: "unix:///x", $version: "1.0"`: "version does not apply",
	}
	for meta, frag := range bad {
		if _, err := loadSettings(t, providerSpec("a", "b", meta)); err == nil || !strings.Contains(err.Error(), frag) {
			t.Errorf("%s: err = %v, want %q", meta, err, frag)
		}
	}
	root, err := loadSettings(t, `import a "pravo.dev/providers/hashicorp/random"
import b "pravo.dev/providers/x/y"

one: a.#Provider & {}
two: b.#Provider & {$protocol: "tfplugin+reattach", $address: "unix:///tmp/s"}
`)
	if err != nil {
		t.Fatal(err)
	}
	if root.Providers["random"].Protocol != config.ProtocolTfplugin {
		t.Errorf("default protocol = %q", root.Providers["random"].Protocol)
	}
	if b := root.Providers["y"]; b.Protocol != config.ProtocolReattach || b.Address != "unix:///tmp/s" {
		t.Errorf("reattach declaration = %+v", b)
	}
}

func TestResolveReattachNeedsNoRegistryAndStaysOutOfTheLock(t *testing.T) {
	root, err := loadSettings(t, providerSpec("acme", "dev", `$protocol: "tfplugin+reattach", $address: "tcp://127.0.0.1:4711"`))
	if err != nil {
		t.Fatal(err)
	}
	m := NewProviders(root, nil) // a nil registry client: any use would panic
	lock := &Lock{Providers: map[string]*LockEntry{}}
	if err := m.Resolve(context.Background(), lock, false, true); err != nil {
		t.Fatal(err)
	}
	rp := m.Resolved["dev"]
	if rp == nil || !rp.Reattached() || rp.Reattach.Address != "127.0.0.1:4711" || rp.Version != "reattach" {
		t.Fatalf("resolved = %+v", rp)
	}
	m.UpdateLock(lock)
	if len(lock.Providers) != 0 {
		t.Errorf("a reattached provider must not be locked: %+v", lock.Providers)
	}

	// An address that is not loopback is refused at resolution.
	root2, _ := loadSettings(t, providerSpec("acme", "dev", `$protocol: "tfplugin+reattach", $address: "tcp://10.0.0.9:4711"`))
	if err := NewProviders(root2, nil).Resolve(context.Background(), lock, false, true); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Errorf("non-loopback address: %v", err)
	}
	// A reattach declaration without an address is an error, not a fallback.
	root3, _ := loadSettings(t, providerSpec("acme", "dev", `$protocol: "tfplugin+reattach"`))
	if err := NewProviders(root3, nil).Resolve(context.Background(), lock, false, true); err == nil || !strings.Contains(err.Error(), "needs an address") {
		t.Errorf("missing address: %v", err)
	}
}

func TestEnvironmentReattachOverridesRegistryDeclaration(t *testing.T) {
	t.Setenv("TF_REATTACH_PROVIDERS", `{"registry.terraform.io/acme/dev":{"Protocol":"grpc","ProtocolVersion":6,"Test":true,"Addr":{"Network":"unix","String":"/tmp/dev.sock"}}}`)
	root, err := loadSettings(t, providerSpec("acme", "dev", `$version: "~> 1.0"`))
	if err != nil {
		t.Fatal(err)
	}
	m := NewProviders(root, nil)
	if err := m.Resolve(context.Background(), &Lock{Providers: map[string]*LockEntry{}}, false, true); err != nil {
		t.Fatal(err)
	}
	if rp := m.Resolved["dev"]; !rp.Reattached() || rp.Reattach.ProtocolVersion != 6 {
		t.Fatalf("TF_REATTACH_PROVIDERS ignored: %+v", rp)
	}
}
