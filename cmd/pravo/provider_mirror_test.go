package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mirrorZip is the archive of the fake provider binary, named as a provider archive names it.
func mirrorZip(t *testing.T) []byte {
	t.Helper()
	bin, err := os.ReadFile(buildDemoProvider(t))
	if err != nil {
		t.Fatal(err)
	}
	name := "terraform-provider-demo_v1.0.0"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(bin)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The source of the provider names a host that no one can reach, as a registry that a platform
// without egress cannot reach. A run that read from it would fail.
const unreachableSource = "registry.invalid/acme/demo"

// A mirror changes where a provider's archive is downloaded from and nothing else: the import
// path, the key of the lock file and the provider of the state records stay the source's.
func TestProviderMirrorInstallsFromTheMirrorAndChangesNothingElse(t *testing.T) {
	t.Setenv("PRAVO_HOME", t.TempDir())
	rt := &runContext{}
	host := ociRegistry(t)
	z := mirrorZip(t)
	plat := runtime.GOOS + "_" + runtime.GOARCH
	sums := pushProviderArchives(t, host, "mirror/acme/demo", "1.0.0", map[string][]byte{plat: z})
	importPath := "pravo.dev/providers/registry.invalid/acme/demo"
	spec := "package estate\n\nimport demo \"" + importPath + "\"\n\n$demo: demo.#Provider & {$version: \"1.0.0\"}\n\nsrv: $demo.#Resource.demo_server & {name: \"a\"}\n"
	lock := "providers: \"" + unreachableSource + "\": {\n\tversion: \"1.0.0\"\n\thashes: \"" + plat + "\": \"" + sums[plat] + "\"\n}\n"
	dir := specDir(t, map[string]string{"main.cue": spec, "pravo.lock.cue": lock})
	t.Setenv("PRAVO_PROVIDER_MIRROR", "oci://"+host+"/mirror")

	for _, args := range [][]string{{"init"}, {"plan"}, {"apply", "--auto-approve"}} {
		if out, code := runPravo(t, rt, dir, args...); code != 0 {
			t.Fatalf("pravo %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
	}
	// Running again changes nothing: not the lock file, the import, or the state.
	locked := readFile(t, dir, "pravo.lock.cue")
	if !strings.Contains(locked, `"`+unreachableSource+`"`) || !strings.Contains(locked, sums[plat]) || strings.Contains(locked, "oci://") {
		t.Fatalf("the lock file is not keyed by the source, or lost its hash:\n%s", locked)
	}
	for _, args := range [][]string{{"init"}, {"plan"}} {
		if out, code := runPravo(t, rt, dir, args...); code != 0 {
			t.Fatalf("pravo %s again: exit %d\n%s", strings.Join(args, " "), code, out)
		}
	}
	if again := readFile(t, dir, "pravo.lock.cue"); again != locked {
		t.Errorf("the lock file changed:\n--- before\n%s\n--- after\n%s", locked, again)
	}
	if got := readFile(t, dir, "main.cue"); got != spec {
		t.Errorf("the specification changed:\n%s", got)
	}
	out, code := runPravo(t, rt, dir, "plan")
	if code != 0 || !strings.Contains(out, "No changes") {
		t.Errorf("plan after the apply: exit %d\n%s", code, out)
	}
	// The state names the provider by its source.
	found := false
	err := filepath.WalkDir(filepath.Join(dir, "pravo.state"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "oci://") {
			t.Errorf("%s names the mirror", p)
		}
		if strings.Contains(string(b), unreachableSource) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Errorf("no state record names %s", unreachableSource)
	}
}

// The archive of a mirror is installed only when the lock file pins its hash.
func TestProviderMirrorRefusesWhatTheLockFileDoesNotPin(t *testing.T) {
	rt := &runContext{}
	host := ociRegistry(t)
	z := mirrorZip(t)
	plat := runtime.GOOS + "_" + runtime.GOARCH
	sums := pushProviderArchives(t, host, "mirror/acme/demo", "1.0.0", map[string][]byte{plat: z})
	spec := "package estate\n\nimport demo \"pravo.dev/providers/registry.invalid/acme/demo\"\n\n$demo: demo.#Provider & {$version: \"1.0.0\"}\n"
	wrong := "sha256:" + hex.EncodeToString(func() []byte { s := sha256.Sum256([]byte("other")); return s[:] }())
	for name, c := range map[string]struct{ lock, want string }{
		"no lock file":     {"", `pins no hash for ` + plat},
		"another version":  {`providers: "` + unreachableSource + `": {version: "0.9.0", hashes: "` + plat + `": "` + sums[plat] + `"}`, `pins no hash for ` + plat},
		"another platform": {`providers: "` + unreachableSource + `": {version: "1.0.0", hashes: "plan9_mips": "` + sums[plat] + `"}`, `pins no hash for ` + plat},
		"another archive":  {`providers: "` + unreachableSource + `": {version: "1.0.0", hashes: "` + plat + `": "` + wrong + `"}`, "The mirror holds other bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PRAVO_HOME", t.TempDir())
			files := map[string]string{"main.cue": spec}
			if c.lock != "" {
				files["pravo.lock.cue"] = c.lock
			}
			dir := specDir(t, files)
			out, code := runPravo(t, rt, dir, "init", "--provider-mirror", "oci://"+host+"/mirror")
			if code == 0 || !strings.Contains(out, c.want) {
				t.Fatalf("exit %d, want an error with %q:\n%s", code, c.want, out)
			}
			if strings.Contains(out, "registry.invalid") && strings.Contains(out, "no such host") {
				t.Errorf("the source was asked:\n%s", out)
			}
		})
	}
}

func TestProvidersMirrorNeedsALockedProviderAndAPrefix(t *testing.T) {
	rt := &runContext{}
	dir := specDir(t, map[string]string{"main.cue": "package estate\n"})
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"providers", "mirror", "registry.example.com/x"}, "want oci://host/path"},
		{[]string{"providers", "mirror", "oci://localhost:5/mirror"}, "locks no provider"},
	} {
		out, code := runPravo(t, rt, dir, c.args...)
		if code == 0 {
			t.Errorf("pravo %s succeeded:\n%s", strings.Join(c.args, " "), out)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("pravo %s: no %q in\n%s", strings.Join(c.args, " "), c.want, out)
		}
	}
	// A mirror that does not parse stops a command that reads providers.
	spec := "package estate\n\nimport demo \"pravo.dev/providers/registry.invalid/acme/demo\"\n\n$demo: demo.#Provider & {$version: \"1.0.0\"}\n"
	dir = specDir(t, map[string]string{"main.cue": spec, "pravo.lock.cue": "providers: \"" + unreachableSource + "\": {version: \"1.0.0\", hashes: linux_amd64: \"sha256:" + strings.Repeat("0", 64) + "\"}\n"})
	out, code := runPravo(t, rt, dir, "init", "--provider-mirror", "nonsense")
	if code == 0 || !strings.Contains(out, "PRAVO_PROVIDER_MIRROR") || !strings.Contains(out, "want oci://host/path") {
		t.Errorf("init with a bad mirror: exit %d\n%s", code, out)
	}
}
