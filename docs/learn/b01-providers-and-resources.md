---
title: Providers and resources
description: A provider is a struct built from a generated definition. A resource is a struct built through it. Its address is its path.
order: 31
group: Track B · pravo
minutes: 10
needs: A8
learn: What makes a CUE struct a resource, and how does pravo name it?
done_when: "`pravo validate` counts four resources and refuses a misspelt attribute before any provider starts."
level: beginner
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
---

## Warm-up

Two questions from Track A. Decide, then open the answer.

:::predict A definition `#Bucket` declares the field `name`. You build `logs: #Bucket & {nmae: "logs"}`. Is it accepted?
::reveal
No. A definition is closed, so `nmae` is `field not allowed` ([A8](a08-definitions-reject-typos.md)).
:::

:::predict A file says only `size: int`. Does `pravo eval -c` accept it?
::reveal
No. `int` is a type and not a value, and `-c` demands a concrete value ([A2](a02-a-value-can-be-a-type.md)).
:::

## The problem

You want to know what a resource sends to its provider before anything runs. In HCL the answer is a plan. A plan starts the provider, needs the credentials of a cloud provider and reads the real objects.

The block gives no other way to look. A resource is a block with two labels, and the labels make its address, here `random_integer.port`. Other code cannot pass a block around, merge it with another block or constrain it.

## The idea

In CUE there are no blocks. A resource is a struct, and pravo recognises it by what the struct was built from. A struct is a value, so pravo can print it before any provider starts. The same intent in both languages:

:::compare
```hcl title="main.tf"
resource "random_integer" "port" {
  min  = 20000
  max  = 29999
  seed = "web"
}

resource "local_file" "web" {
  filename = "web.conf"
  content  = "port = ${random_integer.port.result}\n"
}
```

```cue title="main.cue"
$random: rnd.#Provider
$local:  loc.#Provider

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```
:::

A provider is a struct built from `#Provider`, a definition that `pravo init` generates. A resource is a struct built through a provider. Its address is its path in the file. A reference to another resource is the dependency.

This lesson uses two providers that run on your machine, `hashicorp/random` and `hashicorp/local`.

## Write the specification

Work in an empty directory. Save this as `main.cue`. pravo reads every `.cue` file of the package in the directory.

```cue file=main.cue ln
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local: loc.#Provider

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```

Read it from the top.

- `rnd` and `loc` are packages that `pravo init` generates, one per provider. They do not exist yet.
- `$random` is a provider struct. `$version` pins the provider and is not sent to it.
- `port` is a resource. `$random.#Resource.random_integer` selects its type from the provider. The `seed` makes the number repeatable, so your output matches this page.
- `web` reads `port.result`. That reference is the dependency, so pravo creates `port` first. There is no `depends_on` to write.

## Install the providers

```sh run
pravo init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

The lines in the middle name the provider versions that were chosen. The first line says that the file could not load before the packages existed. `init` read the imports, installed the providers, and wrote the packages.

```sh run
ls
find cue.mod -type f | sort
```

```text out
cue.mod
main.cue
pravo.lock.cue
cue.mod/gen/pravo.dev/pravo/settings_gen.cue
cue.mod/gen/pravo.dev/providers/hashicorp/local/local_gen.cue
cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue
cue.mod/module.cue
```

`pravo.lock.cue` pins the provider versions. Commit it. The files under `cue.mod/gen` are generated. Do not edit them. The file of a provider defines `#Provider`, and the types of the provider sit inside it:

```sh run
grep -E '^#|^[[:space:]]#' cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue
```

```text out
#Lifecycle: {
#Provider: {
	#Resource: {
	#Data: {
	#Ephemeral: {
	#Function: {
```

This is why `main.cue` writes `$random.#Resource.random_integer`: the resource type is a definition inside the provider it runs on.

## Validate

```sh run
pravo validate
```

```text out
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

`validate` evaluates the file and checks every field against the provider definitions. It starts no provider. It found two resources and two providers without a keyword that says so.

## See what pravo sees

:::predict Which fields of port are not yet concrete?
`random_integer` has the attributes `id`, `keepers`, `max`, `min`, `result` and `seed`. The file sets `min`, `max` and `seed`. Decide which fields `pravo eval -e port` prints as a type and not as a value.

::reveal
```sh run
pravo eval -e port
```

```text out
// Identity: pravo treats a struct that carries these as a resource.
$kind:   "resource"
$type:   "random_integer"
$source: "registry.opentofu.org/hashicorp/random"
// The string representation of the integer result.
id: string | null
// The maximum inclusive value of the range.
max: 29999
// The minimum inclusive value of the range.
min: 20000
// The random integer result.
result: number | null
// A custom seed to always produce the same value.
seed: "web"
```

`id` and `result` are types. The provider computes them when it creates the object, so before `apply` they are not yet concrete. That is not an error. It is the state of a value that does not exist yet. [B2](b02-reading-a-plan.md) shows how a plan prints it.
:::

That answers the question at the top. `pravo eval` started no provider and read no credential. The first three lines of the output are the identity. A struct with `$kind: "resource"` is a resource, wherever it sits in the file. `$type` is the resource type, and `$source` names the provider that runs it. The `//` lines are the provider's own documentation.

To look up what a resource accepts, ask for its definition. `--required` keeps the attributes that must be set:

```sh run
pravo schema random_integer --required
```

```text out
// resource random_integer, from hashicorp/random
...
random_integer: {
...
	// The maximum inclusive value of the range.
	max: number
	// The minimum inclusive value of the range.
	min: number
}
```

## Read the address

A plan names every resource that apply creates. Print only the header lines:

```sh run
pravo plan | grep '^  #'
```

```text out
  # port (random_integer) will be created
  # web (local_file) will be created
```

Each header holds an address and, in parentheses, the type. `port` and `web` are paths in `main.cue`. The next lesson reads the whole plan.

## Break it: a resource built outside a provider

The next file is rejected before any provider starts. It builds a resource from the bare definition, not through a provider struct. Write it beside `main.cue`:

```cue file=extra.cue status=rejected
package estate

import rnd "pravo.dev/providers/hashicorp/random"

other: rnd.#Provider.#Resource.random_pet & {length: 2}
```

```sh run exit=6
pravo validate
```

```text out
Error: other was not built through a provider. Declare a provider, for example
	$aws: awsp.#Provider & {...}
and build the resource through it: $aws.#Resource.random_pet & {...}
```

The path is `other`. The rule is that every resource is built through a provider struct that the file declares. The bare definition `rnd.#Provider` is not one. The error shows the form to use. Every command that loads the specification exits with code 6 when the specification is invalid.

Remove the file:

```sh run
rm extra.cue
pravo validate
```

```text out
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

## Break it: a misspelt attribute

This file is rejected too. `filnename` is not an attribute of `local_file`. Write it beside `main.cue`:

```cue file=typo.cue status=rejected
package estate

backup: $local.#Resource.local_file & {
	filnename: "backup.conf"
	content:   "port = 0\n"
}
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
backup.filnename: field not allowed:
    ./typo.cue:4:2
```

Read it as path, rule, location. The path is `backup.filnename`. The rule is `field not allowed`. The definition of `local_file` is closed, as in [A8](a08-definitions-reject-typos.md), and lists the only attributes a struct may carry. The location is line 4, column 2 of `typo.cue`.

No provider was involved. `validate` reads the definitions that `init` wrote. Ask for the log and look for a provider process:

```sh run
pravo validate -v 2>&1 | grep 'provider started' || echo 'no provider started'
```

```text out
no provider started
```

A plan does start the providers. Remove the typo and count them:

```sh run
rm typo.cue
pravo plan -v 2>&1 | grep -c 'provider started'
```

```text out
2
```

:::deep-dive What validate leaves to the provider
`validate` checks names, types and constraints. A provider also has rules of its own, such as a required attribute. The provider checks those when it plans, and `validate` does not start it.

```cue file=lonely.cue
package estate

lonely: $random.#Resource.random_integer & {min: 1}
```

```sh run
pravo validate
```

```text out
Success! 3 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

```sh run exit=1
pravo plan
```

```text out
Error: lonely: Missing Configuration for Required Attribute (at max): Must set a configuration value for the max attribute as the provider has marked it as required.

Refer to the provider documentation or contact the provider developers for additional information about configurable attributes that are required.
```
:::

## Exercise

:::exercise Add a second port
Add two resources to `main.cue`. `admin_port` is a `random_integer` from 30000 to 30999 with the seed `"admin"`. `admin` is a `local_file` named `admin.conf` that records the port as `web` does.

Then misspell `max` as `mx` in `admin_port`, run `pravo validate`, read the error, and undo the change.

When you are done, this must print `ok`:

```sh title="check"
pravo validate | grep -q 'Success! 4 resources' && echo ok
```

::hint
Copy the two resources and change the names, the numbers and the file name. Run `pravo schema random_integer --required` to see the attributes you must set.

::hint
`admin` interpolates `admin_port.result`. A misspelling in `admin_port` is reported at that interpolation, as `invalid interpolation: field not allowed`. The path in front of the message, `admin_port.mx`, names the typo. [Break it: a misspelt attribute](#break-it-a-misspelt-attribute) shows a clean example.

::solution
```cue file=main.cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local: loc.#Provider

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}

admin_port: $random.#Resource.random_integer & {
	min:  30000
	max:  30999
	seed: "admin"
}

admin: $local.#Resource.local_file & {
	filename: "admin.conf"
	content:  "port = \(admin_port.result)\n"
}
```

```sh run check
pravo validate | grep -q 'Success! 4 resources'
```
:::

## Recap

- A provider is a struct built from the generated `#Provider`. A resource is a struct built through a provider.
- The address of a resource is its path, and a reference to another resource is the dependency.
- `pravo eval` shows a resource and `pravo validate` checks it before any provider starts. A resource built outside a provider and a misspelt attribute are refused.

:::check
Which fields of a resource does pravo read to learn what it is?
::answer
`$kind`, `$type` and `$source`. A struct that carries `$kind: "resource"` is a resource, wherever it sits.

Why does `pravo validate` reject `filnename`?
::answer
The generated definition of `local_file` is closed. A field it does not list is `field not allowed`.
:::

Reference: [Providers](../concepts/providers.md), [Resources and addresses](../concepts/resources-and-addresses.md), [`pravo schema`](../reference/schema.md) and [`pravo validate`](../reference/validate-eval-fmt-gen.md).

[B2](b02-reading-a-plan.md) reads the plan that this file produces.
