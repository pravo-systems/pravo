package estate

port: {
	for name, _ in services {
		(name): $random.#Resource.random_integer & {
			min:  20000
			max:  29999
			seed: name
		}
	}
}

conf: {
	for name, svc in services {
		(name): $local.#Resource.local_file & {
			filename: "\(name).conf"
			content:  "port = \(port[name].result)\nworkers = \(svc.workers)\n"
		}
	}
}
