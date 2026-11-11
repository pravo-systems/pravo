package plugin

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The clients of protocol 5 and 6 are one file written twice, because the generated message
// types of the two protocols are distinct types that no function can take together. A change
// to one that is not made to the other is a difference between two providers, as the legacy
// type system flag of #261 was, so this test reads both files as one: v5.go with its names
// turned into those of protocol 6 must equal v6.go, except for the places listed here, which
// are the places where the protocols differ.
func TestProtocolClientsAreTheSameCode(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	v5, v6 := read("v5.go"), read("v6.go")

	// v5 spelled as v6: the package, the suffix of the helpers, the receiver.
	suffix := regexp.MustCompile(`([A-Za-z])5\b`)
	norm := strings.NewReplacer("tfplugin5", "tfplugin6", "v5", "v6", "V5", "V6", "pb5", "pb6").Replace(v5)
	norm = suffix.ReplaceAllString(norm, "${1}6")

	// Where the protocols differ: the version number, the names of five messages, and the
	// nested object type of an attribute, which protocol 5 does not have.
	for _, d := range []struct{ v5, v6 string }{
		{"ProtocolVersion() int { return 5 }", "ProtocolVersion() int { return 6 }"},
		{"PrepareProviderConfig_Request", "ValidateProviderConfig_Request"},
		{"pb.Configure_Request", "pb.ConfigureProvider_Request"},
		{"ValidateResourceTypeConfig_Request", "ValidateResourceConfig_Request"},
		{"ValidateDataSourceConfig_Request", "ValidateDataResourceConfig_Request"},
		{"pb.Stop_Request", "pb.StopProvider_Request"},
	} {
		if !strings.Contains(norm, d.v5) {
			t.Fatalf("v5.go no longer holds %q: update the list of differences in this test", d.v5)
		}
		norm = strings.ReplaceAll(norm, d.v5, d.v6)
	}
	// What only protocol 6 holds: the nesting mode of an object and its use.
	nesting := regexp.MustCompile(`(?s)func objNesting6\(.*?\n}\n\n`)
	usage := regexp.MustCompile(`\t\tif a\.NestedType != nil \{\n.*?\n\t\t}\n`)
	only6 := v6
	for _, re := range []*regexp.Regexp{nesting, usage} {
		if !re.MatchString(only6) {
			t.Fatalf("v6.go no longer holds %v: update the list of differences in this test", re)
		}
		only6 = re.ReplaceAllString(only6, "")
	}

	if norm != only6 {
		a, b := strings.Split(norm, "\n"), strings.Split(only6, "\n")
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] != b[i] {
				t.Fatalf("v5.go and v6.go differ at line %d of v6.go (as normalised):\n v5: %s\n v6: %s\nmake the same change in both files", i+1, a[i], b[i])
			}
		}
		t.Fatalf("v5.go and v6.go differ in length (%d and %d lines as normalised): make the same change in both files", len(a), len(b))
	}
}
