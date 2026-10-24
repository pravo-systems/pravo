package config

import (
	"strings"
	"testing"
)

func TestDiscoveryIsFoundAndIsNotAResource(t *testing.T) {
	r, err := loadSpec(t, `
files: $local.#Discovery.local_file & {dir: "/srv", prefix: "a-", $limit: 50}
motd:  $local.#Resource.local_file & {filename: "motd"}
group: {inner: $local.#Discovery.local_file & {dir: "/var"}}
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Resources) != 1 || r.Resources[0].Address() != "motd" {
		t.Fatalf("a discovery is a node of the graph: %+v", r.Resources)
	}
	if got := r.DiscoveryNames(); strings.Join(got, ",") != "files,group.inner" {
		t.Fatalf("discoveries = %v", got)
	}
	d := r.DiscoveryByName("files")
	if d.Type != "local_file" || d.ProviderKey != "local" || d.Limit != 50 || d.Source.Type != "local" {
		t.Errorf("%+v", d)
	}
	if d.ScopeErr != nil || d.Scope["dir"] != "/srv" || d.Scope["prefix"] != "a-" || len(d.Scope) != 2 {
		t.Errorf("the scope is the fields of the schema, without the $ fields: %+v, %v", d.Scope, d.ScopeErr)
	}
	if d.Digest == "" || d.Digest == r.DiscoveryByName("group.inner").Digest {
		t.Errorf("digest %q", d.Digest)
	}
	if r.DiscoveryByName("nope") != nil {
		t.Error("a discovery that is not declared was found")
	}
	if _, ok := r.Providers["local"]; !ok || !contains(r.ProviderKeys, "local") {
		t.Errorf("a discovery needs its provider: %v %v", r.Providers, r.ProviderKeys)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// The digest names the scope: it changes with the scope, the limit and the
// type, and not with the order in which the fields were written.
func TestDiscoveryDigestFollowsTheScope(t *testing.T) {
	digest := func(body string) string {
		t.Helper()
		r, err := loadSpec(t, body)
		if err != nil {
			t.Fatal(err)
		}
		return r.DiscoveryByName("d").Digest
	}
	base := digest(`d: $local.#Discovery.local_file & {dir: "/srv", prefix: "a-"}`)
	if got := digest(`d: $local.#Discovery.local_file & {prefix: "a-", dir: "/srv"}`); got != base {
		t.Error("the order of the fields changed the digest")
	}
	if got := digest(`
other: "x"
d: $local.#Discovery.local_file & {dir: "/srv", prefix: "a-"}`); got != base {
		t.Error("a field elsewhere in the specification changed the digest")
	}
	for name, body := range map[string]string{
		"another value":  `d: $local.#Discovery.local_file & {dir: "/srv", prefix: "b-"}`,
		"a field less":   `d: $local.#Discovery.local_file & {dir: "/srv"}`,
		"a field more":   `d: $local.#Discovery.local_file & {dir: "/srv", prefix: "a-", depth: 2}`,
		"a limit":        `d: $local.#Discovery.local_file & {dir: "/srv", prefix: "a-", $limit: 5}`,
		"another number": `d: $local.#Discovery.local_file & {dir: "/srv", prefix: "a-", depth: 1}`,
	} {
		if digest(body) == base {
			t.Errorf("%s did not change the digest", name)
		}
	}
}

func TestDiscoveryScopeThatIsNotWrittenOutIsAnErrorOfTheDiscoveryAlone(t *testing.T) {
	r, err := loadSpec(t, `
motd: $local.#Resource.local_file & {filename: "motd"}
d:    $local.#Discovery.local_file & {prefix: "a-"}
e:    $local.#Discovery.local_file & {dir: motd.filename}
`)
	if err != nil {
		t.Fatalf("a scope without its required field fails the load: %v", err)
	}
	d := r.DiscoveryByName("d")
	if d.ScopeErr == nil || !strings.Contains(d.ScopeErr.Error(), "dir") || d.Digest != "" || d.Scope != nil {
		t.Errorf("a missing field: %+v", d)
	}
	// A scope that reads a resource is concrete only if the specification says so.
	// motd.filename is "motd", so it is a literal as far as CUE can tell.
	if e := r.DiscoveryByName("e"); e.ScopeErr != nil || e.Scope["dir"] != "motd" {
		t.Errorf("a scope that reads a literal: %+v", e)
	}
}

func TestDiscoveryRefusesWhatTheDefinitionDoesNot(t *testing.T) {
	for name, body := range map[string]string{
		"a field of another type":           `d: $local.#Discovery.local_file & {dir: 3}`,
		"a field that is not scope":         `d: $local.#Discovery.local_file & {dir: "/", nope: 1}`,
		"a limit of zero":                   `d: $local.#Discovery.local_file & {dir: "/", $limit: 0}`,
		"a resource inside of it":           `d: $local.#Discovery.local_file & {dir: "/", inner: $local.#Resource.local_file & {filename: "x"}}`,
		"an import, which is for resources": `d: $local.#Discovery.local_file & {dir: "/", $import: "x"}`,
	} {
		if _, err := loadSpec(t, body); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := loadSpec(t, `d: {$kind: "discovery", $type: "t"}`); err == nil || !strings.Contains(err.Error(), "no $source") {
		t.Errorf("a discovery that is not built from a provider: %v", err)
	}
	if _, err := loadSpec(t, `d: {$kind: "discovery", $source: "hashicorp/local"}`); err == nil || !strings.Contains(err.Error(), "no $type") {
		t.Errorf("a discovery without a type: %v", err)
	}
}

func TestDiscoveryNeedsAProviderItWasBuiltThrough(t *testing.T) {
	_, err := loadSpec(t, `d: #Local.#Discovery.local_file & {dir: "/"}`)
	if err == nil || !strings.Contains(err.Error(), "was not built through a provider") {
		t.Errorf("%v", err)
	}
	_, err = loadSpec(t, `d: $random.#Resource.random_pet`+"\n"+`e: {$kind: "discovery", $type: "t", $source: "hashicorp/local"} & $random.#Resource.random_pet`)
	if err == nil {
		t.Error("a discovery built through the provider of another source was accepted")
	}
}

func TestSeveralProviderInstancesGiveDiscoveriesTheirOwnKeys(t *testing.T) {
	r, err := loadSpec(t, `
$local2: #Local & {region: "eu"}
a: $local.#Discovery.local_file & {dir: "/"}
b: $local2.#Discovery.local_file & {dir: "/"}
`)
	if err != nil {
		t.Fatal(err)
	}
	ka, kb := r.DiscoveryByName("a").ProviderKey, r.DiscoveryByName("b").ProviderKey
	if ka == kb || !strings.HasPrefix(ka, "local.") || !strings.HasPrefix(kb, "local.") {
		t.Errorf("keys %q %q", ka, kb)
	}
	if r.DiscoveryByName("a").Digest == r.DiscoveryByName("b").Digest {
		t.Error("the same scope through two provider instances has one digest")
	}
}

func TestDiscoveryReadsItsAcks(t *testing.T) {
	r, err := loadSpec(t, `d: $local.#Discovery.local_file & {
	dir: "/srv"
	$ack: [
		{names: ["batch-*"], reason: "nightly"},
		{ids: ["a-1", "b-?"]},
	]
}`)
	if err != nil {
		t.Fatal(err)
	}
	d := r.DiscoveryByName("d")
	if len(d.Acks) != 2 || d.Acks[0].Ref() != "$ack[0]" || d.Acks[0].Names[0] != "batch-*" || d.Acks[0].Reason != "nightly" || d.Acks[1].IDs[1] != "b-?" {
		t.Fatalf("%+v", d.Acks)
	}
	if !d.Acks[0].Covers("x", "x", "", "batch-7") || d.Acks[0].Covers("x", "x", "", "web") || d.Acks[0].Covers("batch-7", "batch-7", "", "") {
		t.Error("a name pattern covers by name alone")
	}
	if !d.Acks[1].Covers("b-2", "k", "", "") || !d.Acks[1].Covers("x", "a-1", "", "") || !d.Acks[1].Covers("x", "k", "a-1", "") || d.Acks[1].Covers("b-22", "k", "", "") {
		t.Error("an id pattern covers by id, key or import id")
	}
	// The acknowledgements are not part of the question a run answers.
	without, err := loadSpec(t, `d: $local.#Discovery.local_file & {dir: "/srv"}`)
	if err != nil {
		t.Fatal(err)
	}
	if without.DiscoveryByName("d").Digest != d.Digest {
		t.Error("an $ack changed the digest of the scope")
	}
}

func TestDiscoveryRefusesAnAckThatCoversNothingOrEverything(t *testing.T) {
	long := strings.Repeat("a", MaxPatternLen+1)
	for name, entry := range map[string]string{
		"nothing":     `{reason: "x"}`,
		"an empty id": `{ids: [""]}`,
		"everything":  `{ids: ["*"]}`,
		"every name":  `{names: ["**"]}`,
		"a long one":  `{ids: ["` + long + `"]}`,
		"no reason":   `{ids: ["a"], reason: 3}`,
	} {
		body := `d: $local.#Discovery.local_file & {dir: "/", $ack: [` + entry + `]}`
		_, err := loadSpec(t, body)
		if err == nil || !strings.Contains(err.Error(), "$ack") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := loadSpec(t, `d: $local.#Discovery.local_file & {dir: "/", $ack: [{ids: ["?"]}]}`); err != nil {
		t.Errorf("? is one character, not everything: %v", err)
	}
}

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		pattern, s string
		want       bool
	}{
		{"i-1", "i-1", true}, {"i-1", "i-12", false}, {"i-*", "i-12", true}, {"i-*", "i-", true}, {"*-1", "i-1", true},
		{"*", "", true}, {"a*b*c", "aXXbYYc", true}, {"a*b*c", "aXXbYY", false}, {"i-?", "i-1", true}, {"i-?", "i-12", false},
		{"arn:aws:s3:::*/logs", "arn:aws:s3:::b/x/logs", true}, {"[a]", "a", false}, {"[a]", "[a]", true},
		{"héllo*", "héllo wörld", true}, {"", "", true}, {"", "x", false},
	} {
		if got := Glob(c.pattern, c.s); got != c.want {
			t.Errorf("Glob(%q, %q) = %v", c.pattern, c.s, got)
		}
	}
}
