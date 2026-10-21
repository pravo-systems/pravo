package infra

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

// pravo's own settings are a declaration like any other: $settings.
$settings: pravo.#Settings & {
	// Refresh a resource from its provider only when its recorded state is
	// older than this; override per resource with $lifecycle: refresh.
	refresh: "1h"
}

// A provider is a struct built from its definition. $version says which
// version to install; the rest of the struct would be its configuration. The
// leading $ is a convention that keeps a provider's name from colliding with
// the attributes of a resource.
$random: rnd.#Provider & {$version: "~> 3.6"}
$local: loc.#Provider

env: *"dev" | "staging" | "prod" @tag(env)

// A resource is a struct built through a provider: that is how pravo knows
// which provider runs it. Its address is where it sits: this one is "server".
server: $random.#Resource.random_pet & {
	length:    3
	separator: "-"
	keepers: environment: env
}

listen: $random.#Resource.random_integer & {
	min: 1024
	max: 65535
}

hello: $local.#Resource.local_file & {
	filename: "hello-\(env).txt"
	content:  "Hello from \(server.id) on port \(listen.result)\n"
}

pet: pravo.#Output & {value: server.id}
port: pravo.#Output & {value: listen.result}
file: pravo.#Output & {value: hello.filename}
