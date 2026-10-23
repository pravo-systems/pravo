<!-- Generated from docs/reference/spec-providers.md by tools/skillgen. Do not edit. -->

# Provider declarations

How to declare a provider, the $-fields that tell pravo how to obtain it, instance keys, and rules that bind every resource built through a provider.

A provider is a struct built from the `#Provider` definition of a generated provider package. It is found like a resource: it carries `$kind: "provider"` and `$source`, and its address is its path.

| Field | Meaning |
|---|---|
| any field without `$` | The provider's own configuration, checked against its configuration schema. |
| `$version` | A version constraint such as `"~> 3.6"`. Omit it to accept any version. |
| `$path` | A provider binary or its directory. Nothing is downloaded. A relative path is relative to the root. |
| `$protocol` | `"tfplugin"` (default) or `"tfplugin+reattach"`. |
| `$address` | With `tfplugin+reattach`: `unix:///path` or `tcp://127.0.0.1:port`. |
| `$from` | The registry address the provider had before, such as `"registry.opentofu.org/hashicorp/aws"`. |
| `#Resource`, `#Data`, `#Ephemeral`, `#Function` | The members that resources are built from. |

The `$` fields tell pravo how to obtain and reach the provider. They are never sent to it. `$source` and `$kind` come from the definition.

## Declaring a provider

Every provider that a resource, data source, ephemeral resource or function call is built through must be declared, including providers that need no configuration. A provider struct can sit anywhere, under any field name. Name it with a leading `$` by convention.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 2}
```

```sh
pravo init
pravo providers | grep -v '^ '
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
local = hashicorp/local v2.9.1
random = hashicorp/random v3.9.1
```

A provider called `random` is shadowed inside a resource that has an attribute called `random`. The `$` prefix avoids the clash. The import alias (`rnd`) must differ from every field name in scope.

## One version per source

pravo uses one version of a provider source. Two structs of one source with different `$version` values are an error.

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$a: rnd.#Provider & {$version: "~> 3.6"}
$b: rnd.#Provider & {$version: "~> 3.5"}
```

```sh
pravo validate
```

Output:

```text
Error: $b: $version "~> 3.5" conflicts with "~> 3.6" on another hashicorp/random provider; one version of a provider is used
```

Structs of one source can differ in their configuration. Each resource uses the struct it was built through. See [Resources](spec-resources.md#how-a-resource-finds-its-provider).

`$path`, `$protocol` and `$address` merge across the structs of one source.

## Protocol

A provider has a `$protocol`: how pravo reaches it. Both values speak the OpenTofu provider plugin protocol, versions 5 and 6.

| `$protocol` | Meaning |
|---|---|
| `tfplugin` | pravo installs the provider, starts it, talks to it and stops it. This is the default. |
| `tfplugin+reattach` | The provider is already running. pravo connects to it at `$address` and never starts, stops or signals it. |

`$address` is `unix:///path/to/socket` or `tcp://127.0.0.1:port`. TCP is accepted on the loopback interface only. pravo detects the protocol version by trying 6, then 5.

```cue
import dev "pravo.dev/providers/acme/dev"

$dev: dev.#Provider & {
	$protocol: "tfplugin+reattach"
	$address:  "unix:///tmp/dev-provider.sock"
}
```

A reattached provider has no entry in `pravo.lock.cue`, and its schema is not cached: pravo reads it from the running provider on every run. The environment variable `TF_REATTACH_PROVIDERS` is honoured. When it names a provider, pravo reaches that provider by reattaching, whatever its declaration says.

The combinations that do not make sense are refused:

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$a: rnd.#Provider & {$protocol: "tfplugin+reattach", $version: "~> 3.6"}
```

```sh
pravo validate
```

Output:

```text
Error: provider random: version does not apply to protocol "tfplugin+reattach": pravo does not install or select what it connects to
```

| Declaration | Error |
|---|---|
| `tfplugin+reattach` with `$version` | `version does not apply to protocol "tfplugin+reattach"` |
| `tfplugin+reattach` with `$path` | `path starts a provider; protocol "tfplugin+reattach" connects to one that is already running` |
| `tfplugin+reattach` without `$address` | `protocol "tfplugin+reattach" needs an address` |
| `$address` without `tfplugin+reattach` | `address applies to protocol "tfplugin+reattach", not "tfplugin"` |
| any other `$protocol` | CUE rejects the value: it is not `"tfplugin"` or `"tfplugin+reattach"`. |

## Provider-scoped constraints

A constraint written on the nested `#Resource` of a provider definition applies to every resource built through any struct that embeds the definition. A definition that wraps `#Provider` states the rule once.

File `main.cue`:

```cue
package demo

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

ok:  $files.#Resource.local_file & {filename: "ok.txt", content: "x"}
bad: $files.#Resource.local_file & {filename: "bad.txt", content: "x", file_permission: "0644"}
```

`ok` plans with `file_permission: "0600"`. `bad` does not evaluate, and `pravo validate` exits with 6:

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
bad.file_permission: 2 errors in empty disjunction:
bad.file_permission: conflicting values "0600" and "0644":
    ./main.cue:6:42
    ./main.cue:12:6
    ./main.cue:12:89
bad.file_permission: conflicting values "0644" and null (mismatched types string and null):
    ./cue.mod/gen/pravo.dev/providers/hashicorp/local/local_gen.cue:93:29
    ./main.cue:12:6
    ./main.cue:12:89
```

A constraint rejects a value that is set and wrong. It does not require that an attribute is set. A rule such as "every bucket carries a team tag" is a rule for the [plan policy](policies-overview.md), which judges the changes that a plan makes. A team can still declare its own provider from `loc.#Provider` and build resources through that. The constraint binds the roots that build their providers from the wrapper.

## Passing a provider into a module

A module takes its provider from its caller. It declares a required input of type `#Provider`, `provider!: loc.#Provider`, and builds its resources through it. The input is an alias of the caller's struct, not a second instance, and the instance is configured from the caller's struct alone.

- An instance that passes no provider is an error when the specification loads: `provider two.provider was not supplied`.
- Configuration that the module writes on the input must agree with the caller's struct. Another value for the same field is a CUE conflict. A field that the caller's struct leaves out is an error.

See [Modules](spec-modules.md#providers-in-a-module) for both errors as they are printed.

## Instance keys

Each provider struct is one provider instance, and each instance has a key. The state records it as `provider_key`. The key is the provider's local name when the struct is the only one of its source (`random`, `aws`), and `<local>.<path>` when there are several (`aws.$eu`, `local.$primary`). The local name is the provider's type name, made unique with the namespace if two sources share a type.

File `main.cue`:

```cue
package demo

import loc "pravo.dev/providers/hashicorp/local"

$primary:   loc.#Provider & {$version: "~> 2.5"}
$secondary: loc.#Provider

a: $primary.#Resource.local_file & {filename: "a.txt", content: "a\n"}
b: $secondary.#Resource.local_file & {filename: "b.txt", content: "b\n"}
```

```sh
pravo apply --auto-approve | tail -n 1
pravo state show b | head -n 1
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
// b (provider registry.opentofu.org/hashicorp/local v2.9.1 as local.$secondary, schema version 0, revision 1)
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

A key changes when a struct of the same source is added or removed, or when a resource is built through another struct. The plan proposes no change to the object. `pravo why` gives the reason `provider instance changed (A -> B)`, and the next apply records the new key. [Providers](../concepts/providers.md#instances-and-keys) shows the run.

## Provider configuration that uses resources

A provider's configuration may refer to resources, data sources and ephemeral resources. A Kubernetes provider can take its endpoint from a cluster resource, and a provider can take a proxy password from an ephemeral secret. What the configuration refers to becomes a dependency of every resource built through that provider. The referenced objects run first, and the provider is configured with known values.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

$random: rnd.#Provider

secret: $random.#Ephemeral.random_password & {length: 24, special: false}

$tls: tlsp.#Provider & {
	proxy: [{
		url:      "http://proxy.internal:3128"
		username: "ci"
		password: secret.result
	}]
}

key: $tls.#Resource.tls_private_key & {algorithm: "ECDSA", ecdsa_curve: "P256"}
```

```sh
pravo init
pravo plan --json | jq -c '.changes[] | select(.addr == "key") | {addr, dependencies}'
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  random = hashicorp/random v3.9.1
  tls = hashicorp/tls v4.4.2
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/tls (4 resource types, 2 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
secret: Opening...
{"addr":"key","dependencies":["secret"]}
```

An ephemeral value may be used here, because provider configuration is never recorded.

## Removing a provider

Remove a provider struct only when no resource built through it remains in the state. When the struct and its resources leave the specification together, the records are destroyed on an instance of the provider that has no configuration. That works for a provider that needs none, such as `random`.

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2}
```

```sh
pravo apply --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

Remove the provider and the resource together:

File `main.cue`:

```cue
package demo
```

```sh
pravo plan | grep -E '^  #|^Plan'
pravo providers | grep -v '^ '
```

Output:

```text
  # pet (random_pet) will be destroyed
Plan: 0 to add, 0 to change, 1 to destroy.
random = hashicorp/random v3.9.1 (implied by resource types)
```

A provider that needs configuration, such as a region or credentials, cannot be configured at that point. `pravo plan` stops with `Error: configuring provider aws: ...`. Keep the struct until its resources are gone.

## Development override

`$path` points a provider at a binary on disk. Nothing is downloaded or verified. `pravo init` starts the binary to read its schema, and the provider has no entry in the lock file. The source still names the provider in the state, so switching between an override and a registry build does not change addresses.

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$path: "./dev-random"}

pet: $random.#Resource.random_pet & {length: 2}
```

`dev-random` is a provider binary. `pravo init` reads its schema, and `pravo providers` shows `vdev` where a version normally appears:

```sh
pravo init
pravo providers | grep -v '^ '
```

Output:

```text
Providers:
  random = hashicorp/random vdev
Generated cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
random = hashicorp/random vdev
```

## See also

- [Providers](../concepts/providers.md) explains how pravo finds, starts and locks a provider.
- [Upgrade pravo and providers](../how-to/upgrading.md) and [Install providers from your own registry](../how-to/providers-from-a-registry.md) use these fields.
- [`pravo providers`](providers.md) lists, pins and copies providers, and [Generated packages](spec-generated-packages.md) describes what `pravo init` writes.
