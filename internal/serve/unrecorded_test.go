package serve

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

const (
	secretEnvValue = "very-secret-env-value-77"
	ephemeralKey   = "api-key-literal-1234"
)

const unrecordedCUE = `package demo

$fake: {
	$kind:   "provider"
	$source: "example.test/x/fake"
	#Resource: {
		fake_db: {
			$kind:   "resource"
			$type:   "fake_db"
			$source: "example.test/x/fake"
			name:    string
			...
		}
	}
	#Ephemeral: {
		fake_token: {
			$kind:   "ephemeral"
			$type:   "fake_token"
			$source: "example.test/x/fake"
			scope:   string
			...
		}
	}
}

declared: $fake.#Resource.fake_db & {name: "declared"}
lazy:     $fake.#Resource.fake_db & {name: string}

token: $fake.#Ephemeral.fake_token & {
	scope:   lazy.name
	ttl:     300
	api_key: "` + ephemeralKey + `"
}

region: {$kind: "env", name: "UNRECORDED_REGION", default: "eu-west-1", value: string}
shell:  {$kind: "env", name: "UNRECORDED_SHELL", value: string}
nodef:  {$kind: "env", name: "UNRECORDED_UNSET", value: string}
admin:  {$kind: "env", name: "UNRECORDED_PASSWORD", default: "fallback", value: string}
key:    {$kind: "secret_env", name: "UNRECORDED_KEY", value: string}
`

// ephemeralLookup is the fake provider with an ephemeral resource type.
func ephemeralLookup(src registry.Source, version string) (Schemas, bool) {
	s, ok := fakeLookup(src, version)
	if !ok {
		return nil, false
	}
	str := json.RawMessage(`"string"`)
	num := json.RawMessage(`"number"`)
	ps := s.(ProviderSchemas)
	ps.PS.Ephemerals = map[string]*schema.Schema{"fake_token": {Version: 1, Block: &schema.Block{Attributes: []*schema.Attribute{
		{Name: "scope", TypeJSON: str, Required: true, Description: "What the token may do."},
		{Name: "ttl", TypeJSON: num, Optional: true},
		{Name: "api_key", TypeJSON: str, Optional: true},
		{Name: "result", TypeJSON: str, Computed: true},
	}}}}
	return ps, true
}

func attrOf(t *testing.T, list []Attr, name string) Attr {
	t.Helper()
	for _, a := range list {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no attribute %q in %v", name, list)
	return Attr{}
}

func hasName(list []Attr, name string) bool {
	for _, a := range list {
		if a.Name == name {
			return true
		}
	}
	return false
}

// A node with no record has a State and a Schema of its own: the schema of its type, and what the
// specification sets.
func TestDetailOfNodesWithoutRecord(t *testing.T) {
	t.Setenv("UNRECORDED_SHELL", "/bin/zsh")
	t.Setenv("UNRECORDED_PASSWORD", "hunter2-hunter2")
	t.Setenv("UNRECORDED_KEY", secretEnvValue)
	dir := t.TempDir()
	writeSpec(t, dir, unrecordedCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{Schemas: ephemeralLookup, Env: env.OS()})

	t.Run("ephemeral resource", func(t *testing.T) {
		d := p.detail("token")
		for _, want := range []string{"scope", "ttl", "api_key", "result"} {
			if !hasName(d.Schema, want) {
				t.Errorf("the schema lacks %s: %v", want, d.Schema)
			}
		}
		if got := attrOf(t, d.Schema, "scope"); got.Kind != "required" || got.Type != "string" || got.Description != "What the token may do." {
			t.Errorf("scope in the schema is %+v", got)
		}
		if got := attrOf(t, d.Schema, "result"); got.Kind != "computed" {
			t.Errorf("result in the schema is %+v", got)
		}
		if got := attrOf(t, d.Attrs, "scope"); !got.Unknown {
			t.Errorf("scope reads another node, so it is not known: %+v", got)
		}
		if got := attrOf(t, d.Attrs, "ttl"); got.Unknown || got.Null || got.Value == nil {
			t.Errorf("ttl is set to 300: %+v", got)
		}
		if got := attrOf(t, d.Attrs, "api_key"); got.Redacted != "name" {
			t.Errorf("api_key is a secret by its name: %+v", got)
		}
		if hasName(d.Attrs, "result") {
			t.Errorf("result is filled in by the provider and the specification does not set it")
		}
		if d.Times.RefreshedAt != nil || len(d.History) != 0 {
			t.Errorf("an ephemeral resource has no times and no history: %+v", d)
		}
		noSecrets(t, "the detail of the ephemeral resource", p.get("/api/detail?addr=token"), ephemeralKey)
	})

	t.Run("declared and not created", func(t *testing.T) {
		d := p.detail("declared")
		if !hasName(d.Schema, "name") || len(d.Attrs) != 0 {
			t.Errorf("a node that is not created has the schema and no values: %+v", d)
		}
	})

	t.Run("environment variable", func(t *testing.T) {
		d := p.detail("region")
		if len(d.Schema) != 3 || attrOf(t, d.Schema, "name").Kind != "required" || attrOf(t, d.Schema, "default").Kind != "optional" || attrOf(t, d.Schema, "value").Kind != "computed" {
			t.Errorf("the schema of #Env is %+v", d.Schema)
		}
		if got := attrOf(t, d.Attrs, "name"); got.Value != "UNRECORDED_REGION" {
			t.Errorf("name is %+v", got)
		}
		if got := attrOf(t, d.Attrs, "default"); got.Value != "eu-west-1" {
			t.Errorf("default is %+v", got)
		}
		if got := attrOf(t, d.Attrs, "value"); got.Value != "eu-west-1" {
			t.Errorf("an unset variable has its default: %+v", got)
		}
		if got := attrOf(t, p.detail("shell").Attrs, "value"); got.Value != "/bin/zsh" {
			t.Errorf("a set variable has its value: %+v", got)
		}
		unset := p.detail("nodef")
		if got := attrOf(t, unset.Attrs, "value"); !got.Null {
			t.Errorf("an unset variable with no default has no value: %+v", got)
		}
		if !strings.Contains(strings.Join(unset.Notes, " "), "UNRECORDED_UNSET is not set") {
			t.Errorf("the notes do not say that the variable is not set: %v", unset.Notes)
		}
	})

	t.Run("secrets", func(t *testing.T) {
		admin := p.detail("admin")
		if got := attrOf(t, admin.Attrs, "value"); got.Redacted != "name" || got.Value != nil {
			t.Errorf("the value of a variable named for a password is withheld: %+v", got)
		}
		if got := attrOf(t, admin.Attrs, "default"); got.Redacted != "name" || got.Value != nil {
			t.Errorf("so is its default: %+v", got)
		}
		key := p.detail("key")
		if got := attrOf(t, key.Attrs, "value"); !got.Sensitive || got.Redacted != "sensitive" || got.Value != nil {
			t.Errorf("a secret variable shows no value: %+v", got)
		}
		if len(key.Schema) != 2 || attrOf(t, key.Schema, "value").Sensitive != true {
			t.Errorf("the schema of #SecretEnv is %+v", key.Schema)
		}
		for _, path := range []string{"/api/graph", "/api/detail?addr=key", "/api/detail?addr=admin", "/api/source?addr=key", "/api/search?q=key"} {
			noSecrets(t, path, p.get(path), secretEnvValue, "hunter2-hunter2")
		}
	})
}
