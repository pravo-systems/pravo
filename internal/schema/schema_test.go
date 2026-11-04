package schema

import "testing"

// The hashes of a sensitive file are sensitive: a short secret falls to an unsalted hash.
func TestHashesOfASensitiveFileAreSensitive(t *testing.T) {
	attrs := func() []*Attribute {
		var out []*Attribute
		for _, n := range []string{"content", "content_md5", "content_sha256", "content_base64sha512", "id", "filename"} {
			out = append(out, &Attribute{Name: n, Sensitive: n == "content"})
		}
		return out
	}
	p := &ProviderSchema{Resources: map[string]*Schema{
		"local_sensitive_file": {Block: &Block{Attributes: attrs()}},
		"local_file":           {Block: &Block{Attributes: attrs()}},
	}}
	p.markDerivedSecrets()
	for _, a := range p.Resources["local_sensitive_file"].Block.Attributes {
		if want := a.Name != "filename"; a.Sensitive != want {
			t.Errorf("local_sensitive_file.%s sensitive = %v", a.Name, a.Sensitive)
		}
	}
	for _, a := range p.Resources["local_file"].Block.Attributes {
		if a.Sensitive != (a.Name == "content") {
			t.Errorf("local_file.%s sensitive = %v", a.Name, a.Sensitive)
		}
	}
}
