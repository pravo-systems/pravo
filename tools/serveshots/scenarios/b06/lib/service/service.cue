package service

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

#Service: {
	random!: rnd.#Provider
	local!:  loc.#Provider

	name: string & =~"^[a-z][a-z0-9-]*$"
	port: int & >1024 & <65536

	serial: random.#Resource.random_integer & {min: 1000, max: 9999, seed: name}
	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\nport \(port)\nserial \(serial.result)\n"
	}
}
