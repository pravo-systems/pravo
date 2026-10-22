package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestFingerprint(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.cue", "package x\na: 1\n")
	write("cue.mod/module.cue", "module: \"x.y\"\n")
	f1, err := Fingerprint(dir)
	if err != nil || len(f1) != 32 {
		t.Fatalf("fingerprint: %q %v", f1, err)
	}
	f2, _ := Fingerprint(dir)
	if f1 != f2 {
		t.Error("fingerprint is not stable")
	}
	write("main.cue", "package x\na: 2\n")
	if f3, _ := Fingerprint(dir); f3 == f1 {
		t.Error("fingerprint did not change with the configuration")
	}
	write("cue.mod/gen/p/p.cue", "package p\n")
	if f4, _ := Fingerprint(dir); f4 == f2 {
		t.Error("fingerprint ignores cue.mod")
	}
	write("notes.txt", "ignored")
	before, _ := Fingerprint(dir)
	write("notes.txt", "still ignored")
	if after, _ := Fingerprint(dir); after != before {
		t.Error("non-CUE files must not affect the fingerprint")
	}
}

// defs is a stand-in for generated provider packages: a provider definition
// with its resource, data source and function definitions nested inside, each
// carrying the identity fields pravo looks for.
const defs = `
#Local: {
	$kind: "provider", $source: "hashicorp/local"
	$version?:  string
	$protocol?: string
	region?:    string
	#Resource: {
		local_file: {
			$kind: "resource", $type: "local_file", $source: "hashicorp/local"
			$depends_on?: [..._]
			$from?:       string
			$import?:     string
			filename:     string
			content?:     string
			id:           string | *null
		}
	}
	#Data: {
		local_file: {
			$kind: "data", $type: "local_file", $source: "hashicorp/local"
			$from?:   string
			$import?: string
			filename: string
			content:  string | *null
		}
	}
}
#Random: {
	$kind: "provider", $source: "hashicorp/random"
	#Resource: {
		random_pet: {
			$kind: "resource", $type: "random_pet", $source: "hashicorp/random"
			length?: int
			id:      string | *null
		}
	}
}
$local:  #Local
$random: #Random
#Output: {$kind: "output", value: _, description?: string, sensitive?: bool}
#Env: {$kind: "env", name: string, default?: string, value: string}
#SecretEnv: {$kind: "secret_env", name: string, value: string}
#RemoteOutputs: {$kind: "remote_outputs", state: {path?: string, backend?: string, bucket?: string, prefix?: string}, outputs: {...}}
`

func loadSpec(t *testing.T, body string) (*Root, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.cue"), []byte("package x\n"+defs+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir, LoadOptions{})
}

func TestDiscoverByMarker(t *testing.T) {
	r, err := loadSpec(t, `
// a module-like definition instantiated twice, and a list of resources
#Service: {
	name: string
	pet:  $random.#Resource.random_pet & {length: 2}
	conf: $local.#Resource.local_file & {filename: "\(name).conf", content: pet.id}
}
api: #Service & {name: "api"}
web: #Service & {name: "web"}
servers: [$local.#Resource.local_file & {filename: "a"}, $local.#Resource.local_file & {filename: "b"}]
motd:    $local.#Resource.local_file & {filename: "motd"}
info:    $local.#Data.local_file & {filename: "info"}
plain:   {not: "a resource", nested: {deeper: 1}}
`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, res := range r.Resources {
		got[res.Address()] = res.Mode + " " + res.Type + " " + res.ProviderKey
	}
	want := map[string]string{
		"api.pet":    "managed random_pet random",
		"api.conf":   "managed local_file local",
		"web.pet":    "managed random_pet random",
		"web.conf":   "managed local_file local",
		"servers[0]": "managed local_file local",
		"servers[1]": "managed local_file local",
		"motd":       "managed local_file local",
		"info":       "data local_file local",
	}
	if len(got) != len(want) {
		t.Errorf("found %d resources, want %d: %v", len(got), len(want), got)
	}
	for addr, w := range want {
		if got[addr] != w {
			t.Errorf("%s = %q, want %q", addr, got[addr], w)
		}
	}
	if len(r.ProviderDecls) != 2 {
		t.Errorf("%d providers, want 2", len(r.ProviderDecls))
	}
}

func TestProviderStructsAndInstanceKeys(t *testing.T) {
	// A provider's version requirement is recorded. The shared definitions
	// already declare one local provider, so this makes two and the key
	// carries the path.
	r, err := loadSpec(t, `
files: #Local & {$version: "~> 2.5", region: "x"}
motd:  files.#Resource.local_file & {filename: "motd"}
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resource("motd").ProviderKey; got != "local.files" {
		t.Errorf("provider key = %q, want local.files", got)
	}
	if req := r.Providers["local"]; req == nil || req.Version != "~> 2.5" {
		t.Errorf("requirement = %+v", req)
	}

	// Two providers of one source: each resource uses the one it was built
	// through, and the keys carry the path.
	r, err = loadSpec(t, `
eu: #Local & {region: "eu"}
us: #Local & {region: "us"}
a: eu.#Resource.local_file & {filename: "a"}
b: us.#Resource.local_file & {filename: "b"}
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Resource("a").ProviderKey != "local.eu" || r.Resource("b").ProviderKey != "local.us" {
		t.Errorf("keys = %q %q", r.Resource("a").ProviderKey, r.Resource("b").ProviderKey)
	}
	if pp, ok := r.ProviderPath("local.eu"); !ok || pp.String() != "eu" {
		t.Errorf("provider path = %v %v", pp, ok)
	}

	// A provider passed into a definition shares the caller's instance
	// instead of becoming a second one, and the resource built through the
	// input belongs to the caller's provider.
	r, err = loadSpec(t, `
eu: #Local & {region: "eu"}
us: #Local & {region: "us"}
#M: {
	provider: #Local
	f:        provider.#Resource.local_file & {filename: "m"}
}
m: #M & {provider: eu}
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resource("m.f").ProviderKey; got != "local.eu" {
		t.Errorf("m.f uses %q, want the caller's local.eu", got)
	}
	if n := len(r.ProviderDecls); n != 4 { // $local, $random, eu, us
		t.Errorf("%d provider instances, want 4 (the module's input is an alias, not a new one)", n)
	}

	// The definition reached through an intermediate field still finds its
	// provider.
	r, err = loadSpec(t, `
eu:  #Local & {region: "eu"}
us:  #Local & {region: "us"}
def: eu.#Resource.local_file
b:   def & {filename: "b"}
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resource("b").ProviderKey; got != "local.eu" {
		t.Errorf("b uses %q, want local.eu through def", got)
	}

	// A resource has to be built through a provider.
	if _, err := loadSpec(t, `
#File: {$kind: "resource", $type: "local_file", $source: "hashicorp/local", filename: string}
x: #File & {filename: "a"}
`); err == nil || !strings.Contains(err.Error(), "was not built through a provider") {
		t.Errorf("a resource outside a provider must be rejected, got %v", err)
	}
	// One version per provider.
	if _, err := loadSpec(t, `
a: #Local & {$version: "~> 1.0"}
b: #Local & {$version: "~> 2.0"}
`); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("conflicting versions must be rejected, got %v", err)
	}
}

func TestNestedResourceIsAnError(t *testing.T) {
	_, err := loadSpec(t, `outer: {$kind: "resource", $type: "local_file", $source: "hashicorp/local", inner: $local.#Resource.local_file & {filename: "b"}}`)
	if err == nil || !strings.Contains(err.Error(), "cannot contain") {
		t.Errorf("expected a nested resource to be rejected, got %v", err)
	}
	// $depends_on may point at other resources.
	if _, err := loadSpec(t, `
a: $local.#Resource.local_file & {filename: "a"}
b: $local.#Resource.local_file & {filename: "b", $depends_on: [a]}
`); err != nil {
		t.Errorf("$depends_on was treated as a nested resource: %v", err)
	}
}

func TestMovesAndImportsAreProperties(t *testing.T) {
	r, err := loadSpec(t, `
greeting: $local.#Resource.local_file & {filename: "a", $from: "hello", $import: "5"}
#Group: {
	$from?: string
	one:    $local.#Resource.local_file & {filename: "1"}
}
group: #Group & {$from: "old"}
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Moves) != 2 {
		t.Fatalf("moves = %+v", r.Moves)
	}
	var single, prefix *Move
	for i := range r.Moves {
		if r.Moves[i].Prefix {
			prefix = &r.Moves[i]
		} else {
			single = &r.Moves[i]
		}
	}
	if single == nil || single.From != "hello" || single.To != "greeting" {
		t.Errorf("single move = %+v", single)
	}
	if prefix == nil || prefix.From != "old" || prefix.To != "group" {
		t.Errorf("prefix move = %+v", prefix)
	}
	if imp := r.Imports["greeting"]; imp == nil || imp.ID != "5" {
		t.Errorf("imports = %+v", r.Imports)
	}
	for name, bad := range map[string]string{
		"from is the resource itself": `x: $local.#Resource.local_file & {filename: "a", $from: "x"}`,
		"group without resources":     `#G: {$from?: string}` + "\n" + `g: #G & {$from: "old"}`,
		"import of a data source":     `d: $local.#Data.local_file & {filename: "i", $import: "y"}`,
	} {
		if _, err := loadSpec(t, bad); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestUnknownPravoSettingIsAnError(t *testing.T) {
	if _, err := loadSpec(t, `$settings: refesh: "1h"`); err == nil || !strings.Contains(err.Error(), "is not a setting") {
		t.Errorf("expected a misspelt setting to be rejected, got %v", err)
	}
}

func TestStateBackendFields(t *testing.T) {
	dir := t.TempDir()
	load := func(state string) (*Root, error) {
		if err := os.WriteFile(filepath.Join(dir, "main.cue"), []byte("package x\n"+state+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return Load(dir, LoadOptions{})
	}
	if r, err := load(`$settings: state: {backend: "s3", bucket: "b", prefix: "p"}`); err != nil || r.State.Backend != "s3" || r.State.Bucket != "b" {
		t.Errorf("s3: %+v %v", r, err)
	}
	if r, err := load(`$settings: state: bucket: "b"`); err != nil || r.State.Backend != "s3" {
		t.Errorf("a bucket alone must mean s3: %+v %v", r, err)
	}
	if r, err := load(`$settings: state: path: "s"`); err != nil || r.StatePath != "s" || r.State.Backend != "" {
		t.Errorf("local path: %+v %v", r, err)
	}
	for name, bad := range map[string]string{
		"s3 without a bucket": `$settings: state: backend: "s3"`,
		"s3 field with local": `$settings: state: {backend: "local", region: "eu"}`,
		"unknown backend":     `$settings: state: {backend: "gcs", bucket: "b"}`,
		"local path with s3":  `$settings: state: {backend: "s3", bucket: "b", path: "x"}`,
		"s3 field without s3": `$settings: state: {path: "s", profile: "p"}`,
	} {
		if _, err := load(bad); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSettingsReadAnEnvironmentVariable(t *testing.T) {
	const spec = `
dir:         #Env & {name: "PRAVO_TEST_SETTINGS_DIR", default: "state/dev"}
stateBucket: #Env & {name: "PRAVO_TEST_SETTINGS_BUCKET"}
`
	t.Run("the default", func(t *testing.T) {
		r, err := loadSpec(t, spec+`$settings: state: path: dir.value`)
		if err != nil {
			t.Fatal(err)
		}
		if r.StatePath != "state/dev" {
			t.Errorf("path = %q", r.StatePath)
		}
	})
	t.Run("the variable", func(t *testing.T) {
		t.Setenv("PRAVO_TEST_SETTINGS_DIR", "state/prod")
		r, err := loadSpec(t, spec+`$settings: state: path: dir.value`)
		if err != nil {
			t.Fatal(err)
		}
		if r.StatePath != "state/prod" {
			t.Errorf("path = %q", r.StatePath)
		}
	})
	t.Run("a bucket", func(t *testing.T) {
		t.Setenv("PRAVO_TEST_SETTINGS_BUCKET", "estate")
		r, err := loadSpec(t, spec+`$settings: state: {bucket: stateBucket.value, prefix: "demo/\(dir.value)"}`)
		if err != nil {
			t.Fatal(err)
		}
		if r.State.Backend != "s3" || r.State.Bucket != "estate" || r.State.Prefix != "demo/state/dev" {
			t.Errorf("state = %+v", r.State)
		}
	})
	t.Run("a variable that is not set", func(t *testing.T) {
		_, err := loadSpec(t, spec+`$settings: state: bucket: stateBucket.value`)
		if err == nil || !strings.Contains(err.Error(), "$settings.state.bucket is not concrete") || !strings.Contains(err.Error(), "PRAVO_TEST_SETTINGS_BUCKET, which is not set and has no default") {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("a secret", func(t *testing.T) {
		t.Setenv("PRAVO_TEST_SETTINGS_SECRET", "x")
		_, err := loadSpec(t, `
key: #SecretEnv & {name: "PRAVO_TEST_SETTINGS_SECRET"}
$settings: state: path: key.value`)
		if err == nil || !strings.Contains(err.Error(), "is not concrete") {
			t.Errorf("a secret must not be read into a setting: %v", err)
		}
	})
}

func TestOutputsAreFoundByMarker(t *testing.T) {
	r, err := loadSpec(t, `
#Service: {
	pet: $random.#Resource.random_pet & {length: 2}
	url: #Output & {value: pet.length}
}
api:  #Service
file: $local.#Resource.local_file & {filename: "p"}
port: #Output & {value: file.id, description: "where"}
`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range r.Outputs {
		names = append(names, o.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "api.url,port" {
		t.Errorf("outputs = %v", names)
	}
	if len(r.Resources) != 2 {
		t.Errorf("an output must not be a resource: %v", r.Resources)
	}
	if _, err := loadSpec(t, `
file: $local.#Resource.local_file & {filename: "p", inner: #Output & {value: 1}}
`); err == nil || !strings.Contains(err.Error(), "file") {
		t.Errorf("an output inside a resource should be rejected, got %v", err)
	}
}

func TestLoadSettingsReadsReferencesAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.cue", "package t\n\nimport rnd \"pravo.dev/providers/hashicorp/random\"\n\n$random: rnd.#Provider\n\n$settings: state: path: \"state/\\(env)\"\n\npet: $random.#Resource.random_pet & {length: 2}\n")
	write("env.cue", "package t\n\nenv: *\"dev\" | \"prod\" @tag(env)\n")
	r, err := LoadSettings(dir)
	if err != nil {
		t.Fatalf("a pravo block that refers to a field in another file must be readable: %v", err)
	}
	if r.StatePath != "state/dev" {
		t.Errorf("state path = %q", r.StatePath)
	}
	if r.Providers["random"] == nil {
		t.Errorf("providers = %+v; want random from the import", r.Providers)
	}
}

func TestLoadSettingsDropsImportedDefinitionsInsideTheBlock(t *testing.T) {
	dir := t.TempDir()
	src := `package t

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

_vars: {region: "eu-north-1"}

$settings: pravo.#Settings & {
	state:   pravo.#S3State & {bucket: "b", prefix: "demo/app", region: _vars.region}
	refresh: "5m"
}

$random: rnd.#Provider
`
	if err := os.WriteFile(filepath.Join(dir, "main.cue"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := LoadSettings(dir)
	if err != nil {
		t.Fatalf("an imported definition inside the pravo block must not stop init: %v", err)
	}
	if r.State.Backend != "s3" || r.State.Bucket != "b" || r.State.Region != "eu-north-1" || r.Refresh != "5m" {
		t.Errorf("settings = state %+v refresh %q", r.State, r.Refresh)
	}
}

func TestSettingsAreCheckedWithoutTheSchema(t *testing.T) {
	for name, tc := range map[string]struct{ spec, frag string }{
		"refresh age":     {`$settings: refresh: "soon"`, "is not a refresh age"},
		"unknown backend": {`$settings: state: {backend: "gcs", bucket: "b"}`, "unknown backend"},
	} {
		if _, err := loadSpec(t, tc.spec); err == nil || !strings.Contains(err.Error(), tc.frag) {
			t.Errorf("%s: error %v, want %q", name, err, tc.frag)
		}
	}
	if _, err := loadSpec(t, `$settings: refresh: "30m"`); err != nil {
		t.Errorf("a valid refresh was rejected: %v", err)
	}
}

func TestEnvAndSensitiveOutput(t *testing.T) {
	r, err := loadSpec(t, `
secret: #SecretEnv & {name: "DB_PASSWORD"}
region: #Env & {name: "REGION"}
pet:    $random.#Resource.random_pet & {length: 2}
token:  #Output & {value: pet.length, sensitive: true}
plain:  #Output & {value: pet.length}
`)
	if err != nil {
		t.Fatal(err)
	}
	var env *Resource
	for _, res := range r.Resources {
		if res.Address() == "secret" {
			env = res
		}
	}
	if env == nil || !env.Builtin || env.Mode != "ephemeral" || env.ProviderKey != "" {
		t.Errorf("a sensitive env must be ephemeral: %+v", env)
	}
	for _, res := range r.Resources {
		if res.Address() == "region" && (!res.Builtin || res.Mode != "builtin") {
			t.Errorf("a plain env must not be ephemeral: %+v", res)
		}
	}
	got := map[string]bool{}
	for _, o := range r.Outputs {
		got[o.Name] = o.Sensitive
	}
	if !got["token"] || got["plain"] || len(got) != 2 {
		t.Errorf("outputs = %v", got)
	}
}

func TestRemoteOutputs(t *testing.T) {
	r, err := loadSpec(t, `
local: #RemoteOutputs & {state: path: "../net/pravo.state"}
shared: #RemoteOutputs & {state: {backend: "s3", bucket: "b", prefix: "net"}}
`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*Resource{}
	for _, res := range r.Resources {
		got[res.Address()] = res
	}
	if l := got["local"]; l == nil || !l.Builtin || l.Mode != "builtin" || l.Remote == nil || !strings.HasSuffix(filepath.ToSlash(l.Remote.Dir), "net/pravo.state") {
		t.Errorf("local = %+v", l)
	}
	if s := got["shared"]; s == nil || s.Remote == nil || s.Remote.State.Backend != "s3" || s.Remote.State.Bucket != "b" {
		t.Errorf("shared = %+v", s)
	}
	if _, err := loadSpec(t, `bad: #RemoteOutputs & {state: {}}`); err == nil || !strings.Contains(err.Error(), "needs a path") {
		t.Errorf("a state with neither path nor bucket should be rejected, got %v", err)
	}
}

// Without a loaded root the hashes list the CUE files of the directory and of
// cue.mod, as the fingerprint digests them.
func TestFileHashesOfADirectory(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"main.cue": "package x\na: 1\n", "cue.mod/module.cue": "module: \"x.y\"\n", "cue.mod/gen/p/p.cue": "package p\n", "notes.txt": "ignored"} {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := FileHashes(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := range h {
		names = append(names, n)
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "cue.mod/gen/p/p.cue,cue.mod/module.cue,main.cue" {
		t.Errorf("files %s", got)
	}
}
