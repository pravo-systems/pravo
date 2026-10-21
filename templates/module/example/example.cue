// Package example is a minimal reusable module: inputs are fields with
// constraints, and the resources it contains are built through a provider it
// is given. A consumer writes
//
//	import (
//		loc     "pravo.dev/providers/hashicorp/local"
//		"github.com/OWNER/NAME/example"
//	)
//
//	$local: loc.#Provider
//	site:   example.#Site & {provider: $local, name: "docs"}
//
// and pravo finds the resources inside, at site.file. An instance that does
// not pass the provider is an error at evaluation: provider site.provider was
// not supplied.
package example

import loc "pravo.dev/providers/hashicorp/local"

// A Site writes a small file; replace it with something useful.
#Site: {
	// provider runs the resources of the site. The field is required: the
	// consumer passes a provider struct of its root.
	provider!: loc.#Provider
	name:     string & =~"^[a-z][a-z0-9-]*$"

	file: provider.#Resource.local_file & {
		filename: "out/\(name).txt"
		content:  "site \(name)\n"
	}
}
