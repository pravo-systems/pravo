// Package kit holds the definitions that the platform estate reuses. A
// definition takes its providers as inputs and builds its resources through
// them, so a root decides which providers run.
package kit

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	nul "pravo.dev/providers/hashicorp/null:null_"
)

// #Zone is one availability zone of the network: a subnet, a gateway that is
// rebuilt when the subnet changes, and the routing file that names both.
#Zone: {
	random!: rnd.#Provider
	local!:  loc.#Provider
	steps!:  nul.#Provider

	name!: string
	vpc!:  string

	subnet: random.#Resource.random_integer & {
		min:  10
		max:  250
		seed: "subnet-\(vpc)-\(name)"
	}

	gateway: steps.#Resource.null_resource & {
		triggers: {
			zone:  name
			octet: "\(subnet.result)"
		}
	}

	routes: local.#Resource.local_file & {
		filename: "out/network/zone-\(name).conf"
		content:  "zone \(name)\nvpc \(vpc)\nsubnet 10.\(subnet.result).0.0/24\ngateway \(gateway.id)\n"
	}
}

// #Service is one service of the api: an identity, a key that the service
// presents to the database, its rendered configuration and the step that
// deploys it. The key is sensitive, so the file that holds it is too.
#Service: {
	random!: rnd.#Provider
	local!:  loc.#Provider
	steps!:  nul.#Provider

	name!:     string
	port!:     int & >1024 & <65536
	database!: string
	subnet!:   int

	id: random.#Resource.random_id & {byte_length: 4}

	api_key: random.#Resource.random_password & {
		length:  32
		special: false
	}

	config: local.#Resource.local_file & {
		filename: "out/api/\(name).json"
		content:  "{\"service\": \"\(name)\", \"id\": \"\(id.hex)\", \"port\": \(port), \"database\": \"\(database)\", \"subnet\": \(subnet)}\n"
	}

	env: local.#Resource.local_sensitive_file & {
		filename: "out/api/\(name).env"
		content:  "SERVICE=\(name)\nAPI_KEY=\(api_key.result)\n"
	}

	deploy: steps.#Resource.null_resource & {
		triggers: {
			config_hash: config.content_sha256
			service_id:  id.hex
		}
	}
}

// #Certificate is a certificate for one host: a serial, a private key and the
// bundle that is written for the balancer.
#Certificate: {
	random!: rnd.#Provider
	local!:  loc.#Provider
	steps!:  nul.#Provider

	host!: string

	serial: random.#Resource.random_id & {byte_length: 8}

	key: random.#Resource.random_password & {
		length:  48
		special: false
	}

	bundle: local.#Resource.local_sensitive_file & {
		filename: "out/edge/\(host).pem"
		content:  "# \(host) serial \(serial.hex)\n\(key.result)\n"
	}
}
