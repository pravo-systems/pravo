package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {refresh: "1h"}

$local: loc.#Provider

motd: $local.#Resource.local_file & {
	filename: "motd.txt"
	content:  "welcome\n"
}

banner: $local.#Resource.local_file & {
	filename: "banner.txt"
	content:  "release 1\n"
}
