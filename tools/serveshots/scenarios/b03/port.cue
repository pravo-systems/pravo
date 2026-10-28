package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}
