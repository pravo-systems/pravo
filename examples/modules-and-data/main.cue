package infra

import (
	"pravo.dev/pravo"
	"strings"

	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	nul "pravo.dev/providers/hashicorp/null:null_" // null is a CUE keyword, so the package is null_
)

$random: rnd.#Provider & {$version: ">= 3.5"}
$local: loc.#Provider
$null:  nul.#Provider

// A reusable unit is a definition that contains resources. Its resources are
// written exactly as they are in a root.
#Service: {
	name: string
	port: int & >1024
	env:  "dev" | "prod"

	secret: $random.#Resource.random_password & {
		length:  24
		special: false
	}
	config: $local.#Resource.local_file & {
		filename: "out/\(name)-\(env).json"
		content: strings.Join([
			"{",
			"  \"service\": \"\(name)\",",
			"  \"port\": \(port),",
			"  \"secret\": \"\(secret.result)\"",
			"}",
		], "\n")
	}
}

// Instantiating it is enough: pravo finds the resources inside, at
// services.api.secret, services.api.config and so on.
services: {
	api: #Service & {name: "api", port: 8080, env: "dev"}
	web: #Service & {name: "web", port: 3000, env: "dev"}
}

// count / for_each replacement: a comprehension makes one resource per key.
regions: ["eu", "us", "ap"]
pets: {
	for r in regions {
		"\(r)": $random.#Resource.random_pet & {length: 2, prefix: r}
	}
}

// A data source that depends on a resource (deferred until apply).
apiConfig: $local.#Data.local_file & {filename: services.api.config.filename}

// null_resource with triggers and an explicit dependency.
notify: $null.#Resource.null_resource & {
	triggers: {
		config_hash: apiConfig.content_sha256
		nodes: strings.Join([for r in regions {pets[r].id}], ",")
	}
	$depends_on: [services.web.config]
}

api_secret_length: pravo.#Output & {value: len(services.api.secret.result)}
nodes: pravo.#Output & {value: [for r in regions {pets[r].id}]}
api_config: pravo.#Output & {value: apiConfig.content}
