package main

import "testing"

// A folder with a name that a module path cannot hold still gets a module that pravo accepts.
func TestModuleSegment(t *testing.T) {
	for in, want := range map[string]string{
		"estate": "estate", "My Estate": "my-estate", "my_estate": "my-estate", "a=b": "a-b", "a+b": "a-b", "me@home": "me-home",
		"a,b": "a-b", "a:b": "a-b", "~tmp": "tmp", "it's": "it-s", "Übung": "bung", "v1.2": "v1.2", "***": "project", "a..b": "a-b", "a...b": "a-b", "a.-b": "a-b", "a-.b": "a-b", "a._b": "a-b", "a--b": "a-b", "a b.c": "a-b.c",
	} {
		if got := moduleSegment(in); got != want {
			t.Errorf("moduleSegment(%q) = %q, want %q", in, got, want)
		}
	}
}
