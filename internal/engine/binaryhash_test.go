package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/registry"
)

// installedProvider puts an executable where the store keeps provider acme/dev 1.0.0.
func installedProvider(t *testing.T, body string) (*Providers, *registry.Client, string) {
	t.Helper()
	root, err := loadSettings(t, providerSpec("acme", "dev", `$version: "1.0.0"`))
	if err != nil {
		t.Fatal(err)
	}
	reg := &registry.Client{Home: t.TempDir(), Env: env.Map(nil)}
	src := root.Providers["dev"].Source
	dir := reg.ProviderDir(src, "1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "terraform-provider-dev_v1.0.0"
	if runtime.GOOS == "windows" {
		name += ".exe" // only a file named like a program is one on Windows
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".complete"), []byte(fmt.Sprintln(len(body))), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewProviders(root, reg), reg, src.String()
}

func resolveWith(t *testing.T, m *Providers, lock *Lock, install bool) error {
	t.Helper()
	return m.Resolve(context.Background(), lock, false, install)
}

func TestInitRecordsTheDigestOfAnInstalledExecutable(t *testing.T) {
	m, _, src := installedProvider(t, "one")
	lock := &Lock{Providers: map[string]*LockEntry{src: {Version: "1.0.0", Hashes: map[string]string{registry.CurrentPlatform().String(): "sha256:archive"}}}}
	if err := resolveWith(t, m, lock, true); err != nil {
		t.Fatal(err)
	}
	m.UpdateLock(lock)
	got := lock.Providers[src].Hashes[BinaryHashKey(registry.CurrentPlatform().String())]
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("binary hash = %q, hashes = %v", got, lock.Providers[src].Hashes)
	}
	if lock.Providers[src].Hashes[registry.CurrentPlatform().String()] != "sha256:archive" {
		t.Errorf("the archive hash changed: %v", lock.Providers[src].Hashes)
	}
}

func TestOtherCommandsDoNotTakeTheDigest(t *testing.T) {
	m, _, src := installedProvider(t, "one")
	lock := &Lock{Providers: map[string]*LockEntry{src: {Version: "1.0.0", Hashes: map[string]string{registry.CurrentPlatform().String(): "sha256:archive"}}}}
	if err := resolveWith(t, m, lock, false); err != nil {
		t.Fatal(err)
	}
	m.UpdateLock(lock)
	if h := lock.Providers[src].Hashes[BinaryHashKey(registry.CurrentPlatform().String())]; h != "" {
		t.Errorf("a command other than init took the digest: %q", h)
	}
}

func TestAChangedExecutableIsRefused(t *testing.T) {
	m, reg, src := installedProvider(t, "one")
	lock := &Lock{Providers: map[string]*LockEntry{src: {Version: "1.0.0", Hashes: map[string]string{registry.CurrentPlatform().String(): "sha256:archive"}}}}
	if err := resolveWith(t, m, lock, true); err != nil {
		t.Fatal(err)
	}
	m.UpdateLock(lock)
	// another build appears under the same name, as in a poisoned cache
	p, _ := reg.FindInstalled(m.Root.Providers["dev"].Source, "1.0.0")
	if err := os.WriteFile(p, []byte("two"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, install := range []bool{true, false} {
		m2 := NewProviders(m.Root, reg)
		err := resolveWith(t, m2, lock, install)
		if err == nil || !strings.Contains(err.Error(), "pins") || !strings.Contains(err.Error(), "pravo init") {
			t.Errorf("install=%v: err = %v", install, err)
		}
	}
}

func TestALockWithoutTheDigestStillLoads(t *testing.T) {
	m, _, src := installedProvider(t, "one")
	dir := t.TempDir()
	old := "providers: \"" + src + "\": {\n\tversion: \"1.0.0\"\n\thashes: {\n\t\t\"" + registry.CurrentPlatform().String() + "\": \"sha256:abc\"\n\t}\n}\n"
	os.WriteFile(filepath.Join(dir, LockFileName), []byte(old), 0o644)
	lock, err := ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolveWith(t, m, lock, false); err != nil {
		t.Errorf("a lock of an older pravo is refused: %v", err)
	}
	// and a lock with the key round trips, for the older pravo that ignores it
	lock.Providers[src].Hashes[BinaryHashKey("linux_amd64")] = "sha256:def"
	if err := lock.Write(dir); err != nil {
		t.Fatal(err)
	}
	again, err := ReadLock(dir)
	if err != nil || again.Providers[src].Hashes[BinaryHashKey("linux_amd64")] != "sha256:def" {
		t.Errorf("round trip: %v %v", again, err)
	}
	_ = config.ProtocolTfplugin
}
