package platform

import demo "pravo.dev/providers/demo/demo"

// storage: the buckets of the platform, through a provider that lists what
// exists in a region, so that a discovery can show the buckets that nothing
// manages.
$demo: demo.#Provider & {$path: "@FAKEPROVIDER@"}

storage: {
	assets: $demo.#Resource.demo_bucket & {bucket: "platform-assets"}

	backups: $demo.#Resource.demo_bucket & {bucket: "platform-backups"}

	// Every bucket of the region. The one that the nightly export writes is
	// known and left alone.
	buckets: $demo.#Discovery.demo_bucket & {
		region: "eu"
		$ack: [
			{names: ["platform-export-*"], reason: "written by the nightly export and removed by it"},
		]
	}
}
