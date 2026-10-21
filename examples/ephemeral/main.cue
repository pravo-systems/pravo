package infra

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

$random: rnd.#Provider & {$version: ">= 3.7"}

// An ephemeral resource is opened for the duration of a run and never
// written to state. Its attributes may feed provider configuration,
// write-only attributes and other ephemeral resources only.
secret: $random.#Ephemeral.random_password & {
	length:  24
	special: false
}

// Provider configuration may use ephemeral values; the provider is
// configured after the ephemeral resource has been opened.
$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://127.0.0.1:1"
		username: "pravo"
		password: secret.result
	}]
}

deploy: $tls.#Resource.tls_private_key & {algorithm: "ED25519"}

public_key: pravo.#Output & {value: deploy.public_key_openssh}
