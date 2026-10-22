package platform

import "example.com/platform/kit"

// api: a database, a cache and the services that use them. The database reads
// the address range of the network, and each service reads the database and the
// subnet of zone a, so a change to the network reaches every one of them.
api: {
	database: {
		user: $random.#Resource.random_pet & {length: 1}

		password: $random.#Resource.random_password & {
			length:  32
			special: true
		}

		dsn: $local.#Resource.local_sensitive_file & {
			filename: "out/api/database.dsn"
			content:  "postgres://\(user.id):\(password.result)@db.10.\(network.cidr.result).internal/app\n"
		}

		migrate: $null.#Resource.null_resource & {
			triggers: dsn_hash: dsn.content_sha256
		}
	}

	cache: {
		auth: $random.#Resource.random_password & {length: 24, special: false}
		// The password is written into a plain local_file, whose content the
		// provider does not mark sensitive.
		config: $local.#Resource.local_file & {
			filename: "out/api/cache.conf"
			content:  "maxmemory 256mb\nrequirepass \(auth.result)\n"
		}
	}

	// Each service is an instance of the definition in kit/kit.cue.
	services: {
		billing: kit.#Service & {
			random:   $random
			local:    $local
			steps:    $null
			name:     "billing"
			port:     8081
			database: api.database.dsn.filename
			subnet:   network.zones.a.subnet.result
		}
		orders: kit.#Service & {
			random:   $random
			local:    $local
			steps:    $null
			name:     "orders"
			port:     8082
			database: api.database.dsn.filename
			subnet:   network.zones.b.subnet.result
		}
		search: kit.#Service & {
			random:   $random
			local:    $local
			steps:    $null
			name:     "search"
			port:     8083
			database: api.database.dsn.filename
			subnet:   network.zones.c.subnet.result
		}
	}
}
