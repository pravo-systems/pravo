package config

import (
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/schema"
)

const ladderSpec = `package app

image: string | *"1.0" @tag(image)

$environments: {
	dev:     {workers: 1, $default: true}
	staging: {workers: 2, $after: "dev"}
	prod:    {workers: 3, $after: "staging"}
}

$settings: state: path: "state/\($environment.$name)"

out: "\($environment.$name)-\($environment.workers)-\(image)"
`

func outOf(t *testing.T, r *Root) string {
	t.Helper()
	s, err := r.Value.LookupPath(cue.ParsePath("out")).String()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// -t env=NAME picks a row, $environment holds it with $name, and the selector is not a tag
// of the specification.
func TestEnvironmentSelects(t *testing.T) {
	dir := writeFiles(t, map[string]string{"main.cue": ladderSpec})
	r, err := Load(dir, LoadOptions{Tags: []string{"env=prod", "image=2"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := outOf(t, r); got != "prod-3-2" {
		t.Errorf("out = %q", got)
	}
	if r.Environment != "prod" || strings.Join(r.EnvironmentNames(), ",") != "dev,staging,prod" {
		t.Errorf("environment %q of %v", r.Environment, r.EnvironmentNames())
	}
	if want := filepath.Join(dir, "state", "prod"); r.StateFile() != want {
		t.Errorf("state %s, want %s", r.StateFile(), want)
	}
	for _, s := range r.Sources {
		if strings.HasSuffix(s, envOverlayName) {
			t.Errorf("the filled file is a source: %v", r.Sources)
		}
	}
	if _, err := r.Fingerprint(); err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	// The default row.
	r, err = Load(dir, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := outOf(t, r); got != "dev-1-1.0" {
		t.Errorf("default out = %q", got)
	}
	// The settings alone.
	s, err := LoadSettingsWith(dir, LoadOptions{Tags: []string{"env=staging"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "state", "staging"); s.StateFile() != want || s.Environment != "staging" {
		t.Errorf("settings state %s (%s), want %s", s.StateFile(), s.Environment, want)
	}
}

// The fingerprint does not depend on the environment: staging and prod run one
// specification.
func TestEnvironmentFingerprint(t *testing.T) {
	dir := writeFiles(t, map[string]string{"main.cue": ladderSpec})
	var fps []string
	for _, env := range []string{"staging", "prod"} {
		r, err := Load(dir, LoadOptions{Tags: []string{"env=" + env}})
		if err != nil {
			t.Fatal(err)
		}
		fp, err := r.Fingerprint()
		if err != nil {
			t.Fatal(err)
		}
		fps = append(fps, fp)
	}
	if fps[0] != fps[1] {
		t.Errorf("fingerprints differ: %v", fps)
	}
}

func TestEnvironmentErrors(t *testing.T) {
	for _, c := range []struct {
		name, envs, settings string
		tags                 []string
		want                 string
	}{
		{"unknown", `dev: {$default: true}, prod: {}`, "", []string{"env=qa"}, `"qa" is not an environment of this specification; the environments are dev and prod`},
		{"no default", `dev: {}, prod: {}`, "", nil, "name the environment with --env NAME: this specification declares dev and prod, and none is marked $default: true"},
		{"two defaults", `dev: {$default: true}, prod: {$default: true}`, "", nil, "dev and prod are each marked $default: true"},
		{"after typo", `dev: {$default: true}, prod: {$after: "stagin"}`, "", nil, `$environments.prod.$after names "stagin", which is not an environment (they are dev and prod)`},
		{"self", `dev: {$default: true, $after: "dev"}`, "", nil, "$environments.dev.$after names dev itself"},
		{"circle", `a: {$after: "b", $default: true}, b: {$after: "a"}, c: {}`, "", nil, "a and b follow one another in a circle"},
		{"circle with a follower", `x: {$after: "b"}, a: {$after: "b", $default: true}, b: {$after: "a"}, c: {}`, "", nil, "$environments: a and b follow one another in a circle"},
		{"bad name", `"my env": {$default: true}`, "", nil, "an environment is named with letters"},
		{"empty list", `dev: {$default: true}, prod: {$after: []}`, "", nil, "$after is an empty list"},
		{"shared state", `dev: {$default: true}, prod: {}`, `$settings: state: path: "state"`, nil, "the environments dev and prod keep their state in one place"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"main.cue": "package app\n\n$environments: {" + c.envs + "}\n" + c.settings + "\n"})
			_, err := Load(dir, LoadOptions{Tags: c.tags})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

// A field that is a tag named env competes with the selector, and is refused.
func TestEnvironmentRefusesEnvTag(t *testing.T) {
	dir := writeFiles(t, map[string]string{"main.cue": `package app

env: *"dev" | "prod" @tag(env)
$environments: {dev: {$default: true}, prod: {}}
$settings: state: path: "state/\(env)"
`})
	_, err := Load(dir, LoadOptions{Tags: []string{"env=prod"}})
	if err == nil || !strings.Contains(err.Error(), "carries @tag(env)") {
		t.Fatalf("got %v", err)
	}
}

// A specification without $environments keeps its tags as they are: env is its own tag.
func TestNoEnvironmentsKeepsTheTag(t *testing.T) {
	dir := writeFiles(t, map[string]string{"main.cue": `package app

env: *"dev" | "prod" @tag(env)
x: {$environments: 1}
$settings: state: path: "state/\(env)"
`})
	r, err := Load(dir, LoadOptions{Tags: []string{"env=prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Environments != nil || r.StateFile() != filepath.Join(dir, "state", "prod") {
		t.Fatalf("envs %v, state %s", r.Environments, r.StateFile())
	}
}

// Fan-out and fan-in: two environments after one, and one after two.
func TestEnvironmentLadderOrder(t *testing.T) {
	dir := writeFiles(t, map[string]string{"main.cue": `package app

$environments: {
	"prod-eu": {$after: "staging"}
	"prod-us": {$after: "staging"}
	staging:   {$after: "dev"}
	dev:       {$default: true}
	audit:     {$after: ["prod-eu", "prod-us"]}
}
$settings: state: path: "state/\($environment.$name)"
`})
	r, err := Load(dir, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.EnvironmentNames(), ","); got != "dev,staging,prod-eu,prod-us,audit" {
		t.Errorf("order %s", got)
	}
	if got := FlowText(r.Environments); got != "dev → staging → {prod-eu, prod-us} → audit" {
		t.Errorf("ladder %q", got)
	}
	if got := strings.Join(r.Followers("staging"), ","); got != "prod-eu,prod-us" {
		t.Errorf("followers %s", got)
	}
}

// Each environment can name its readers file and the source of its credentials.
func TestEnvironmentSettings(t *testing.T) {
	dir := writeFiles(t, map[string]string{"main.cue": `package app

$environments: {
	staging: {$default: true, profile: "stg"}
	prod:    {$after: "staging", profile: "prd"}
}
$settings: {
	state: {backend: "s3", bucket: "acme-state", prefix: "app/\($environment.$name)", profile: $environment.profile}
	access: dir: ".pravo/access/\($environment.$name)"
}
`})
	all, err := ReadEnvironmentSettings(dir, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[1].Name != "prod" || all[1].State.Profile != "prd" || all[1].AccessDir != ".pravo/access/prod" || all[1].Where() != "s3://acme-state/app/prod" {
		t.Fatalf("%+v", all)
	}
	r, err := Load(dir, LoadOptions{Tags: []string{"env=prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.AccessDir != ".pravo/access/prod" || r.State.Profile != "prd" {
		t.Errorf("access %q profile %q", r.AccessDir, r.State.Profile)
	}
}

func TestTokenEnv(t *testing.T) {
	r, err := loadSpec(t, `$settings: state: {backend: "http", url: "https://state.example.com/app", token_env: "STATE_TOKEN_PROD"}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.State.TokenEnv != "STATE_TOKEN_PROD" {
		t.Errorf("token_env %q", r.State.TokenEnv)
	}
	if _, err := loadSpec(t, `$settings: state: {backend: "http", url: "https://x", token_env: "abc def"}`); err == nil || !strings.Contains(err.Error(), "is not the name of an environment variable") {
		t.Errorf("bad name: %v", err)
	}
	if _, err := loadSpec(t, `$settings: state: {path: "s", token_env: "X"}`); err == nil || !strings.Contains(err.Error(), "belongs to the http backend") {
		t.Errorf("local: %v", err)
	}
}

// A row typed by a definition of your own, and $environment declared with it, so that an
// editor knows it: the rows are read with the definition, and a row of the wrong type is
// refused with the reason.
func TestEnvironmentRowsTypedByADefinition(t *testing.T) {
	spec := func(prodWorkers string) string {
		return `package app

import "pravo.dev/pravo"

#Row: pravo.#Environment & {
	workers: int & >=1
}

$environments: [string]: #Row
$environments: pravo.#Environments & {
	dev:  {workers: 1, $default: true}
	prod: {workers: ` + prodWorkers + `, $after: "dev"}
}

$environment: #Row

$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}

out: "\($environment.$name)-\($environment.workers)"
`
	}
	files := func(prodWorkers string) map[string]string {
		return map[string]string{"main.cue": spec(prodWorkers), "cue.mod/module.cue": "module: \"example.com/app@v0\"\nlanguage: version: \"v0.14.0\"\n",
			"cue.mod/gen/pravo.dev/pravo/settings_gen.cue": schema.SettingsPackage}
	}
	r, err := Load(writeFiles(t, files("3")), LoadOptions{Tags: []string{"env=prod"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := outOf(t, r); got != "prod-3" {
		t.Errorf("out = %q", got)
	}
	if strings.Join(r.EnvironmentNames(), ",") != "dev,prod" {
		t.Errorf("environments %v", r.EnvironmentNames())
	}
	_, err = Load(writeFiles(t, files(`"x"`)), LoadOptions{Tags: []string{"env=prod"}})
	if err == nil || !strings.Contains(err.Error(), "$environments.prod.workers: conflicting values") {
		t.Fatalf("a row of the wrong type: %v", err)
	}
}
