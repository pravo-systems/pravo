package platform

import "example.com/platform/kit"

// edge: certificates, the balancer and one route for each service. The edge
// reads the api and the network: nothing in them reads the edge.
edge: {
	certs: {
		www: kit.#Certificate & {
			random: $random
			local:  $local
			steps:  $null
			host:   "www.example.test"
		}
		api: kit.#Certificate & {
			random: $random
			local:  $local
			steps:  $null
			host:   "api.example.test"
		}
	}

	balancer: {
		host: "www.example.test"

		config: $local.#Resource.local_file & {
			filename: "out/edge/balancer.conf"
			content:  "listen 443\ncert \(certs.www.bundle.filename)\nzones \(network.zones.a.subnet.result) \(network.zones.b.subnet.result) \(network.zones.c.subnet.result)\n"
		}

		reload: $null.#Resource.null_resource & {
			triggers: config_hash: config.content_sha256
		}
	}

	routes: {
		billing: $local.#Resource.local_file & {
			filename: "out/edge/route-billing.conf"
			content:  "route /billing -> \(api.services.billing.id.hex):\(api.services.billing.port) via \(certs.api.serial.hex)\n"
		}
		orders: $local.#Resource.local_file & {
			filename: "out/edge/route-orders.conf"
			content:  "route /orders -> \(api.services.orders.id.hex):\(api.services.orders.port) via \(certs.api.serial.hex)\n"
		}
		search: $local.#Resource.local_file & {
			filename: "out/edge/route-search.conf"
			content:  "route /search -> \(api.services.search.id.hex):\(api.services.search.port) via \(certs.api.serial.hex)\n"
		}
	}
}
