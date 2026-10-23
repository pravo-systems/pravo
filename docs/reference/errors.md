---
title: Error catalogue
description: "Every error a user is likely to meet, as pravo prints it: the symptom, the cause and the fix."
order: 60
group: Lookup
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\[id=[a-z]+(-[a-z]+)+\]'
    replace: '[id=<pet>]'
  - pattern: '(id:\s+)"[a-z]+(-[a-z]+)+"'
    replace: '$1"<pet>"'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]+(Z|[+-][0-9]{2}:[0-9]{2})'
    replace: '<time>'
  - pattern: 'sha256:[0-9a-f]{12}'
    replace: 'sha256:<digest>'
keywords: "field not allowed conflicting values incomplete value error message exit code"
---

An error starts with `Error:` and goes to standard error. The exit code says what kind of failure it is: see [Exit codes](exit-codes.md). Under `--json` the same error is also an object on standard output: `{"code":N,"kind":"error","message":"..."}`.

An error from CUE has a fixed shape. The first line is the path of the value and the rule it broke. The lines below it are positions, `file:line:column`, one for each declaration that took part.

```text
size: conflicting values 11 and 3:
    ./main.cue:3:7
    ./main.cue:4:7
```

| Where the error arises | Section |
|---|---|
| Before the specification loads | [Loading](#loading) |
| While CUE evaluates the specification | [Evaluating CUE](#evaluating-cue) |
| In `$settings` or in a provider | [Settings and providers](#settings-and-providers) |
| In a resource, an output or an environment variable | [Resources and values](#resources-and-values) |
| In `$from` or `$import` | [Moves and imports](#moves-and-imports) |
| In another root's outputs | [Remote outputs](#remote-outputs) |
| In plan, apply, the state or a saved plan | [Plans and state](#plans-and-state) |
| In a policy | [Policies](#policies) |
| In a module lock | [Modules](#modules) |

Work in an empty directory. The commands print paths in full. This page shows them relative to the directory.

## Loading

pravo loads the package before it evaluates anything. A provider that the specification imports must have a generated package under `cue.mod/gen/`.

```cue file=cue.mod/module.cue
module: "example.com/errors@v0"
language: version: "v0.14.0"
```

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo plan; echo "exit=$?"
pravo validate; echo "exit=$?"
```

```text out
Error: loading .:
example.com/errors@v0: import failed: cannot find package "pravo.dev/providers/hashicorp/random": cannot find module providing package pravo.dev/providers/hashicorp/random:
    ./main.cue:3:8


hint: a provider schema package is missing. Run "pravo init" to generate it
exit=6

Error: loading .:
example.com/errors@v0: import failed: cannot find package "pravo.dev/providers/hashicorp/random": cannot find module providing package pravo.dev/providers/hashicorp/random:
    ./main.cue:3:8


hint: a provider schema package is missing. Run "pravo init" to generate it
exit=6
```

A package name that differs from the last element of the import path needs a colon in the import. The provider `null` is generated as the package `null_`. Generate it, then drop the colon:

```cue file=main.cue
package errors

import nul "pravo.dev/providers/hashicorp/null:null_"

$null: nul.#Provider

marker: $null.#Resource.null_resource & {triggers: build: "1"}
```

```sh run
pravo init > /dev/null
```

```cue file=main.cue
package errors

import nul "pravo.dev/providers/hashicorp/null"

$null: nul.#Provider

marker: $null.#Resource.null_resource & {triggers: build: "1"}
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: loading .:
example.com/errors@v0: import failed: cannot find package "pravo.dev/providers/hashicorp/null": no files in package directory with package name "null":
    ./main.cue:3:8


hint: a provider schema package is missing. Run "pravo init" to generate it
exit=6
```

A file with another package name stops the load. So does a file with none. The error names the file and the package of the other files:

```cue file=main.cue
package errors

x: 1
```

```cue file=other.cue
package other

y: 2
```

```sh run
pravo validate; echo "exit=$?"
rm other.cue
```

```text out
Error: loading .:
other.cue is package other; the other files are package errors
exit=6
```

```cue file=other.cue
y: 2
```

```sh run
pravo validate; echo "exit=$?"
rm other.cue
```

```text out
Error: loading .:
other.cue has no package clause; the other files are package errors
exit=6
```

| Message | Cause | Fix |
|---|---|---|
| `import failed: cannot find package "pravo.dev/providers/hashicorp/random": cannot find module providing package pravo.dev/providers/hashicorp/random:` | The generated package of a provider does not exist yet. | Run `pravo init`. The hint on the last line says so. |
| `import failed: cannot find package "pravo.dev/providers/hashicorp/null": no files in package directory with package name "null":` | The import omits the package name `null_`. | Write `"pravo.dev/providers/hashicorp/null:null_"`. See [Generated packages](spec-generated-packages.md#package-names). |
| `builtin package "hashicorp/random" undefined` or `import failed: cannot find package "registry.opentofu.org/hashicorp/random"` | The import path is the address of a provider in a registry. A provider is imported from `pravo.dev/providers` followed by that address. The hint under the error names the path. | Write `import rnd "pravo.dev/providers/hashicorp/random"`. |
| `import failed: imports are unavailable because there is no cue.mod/module.cue file:` | The root imports a package and has no module yet. | Run `pravo init`. The hint on the last line says so. |
| `other.cue is package other; the other files are package errors` | A `.cue` file of the root has a package clause that differs from the others. | Give the files one package name, or move the file to another directory. |
| `other.cue has no package clause; the other files are package errors` | A `.cue` file of the root starts without a package clause. | Add the package clause of the other files. `pravo.lock.cue` has no clause and is not part of the package, and neither are files whose names start with `.` or `_`. |

### An import that is the address of a provider

A provider is imported from `pravo.dev/providers`, followed by its address. The address alone is read as a package of the CUE standard library, or as a module that does not exist. The error ends with a hint that names the path:

```cue file=imports/main.cue
package imports

import rnd "hashicorp/random"

$random: rnd.#Provider
```

```sh run exit=6
pravo -C imports init > /dev/null 2>&1
pravo -C imports validate
```

```text out
Error: evaluating ./imports:
builtin package "hashicorp/random" undefined:
    ./imports/main.cue:3:8


hint: a provider is imported from pravo.dev/providers/hashicorp/random, as in
  import random "pravo.dev/providers/hashicorp/random"
pravo init generates the package.
```

## Evaluating CUE

These errors come from CUE. Every command that loads the specification exits with 6 for them.

```cue file=main.cue
package errors

size: 3
size: 11
```

```sh run
pravo validate; echo "exit=$?"
pravo eval; echo "exit=$?"
```

```text out
Error: evaluating .:
size: conflicting values 11 and 3:
    ./main.cue:3:7
    ./main.cue:4:7

exit=6

Error: evaluating .:
size: conflicting values 11 and 3:
    ./main.cue:3:7
    ./main.cue:4:7

exit=6
```

```cue file=main.cue
package errors

y: z + 1
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: evaluating .:
y: reference "z" not found:
    ./main.cue:3:4

exit=6
```

```cue file=main.cue
package errors

import "math"

y: 1
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: evaluating .:
imported and not used: "math":
    ./main.cue:3:8

exit=6
```

A misspelt attribute of a resource is a field that the definition does not allow:

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {lenght: 2}
```

```sh run
pravo init > /dev/null
pravo validate; echo "exit=$?"
```

```text out
Error: evaluating .:
pet.lenght: field not allowed:
    ./main.cue:7:38

exit=6
```

| Message | Cause | Fix |
|---|---|---|
| `size: conflicting values 11 and 3:` | Two declarations of one field disagree. | Read the positions: both declarations are listed. Remove one, or make the values agree. See [C1](../learn/c1-reading-a-cue-error.md). |
| `y: reference "z" not found:` | A name that no declaration defines. | Declare it, or fix the spelling. |
| `imported and not used: "math":` | An import that no expression uses. | Remove the import. |
| `pet.lenght: field not allowed:` | The attribute is not in the definition of the resource, and a definition is closed. | Fix the spelling. `pravo schema <type>` prints the attributes of a type. See [C3](../learn/c3-closed-or-open.md). |

### Tags and incomplete values

```cue file=main.cue
package errors

env:   "dev" | "staging" | "prod" @tag(env)
count: *1 | int                    @tag(count,type=int)
name:  string
```

```sh run
pravo eval -t env=qa; echo "exit=$?"
pravo eval -t env=dev -t nope=1; echo "exit=$?"
pravo eval -t env=dev -t count=x; echo "exit=$?"
pravo eval -t env=dev -c; echo "exit=$?"
```

```text out
Error: evaluating .:
env: 3 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./main.cue:3:8
env: conflicting values "prod" and "qa":
    ./main.cue:3:28
env: conflicting values "staging" and "qa":
    ./main.cue:3:16

exit=6

Error: loading .:
no tag for "nope"

exit=6

Error: loading .:
invalid number for injection tag "count": illegal number start "x"

exit=6

Error: the value is not concrete:
name: incomplete value string:
    ./main.cue:5:8

exit=1
```

A field that stays without a value stops the plan at the first resource that reads it. This one repeats its own name: the right-hand `port` is the field itself.

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

port: 8080

svc: {
	port: port
	pet:  $random.#Resource.random_pet & {length: 2, prefix: "p\(port)"}
}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
Error: svc.pet.prefix: value depends on svc.port, which is not concrete (is a -t tag missing?)
exit=1
```

| Message | Cause | Fix |
|---|---|---|
| `env: 3 errors in empty disjunction:` | A tag value outside the allowed values. | Pass one of the values the field allows. |
| `no tag for "nope"` | `-t nope=...` names a tag that no field declares. | Fix the name, or add `@tag(nope)` to a field. |
| `invalid number for injection tag "count": illegal number start "x"` | The value does not parse as the type declared by `type=`. | Pass a value of that type. |
| `the value is not concrete:` followed by `name: incomplete value string:` | `eval -c` found a field that has a type and no value. | Give the field a value, or a tag that fills it. |
| `svc.pet.prefix: value depends on svc.port, which is not concrete (is a -t tag missing?)` | A resource reads a field that never gets a value. A tag is missing, or a field repeats its own name. | Pass the tag. Rename a loop variable that shadows the field it fills. See [References](spec-resources.md#references). |

## Settings and providers

```cue file=main.cue
package errors

$settings: nope: 1
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: $settings.nope is not a setting (the block takes state and refresh)
exit=6
```

```cue file=main.cue
package errors

$settings: refresh: "soon"
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: $settings.refresh: "soon" is not a refresh age; want "always", "never" or a duration such as "30m", "6h" or "7d"
exit=6
```

```cue file=main.cue
package errors

$settings: state: {backend: "s3", path: "x"}
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: $settings.state.path belongs to the local backend: remove it, or remove backend: "s3"
exit=6
```

```cue file=main.cue
package errors

env: "dev" | "prod" @tag(env)

$settings: state: path: "state/\(env)"
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: $settings.state.path is not concrete: $settings.state.path: invalid interpolation: unresolved disjunction "dev" | "prod" (type string)
(if it depends on a tagged field such as env, pass -t env=...)
exit=6
```

A state path names a directory. A path that names a file stops every command that opens the state, and the file is left as it is:

```cue file=filestate/main.cue
package filestate

$settings: state: path: "terraform.tfstate"
```

```sh run
echo '{"version":4}' > filestate/terraform.tfstate
pravo -C filestate plan; echo "exit=$?"
ls filestate
```

```text out
Error: the state path ./filestate/terraform.tfstate is a file; a state path is a directory (set $settings.state.path to a directory such as pravo.state, and read an OpenTofu state file with "pravo state import-tfstate")
exit=1
main.cue
terraform.tfstate
```

| Message | Cause | Fix |
|---|---|---|
| `$settings.nope is not a setting (the block takes state and refresh)` | A key other than `state` or `refresh`. | Remove it. See [Settings](spec-settings.md). |
| `$settings.refresh: "soon" is not a refresh age; want "always", "never" or a duration such as "30m", "6h" or "7d"` | The value is not a refresh age. | Use `"always"`, `"never"` or a number with `ms`, `s`, `m`, `h` or `d`. |
| `$settings.state.path belongs to the local backend: remove it, or remove backend: "s3"` | Local and S3 keys are mixed. | Keep the keys of one backend. |
| `$settings.state.path is not concrete:` | The state path depends on a tag that has no value. | Pass `-t env=...`, or give the field a default. |
| `the state path ./filestate/terraform.tfstate is a file; a state path is a directory` | `state.path` names a file, such as the state file of OpenTofu. | Set `state.path` to a directory, or remove it. Convert the file with `pravo state import-tfstate`. |

Provider declarations have their own errors:

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$a: rnd.#Provider & {$version: "~> 3.6"}
$b: rnd.#Provider & {$version: "~> 3.5"}
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: $b: $version "~> 3.5" conflicts with "~> 3.6" on another hashicorp/random provider; one version of a provider is used
exit=6
```

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$a: rnd.#Provider & {$protocol: "tfplugin+reattach", $version: "~> 3.6"}
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: provider random: version does not apply to protocol "tfplugin+reattach": pravo does not install or select what it connects to
exit=6
```

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

x: rnd.#Provider.#Resource.random_pet & {length: 2}
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: x was not built through a provider. Declare a provider, for example
	$aws: awsp.#Provider & {...}
and build the resource through it: $aws.#Resource.random_pet & {...}
exit=6
```

A module that needs a provider from its caller declares the field as required, `provider!:`. An instance that leaves the field out stops the load:

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

#Pet: {
	provider!: rnd.#Provider
	pet:       provider.#Resource.random_pet & {length: 2}
}

$random: rnd.#Provider

api:  #Pet & {provider: $random}
docs: #Pet
```

```sh run
pravo validate; echo "exit=$?"
```

```text out
Error: provider docs.provider was not supplied: docs requires a hashicorp/random provider in its field provider. Pass one, for example
	docs: ... & {provider: $random}
exit=6
```

| Message | Cause | Fix |
|---|---|---|
| `$b: $version "~> 3.5" conflicts with "~> 3.6" on another hashicorp/random provider; one version of a provider is used` | Two provider structs of one source with different constraints. | Use one constraint. |
| `provider random: version does not apply to protocol "tfplugin+reattach": pravo does not install or select what it connects to` | `$version` or `$path` with a reattached provider. | Remove it. See [Providers](spec-providers.md#protocol). |
| `x was not built through a provider. Declare a provider, for example` | A resource written from `#Provider.#Resource...` directly. | Declare a provider struct and build the resource through it: `$aws.#Resource.<type>`. |
| `provider docs.provider was not supplied: docs requires a hashicorp/random provider in its field provider.` | An instance of a module leaves out a provider input that the module marks required. | Pass a provider struct of the root: `docs: ... & {provider: $random}`. See [Modules](spec-modules.md#an-instance-that-passes-no-provider). |
| `api.tls sets proxy, but api.tls is another name for the provider $tls, which does not set it.` | A module, or a struct built from a provider struct, sets a field that the provider it shares leaves unset. | Set the field on the struct that is passed in. See [Modules](spec-modules.md#configuration-in-the-module-and-in-the-caller). |
| `provider registry.opentofu.org/hashicorp/ramdom was not found; check the name` | The registry has no provider of the name in the import path. | Fix the spelling of the path. |
| `registry discovery for registry.opentofu.org: Get "https://registry.opentofu.org/.well-known/terraform.json": ...` | `pravo init` could not reach the registry. The words after the address name the cause. | Fix the proxy, firewall or network. See [the registry cannot be reached](#the-registry-cannot-be-reached). |

A provider name that the registry does not know stops `pravo init`. The error is one line, whatever the registry answered:

```cue file=typo/main.cue status=rejected
package typo

import rnd "pravo.dev/providers/hashicorp/ramdom"

$random: rnd.#Provider
```

```sh run exit=1
pravo -C typo init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.

Error: provider registry.opentofu.org/hashicorp/ramdom was not found; check the name
```

Remove the directory, because `init` reads every file of the module:

```sh run
rm -r typo
```

### The registry cannot be reached

`pravo init` asks the registry for the provider versions and downloads the archives. The block sets a proxy that refuses connections to reproduce the failure:

```cue file=offline/main.cue
package offline

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider
```

```sh run exit=1
HTTPS_PROXY=http://127.0.0.1:9 pravo -C offline init
```

```text out
Error: registry discovery for registry.opentofu.org: Get "https://registry.opentofu.org/.well-known/terraform.json": proxyconnect tcp: dial tcp 127.0.0.1:9: connect: connection refused
```

A refused connection points to a proxy, a firewall or a missing network. Fix it and run `pravo init` again. After a successful `init`, `validate` and `plan` need no registry access.

Remove the directory, because `init` reads every file of the module:

```sh run
rm -r offline
```

A provider that is configured with no credentials stops the plan. The text after the first line depends on the credential sources the provider tried:

```text
Error: configuring provider aws: No valid credential sources found: Please see https://registry.terraform.io/providers/hashicorp/aws
for more information about providing credentials.
```

Set the credentials that the provider documents, such as `AWS_ACCESS_KEY_ID`.

## Resources and values

```cue file=main.cue
package errors

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

secret: $random.#Ephemeral.random_password & {length: 16}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  secret.result
}
```

```sh run
pravo init > /dev/null
pravo plan; echo "exit=$?"
```

```text out
secret: Opening...

Error: note.content: references secret, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)
exit=1
```

```cue file=main.cue
package errors

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

secret: $random.#Ephemeral.random_password & {length: 16}

leak: pravo.#Output & {value: secret.result}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
Error: output leak references secret; outputs are recorded in state, so ephemeral values cannot be used in them
exit=1
```

```cue file=main.cue
package errors

import (
	rnd "pravo.dev/providers/hashicorp/random"
	k8s "pravo.dev/providers/hashicorp/kubernetes"
)

$random: rnd.#Provider
$k8s:    k8s.#Provider

secret: $random.#Ephemeral.random_password & {length: 16}

decoded: $k8s.#Function.manifest_decode & {args: [secret.result]}
```

```sh run
pravo init > /dev/null
pravo plan; echo "exit=$?"
```

```text out
secret: Opening...

Error: decoded: args reference secret; function results may be recorded in state, so ephemeral values cannot be passed to a function
exit=1
```

```cue file=main.cue
package errors

import "pravo.dev/pravo"

region: pravo.#Env & {name: "ERRORS_REGION"}
db:     pravo.#SecretEnv & {name: "ERRORS_DB_PASSWORD"}

where: pravo.#Output & {value: region.value}
```

```sh run
ERRORS_REGION=eu-west-1 pravo plan; echo "exit=$?"
ERRORS_DB_PASSWORD=x pravo plan; echo "exit=$?"
```

```text out
Error: db: the environment variable ERRORS_DB_PASSWORD is not set
exit=1

Error: region: the environment variable ERRORS_REGION is not set, and there is no default
exit=1
```

```cue file=main.cue
package errors

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2}

summary: pravo.#Output & {
	value: {pet: pet.id}
}
```

```sh run
pravo apply --auto-approve; echo "exit=$?"
```

```text out
pravo will perform the following actions:

  # pet (random_pet) will be created
  + pet: {
  +     id:        (known after apply)
  +     length:    2
  +     separator: "-"
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 of 1.

Outputs:
  summary: (known after apply)
pet: Creating...
pet: Creation complete after 0s [id=<pet>]

Error: output summary is not concrete after apply. A name inside its value may refer to a field of the output itself, as in value: {pet: pet.id}; rename the field
exit=1
```

Resources that read each other in a circle have no first one to run. The plan stops before any provider is asked and names the resources on the cycle. `d` is not part of it. `pravo validate` reports the same cycle:

```cue file=main.cue
package errors

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

a: $local.#Resource.local_file & {filename: "a.txt", content: "from \(b.id)"}
b: $local.#Resource.local_file & {filename: "b.txt", content: "from \(c.id)"}
c: $local.#Resource.local_file & {filename: "c.txt", content: "from \(a.id)"}
d: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo init > /dev/null
pravo plan; echo "exit=$?"
pravo validate; echo "exit=$?"
```

```text out
Error: dependency cycle between a, b, c
exit=6

Error: dependency cycle between a, b, c
exit=6
```

| Message | Cause | Fix |
|---|---|---|
| `note.content: references secret, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)` | An ephemeral value in an attribute that is recorded. | Use it in provider configuration or in a write-only attribute. See [Resources](spec-resources.md#ephemeral-resources). |
| `output leak references secret; outputs are recorded in state, so ephemeral values cannot be used in them` | An ephemeral value in an output. | Do not publish it. |
| `decoded: args reference secret; function results may be recorded in state, so ephemeral values cannot be passed to a function` | An ephemeral value as an argument of a function call. | Pass a value that is not ephemeral. |
| `db: the environment variable ERRORS_DB_PASSWORD is not set` | A `pravo.#SecretEnv` and no variable. A secret has no default. | Set the variable. See [Environment](spec-env.md#secret-env). |
| `region: the environment variable ERRORS_REGION is not set, and there is no default` | A `pravo.#Env` without a default and without the variable. | Set the variable, or add a `default`. |
| `output summary is not concrete after apply. A name inside its value may refer to a field of the output itself, as in value: {pet: pet.id}; rename the field` | A key in the value repeats the name of the resource it reads. | Name the keys differently from the resources. See [Outputs](spec-outputs.md#rules). |
| `dependency cycle between a, b, c` | The named resources read each other, directly or through others, so none can run first. A resource that only reads a member of the cycle is not named. `validate`, `plan`, `apply` and `destroy` report it, with exit code 6. | Remove one of the references. |

## Moves and imports

Apply a root with two pets and a group of two, then break the moves one at a time:

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 1}
b: $random.#Resource.random_pet & {length: 2}

k: {
	x: $random.#Resource.random_pet & {length: 1}
	y: $random.#Resource.random_pet & {length: 2}
}
```

```sh run
rm -rf pravo.state
pravo apply --auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 4 added, 0 changed, 0 destroyed. [txn <id>]
```

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 1}
b: $random.#Resource.random_pet & {$from: "a", length: 2}

k: {
	x: $random.#Resource.random_pet & {length: 1}
	y: $random.#Resource.random_pet & {length: 2}
}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
Error: $from a -> b: both addresses exist in state; remove one with "pravo state rm"
exit=1
```

```cue file=main.cue
package errors

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

a: $random.#Resource.random_pet & {length: 1}
c: $local.#Resource.local_file & {$from: "b", filename: "c.txt", content: "x"}

k: {
	x: $random.#Resource.random_pet & {length: 1}
	y: $random.#Resource.random_pet & {length: 2}
}
```

```sh run
pravo init > /dev/null
pravo plan; echo "exit=$?"
```

```text out
Error: $from b -> c: the record is a managed random_pet but c is declared as a managed local_file; the type must stay the same
exit=1
```

```cue file=main.cue
package errors

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 1}
b: $random.#Resource.random_pet & {length: 2}

#Naming: {
	pravo.#Movable
	x: $random.#Resource.random_pet & {length: 1}
}

g: #Naming & {$from: "k"}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
Error: $from k -> g: the record k.y would move to g.y, but no resource is declared there
exit=1
```

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 1}
b: $random.#Resource.random_pet & {length: 2}

#Naming: {
	x: $random.#Resource.random_pet & {length: 1}
	y: $random.#Resource.random_pet & {length: 2}
}

g: #Naming & {$from: "k"}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
Error: evaluating .:
g.$from: field not allowed:
    ./main.cue:15:15

exit=6
```

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {$from: "a", length: 1}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
Error: a: $from is the address of the resource itself
exit=6
```

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 1}
q: {$from: "x", v: 1}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
Error: q: $from on a struct that holds no resources
exit=6
```

```cue file=main.cue
package errors

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

n: $random.#Resource.random_integer & {min: 1, max: 10, $import: "zzz"}
d: $local.#Data.local_file & {filename: "x", $import: "x"}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
Error: evaluating .:
d.$import: field not allowed:
    ./main.cue:12:46

exit=6
```

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

n: $random.#Resource.random_integer & {min: 1, max: 10, $import: "zzz"}
```

```sh run
pravo plan; echo "exit=$?"
```

```text out
n: Importing from id "zzz"...

Error: n: importing "zzz": Import Random Integer Error: Invalid import usage: expecting {result},{min},{max} or {result},{min},{max},{seed}
exit=1
```

| Message | Cause | Fix |
|---|---|---|
| `$from a -> b: both addresses exist in state; remove one with "pravo state rm"` | The record at the old address and at the new one both exist. | Remove the record you do not want with `pravo state rm`. |
| `$from b -> c: the record is a managed random_pet but c is declared as a managed local_file; the type must stay the same` | `$from` points at a record of another type. | Move only between resources of one type. |
| `$from k -> g: the record k.y would move to g.y, but no resource is declared there` | A group move puts a record where nothing is declared. | Declare the resource, or remove the record first. |
| `g.$from: field not allowed:` | The definition of a group does not declare `$from`. | Embed `pravo.#Movable`. |
| `q: $from on a struct that holds no resources` | A group move on a struct with no resource inside. | Put `$from` on the resource, or on a struct that holds resources. |
| `a: $from is the address of the resource itself` | `$from` names the address where it sits. | Remove it, or write the old address. |
| `d.$import: field not allowed:` | `$import` on a data source. The generated data definitions do not declare it. | Data sources are read, not adopted. |
| `n: importing "zzz": Import Random Integer Error: Invalid import usage: expecting {result},{min},{max} or {result},{min},{max},{seed}` | The id has a form the provider does not accept. | Use the id format of the provider's documentation. |

See [Moves and imports](spec-moves-and-imports.md).

## Remote outputs

```cue file=network/main.cue
package network

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

subnet:    $random.#Resource.random_pet & {length: 2}
subnet_id: pravo.#Output & {value: subnet.id}
```

```cue file=app/main.cue
package app

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

net: pravo.#RemoteOutputs & {state: path: "../network/pravo.state"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  net.outputs.nope
}
```

```sh run
rm main.cue
pravo -C network init > /dev/null
pravo -C app init > /dev/null
pravo -C app plan; echo "exit=$?"
```

```text out
Error: net: ./network/pravo.state has no recorded outputs; apply that root first
exit=1
```

```sh run
pravo -C network apply --auto-approve | grep '^Apply complete'
pravo -C app apply --auto-approve 2>&1 | tail -n 1
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
Error: note.content: value depends on net, which is still unknown at apply time (note.content: undefined field: nope)
```

| Message | Cause | Fix |
|---|---|---|
| `net: ./network/pravo.state has no recorded outputs; apply that root first` | The other root has not been applied. | Apply the producer first. |
| `note.content: value depends on net, which is still unknown at apply time (note.content: undefined field: nope)` | The other root has no output of that name, or marked it sensitive. The plan shows the value as unknown, and the apply stops. | Publish the output, or read the value another way. |

## Plans and state

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2, $lifecycle: prevent_destroy: true}
```

```sh run
rm -r app network
rm -rf pravo.state
pravo init > /dev/null
pravo apply --auto-approve | grep '^Apply complete'
sed -i.bak 's/length: 2/length: 3/' main.cue
pravo plan; echo "exit=$?"
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
pet: Refreshing state... [id=<pet>]

Error: pet: replacement requires destroying the resource, but $lifecycle.prevent_destroy is set
exit=1
```

The setting also refuses a destroy of the state, before a plan is made:

```sh run
pravo destroy --auto-approve; echo "exit=$?"
```

```text out
Error: destroy refused: $lifecycle.prevent_destroy is set on pet; remove the setting from the specification to destroy the resource
exit=1
```

A saved plan is stale when the specification changes after it was made:

```sh run
sed -i.bak 's/length: 3/length: 2/' main.cue
pravo plan --out change.plan > /dev/null
echo "extra: 1" >> main.cue
pravo apply change.plan; echo "exit=$?"
sed -i.bak '$d' main.cue
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (main.cue changed)
exit=3
```

Another actor's lock or reservation refuses the run before it changes anything. A lock from another host looks like this:

```cue file=main.cue
package errors

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 3}
```

```sh run
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "{\"addr\":\"pet\",\"pid\":48213,\"host\":\"build-7\",\"user\":\"ci\",\"command\":\"pravo apply\",\"nonce\":\"abc\",\"actor\":\"ci\",\"since\":\"$NOW\",\"kind\":\"lock\",\"renewed_at\":\"$NOW\"}" > pravo.state/locks/pet.lock
pravo apply --auto-approve; echo "exit=$?"
pravo state unlock pet
```

```text out
pet: Refreshing state... [id=<pet>]

pravo will perform the following actions:

  # pet (random_pet) must be replaced
-/+ pet: {
  ~     id:        "<pet>" -> (known after apply)
  ~     length:    2 -> 3 # forces replacement
        separator: "-"
    }

Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 of 1, 1 refreshed.

Error: apply rejected: pet is locked by pravo apply (pid 48213 on build-7, ci, since <time>)
exit=4
Unlocked pet
```

A reservation is taken by `plan --reserve`:

```sh run
pravo plan --reserve 15m --actor ci > /dev/null 2>&1
pravo plan --actor bob; echo "exit=$?"
pravo state unlock pet
```

```text out
pet: Refreshing state... [id=<pet>]

Error: plan rejected: pet is reserved by ci (pravo plan --reserve 15m --actor ci until <time>, since <time>)
exit=4
Unlocked pet
```

Flags that do not fit the command:

```sh run
pravo apply --json 2>/dev/null | tail -n 1
pravo plan --out change.plan > /dev/null 2>&1
pravo apply change.plan --replace pet; echo "exit=$?"
pravo apply --only pet; echo "exit=$?"
pravo apply change.plan --refresh=all; echo "exit=$?"
pravo plan --refresh=x; echo "exit=$?"
pravo plan --replace nope; echo "exit=$?"
pravo plan --replace 'a."b"'; echo "exit=$?"
```

```text out
{"code":1,"kind":"error","message":"apply with --json needs -auto-approve, or apply a reviewed plan file"}

Error: -replace is decided when the plan is made: use "pravo plan -replace <addr> -out <file>"
exit=1

Error: -only applies to a saved plan: run "pravo plan -out <file>" first, then "pravo apply <file> -only ..."
exit=1

Error: -refresh and -full do not apply to a saved plan
exit=1

Error: invalid -refresh value "x": want age, expired, false or all
exit=1

Error: -replace nope: the specification declares no such resource
exit=1

Error: invalid argument "a.\"b\"" for "--replace" flag: parse error on line 1, column 3: bare " in non-quoted-field
exit=1
```

Addresses and the state:

```sh run
pravo state show nope; echo "exit=$?"
pravo state mv pet pet; echo "exit=$?"
pravo import nope 1; echo "exit=$?"
echo no | pravo destroy; echo "exit=$?"
```

```text out
Error: nope is not in state
exit=1

Error: pet already exists in state
exit=1

Error: nope is not declared in the configuration; add it before importing
exit=1
pet: Refreshing state... [id=<pet>]

pravo will perform the following actions:

  # pet (random_pet) will be destroyed
  - pet: {
  -     id:        "<pet>"
  -     length:    2
  -     separator: "-"
    }

Plan: 0 to add, 0 to change, 1 to destroy.
Graph: 1 resources in state, 1 refreshed.

Do you really want to destroy all resources?
  Only 'yes' will be accepted to approve.

  Enter a value:

Error: apply cancelled
exit=1
```

Versions of the stores and files:

```sh run
cp pravo.state/meta.json meta.json.bak
echo '{"format":4,"lineage":"abc","created":"2026-01-01T00:00:00Z"}' > pravo.state/meta.json
pravo plan; echo "exit=$?"
cp meta.json.bak pravo.state/meta.json
echo '{}' > not-a-plan.plan
pravo show not-a-plan.plan; echo "exit=$?"
sed -i.bak 's/"format": 1/"format": 2/' change.plan
pravo show change.plan; echo "exit=$?"
```

```text out
Error: state at ./pravo.state was written by a newer pravo (format 4)
exit=1

Error: not-a-plan.plan is not a pravo plan file
exit=1

Error: change.plan was written by a newer pravo (plan format 2)
exit=1
```

| Message | Cause | Fix |
|---|---|---|
| `pet: replacement requires destroying the resource, but $lifecycle.prevent_destroy is set` | The plan replaces a resource with `prevent_destroy`. | Change the attribute back, or remove `prevent_destroy` in a reviewed change. |
| `destroy refused: $lifecycle.prevent_destroy is set on pet; remove the setting from the specification to destroy the resource` | `pravo destroy` or `pravo plan --destroy` found a declared resource with `prevent_destroy` in the state. Nothing was changed. | Remove `prevent_destroy` in a reviewed change. |
| `saved plan is stale; run "pravo plan" again:` | The configuration, a provider version, the state lineage or a node changed since the plan was made. The lines below say which. Exit code 3. A stored plan that lists as `partly-stale` gives the same message when the apply includes a change that conflicts, as the whole plan does. | Make a new plan. For a `partly-stale` plan, apply the live part with the `--only` set that `pravo plan list` prints. |
| `plan-2f9c1a4e expired 2 days ago; plans are kept for a limited time: run "pravo plan --store" again` | `pravo apply ID` found that the time to live of a stored plan that was not applied ended, and no collection has removed it yet. Nothing was changed. Exit code 3. A plan that was applied completely says that nothing is left to apply, also after its time to live. | Store a new plan. `--ttl` keeps a plan longer. |
| `the plan is being removed: a collection is removing plan-2f9c1a4e; run "pravo plan --store" again` | A collection claimed the stored plan to remove it before the apply began. Exit code 3. It is the only claim that gives this code. | Store a new plan. |
| `no such stored plan: plan-2f9c1a4e (see: pravo plan list --all)` | No stored plan has the id, or a collection removed it. Exit code 1. | `pravo plan list --all` lists the plans that are left. |
| `plan-2f9c1a4e was made with other -t and -T than these: run pravo apply plan-2f9c1a4e -t env=prod` | `apply`, `show` or `plan show` of a stored plan with other tags than the plan was made with. A plan made without tags says `was made without -t and -T`. Exit code 1. | Run the command the message names. The values in it are quoted for a shell. |
| `the progress of plan-2f9c1a4e is not readable: ...` and `the summary of plan-2f9c1a4e is not readable: ...` | The object of the plan cannot be parsed. `apply` stops with exit code 1, and the listing leaves the plan out. A collection never removes a plan for this reason. It makes an unreadable progress object empty once the plan is older than an hour. | Wait for a collection, or store the plan again. |
| `plan-2f9c1a4e was stored by a newer pravo (format 2)` | The summary has a higher format number than this pravo reads. The plan is left alone. | Use the pravo that stored it, or a newer one. |
| `the changes were applied (txn ...) but the progress of plan-2f9c1a4e could not be recorded: ...` | An apply from a stored plan ended, and writing what it did into the plan failed. The changes stand, and the transaction is in the log. Exit code 1. | Run `pravo state log`, then plan again: the plan lists as stale or partly stale. |
| `3 changes were applied before the error and are recorded in plan-2f9c1a4e; run "pravo apply plan-2f9c1a4e" again to apply the rest.` | An apply from a stored plan failed or was interrupted after it finished some changes. A plan file gets the same line with `pravo apply FILE` as the command. The line comes before the error, on stdout, and with `--json` on stderr. The exit code is that of the error. The plan lists as `partial`. | Remove the cause of the error and run the command in the message. A change that failed is not recorded and is tried again. |
| `--ttl applies to --store` | `pravo plan --ttl` without `--store`. Exit code 1. | Add `--store`, or leave `--ttl` out. |
| `--ttl "soon": want a length of time such as 90m, 36h or 7d` | The value of `--ttl` or `--older-than` is not a number and a unit. Exit code 1. | Write `90m`, `36h` or `7d`. |
| `-refresh and -full do not apply to a stored plan` | A stored plan already holds its refresh. Exit code 1. | Drop the flag. |
| `the plan changed while it waited; run "pravo plan" again:` | A plain `pravo apply` found, once it held its locks, that another command wrote a node the plan writes or reads. The lines below say which. Nothing was changed. Exit code 3. | Make a new plan. |
| `pet: the plan changed while it waited: REASON; run "pravo plan" again` | In a plain `pravo apply`, a provider answered differently when the apply planned `pet` again than when the plan was printed. Nothing was done to `pet`. Resources applied before the apply reached it keep their changes. Exit code 3. | Make a new plan. |
| `pet: REASON; run "pravo plan" again` | In `pravo apply FILE`, a provider answered differently when the apply planned `pet` again than when the plan was saved, for example `the provider produced a different plan than the saved one`. Nothing was done to `pet`. Resources applied before the apply reached it keep their changes, and the file is marked with them. Exit code 3. | Make a new plan. |
| `s3://infra-state/prod cannot hold a state that more than one command writes: ...` | The store ignores or rejects a conditional request that the locks need. The rest of the message names which. | Use a store that honours them, or test the store and set `PRAVO_S3_PROBE=off`. See [S3-compatible stores](s3-stores.md). |
| `apply rejected: pet is locked by pravo apply (pid 48213 on build-7, ci, since <time>)` | Another command holds the node. Exit code 4. | Wait for it to finish. `pravo state locks` shows the holder, and `pravo state unlock` removes a lock that a crashed command left. |
| `plan rejected: pet is reserved by ci (...)` | Another actor reserved the node with `plan --reserve`. Exit code 4. | Wait until the reservation ends, or ask its owner to release it. |
| `apply with --json needs -auto-approve, or apply a reviewed plan file` | `apply --json` has changes to make and no prompt to answer. | Add `--auto-approve`, or apply a saved plan. |
| `-replace is decided when the plan is made: use "pravo plan -replace <addr> -out <file>"` | `--replace` with a saved plan. | Pass it to `pravo plan --out`. |
| `-only applies to a saved plan: run "pravo plan -out <file>" first, then "pravo apply <file> -only ..."` | `--only` without a plan file. | Save a plan first. |
| `-refresh and -full do not apply to a saved plan` | A saved plan already holds its refresh. | Drop the flag. |
| `invalid -refresh value "x": want age, expired, false or all` | The value of `-refresh` is not one of the four. | Use `age`, `expired`, `false` or `all`. |
| `-replace nope: the specification declares no such resource` | The address is not declared. | Check the address with `pravo plan --json`. |
| `invalid argument "a.\"b\"" for "--replace" flag: parse error` | A list flag reads a quoted address as CSV. | Quote the address as a CSV field. See [Addresses](spec-resources.md#addresses). |
| `nope is not in state` | `state show`, `rm` or `mv` of an address with no record. | List the addresses with `pravo state list`. |
| `pet already exists in state` | `state mv` onto an address that has a record. | Remove the target first. |
| `nope is not declared in the configuration; add it before importing` | `pravo import` of an address the specification does not declare. | Write the resource first. |
| `state at ./pravo.state was written by a newer pravo (format 4)` | A store with a format number above the one this pravo writes. | Use the pravo that wrote it, or a newer one. See [Compatibility](compatibility.md). |
| `you are not a reader of this state (no identity found).` and `Readers are listed in .pravo/access/readers.txt. Ask one of them to add yours.` | A sealed state, and no identity that can open it: none was found, or the one found is not on the list (`identity ada is not a reader`). Exit code 5. An identity whose passphrase is missing or wrong, several identity files and none chosen, and a `--identity` that names no file give the same code and the first line only, with the reason in the parenthesis. `pravo state list` works without an identity. | Run `pravo id new --name NAME`, send the line it prints to a reader, who adds it to the readers file and runs `pravo protect rewrap`. See [pravo id and pravo protect](protect.md#the-identity). |
| `.pravo/access/readers.txt and the state differ (dan added, fay removed). Run pravo protect rewrap.` | A command that writes a sealed state found that the readers file lists other readers than the state is sealed to. A command that only reads prints it as a warning. Exit code 5. | Run `pravo protect rewrap`. If nobody edited the file, compare it with `git log -p -- .pravo/access/readers.txt`. |
| `the readers file was not found (looked in .pravo/access)` | A command that writes a sealed state, or `pravo protect init` or `rewrap`, found no readers file. Exit code 5 for a command that writes, and 6 for `protect`. | Check out the repository that holds `.pravo/access/readers.txt`, or name its directory with `--access-dir` or `PRAVO_ACCESS_DIR`. |
| `the repository lists readers in .pravo/access/readers.txt, but the state is not sealed: it has not been sealed yet, or its meta.json was changed. Run pravo protect status; to seal it, pravo protect init --encrypt; if the state is not meant to be sealed, remove the readers file.` | A command that writes a state that is not sealed, in a repository that has a readers file. A command that only reads prints it as a warning. `protect check`, `init` and `status` are not stopped. Exit code 5. | Run `pravo protect status`. Seal the state with `pravo protect init --encrypt`, or remove the readers file if the state is not meant to be sealed. If the state was sealed before, put `meta.json` back from a version of the bucket: [sealed state](../concepts/sealed-state.md). |
| `the repository lists readers in .pravo/access/readers.txt, but the state does not exist yet. Create it sealed with: pravo protect init --encrypt` | A command that writes found a readers file and no state. It creates nothing. A command that only reads prints it as a warning. Exit code 5. | Run `pravo protect init --encrypt`, which creates the state sealed. |
| `.pravo/access/readers.txt: line 3: want 4 fields: NAME ssh-ed25519 BASE64 x25519:BASE64URL (got 3)` | A line of the readers file does not parse. The message never repeats the line. Exit code 6. | Replace the line with the one `pravo id show` prints. |
| `your identity ada is not in .pravo/access/readers.txt. Sealing to a list you are not on would lock you out.` | `pravo protect init` was run by an identity that is not on the list. Exit code 5. | Add the line of the identity, or run the command as a reader. |
| `the identity ada is sealed with a passphrase and stdin is not a terminal; set PRAVO_PASSPHRASE.` | A command needs the identity, which is sealed with a passphrase, and there is no terminal to ask on. | Set `PRAVO_PASSPHRASE` for the command, or use an identity made with `pravo id new --ci`. |
| `identity ada already exists at ~/.config/pravo/id/ada` | `pravo id new` never overwrites an identity. Exit code 1. | Choose another `--name`, or remove the file if you are sure that no state is sealed to its key. |
| `no identity found: ~/.config/pravo/id holds several identities (ada dan) and none is named like you; choose one with --identity NAME` | The identity directory holds several files, none is named like the user, and none or more than one has a reader key with a lock in the current group of the state. When more than one has, the message names them. Exit code 5. | Pass `--identity NAME`, or set `PRAVO_IDENTITY`. See [pravo id and pravo protect](protect.md#the-identity). |
| `--ci needs --name NAME: name the identity after the job, such as deploy-bot. ...` | `pravo id new --ci` was run without `--name`. Exit code 1. | Name the identity after the job: `pravo id new --ci --name deploy-bot --out FILE`. |
| `refusing to ask, stdin is not a terminal; pass --yes` | `pravo protect init` has no terminal to ask on. Exit code 1. | Pass `--yes` once you have read what it will do. |
| `the state at ./pravo.state is a local directory. Its compare-and-swap is a per-user mutex, ...` | `pravo protect check` or `init` on a state in a directory. Exit code 1. | Move the state to a bucket, or pass `--allow-local` for a directory that only you use. |
| `a seal is in progress; run pravo protect init --encrypt to finish` | A seal was started and did not finish. A command that writes the state is refused until it does. Exit code 4. | Run `pravo protect init --encrypt` again. |
| `.pravo/access/recovery.txt lists a recovery recipient that the state does not have. Run pravo protect rewrap.` and `.pravo/access/recovery.txt and the state name different recovery keys. Run pravo protect rewrap.` | A command that writes a sealed state compared the recovery file of the repository with `meta.json` and they disagree. Exit code 5. | `pravo protect rewrap` makes the state follow the file. If the file is the mistake, restore it from git. |
| `the state has a recovery recipient, and .pravo/access/recovery.txt was not found (looked in .pravo/access). Commit the file, or remove the recovery with pravo protect rewrap --remove-recovery.` | The state has a recovery and the repository has no recovery file, which is how a recovery taken out of the bucket looks. Exit code 5. | Commit the file, or remove the recovery with `--remove-recovery` if that was meant. |
| `the recovery lock is missing from group g1. Run pravo protect rewrap.` | A lock was taken out of `meta.json`. Exit code 5. | `pravo protect rewrap` makes it again. |
| `the shares do not rebuild the recovery key of this state: it takes 2 shares and 1 were given` | `pravo protect recover` was given too few shares, a share of another recovery, a share twice, or a wrong or mistyped one. The text of a share is not repeated. Exit code 5. | Collect K different share files that belong to this state and run the command again. |
| `recovery-shares already holds shares (share-1-of-3.txt). Move them away first: two sets of shares must not be mixed.` | A command that sets a recovery found shares in the directory where it runs. Exit code 1. | Move the old shares away, or destroy them if the recovery they belong to is replaced. |
| `the state has no recovery recipient; it was sealed without one, and only a reader can open it` | `pravo protect recover` on a state with no recovery. Exit code 1. | Open it with the identity of a reader. A state cannot be given a recovery by someone who cannot open it. |
| `Untrusted: rec/9f2c41... does not match its name` | `pravo protect status` found an object whose bytes do not hash to its name, or a pointer that disagrees with its record. Exit code 7. | Restore the object from a version of the bucket or a backup that verifies. See [7: untrusted](exit-codes.md#7-untrusted). |
| `meta.json of the state at s3://northwind-state/prod changed again and again while it was written; run the command again` | Other commands kept writing `meta.json` while `pravo protect rewrap` or `init` tried to. Exit code 1. | Run it again. |
| `change.plan was written by a newer pravo (plan format 2)` | A plan file with a newer format. | Make the plan again with this pravo. |
| `not-a-plan.plan is not a pravo plan file` | The file is not a saved plan. | Pass a file written by `plan --out`. |
| `apply cancelled` | The answer to the prompt was not `yes`. | Answer `yes`, or use `--auto-approve`. |
| `lock lost: this apply does not hold the lock on pet any more (...)` | A renewal found that another command took the lock on `pet` over, after it went stale while the apply ran, for example on a computer that was suspended. The apply makes no further change. The changes it made are recorded, and the transaction is `failed`. Exit code 4. | Run `pravo plan`, and apply again. |
| `cannot confirm that this apply still holds its locks: they were last renewed 6m0s ago` | More than five minutes passed since the apply knew that its locks were its own, and a renewal failed. The apply makes no further change. Exit code 1. | Check the connection to the state, run `pravo state locks`, then plan and apply again. |
| `pet changed while the plan read it; run pravo plan again` | A plan read the records that the resources it reached read, and found the record of `pet` changed in a walk after it had been found changed in an earlier one. The marks that the plan wrote stay. Exit code 1. | Run `pravo plan` again. |
| `pet: the record could not be written: ...` | The provider changed the object and the state did not take the record. The message that follows says that the creation, update or destruction of `pet` finished, but the state does not record it. Exit code 1. | Look at the object with the provider's own tools, restore the connection to the state, then run `pravo plan`. See [Interrupting an apply](apply.md#interrupting-an-apply). |
| `pet depends on base and changed in the state since it was read; run the command again` | A move of `base`, with `$from` or `pravo state mv`, rewrites the records that depend on it. Another command wrote one of them since this command read it. Nothing was moved. Exit code 1. | Run the command again. |
| `writing resources/pet.json: s3 conditional put resources/pet.json: the store did not say whether the write was made: ...; run the command again` | A write to a bucket was applied or not, and the bucket did not answer, neither to the write nor to the read that pravo made to find out. The record or pointer may be in the state. It is not a change by another command. Exit code 1. | Run the command again. It reads the state as it is. |
| `pet: the record changed in the state since it was read` | A command that rewrites the record of `pet` found it written by another command since it read it. The message ends this way for a move (`$from` or `pravo state mv`), an import, a provider `$from` and the record of a node in an apply. Exit code 1. | Run the command again. |
| `outputs.json stayed locked for 1m10s: ...` | Another command held the lock on the recorded outputs for longer than an update waits, which is longer than the time after which the lock of a command on another host is reclaimed. Exit code 4 when the message names the holder, and 1 when the lock cannot be read. | Run `pravo state locks`. Run the command again when the holder is done. |
| `Warning: pet changed in the state since this command read it; what the refresh read is not recorded` | A plan or a drift run read `pet` from its provider, and another command wrote the record before this one could. The command goes on with what it read. The record in the state is the one the other command wrote. | Run `pravo plan` again if the other command was an apply of `pet`. |
| `--limit applies to --due` | `pravo drift --limit N` without `--due`. Exit code 1. | Add `--due`, or leave `--limit` out. |
| `--limit -1: want a number above zero` | `--limit` is negative. Exit code 1. | Give a number above zero, or leave `--limit` out to read every record that is due. |
| `provider tls is set up from key, which the state does not hold; read the records of that provider with "pravo drift"` | `pravo drift --due` reads records of a provider whose configuration reads a resource or data source that has no record. A provider that is configured from the environment, an ephemeral resource, a function call or another root's outputs needs no record, and is set up. Exit code 1. | Apply the resource that the provider reads, or run `pravo drift`. |
| `interrupted: 1 of 3 tasks were not started` | The command received `SIGINT` or `SIGTERM` before the apply had started every node. The nodes at work finished and were recorded, and an apply from a plan records them in the plan. Exit code 1. | Run the apply again. |
| `creation of pet was in flight; its result is unknown; check the provider` | The apply stopped while a provider was changing `pet`, the call to the provider failed, or the provider answered an update or a destruction with an error and no state, and the provider did not say what it had done. The verb is `creation`, `update` or `destruction`. The record of `pet`, if it has one, is marked for a read. Exit code 1. | Look at the object with the provider's own tools, then run `pravo plan`. See [Interrupting an apply](apply.md#interrupting-an-apply). |
| `creation of pet finished, but the state does not record it; check the provider` | The provider finished the change to `pet` and writing the record failed. The verb is `creation`, `update` or `destruction`. The record of `pet`, if it has one, is marked for a read. Exit code 1. | Look at the object with the provider's own tools, then run `pravo plan`. |
| `The state did not answer within 20s. Exiting; ...` | The third signal, or the end of the wait for the providers, gave the apply up, and the state did not take the marks, the transaction and the release of the locks in 20 seconds. pravo exits with code 1. The changes in flight may have no record, and the locks stay until they go stale. | Look at the objects with the provider's own tools. `pravo state locks` shows the locks, and `pravo state unlock` removes them. |

## Policies

```cue file=policy/no-destroy.cue
// A managed resource is neither deleted nor replaced.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && (action == "delete" || action == "replace") {
		error("\(addr): \(action) is not allowed")
	}
}]
```

```cue file=policy/bare.cue
changes: [...{
	addr:    string
	before?: {id: string}
	if before.id == "x" {
		error(addr)
	}
}]
```

```sh run
rm -rf pravo.state
pravo plan --policy policy/bare.cue; echo "exit=$?"
pravo apply --auto-approve | grep '^Apply complete'
pravo plan --destroy --policy policy/no-destroy.cue; echo "exit=$?"
```

```text out
pravo will perform the following actions:

  # pet (random_pet) will be created
  + pet: {
  +     id:        (known after apply)
  +     length:    3
  +     separator: "-"
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 of 1.

Error: the plan violates the policy policy/bare.cue (sha256:<digest>):
changes.0: cannot reference optional field: before:
    policy/bare.cue:4:5

exit=5
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
pet: Refreshing state... [id=<pet>]

pravo will perform the following actions:

  # pet (random_pet) will be destroyed
  - pet: {
  -     id:        "<pet>"
  -     length:    3
  -     separator: "-"
    }

Plan: 0 to add, 0 to change, 1 to destroy.
Graph: 1 resources in state, 1 refreshed.

Error: the plan violates the policy policy/no-destroy.cue (sha256:<digest>):
changes.0: pet: delete is not allowed:
    policy/no-destroy.cue:7:3

exit=5
```

```sh run
pravo apply --policy policy/no-destroy.cue; echo "exit=$?"
pravo plan --policy ""; echo "exit=$?"
pravo show change.plan --policy policy/no-destroy.cue; echo "exit=$?"
```

```text out
Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"
exit=5

Error: --policy needs a file; the value is empty
exit=5

Error: unknown flag: --policy
exit=1
```

| Message | Cause | Fix |
|---|---|---|
| `the plan violates the policy policy/no-destroy.cue (sha256:<digest>):` followed by details | The plan does not satisfy the policy. Exit code 5. | Read the details: the path indexes the plan document, and the message is the one the policy wrote. See [Reading a refusal](policies-overview.md#reading-a-refusal). |
| `cannot reference optional field: before:` | A policy reads a field that may be absent. | Test its presence first. See [Writing policies](policies-writing.md#read-an-absent-field-only-after-a-presence-test). |
| `a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"` | `apply` with `--policy` and no plan file. | Save the plan, then apply the file. |
| `--policy needs a file; the value is empty` | `--policy ""`. | Name a file. |
| `unknown flag: --policy` | `show` and the other commands do not take it. | Use it with `plan`, `apply`, `destroy` and `mcp`. |

The other refusals of a policy file, before anything runs, are in [Load refusals](policies-overview.md#load-refusals). An MCP server needs absolute paths: `--policy "../p.cue": the server needs an absolute path`.

## Modules

`pravo mod verify` compares the modules a root loads with `pravo.lock.cue`:

```sh run
echo 'modules: "example.com/x@v0": {version: "v0.1.0", hash: "h1:abc"}' >> pravo.lock.cue
pravo mod verify; echo "exit=$?"
```

```text out
  - example.com/x@v0 is locked but no longer used

Error: 1 module(s) do not match the lock
exit=5
```

| Message | Cause | Fix |
|---|---|---|
| `1 module(s) do not match the lock` | A loaded module differs from the lock, or the lock holds a module nothing loads. Exit code 5. | Run `pravo init` to write the lock again. See [mod](mod.md). |

## Warnings

A warning is printed as `Warning:` and does not change the exit code. A hint is printed as `hint:`, after the result or the error that it explains.

A specification written as HCL loads, since every field is valid CUE, and declares nothing that pravo reads. `validate` and `plan` say so:

```cue file=hcl/main.cue
package hcl

provider: random: source: "hashicorp/random"

resource: random_pet: pet: length: 2
```

```sh run
pravo -C hcl validate
```

```text out
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 0 providers.

hint: this specification declares no resources. The fields "provider", "resource" are named like blocks of HCL, but pravo has no blocks and does not read them. A resource is built through a provider:
  $random: rnd.#Provider
  pet: $random.#Resource.random_pet & {length: 2}
with rnd imported from "pravo.dev/providers/hashicorp/random". See https://pravo.systems/reference/spec-overview.html
```

| Message | Cause | Fix |
|---|---|---|
| `Warning: the generated schema for provider random was made from a different version than the selected v3.9.1; run "pravo init" to refresh it` | The package under `cue.mod/gen` was generated for another provider version. Every command that loads the specification prints it except `init` and `gen`, which write the package again. | Run `pravo init`. |
| `hint: this specification declares no resources. The field "resource" is named like a block of HCL, but pravo has no blocks and does not read them.` | Printed under the result of `pravo validate` and `pravo plan` when the specification declares no resource and no provider and has top-level fields named like blocks of HCL: `providers`, `provider`, `required_providers`, `resource`, `resources`, `data`, `variable`, `variables`, `module`, `modules`, `terraform`, `locals`, `local`, `output`, `outputs` or `backend`, with or without a leading `$`. | Declare a provider and build the resource through it. See [The specification](spec-overview.md). |
| `pravo: telemetry export problem (further ones are not shown): ...` | The collector is unreachable. | Check `OTEL_EXPORTER_OTLP_ENDPOINT`. The exit code is not affected. See [Telemetry](telemetry.md). |

## See also

- [Find out why a run fails](../how-to/debugging.md) reads an error step by step.
- [Exit codes](exit-codes.md) lists the code of each kind of failure.
- [Reading a CUE error](../learn/c1-reading-a-cue-error.md) teaches the shape of an evaluation error.
