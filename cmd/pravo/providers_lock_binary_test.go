package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// zipWithExecutable is a provider archive whose executable holds body.
func zipWithExecutable(t *testing.T, typ, ver, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	name := "terraform-provider-" + typ + "_v" + ver
	if runtime.GOOS == "windows" {
		name += ".exe" // the program of a Windows archive is named so
	}
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(body))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// providers lock writes the digest of the executable of every platform, not only of the one
// it runs on, so that the first init on another kind of machine leaves the lock file as it is.
func TestProvidersLockPinsTheExecutablesOfEveryPlatform(t *testing.T) {
	t.Setenv("PRAVO_HOME", t.TempDir())
	rt := &runContext{}
	host := ociRegistry(t)
	plats := []string{"linux_amd64", "darwin_arm64"}
	if host := runtime.GOOS + "_" + runtime.GOARCH; !slices.Contains(plats, host) {
		plats = append(plats, host) // providers lock always covers the platform it runs on
	}
	archives := map[string][]byte{}
	want := map[string]string{}
	for _, p := range plats {
		body := "#!/bin/sh\n# the build for " + p + "\n"
		archives[p] = zipWithExecutable(t, "thing", "1.2.3", body)
		sum := sha256.Sum256([]byte(body))
		want[p] = "sha256:" + hex.EncodeToString(sum[:])
	}
	sums := pushProviderArchives(t, host, "acme/thing", "1.2.3", archives)
	pkg := "oci/" + strings.ReplaceAll(host, ":", "_") + "/acme/thing"
	stub := "package thing\n\n#Provider: {\n\t$kind:   \"provider\"\n\t$source: \"oci://" + host + "/acme/thing\"\n\t$version?: string\n\t$from?: string\n\t$path?: string\n\t$protocol?: string\n\t$address?: string\n}\n"
	settings := "package estate\n\nimport \"pravo.dev/pravo\"\n\n$settings: pravo.#Settings & {state: path: \".pravo/state\"}\n"
	dir := specDir(t, map[string]string{"main.cue": settings})
	if out, code := runPravo(t, rt, dir, "init"); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, out)
	}
	write := func(name, text string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cue.mod/gen/pravo.dev/providers/"+pkg+"/thing_gen.cue", stub)
	write("main.cue", strings.Replace(settings, `import "pravo.dev/pravo"`, "import (\n\t\"pravo.dev/pravo\"\n\tthing \"pravo.dev/providers/"+pkg+"\"\n)", 1)+"\n$thing: thing.#Provider & {$version: \"1.2.3\"}\n")
	lockText := func() string {
		b, err := os.ReadFile(filepath.Join(dir, "pravo.lock.cue"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(b)), " ") // the columns are padded
	}

	// Without --binaries the archives are pinned and nothing else.
	if out, code := runPravo(t, rt, dir, "providers", "lock", "--platforms", strings.Join(plats, ",")); code != 0 {
		t.Fatalf("providers lock: exit %d\n%s", code, out)
	}
	// An init writes the line of the platform it runs on, and that stays as it was.
	for _, p := range plats {
		if p != runtime.GOOS+"_"+runtime.GOARCH && strings.Contains(lockText(), `"binary:`+p+`"`) {
			t.Errorf("providers lock without --binaries wrote the binary line of %s:\n%s", p, lockText())
		}
	}
	out, code := runPravo(t, rt, dir, "providers", "lock", "--binaries", "--platforms", strings.Join(plats, ","))
	if code != 0 {
		t.Fatalf("providers lock: exit %d\n%s", code, out)
	}
	others := 0
	for _, p := range plats {
		if p != runtime.GOOS+"_"+runtime.GOARCH {
			others++
		}
	}
	if !strings.Contains(out, "Pinned "+plural(others, "digest", "digests")+" of the provider executables") {
		t.Errorf("the output does not say what was pinned:\n%s", out)
	}
	for _, p := range plats {
		for _, line := range []string{`"binary:` + p + `": "` + want[p] + `"`, `"` + p + `": "` + sums[p] + `"`} {
			if !strings.Contains(lockText(), line) {
				t.Errorf("the lock lacks %s:\n%s", line, lockText())
			}
		}
	}
	// A second run with --binaries finds the lines and downloads nothing: the text does not change.
	before := lockText()
	if out, code := runPravo(t, rt, dir, "providers", "lock", "--binaries", "--platforms", strings.Join(plats, ",")); code != 0 || strings.Contains(out, "digests of the provider executables") {
		t.Errorf("second run: exit %d\n%s", code, out)
	}
	if lockText() != before {
		t.Errorf("a second run changed the lock file:\n%s", lockText())
	}
}
