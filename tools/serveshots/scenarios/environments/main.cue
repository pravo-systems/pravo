// An estate with a promotion flow: dev, then staging, then prod. The rows hold
// what differs, and the resources never name an environment.
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$environments: pravo.#Environments & {
	dev: {workers: 1, $default: true}
	staging: {workers: 2, $after: "dev"}
	prod: {workers: 3, $after: "staging"}
}

// One state and one readers file per environment.
$settings: pravo.#Settings & {
	state: path: "state/\($environment.$name)"
	access: dir: ".pravo/access/\($environment.$name)"
}

$random: rnd.#Provider
$local:  loc.#Provider

release: $random.#Resource.random_id & {byte_length: 4}

conf: $local.#Resource.local_file & {
	filename: "out/\($environment.$name).conf"
	content:  "workers \($environment.workers)\nrelease \(release.hex)\nimage 1.0\n"
}
