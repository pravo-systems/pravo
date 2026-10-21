package config

import (
	"strings"
	"testing"
)

// A module that needs a provider from its caller declares the field as
// required. An instance that leaves it out is an error at evaluation that
// names the instance and the field, whether or not a resource reads it.
func TestRequiredProviderMustBeSupplied(t *testing.T) {
	const module = `
#M: {
	provider!: #Local
	f:         provider.#Resource.local_file & {filename: "m"}
}
`
	_, err := loadSpec(t, module+`m: #M`)
	if err == nil {
		t.Fatal("an instance without its required provider must be rejected")
	}
	for _, want := range []string{"provider m.provider was not supplied", "m requires a hashicorp/local provider in its field provider", "m: ... & {provider: $local}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "was not built through a provider") {
		t.Errorf("the error must be about the missing provider, got %q", err)
	}

	// A required provider at the top of the specification has no instance to
	// pass it, and is reported the same way.
	if _, err := loadSpec(t, `stray!: #Local`); err == nil || !strings.Contains(err.Error(), "provider stray was not supplied") {
		t.Errorf("a required provider at the top must be rejected, got %v", err)
	}
	if _, err := loadSpec(t, `maybe?: #Local`); err != nil {
		t.Errorf("an optional provider must load, got %v", err)
	}

	// The definition itself is a template, not an instance.
	if _, err := loadSpec(t, module); err != nil {
		t.Errorf("a definition with a required provider must load, got %v", err)
	}

	// The instance can sit at any depth, in a list or under a field.
	for path, body := range map[string]string{
		"stack.a.provider": `stack: a: #M`,
		"list[1].provider": `list: [#M & {provider: $local}, #M]`,
	} {
		if _, err := loadSpec(t, module+body); err == nil || !strings.Contains(err.Error(), "provider "+path+" was not supplied") {
			t.Errorf("%s: got %v", path, err)
		}
	}

	// A caller that passes a provider gets the caller's instance.
	r, err := loadSpec(t, module+`m: #M & {provider: $local}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resource("m.f").ProviderKey; got != "local" {
		t.Errorf("m.f uses %q, want local", got)
	}
	if n := len(r.ProviderDecls); n != 2 { // $local, $random
		t.Errorf("%d provider instances, want 2: the required input is an alias", n)
	}

	// A provider written at the call is the caller's own choice: a struct of
	// its own.
	r, err = loadSpec(t, module+`m: #M & {provider: #Local & {region: "eu"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resource("m.f").ProviderKey; got != "local.m.provider" {
		t.Errorf("m.f uses %q, want local.m.provider", got)
	}
}

// Only a required provider is checked. A required value of another kind is
// reported by CUE where something needs it, and an optional provider that a
// condition guards is not an error.
func TestOtherUnfilledFieldsAreNotProviderErrors(t *testing.T) {
	if _, err := loadSpec(t, `
#N: {name!: string}
n: #N
`); err != nil {
		t.Errorf("a required string must not be reported as a provider: %v", err)
	}
	r, err := loadSpec(t, `
#O: {
	provider?: #Local
	if provider != _|_ {
		f: provider.#Resource.local_file & {filename: "o"}
	}
}
o: #O
p: #O & {provider: $local}
`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Resource("o.f") != nil {
		t.Error("o.f must not exist without its optional provider")
	}
	if got := r.Resource("p.f"); got == nil || got.ProviderKey != "local" {
		t.Errorf("p.f = %+v, want a resource of the local provider", got)
	}
}

// A provider that is passed in is another name for the caller's struct, and
// the instance is configured from that struct. A module may write nothing on
// it that the caller's struct lacks: the difference would be dropped.
func TestPassedProviderIsConfiguredWhereItIsDeclared(t *testing.T) {
	module := func(field string) string {
		return `
#M: {
	provider!: ` + field + `
	f:         provider.#Resource.local_file & {filename: "m"}
}
`
	}

	// The module sets a field the caller's struct does not.
	_, err := loadSpec(t, module(`#Local & {region: "eu"}`)+`m: #M & {provider: $local}`)
	if err == nil {
		t.Fatal("configuration that the instance would not receive must be rejected")
	}
	for _, want := range []string{"m.provider sets region", "another name for the provider $local", "configured from $local alone: set region there"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}

	// The same holds for the fields that tell pravo how to obtain a provider.
	_, err = loadSpec(t, module(`#Local & {$version: "~> 2.5"}`)+`m: #M & {provider: $local}`)
	if err == nil || !strings.Contains(err.Error(), "m.provider sets $version") {
		t.Errorf("a version written on a passed-in provider must be rejected, got %v", err)
	}

	// A struct built from another struct is held to the same rule.
	_, err = loadSpec(t, `derived: $local & {region: "eu"}`)
	if err == nil || !strings.Contains(err.Error(), "derived sets region") {
		t.Errorf("configuration on a derived provider must be rejected, got %v", err)
	}

	// The caller's struct sets the same value: nothing is lost.
	r, err := loadSpec(t, module(`#Local & {region: "eu"}`)+`
eu: #Local & {region: "eu"}
m:  #M & {provider: eu}
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resource("m.f").ProviderKey; got != "local.eu" {
		t.Errorf("m.f uses %q, want local.eu", got)
	}

	// The caller's struct sets another value: CUE reports the conflict,
	// naming the field on both sides.
	_, err = loadSpec(t, module(`#Local & {region: "eu"}`)+`
us: #Local & {region: "us"}
m:  #M & {provider: us}
`)
	if err == nil || !strings.Contains(err.Error(), "m.provider.region: conflicting values") {
		t.Errorf("two regions must conflict, got %v", err)
	}
}

// A module that declares a provider of its own and is instantiated by a bare
// reference to its definition still finds that provider, once for each
// instance.
func TestInstanceOfAModuleWithItsOwnProvider(t *testing.T) {
	r, err := loadSpec(t, `
#S: {
	provider: #Local & {region: "eu"}
	f:        provider.#Resource.local_file & {filename: "s"}
}
s:     #S
stack: t: #S
`)
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]string{"s.f": "local.s.provider", "stack.t.f": "local.stack.t.provider"} {
		res := r.Resource(addr)
		if res == nil || res.ProviderKey != want {
			t.Errorf("%s uses %+v, want %s", addr, res, want)
		}
	}
}
