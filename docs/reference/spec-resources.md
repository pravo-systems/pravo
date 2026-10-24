---
title: Resources, data sources and calls
description: How to declare managed resources, data sources, ephemeral resources and function calls, how pravo finds them, and how they are addressed.
order: 22
group: Language
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
---

A resource is a struct built through a provider. The provider struct holds the definitions of the types it offers, in four families.

| Kind | Built from | `$kind` | Recorded in state | Meta fields |
|---|---|---|---|---|
| Managed resource | `#Resource.<type>` | `resource` | yes | `$depends_on`, `$lifecycle`, `$from`, `$import` |
| Data source | `#Data.<type>` | `data` | yes | `$depends_on`, `$from` |
| Ephemeral resource | `#Ephemeral.<type>` | `ephemeral` | no | `$depends_on` |
| Function call | `#Function.<name>` | `call` | no | none |

A fifth kind, the discovery, is built from `#Discovery.<type>` and has `$kind` `discovery`. It lists objects and manages none, so it is not a resource: it has no address in the graph, no record and no plan. [Discovery](../concepts/discovery.md) describes it.

Each definition carries three identity fields: `$kind`, `$type` (the provider's name for the type, such as `random_pet`) and `$source` (the provider address). A struct that has them is a resource, whatever else it holds. The rest of the definition is the provider schema, followed by the [meta fields](spec-meta-fields.md).

A provider struct is declared first. See [Providers](spec-providers.md). Work in an empty directory.

## Managed resources

`&` combines the definition with the attributes you set. Computed attributes such as `id` are fields too, so another resource can refer to them before they exist.

```cue file=main.cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 3, separator: "-"}

hello: $local.#Resource.local_file & {
	filename: "hello.txt"
	content:  "Hello, \(pet.id)!"
}
```

```sh run
pravo init
pravo plan | grep -E '^  #|^Plan'
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
  # pet (random_pet) will be created
  # hello (local_file) will be created
Plan: 2 to add, 0 to change, 0 to destroy.
```

A nested block is a struct, or a list of structs. A reference such as `pet.id` is a dependency: `hello` waits for `pet`. `pravo schema random_pet` prints the definition of a type with the provider's documentation.

## Data sources

A data source is read, not managed. It is built from `#Data.<type>`.

```cue file=main.cue
package demo

import (
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

src: $local.#Resource.local_file & {filename: "src.txt", content: "hello\n"}

known: $local.#Data.local_file & {filename: "README.md"}

later: $local.#Data.local_file & {filename: src.filename}
```

```sh run
echo "readme" > README.md
pravo plan | grep -E '^  #|^ <=|^Plan'
```

```text out
  # src (local_file) will be created
  # later (local_file) will be read (configuration depends on values known after apply)
 <= later: (known after apply)
Plan: 1 to add, 0 to change, 0 to destroy.
```

A data source is read during the plan when its configuration is known and nothing it depends on has a pending change. Otherwise the read waits for the apply, and its attributes are unknown in the plan. `known` is read now. `later` depends on `src`, which does not exist yet.

## Ephemeral resources

An ephemeral resource exists for the duration of a run. pravo opens it, uses it and never records it: a short-lived credential, a token, a tunnel.

```cue file=main.cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

secret: $random.#Ephemeral.random_password & {length: 24, special: false}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  secret.result
}
```

An ephemeral value may appear in provider configuration, in write-only attributes of managed resources and in other ephemeral resources. Any other place is recorded in state, so pravo refuses an ephemeral value there and in an output.

```sh run exit=1
pravo plan
```

```text out
secret: Opening...

Error: note.content: references secret, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)
```

## Function calls

A provider-defined function is called by declaring a struct built from `#Function.<name>`. `args` are the positional arguments. `result` is filled in when the call has run. This example uses `hashicorp/kubernetes`, which needs no cluster and no credentials.

```cue file=main.cue
package demo

import (
	"pravo.dev/pravo"
	k8s "pravo.dev/providers/hashicorp/kubernetes"
)

$k8s: k8s.#Provider

_manifest: """
	apiVersion: v1
	kind: ConfigMap
	metadata:
	  name: demo
	"""

decoded: $k8s.#Function.manifest_decode & {args: [_manifest]}

kind: pravo.#Output & {value: decoded.result.kind}
```

```sh run
pravo init
pravo plan
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  kubernetes = hashicorp/kubernetes v3.4.0
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/kubernetes (81 resource types, 27 data sources, 2 ephemeral, 3 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.

No changes. Your infrastructure matches the configuration.
Graph: called 1

Outputs:
  kind: "ConfigMap"
```

Calls are pure. A call leaves no record in state, and it runs again on every plan and apply. `pravo schema kubernetes/manifest_decode` prints the parameters and the documentation. The generated `args` type matches the parameter list, so a wrong number or type of arguments fails when the specification is evaluated. A call whose arguments are not yet known is deferred until the apply. An ephemeral value cannot be an argument.

## How pravo finds resources

pravo walks the evaluated specification and collects every struct that has a `$kind`. It does not search inside a resource.

| The walk | Result |
|---|---|
| Regular fields and list elements | Entered. A comprehension that produces a struct or a list of resources works. |
| `$settings` | Skipped. |
| Definitions (`#Service`), hidden fields (`_x`), optional fields (`x?:`) and pattern constraints | Skipped. A definition declares nothing until a regular field instantiates it. |
| A regular field that holds a bare definition such as `$random.#Resource.random_pet` | Found, because the struct carries `$kind`. |
| `$`-prefixed fields inside a resource | Skipped when pravo looks for nested resources. |

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 1}

servers: [
	for i in [0, 1] {$random.#Resource.random_integer & {min: 1, max: 10 + i}},
]

regions: ["eu", "us"]
pets: {for r in regions {"\(r)": $random.#Resource.random_pet & {length: 2, prefix: r}}}

#Template: $random.#Resource.random_pet & {length: 3}
_hidden:   $random.#Resource.random_pet & {length: 3}
copy:      pet
```

```sh run
pravo plan | grep '^  #'
```

```text out
  # pet (random_pet) will be created
  # servers[0] (random_integer) will be created
  # servers[1] (random_integer) will be created
  # pets.eu (random_pet) will be created
  # pets.us (random_pet) will be created
  # copy (random_pet) will be created
```

`#Template` and `_hidden` declare nothing. `copy: pet` copies the whole struct, so it declares a second resource at the address `copy`.

A resource cannot contain another resource. A definition is closed, so CUE rejects the nesting as `field not allowed`:

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 1}
a: b: $random.#Resource.random_pet & {length: 1}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
a.b: field not allowed:
    ./main.cue:8:4
```

## Addresses

The address of a resource is the CUE path at which its struct sits. `pravo state list` prints addresses. `-replace`, `pravo import`, `pravo why`, `pravo state show` and `pravo state mv` take them.

| Declaration | Address |
|---|---|
| `pet: $random.#Resource.random_pet & {...}` | `pet` |
| `readme: $local.#Data.local_file & {...}` | `readme` |
| `config` inside the module instance `api` | `api.config` |
| the first element of a list `servers` | `servers[0]` |
| the key `"eu-west"` in `pets` | `pets."eu-west"` |

Data sources have no `data.` prefix. The `mode` of the record says what it is. An address that CUE needs to quote is written as CUE writes the path. Quote it for the shell.

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pets: {
	"eu-west": $random.#Resource.random_pet & {length: 1}
	eu:        $random.#Resource.random_pet & {length: 1}
}
```

```sh run
pravo apply --auto-approve | tail -n 1
pravo state list
pravo state show 'pets."eu-west"' | head -n 1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
pets."eu-west"
pets.eu
// pets."eu-west" (provider registry.opentofu.org/hashicorp/random v3.9.1 as random, schema version 0, revision 1)
```

`--replace` and `--only` take a comma-separated list read as CSV. Write a quoted address as a CSV field:

```sh run
pravo plan --replace '"pets.""eu-west"""' | grep -E '^  #|^Plan'
pravo destroy --auto-approve | tail -n 1
```

```text out
  # pets."eu-west" (random_pet) must be replaced
Plan: 1 to add, 0 to change, 1 to destroy.
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

## References

A reference to a resource is a CUE path: `pet.id`, `api.config.filename`, `servers[0].result`. It is a dependency. The path may pass through plain intermediate fields.

Inside a struct, a name resolves to the nearest field of that name. A field that repeats the name of its value, as in `{name: n, port: port}`, refers to itself and is never concrete. Use a different name, or reach the outer field through a hidden alias such as `_proxy: proxy`.

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

proxy: $random.#Resource.random_pet & {length: 2}

web: {
	proxy: {id: "x"}
	r: $random.#Resource.random_pet & {prefix: proxy.id}
}
```

```sh run
pravo eval -e web.r.prefix
```

```text out
"x"
```

`proxy.id` inside `web` means the field `web.proxy.id`, not the resource. A provider name that starts with `$` cannot be the name of an attribute, so `$random.#Resource...` means the provider wherever it is written.

A field that repeats the name of its value refers to itself:

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

port: 8080

svc: {
	name: "api"
	port: port
	pet:  $random.#Resource.random_pet & {length: 2, prefix: "\(name)-\(port)"}
}
```

`pravo validate` succeeds. The plan stops, because `svc.port` is never concrete:

```sh run exit=1
pravo plan
```

```text out
Error: svc.pet.prefix: value depends on svc.port, which is not concrete (is a -t tag missing?)
```

## How a resource finds its provider

A resource uses the provider it was built through. pravo reads this from the expression: it selects `#Resource`, `#Data`, `#Ephemeral` or `#Function` from a path that is a provider struct. There is no search by source and no field to set on the resource. The path is followed through plain intermediate fields.

```cue file=main.cue
package demo

import loc "pravo.dev/providers/hashicorp/local"

$primary:   loc.#Provider & {$version: "~> 2.5"}
$secondary: loc.#Provider

#File: $primary.#Resource.local_file & {content: "x\n"}

a: $primary.#Resource.local_file & {filename: "a.txt", content: "a\n"}
b: $secondary.#Resource.local_file & {filename: "b.txt", content: "b\n"}
c: #File & {filename: "c.txt"}
```

```sh run
pravo apply --auto-approve | tail -n 1
pravo state query '{for a, r in resources {"\(a)": r.provider_key}}'
pravo destroy --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
{
	a: "local.$primary"
	b: "local.$secondary"
	c: "local.$primary"
}
Destroy complete! Resources: 0 added, 0 changed, 3 destroyed. [txn <id>]
```

A resource that was not built through a declared provider is an error that shows the form to use:

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

x: rnd.#Provider.#Resource.random_pet & {length: 2}
```

```sh run exit=6
pravo validate
```

```text out
Error: x was not built through a provider. Declare a provider, for example
	$aws: awsp.#Provider & {...}
and build the resource through it: $aws.#Resource.random_pet & {...}
```

## Several resources from one rule

A comprehension generates fields. The key is the last element of the address.

```cue file=main.cue
package demo

import (
	"list"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

extra: *false | bool @tag(extra,type=bool)

servers: [for i in list.Range(0, 2, 1) {$random.#Resource.random_integer & {min: 1, max: 10 + i}}]

if extra {
	spare: $random.#Resource.random_pet & {length: 1}
}
```

```sh run
pravo plan | grep -E '^  #|^Plan'
pravo plan -t extra=true | grep -E '^  #|^Plan'
```

```text out
  # servers[0] (random_integer) will be created
  # servers[1] (random_integer) will be created
Plan: 2 to add, 0 to change, 0 to destroy.
  # servers[0] (random_integer) will be created
  # servers[1] (random_integer) will be created
  # spare (random_pet) will be created
Plan: 3 to add, 0 to change, 0 to destroy.
```

With `-t extra=true` the resource `spare` exists. Without it nothing is declared, and an existing record for `spare` is planned for destruction. See [Tags and variables](spec-tags-and-variables.md).

## Writing a resource in parts

Unification merges declarations of one path across files.

```cue file=main.cue
package demo

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

logs: $local.#Resource.local_file & {filename: "logs.txt"}
```

```cue file=content.cue
package demo

logs: content: "hello"
```

```sh run
pravo eval -e logs.content
```

```text out
"hello"
```

## See also

- [Resources and addresses](../concepts/resources-and-addresses.md) explains identity, renames and moves.
- [Meta fields](spec-meta-fields.md) and [Providers](spec-providers.md) describe the `$` fields.
- [`pravo schema`](schema.md) prints the definition of one type.
