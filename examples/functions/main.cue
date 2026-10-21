package infra

import (
	"pravo.dev/pravo"
	"encoding/json"

	k8s "pravo.dev/providers/hashicorp/kubernetes"
	loc "pravo.dev/providers/hashicorp/local"
)

// Providers that need no configuration are still declared: a resource, data
// source or function is built through its provider.
$k8s:   k8s.#Provider
$local: loc.#Provider

_manifest: """
	apiVersion: v1
	kind: ConfigMap
	metadata:
	  name: demo
	  namespace: default
	data:
	  greeting: hello
	"""

// A provider-defined function is a struct built from its definition. Its
// result is filled in when the arguments are known.
decoded: $k8s.#Function.manifest_decode & {args: [_manifest]}

// Calls chain like any other resource.
reencoded: $k8s.#Function.manifest_encode & {args: [decoded.result]}

file: $local.#Resource.local_file & {
	filename: "out/decoded.json"
	content:  json.Marshal(decoded.result) + "\n"
}

kind: pravo.#Output & {value: decoded.result.kind}
encoded: pravo.#Output & {value: reencoded.result}
