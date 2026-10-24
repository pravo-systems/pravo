// The platform estate: three namespaces, a network, an api and an edge, built from
// the providers random, local and null so that it needs no cloud account.
// random_password stands for the secrets, local_file and local_sensitive_file
// for rendered configuration and null_resource for deploy steps.
package platform

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	nul "pravo.dev/providers/hashicorp/null:null_"
)

$random: rnd.#Provider
$local:  loc.#Provider
$null:   nul.#Provider

// The release is named once. Every namespace reads it.
release: $random.#Resource.random_id & {byte_length: 4}

manifest: $local.#Resource.local_file & {
	filename: "out/manifest.txt"
	content:  "release \(release.hex)\nnetwork \(network.name.id)\nbalancer \(edge.balancer.config.filename)\n"
}

// The endpoint is public. The admin token is not.
endpoint: pravo.#Output & {
	value:       "https://\(edge.balancer.host)"
	description: "Address of the balancer"
}

admin_token: pravo.#Output & {
	value:       api.database.password.result
	sensitive:   true
	description: "Password of the database administrator"
}
