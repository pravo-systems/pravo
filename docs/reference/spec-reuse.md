---
title: Reuse
description: pravo has no module block. A definition that contains resources is instantiated with `&`, a package shares it between roots and a CUE module between repositories.
order: 29
group: Language
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
keywords: "module reuse definition package source version"
---

A `module` block, a `source` and a `providers` map have no counterpart in pravo. The unit of reuse is a definition that contains resources, or a package of such definitions. Its resources are written as in a root. The instance sits at a path, and that path is the prefix of the addresses of the resources inside. There is nothing to wire.

The word module names one thing in pravo: a CUE module, the tree of packages under `cue.mod/module.cue`. Import paths start with its name, and [`pravo mod`](mod.md) publishes it to a registry, fetches it by version and locks its content. A definition travels between repositories inside one.

| Form | Written as | Resource address |
|---|---|---|
| Definition in the root | `api: #Service & {name: "api"}` | `api.secret` |
| Several instances | `services: {for n, p in ports {"\(n)": #Service & {...}}}` | `services.web.secret` |
| Package in the same CUE module | `api: svc.#Service & {...}` | `api.secret` |
| Package from a published CUE module | `api: svc.#Service & {...}`, after `pravo mod get` | `api.secret` |
| Package with a constraint | `$eu: strict.#Random` | the addresses of the resources built through `$eu` |

Inputs are the fields of the definition. A required input is a field with no default. Constraints on the fields do the work of validation. Two resources whose addresses differ only in case, such as `Logs` and `logs`, are an error: a state on a file system that ignores case would keep both in one file.

Publishing, locking and fetching CUE modules are commands: see [mod](mod.md), [Publish a CUE module](../how-to/publishing-a-module.md), [Use a published CUE module](../how-to/using-a-module.md) and [Share code between roots in one repository](../how-to/share-code-between-roots.md).

## Definitions

Work in an empty directory. Write `cue.mod/module.cue` first: a root is inside a CUE module, and `pravo init` creates the file when none exists.

```cue file=cue.mod/module.cue
module: "example.com/infra@v0"
language: version: "v0.14.0"
```

```cue file=main.cue
package infra

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

#Service: {
	name: string
	port: int & >1024

	secret: $random.#Resource.random_password & {length: 24, special: false}
	config: $local.#Resource.local_file & {
		filename: "out/\(name).json"
		content:  "{\"port\": \(port)}"
	}
}

api: #Service & {name: "api", port: 8080}

services: {
	for n, p in {web: 3000, worker: 3001} {
		"\(n)": #Service & {name: n, port: p}
	}
}
```

```sh run
pravo init > /dev/null
pravo plan | grep -E '^  #|^Plan'
```

```text out
  # api.secret (random_password) will be created
  # api.config (local_file) will be created
  # services.web.secret (random_password) will be created
  # services.web.config (local_file) will be created
  # services.worker.secret (random_password) will be created
  # services.worker.config (local_file) will be created
Plan: 6 to add, 0 to change, 0 to destroy.
```

The resources inside are built through the providers of the package, here `$random` and `$local`. A reference such as `api.config.filename` is a reference to the resource.

A constraint on an input is checked when the specification is evaluated:

```cue file=main.cue
package infra

#Service: {
	name: string
	port: int & >1024
}

api: #Service & {name: "api", port: 80}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
api.port: invalid value 80 (out of bound >1024):
    ./main.cue:5:14
    ./main.cue:8:37
```

A required input that is left out stays non-concrete. The plan stops at the first value that needs it:

```cue file=main.cue
package infra

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

#Service: {
	name: string
	config: $local.#Resource.local_file & {
		filename: "out/\(name).json"
		content:  "{}"
	}
}

api: #Service
```

```sh run exit=1
pravo plan
```

```text out
Error: api.config.filename: value depends on #Service.name, which is not concrete (is a -t tag missing?)
```

Name the loop variable differently from the input it fills. In `{name: n, port: port}` the right-hand `port` is the field itself, which is never concrete.

## Packages

To share a definition between roots, put it in a package and import it. The root must be inside a CUE module. A package cannot see the providers of the root that imports it, so it takes them as inputs and builds its resources through them.

```cue file=svc/svc.cue
package svc

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

#Service: {
	name:    string
	random!: rnd.#Provider
	local!:  loc.#Provider

	secret: random.#Resource.random_password & {length: 16}
	config: local.#Resource.local_file & {
		filename: "\(name).txt"
		content:  secret.result
	}
}
```

```cue file=main.cue
package infra

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	"example.com/infra/svc"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider

api: svc.#Service & {name: "api", random: $random, local: $local}
```

```sh run
pravo init > /dev/null
pravo plan | grep -E '^  #|^Plan'
```

```text out
  # api.secret (random_password) will be created
  # api.config (local_file) will be created
Plan: 2 to add, 0 to change, 0 to destroy.
```

## Providers in a shared definition

A definition that is published or reused declares each provider it needs as a required input of type `#Provider`, `provider!: loc.#Provider`, and builds its resources through it. The caller passes a provider struct. The input is an alias of that struct, not a second instance: the resource is recorded under the provider instance of the caller. There is no line on the resource that names the provider. Building it through `provider` is enough.

A definition with several providers declares one input for each, and the caller passes them all, as `random: $random, local: $local` above. A definition written in the same package as its provider may refer to a top-level provider directly. That ties it to the package, so a definition meant for other roots takes its providers as inputs.

```cue file=main.cue
package infra

import loc "pravo.dev/providers/hashicorp/local"

#Note: {
	provider!: loc.#Provider
	text:      string
	file: provider.#Resource.local_file & {
		filename: "out/\(text).txt"
		content:  "\(text)\n"
	}
}

$primary:   loc.#Provider & {$version: "~> 2.5"}
$secondary: loc.#Provider

one: #Note & {provider: $primary, text: "one"}
two: #Note & {provider: $secondary, text: "two"}
```

```sh run
pravo apply --auto-approve | tail -n 1
pravo state query '{for a, r in resources {"\(a)": r.provider_key}}'
pravo destroy --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
{
	"one.file": "local.$primary"
	"two.file": "local.$secondary"
}
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

### An instance that passes no provider

A required input that an instance leaves out is an error when the specification loads. The message names the instance and the field. No provider starts, and no resource is built through a provider that nobody configured.

```cue file=main.cue status=rejected
package infra

import loc "pravo.dev/providers/hashicorp/local"

#Note: {
	provider!: loc.#Provider
	text:      string
	file: provider.#Resource.local_file & {
		filename: "out/\(text).txt"
		content:  "\(text)\n"
	}
}

$primary: loc.#Provider

one: #Note & {provider: $primary, text: "one"}
two: #Note & {text: "two"}
```

```sh run exit=6
pravo validate
```

```text out
Error: provider two.provider was not supplied: two requires a hashicorp/local provider in its field provider. Pass one, for example
	two: ... & {provider: $local}
```

A field without the `!` is a provider struct of its own. An instance that leaves it out builds its resources through that struct, a second instance of the provider with no configuration, and nothing warns. [Pitfalls](../migrate/m9-pitfalls.md#a-provider-that-was-not-passed) shows the run. A required input has no such default.

### Configuration in the definition and in the caller

The instance of a passed-in provider is configured from the caller's struct. The input is another name for that struct, so what the definition writes on it is checked against what the caller wrote.

| The definition writes | The caller's struct | Result |
|---|---|---|
| nothing | any | The caller's configuration. |
| a value | the same value | The caller's configuration. |
| a value | another value for the same field | CUE reports `conflicting values`, with the path of the field in the instance. |
| a value | nothing for that field | An error that names the field. The definition cannot configure a provider it was given. |

Neither side wins a conflict: the specification does not load. The provider `tls` takes its proxy in configuration, so it can show both errors.

```cue file=main.cue status=rejected
package infra

import tlsp "pravo.dev/providers/hashicorp/tls"

#Key: {
	tls!: tlsp.#Provider & {proxy: [{url: "http://shared.internal:3128"}]}
	key:  tls.#Resource.tls_private_key & {algorithm: "ECDSA", ecdsa_curve: "P256"}
}

$tls: tlsp.#Provider & {proxy: [{url: "http://caller.internal:3128"}]}

a: #Key & {tls: $tls}
```

```sh run exit=6
pravo init > /dev/null
pravo validate
```

```text out
Error: evaluating .:
a.tls.proxy.0.url: conflicting values "http://caller.internal:3128" and "http://shared.internal:3128":
    ./main.cue:6:40
    ./main.cue:10:39
    ./main.cue:12:4
    ./main.cue:12:17
```

When the caller's struct leaves the field out, nothing conflicts, and the value would never reach the provider. pravo reports an error.

```cue file=main.cue status=rejected
package infra

import tlsp "pravo.dev/providers/hashicorp/tls"

#Key: {
	tls!: tlsp.#Provider & {proxy: [{url: "http://shared.internal:3128"}]}
	key:  tls.#Resource.tls_private_key & {algorithm: "ECDSA", ecdsa_curve: "P256"}
}

$tls: tlsp.#Provider

a: #Key & {tls: $tls}
```

```sh run exit=6
pravo validate
```

```text out
Error: a.tls sets proxy, but a.tls is another name for the provider $tls, which does not set it. The provider is configured from $tls alone: set proxy there
```

The same rule holds for the `$` fields, such as `$version`, and for a provider struct that is built from another one at the top level, such as `$other: $tls & {proxy: [...]}`. Configure a provider where it is declared.

## Constraints in a package

Unification is commutative, so a package can constrain resources it did not create. A team ships a provider definition that fixes what the resources built through it may look like, and each root builds its provider from it. The rule is a constraint, checked when the specification evaluates. It is not a [policy](policies-overview.md), which judges a plan.

```cue file=strict/strict.cue
package strict

import rnd "pravo.dev/providers/hashicorp/random"

#Random: rnd.#Provider & {
	#Resource: random_pet: prefix: =~"^eu-"
}
```

```cue file=main.cue
package infra

import "example.com/infra/strict"

$eu: strict.#Random

a: $eu.#Resource.random_pet & {length: 2, prefix: "us-"}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
a.prefix: invalid value "us-" (out of bound =~"^eu-"):
    ./strict/strict.cue:6:33
    ./cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue:238:12
    ./main.cue:7:51
```

With `prefix: "eu-"` the same root validates.

A constraint rejects a value that is set and wrong. It does not reject an attribute that is left out. An unset attribute with no reference to another resource is sent to the provider as null. A rule that an attribute is present is a rule for a policy. See [Providers](spec-providers.md#provider-scoped-constraints) and [Policies](policies-overview.md).

## Templates

A definition that embeds `pravo.#Template` is a template. [The viewer](viewer-sections.md#the-templates-section) lists it in a catalog, builds a form from its inputs and gives the CUE of a new instance to paste. The form is made from the definition: the type, the default, the comment and the constraint of each input field are read from it, so a definition with constraints needs nothing more.

```cue
#Service: pravo.#Template & {
	$template: {
		name:  "service"
		title: "Web service"
		// The fields the form asks for, in order. Without it the form asks for
		// every field that holds no resource.
		inputs: {name: {}, env: {}}
	}
	// Lowercase letters and dashes.
	name: string & =~"^[a-z][a-z0-9-]{2,30}$"
	env:  *"dev" | "staging" | "prod"
	secret: $random.#Resource.random_password & {length: 24}
}

services: web: #Service & {name: "web", env: "prod"}
```

The marker `$template` is an ordinary field. It passes into every instance when the definition is instantiated with `&`, so `services.web` carries `$template: name: "service"`, and that is how the viewer knows which template an instance came from. An instance is a group like any other: its resources have the addresses `services.web.secret`, and nothing about it differs in a plan. `$template` has `name` and, optionally, `title`, `description` and `inputs`. The definition is open, so a template adds fields of its own.

### Inputs that are not typed in

`$template.inputs` is one struct with an entry for each input. The key is a field of the definition, and the order of the keys is the order of the form. An entry says more of an input than its type does, and an empty entry, `name: {}`, says nothing. Every entry may have a `label`, which the form shows instead of the name of the field, and a `description`, which it shows above the input instead of the comment of the field. Without them the form shows the name and the comment.

```cue
#Service: pravo.#Template & {
	$template: {
		name: "service"
		inputs: {
			name: {}
			// Values to choose from, read from a node of the graph.
			region: {choices: regions.outputs}
			// A resource of the estate to refer to.
			network: {entity: {type: "random_id", attr: "hex"}}
			// A long text, with a label and a description of its own.
			notes: {as: "text", label: "Notes for on-call", description: "Who to page, and in what order."}
			tags:  {}
		}
	}
	name:    string
	region:  or([for k, _ in regions.outputs {k}])
	network: string
	notes:   *"" | string
	tags:    *[] | [...string]
}
```

An entry is closed and says at most one of `choices`, `entity` and `as`, so a misspelt key, or two of them, is an error of `pravo validate` with the line. A key that is not a field of the definition, or that names a field that holds a resource or is fixed to a value, is shown on the page of the template in the viewer.

| Entry | The form shows | The code holds |
|---|---|---|
| `choices: NODE.ATTR` | A list of the values that the state holds at the attribute that the reference points to. A list offers its items, and a map its keys, with the value as the label. A value that the Inventory withholds is not offered. | The value that was picked, as a string. |
| `entity: {type, attr}` | The managed resources of that type that the specification declares, with a search. A sensitive or write-only attribute is never offered. | The reference, such as `core.network_id.hex`, so the instance follows the resource. |
| `as: "text"` | A text area. | The text, as one CUE string. |
| `as: "secret"` | A choice of where the secret comes from, and its name. It takes no value. | The input reads the `value` of a secret that the code declares. See below. |

A list of strings or of numbers is a row of chips with an empty entry.

The entry helps the form; the constraint stays in the definition. `region` above is a disjunction over what the data source `regions` reports, so a plan reads the data source and refuses a region that it does not list. The list in the form is what the state recorded at the last plan, and a value that is not in it is refused in the form before the code is pasted. `choices` is a reference, so CUE checks that it names something: a misspelt node is an error of `pravo validate`, as for any reference. The path written as a string, `"regions.outputs"`, also works but nothing checks it, and the form only says that no node is there. A node that has no record yet has nothing to offer, and the form says so and takes a typed value.

A secret input never takes its value on the page. The form asks for the source, `pravo.#Secret` of a sealed state or `pravo.#SecretEnv` for an environment variable, and for a name. The code that it writes declares the secret next to the instance and passes `value` to the input:

```cue
certs: edge: #Certificate & {
	common_name: "edge.example.com"
	signing_key: edge_signing_key.value
}

// Set it before the apply: pravo secret set certificate/edge/signing_key
edge_signing_key: pravo.#Secret & {name: "certificate/edge/signing_key"}
```

The form says whether the sealed state holds the secret already, and gives the command that sets it when it does not. The template has to pass the input only to a write-only attribute, to the configuration of a provider or to an ephemeral resource, as with any secret: [Store secrets in the sealed state](../how-to/store-secrets-in-the-state.md) says where a secret may go.

A root made before `inputs` took a struct has to run [`pravo init`](init.md) again, because the package that it holds describes the older form.

A new instance is checked before it is pasted: the viewer loads the specification with the CUE laid over it in memory, and tries the policies on an approximate plan. The check writes nothing.

The package `pravo.dev/pravo` is written by [`pravo init`](init.md), so a root made before `#Template` was added has to run it again.

## See also

- [Share code between roots in one repository](../how-to/share-code-between-roots.md), [Publish a CUE module](../how-to/publishing-a-module.md) and [Use a published CUE module](../how-to/using-a-module.md) cover the three ways to share code.
- [`pravo mod`](mod.md) lists the commands for CUE modules.
- [Providers](spec-providers.md#passing-a-provider-into-a-definition) describes the provider input.
