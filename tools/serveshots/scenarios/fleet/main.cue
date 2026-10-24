// The fleet estate: three regions, each with a production and a staging
// environment, each environment with three services and a pool of twelve nodes.
// Comprehensions write every region, environment, service and node from the
// lists below, so the addresses are three namespaces deep: eu.prod.api.config.
// The providers random, local and null stand for the real ones.
package fleet

import (
	"list"
	"strings"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	nul "pravo.dev/providers/hashicorp/null:null_"
)

$random: rnd.#Provider
$local:  loc.#Provider
$null:   nul.#Provider

regions: ["eu", "us", "ap"]
envs: ["prod", "stage"]
ports: {api: 8080, web: 8081, jobs: 8082}

// The release is named once. The index reads it.
release: $random.#Resource.random_id & {byte_length: 4}

index: $local.#Resource.local_file & {
	filename: "out/index.txt"
	content:  "release \(release.hex)\nregions \(strings.Join(regions, " "))\n"
}

// A region is a pet name and one #Environment for each of envs. The loop
// variables have their own names: a value written as {region: region} would
// refer to the field it sits in.
for r in regions {
	(r): {
		zone: $random.#Resource.random_pet & {length: 2, prefix: r}

		for e in envs {
			(e): #Environment & {region: r, env: e, zone_id: zone.id}
		}
	}
}

// An #Environment holds a secret, a pool of twelve nodes, one #Service for each
// entry of ports and the manifest that lists them.
#Environment: {
	region:  string
	env:     string
	zone_id: string

	let dir = "out/\(region)/\(env)"

	secret: $random.#Resource.random_password & {length: 24, special: false}

	// The nodes are twelve resources of one type, named n01 to n12.
	nodes: {
		for i in list.Range(1, 13, 1) {
			let n = "\(i)"
			let key = "n" + strings.Repeat("0", 2-len(n)) + n
			(key): $null.#Resource.null_resource & {
				triggers: {
					zone: zone_id
					slot: n
				}
			}
		}
	}

	for sname, sport in ports {
		(sname): #Service & {name: sname, port: sport, folder: dir, secret_id: secret.id}
	}

	manifest: $local.#Resource.local_file & {
		filename: "\(dir)/manifest.txt"
		content:  "zone \(zone_id)\nsecret \(secret.id)\nservices \(strings.Join([for sname, _ in ports {sname}], " "))\n"
	}
}

// A #Service has an identity, a token and its rendered configuration.
#Service: {
	name:      string
	port:      int
	folder:    string
	secret_id: string

	id: $random.#Resource.random_id & {byte_length: 4}

	token: $random.#Resource.random_password & {length: 32, special: false}

	config: $local.#Resource.local_file & {
		filename: "\(folder)/\(name).json"
		content:  "{\"service\": \"\(name)\", \"id\": \"\(id.hex)\", \"port\": \(port), \"secret\": \"\(secret_id)\"}\n"
	}
}
