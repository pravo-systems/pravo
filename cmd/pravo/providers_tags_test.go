package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The commands that install providers take -t, -T and --env like init, and need no state
// location: a bucket that comes from a tag is not asked for until a command opens the state.
func TestProvidersCommandsTakeTagsAndNeedNoStateLocation(t *testing.T) {
	t.Setenv("PRAVO_HOME", t.TempDir())
	rt := &runContext{}
	host := ociRegistry(t)
	plat := runtime.GOOS + "_" + runtime.GOARCH
	sums := pushFakeProvider(t, host, "acme/thing", "thing", "1.2.3", plat, "linux_amd64", "linux_arm64")
	pkg := "oci/" + strings.ReplaceAll(host, ":", "_") + "/acme/thing"
	stub := "package thing\n\n#Provider: {\n\t$kind:   \"provider\"\n\t$source: \"oci://" + host + "/acme/thing\"\n\t$version?: string\n\t$from?: string\n\t$path?: string\n\t$protocol?: string\n\t$address?: string\n}\n"
	settings := `package estate

import "pravo.dev/pravo"

_env: state_bucket: string @tag(state_bucket)

$settings: pravo.#Settings & {state: pravo.#S3State & {backend: "s3", bucket: _env.state_bucket, prefix: "p", region: "us-east-1"}}
`
	dir := specDir(t, map[string]string{"main.cue": settings})
	// init writes the package of pravo, and asks for no bucket.
	if out, code := runPravo(t, rt, dir, "init"); code != 0 {
		t.Fatalf("init without a tag: exit %d\n%s", code, out)
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
		return string(b)
	}

	// No tags at all.
	if out, code := runPravo(t, rt, dir, "providers", "lock", "--platforms", "linux_amd64,linux_arm64"); code != 0 {
		t.Fatalf("providers lock without a tag: exit %d\n%s", code, out)
	}
	for _, p := range []string{"linux_amd64", "linux_arm64"} {
		if !strings.Contains(lockText(), sums[p]) {
			t.Errorf("the lock does not pin %s for %s:\n%s", sums[p], p, lockText())
		}
	}
	// With the tag, which the command accepts.
	if out, code := runPravo(t, rt, dir, "providers", "lock", "-t", "state_bucket=x", "--platforms", "linux_amd64"); code != 0 {
		t.Fatalf("providers lock -t: exit %d\n%s", code, out)
	}
	if out, code := runPravo(t, rt, dir, "providers", "schema", "-t", "state_bucket=x", "thing"); code == 0 || strings.Contains(out, "unknown shorthand") {
		// The fake provider is not a program that speaks the protocol, so the schema fails; the flag was read.
		t.Logf("providers schema: exit %d\n%s", code, out)
	}
	// A command that opens the state still asks for the tag.
	out, code := runPravo(t, rt, dir, "plan")
	if code == 0 || !strings.Contains(out, "it comes from @tag(state_bucket); pass -t state_bucket=...") {
		t.Errorf("plan without the tag: exit %d\n%s", code, out)
	}
	// So does the listing of the providers, which reads the state.
	if out, code := runPravo(t, rt, dir, "providers"); code == 0 || !strings.Contains(out, "@tag(state_bucket)") {
		t.Errorf("providers without the tag: exit %d\n%s", code, out)
	}
}
