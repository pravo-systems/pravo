package main

import demo "pravo.dev/providers/demo/demo"

$demo: demo.#Provider & {$path: "@FAKEPROVIDER@"}

web: $demo.#Resource.demo_server & {name: "web"}

servers: $demo.#Discovery.demo_server & {
	region: "eu"
	$ack: [{
		names:  ["batch-*"]
		reason: "started and removed by the nightly job"
	}]
}
