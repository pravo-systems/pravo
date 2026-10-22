package platform

import "example.com/platform/kit"

// network: a name, an address range, three zones and the records for the
// internal domain.
network: {
	name: $random.#Resource.random_pet & {length: 2}

	cidr: $random.#Resource.random_integer & {
		min:  10
		max:  250
		seed: "platform"
	}

	// A zone is an instance of kit.#Zone: the resources sit at network.zones.a.subnet
	// and so on.
	zones: {
		for z in ["a", "b", "c"] {
			"\(z)": kit.#Zone & {
				random: $random
				local:  $local
				steps:  $null
				name:   z
				vpc:    network.name.id
			}
		}
	}

	dns: {
		zone: $random.#Resource.random_id & {byte_length: 3}
		records: $local.#Resource.local_file & {
			filename: "out/network/dns.zone"
			content:  "zone \(zone.hex).internal\nnet 10.\(cidr.result).0.0/16\n"
		}
	}
}
