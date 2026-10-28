---
title: pravo schema
description: Print the CUE definition of a resource, data source, ephemeral resource or function type, with the provider's documentation.
order: 13
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo schema TYPE [TYPE...] [--required]`, or `pravo schema NAMESPACE/PROVIDER/TYPE ...` |
| Changes infrastructure | No. It starts no provider after `pravo init`. |
| Provider | The prefix of the type up to the first underscore (`random_pet` is in `random`), or `provider/type` with the local name of the provider. The provider must be declared in the root. A type written with the address of its provider, `hashicorp/random/random_pet`, needs no root. |
| Taught in | [B1, Providers and resources](../learn/b01-providers-and-resources.md). |

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--required` | off | Show only the attributes that must be set, and the blocks that contain one. |
| `--discovery` | off | Show the discovery of the type, `#Discovery.<type>`, whose attributes are the scope it lists in. The provider must be able to list the type. |
| `-t`, `--tag key=value`, `-T` | none | Tags, as for [`pravo plan`](plan.md). |

The [global flags](global-flags.md) apply. With `--json`, the output is one object that maps each argument to its definition as a string.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Every definition was printed. |
| 6 | The specification does not load or evaluate, no provider is declared for the type, or the provider has no such type. |

## Examples

Work in an empty directory named `demo`.

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider
```

### Only what must be set

```sh run
pravo init > /dev/null
pravo schema random_integer --required
```

```text out
// resource random_integer, from hashicorp/random
// Use it with:
//
//	import <alias> "pravo.dev/providers/hashicorp/random"
//
//	<provider>: <alias>.#Provider
//	<name>:     <provider>.#Resource.random_integer & {...}
//
// The definition:
// The resource `random_integer` generates random values from a given range, described by the `min` and `max` attributes of a given resource.
//
// This resource can be used in conjunction with resources that have the `create_before_destroy` lifecycle flag set, to avoid conflicts with unique names during the brief period where both the old and new resources exist concurrently.
random_integer: {
	// Identity: pravo treats a struct that carries these as a resource.
	$kind:   "resource"
	$type:   "random_integer"
	$source: "registry.opentofu.org/hashicorp/random"
	// The address the record had before this resource was written here. Moves
	// the record to this address at the next plan.
	$from?: __string
	// The existing object to bring under management: its id as the provider
	// documents it for importing, or its identity, the attributes of the identity of
	// the resource type (what a discovery lists it by). Read at the next plan; leave
	// it in place afterwards.
	$import?: __string | {...}
	// Additional dependencies, as references to other resources.
	$depends_on?: [..._]
	$lifecycle?: #Lifecycle
	// The maximum inclusive value of the range.
	max: number
	// The minimum inclusive value of the range.
	min: number
}
```

The output starts with a usage hint. It shows the shape, with the placeholders `<alias>`, `<provider>` and `<name>`. It is not meant to be pasted as it stands. The definition follows, with the documentation of the provider as comments.

### The whole definition

```sh run
pravo schema random_pet
```

```text out
// resource random_pet, from hashicorp/random
// Use it with:
//
//	import <alias> "pravo.dev/providers/hashicorp/random"
//
//	<provider>: <alias>.#Provider
//	<name>:     <provider>.#Resource.random_pet & {...}
//
// The definition:
// The resource `random_pet` generates random pet names that are intended to be used as unique identifiers for other resources.
//
// This resource can be used in conjunction with resources that have the `create_before_destroy` lifecycle flag set, to avoid conflicts with unique names during the brief period where both the old and new resources exist concurrently.
random_pet: {
	// Identity: pravo treats a struct that carries these as a resource.
	$kind:   "resource"
	$type:   "random_pet"
	$source: "registry.opentofu.org/hashicorp/random"
	// The address the record had before this resource was written here. Moves
	// the record to this address at the next plan.
	$from?: __string
	// The existing object to bring under management: its id as the provider
	// documents it for importing, or its identity, the attributes of the identity of
	// the resource type (what a discovery lists it by). Read at the next plan; leave
	// it in place afterwards.
	$import?: __string | {...}
	// Additional dependencies, as references to other resources.
	$depends_on?: [..._]
	$lifecycle?: #Lifecycle
	// The random pet name.
	id: __string | null
	// Arbitrary map of values that, when changed, will trigger recreation of resource. See [the main provider documentation](../index.html) for more information.
	keepers?: {[string]: __string}
	// The length (in words) of the pet name. Defaults to 2
	length: __number | null
	// A string to prefix the name with.
	prefix?: __string
	// The character to separate words in the pet name. Defaults to "-"
	separator: __string | null
}
```

| In the definition | Meaning |
|---|---|
| `$kind`, `$type`, `$source` | The identity fields. A struct that carries them is a resource of that type. |
| `$from`, `$import`, `$depends_on`, `$lifecycle` | The [meta fields](spec-meta-fields.md) that every resource accepts. |
| `name: type` | A required attribute. |
| `name?: type` | An optional attribute. |
| `name: type \| null` | A computed attribute, or an optional one that has a default. |

### Several types, and the other form

Each argument is printed in turn. `provider/type` names the provider by its local name:

```sh run
pravo schema random/random_pet random_integer | grep '^// resource'
```

```text out
// resource random_pet, from hashicorp/random
// resource random_integer, from hashicorp/random
```

### A type without a specification

A type written with the address of its provider needs no specification. `pravo schema` installs the provider if it is not installed, prints the type and writes nothing to the working directory. The address is `NAMESPACE/PROVIDER/TYPE`, or `HOST/NAMESPACE/PROVIDER/TYPE` for a registry other than the default one. Every type in one call is written this way, or none is.

```sh run
mkdir empty
pravo -C empty schema --required hashicorp/random/random_pet | sed -n 1,3p
ls empty
```

```text out
// resource random_pet, from hashicorp/random
// Use it with:
//
```

In a directory that has no specification, a type written as `random_pet` has no provider to come from. The error says how to name one.

```sh run exit=6
pravo -C empty schema random_pet
```

```text out
Error: loading ./empty:
cue: "." matched no packages


hint: this directory has no specification. To print a type without one, name its provider: pravo schema hashicorp/random/random_pet
```

### Types that do not resolve

```sh run exit=6
pravo schema aws_s3_bucket
```

```text out
Error: no provider "aws" for type aws_s3_bucket; declare a provider struct for it, or name its address: NAMESPACE/TYPE/aws_s3_bucket
```

```sh run exit=6
pravo schema random_nope
```

```text out
Error: the provider has no type "random_nope"
```

When a name is both a resource and a data source, `schema` prints the resource. A type that a provider can list is also a discovery, and `--discovery` prints that one. `pravo providers schema NAME` prints the whole provider package, with `#Resource`, `#Data`, `#Ephemeral`, `#Discovery` and `#Function`.

### JSON

With `--json`, the keys are the arguments and the values are the definitions as strings.

```sh run
pravo schema random_integer random_pet --json | grep -o '^  "[a-z_]*"'
```

```text out
  "random_integer"
  "random_pet"
```

## See also

- [`pravo providers schema`](providers.md) prints a whole provider.
- [Resources](spec-resources.md) describes how a resource is declared.
- [Generated packages](spec-generated-packages.md) describes the packages that hold the definitions.
