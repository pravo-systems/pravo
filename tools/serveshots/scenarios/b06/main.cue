package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
	"example.com/estate/lib/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api:    service.#Service & {random: $random, local: $local, name: "api", port: 8080}
worker: service.#Service & {random: $random, local: $local, name: "worker", port: 9090}
