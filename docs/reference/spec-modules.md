---
title: Modules
description: A module is a CUE definition or package that contains resources. Instantiating it is enough. pravo finds the resources inside the instance.
order: 29
group: Language
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
keywords: "module source version"
---

Two resources whose addresses differ only in case, such as `Logs` and `logs`, are an error: a state on a file system that ignores case would keep both in one file.

There is no module block and nothing to wire. A reusable unit is a definition, or a package of definitions. Its resources are written as in a root. The instance sits at a path, and that path is the prefix of the addresses of the resources inside.

| Form | Written as | Resource address |
|---|---|---|
| Definition in the root | `api: #Service & {name: "api"}` | `api.secret` |
| Several instances | `services: {for n, p in ports {"\(n)": #Service & {...}}}` | `services.web.secret` |
| Package in the module | `api: svc.#Service & {...}` | `api.secret` |
| Package with a policy | `$eu: policy.#Random` | the addresses of the resources built through `$eu` |

Inputs are the fields of the definition. A required input is a field with no default. Constraints on the fields do the work of validation.

Publishing, locking and fetching modules are commands: see [mod](mod.md), [Publish a module](../how-to/publishing-a-module.md), [Use a module](../how-to/using-a-module.md) and [Share code between roots](../how-to/modules-in-a-repository.md).

## Definitions

Work in an empty directory. Write the module path first. A root is inside a CUE module, and `pravo init` creates `cue.mod/module.cue` when none exists.

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

To share a module between roots, put the definitions in a CUE package and import it. The root must be inside a CUE module. A package cannot see the providers of the root that imports it, so it takes them as inputs and builds its resources through them.

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

## Providers in a module

A module that is published or reused declares each provider it needs as a required input of type `#Provider`, `provider!: loc.#Provider`, and builds its resources through it. The caller passes a provider struct. The input is an alias of that struct, not a second instance: the resource is recorded under the provider instance of the caller. There is no line on the resource that names the provider. Building it through `provider` is enough.

A module with several providers declares one input for each, and the caller passes them all, as `random: $random, local: $local` above. A module written in the same package as its provider may refer to a top-level provider directly. That ties it to the package, so a module meant for other roots takes its providers as inputs.

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

### Configuration in the module and in the caller

The instance of a passed-in provider is configured from the caller's struct. The input is another name for that struct, so what the module writes on it is checked against what the caller wrote.

| The module writes | The caller's struct | Result |
|---|---|---|
| nothing | any | The caller's configuration. |
| a value | the same value | The caller's configuration. |
| a value | another value for the same field | CUE reports `conflicting values`, with the path of the field in the instance. |
| a value | nothing for that field | An error that names the field. The module cannot configure a provider it was given. |

Neither side wins a conflict: the specification does not load. The provider `tls` takes its proxy in configuration, so it can show both errors.

```cue file=main.cue status=rejected
package infra

import tlsp "pravo.dev/providers/hashicorp/tls"

#Key: {
	tls!: tlsp.#Provider & {proxy: [{url: "http://module.internal:3128"}]}
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
a.tls.proxy.0.url: conflicting values "http://caller.internal:3128" and "http://module.internal:3128":
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
	tls!: tlsp.#Provider & {proxy: [{url: "http://module.internal:3128"}]}
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

## Policy packages

Unification is commutative, so a package can constrain resources it did not create. A team ships a provider definition that fixes what the resources built through it may look like, and each root builds its provider from it.

```cue file=policy/policy.cue
package policy

import rnd "pravo.dev/providers/hashicorp/random"

#Random: rnd.#Provider & {
	#Resource: random_pet: prefix: =~"^eu-"
}
```

```cue file=main.cue
package infra

import "example.com/infra/policy"

$eu: policy.#Random

a: $eu.#Resource.random_pet & {length: 2, prefix: "us-"}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
a.prefix: invalid value "us-" (out of bound =~"^eu-"):
    ./policy/policy.cue:6:33
    ./cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue:238:12
    ./main.cue:7:51
```

With `prefix: "eu-"` the same root validates.

A constraint rejects a value that is set and wrong. It does not reject an attribute that is left out. An unset attribute with no reference to another resource is sent to the provider as null. A rule that an attribute is present is a rule for the plan policy. See [Providers](spec-providers.md#provider-scoped-constraints) and [Policies](policies-overview.md).

## See also

- [Share code between roots in one repository](../how-to/modules-in-a-repository.md), [Publish a module](../how-to/publishing-a-module.md) and [Use a published module](../how-to/using-a-module.md) cover the three ways to share code.
- [`pravo mod`](mod.md) lists the module commands.
- [Providers](spec-providers.md#passing-a-provider-into-a-module) describes the provider input.
