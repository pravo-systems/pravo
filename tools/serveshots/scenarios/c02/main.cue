package team

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29999, seed: "web"}
