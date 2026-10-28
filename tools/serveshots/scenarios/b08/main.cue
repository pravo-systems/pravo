package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

server: $random.#Resource.random_integer & {min: 1, max: 99, seed: "s1"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(server.result)\n"
}
