---
title: CUE cheat sheet
description: Every construct of Track A on one page. Each has a short example and the output that pravo prints.
order: 24
group: Track A · Companions
minutes: 5
level: beginner
---

Each section holds one file and the output of `pravo eval`. Work in an empty directory. Each file replaces the previous `estate.cue`. The lessons explain the reasons. This page only shows the shapes.

## Symbols

| Write | Name | Lesson |
|---|---|---|
| `a: 1` | [field](../reference/glossary.md#field) | [A1](a01-your-data-is-already-cue.md) |
| `a: b: 1` | nested fields in one line | [A1](a01-your-data-is-already-cue.md) |
| `int`, `string`, `_` | a type is a value | [A2](a02-a-value-can-be-a-type.md) |
| `a: int` then `a: 3` | [unification](../reference/glossary.md#unification) | [A3](a03-writing-a-field-twice.md) |
| `&` | combine two values | [A5](a05-constraints-narrow-a-value.md) |
| `>=1`, `<10`, `!=0` | [bound](../reference/glossary.md#bound) | [A5](a05-constraints-narrow-a-value.md) |
| `=~"re"`, `!~"re"` | regular expression match | [A5](a05-constraints-narrow-a-value.md) |
| `a \| b` | [disjunction](../reference/glossary.md#disjunction), a choice | [A6](a06-choices-and-defaults.md) |
| `*a \| b` | [default](../reference/glossary.md#default) | [A6](a06-choices-and-defaults.md) |
| `@tag(env)` | [tag](../reference/glossary.md#tag), set with `-t env=prod` | [A6](a06-choices-and-defaults.md) |
| `a!: int` | [required field](../reference/glossary.md#required-field) | [A7](a07-required-optional-regular.md) |
| `a?: int` | [optional field](../reference/glossary.md#optional-field) | [A7](a07-required-optional-regular.md) |
| `a: int` | [regular field](../reference/glossary.md#regular-field) | [A7](a07-required-optional-regular.md) |
| `#Name: {...}` | [definition](../reference/glossary.md#definition), a [closed](../reference/glossary.md#closed) shape | [A8](a08-definitions-reject-typos.md) |
| `{a: int, ...}` | an [open](../reference/glossary.md#open) struct | [A8](a08-definitions-reject-typos.md) |
| `{#Other, a: int}` | [embedding](../reference/glossary.md#embedding) | [A8](a08-definitions-reject-typos.md) |
| `[string]: T` | [pattern](../reference/glossary.md#pattern) | [A9](a09-patterns.md) |
| `for k, v in x {...}` | [comprehension](../reference/glossary.md#comprehension) | [A10](a10-comprehensions.md) |
| `a: b` | [reference](../reference/glossary.md#reference) | [A11](a11-references-let-hidden.md) |
| `let x = 1` | [let](../reference/glossary.md#let) | [A11](a11-references-let-hidden.md) |
| `_x: 1` | [hidden field](../reference/glossary.md#hidden-field) | [A11](a11-references-let-hidden.md) |
| `if c {...}` | a condition | [A12](a12-conditions-and-strings.md) |
| `"\(x)"` | interpolation | [A12](a12-conditions-and-strings.md) |
| `package p`, `import "x"` | [package](../reference/glossary.md#package) and [import](../reference/glossary.md#import) | [A13](a13-packages-and-the-standard-library.md) |

## Data

A [struct](../reference/glossary.md#struct) holds fields. A list holds values. A string, a number, a bool and `null` are the scalars. [A1](a01-your-data-is-already-cue.md)

```cue file=estate.cue
package estate

tags:  {owner: "platform"} // a struct
ports: [80, 443]           // a list
a: b: c: 1                 // nested fields in one line
who:   tags.owner          // select a field
first: ports[0]            // index a list

name:  "logs"              // a string
size:  3                   // a number
ratio: 0.5
ready: true                // a bool
none:  null
```

```sh run
pravo eval
```

```text out
tags: {
	owner: "platform"
}
ports: [80, 443]
a: {
	b: {
		c: 1
	}
}
who:   "platform"
first: 80
name:  "logs"
size:  3
ratio: 0.5
ready: true
none:  null
```

## Types and constraints

A type is a value. `&` combines values. A bound or a match is a [constraint](../reference/glossary.md#constraint) that narrows a type. [A2](a02-a-value-can-be-a-type.md), [A5](a05-constraints-narrow-a-value.md)

```cue file=estate.cue
package estate

count:    int                      // a type is a value
label:    string
ready:    bool
price:    number
hosts:    [...string] & ["a", "b"] // a list of strings
anything: _                        // any value

port: int & >=1 & <=65535 & 8080   // bounds narrow a type
code: =~"^[a-z]{3}$" & "abc"       // a match narrows a string
text: !~"^[0-9]+$" & "abc"         // a non-match
rest: !=0 & 7                      // an inequality
```

```sh run
pravo eval
```

```text out
count: int
label: string
ready: bool
price: number
hosts: ["a", "b"]
anything: _
port:     8080
code:     "abc"
text:     "abc"
rest:     7
```

A field that holds a type is not [concrete](../reference/glossary.md#concrete). It is [incomplete](../reference/glossary.md#incomplete) until a value narrows it.

## Writing a field twice

Copies of a field combine. The more specific value wins. Structs merge. Order and file do not matter. [A3](a03-writing-a-field-twice.md), [A4](a04-order-does-not-matter.md)

```cue file=estate.cue
package estate

size: int                // copies combine
size: 3                  // the more specific value wins

tags: owner: "platform"  // structs merge
tags: team:  "infra"
```

```sh run
pravo eval
```

```text out
size: 3
tags: {
	owner: "platform"
	team:  "infra"
}
```

Two different concrete values are a [conflict](../reference/glossary.md#conflict). The result is [bottom](../reference/glossary.md#bottom): no value satisfies both, and pravo prints an error.

## Choices and defaults

`|` offers choices. `*` marks the default. `@tag` takes its value from the command line. [A6](a06-choices-and-defaults.md)

```cue file=estate.cue
package estate

class: "standard" | "ia"           // a choice, undecided
tier:  *"standard" | "ia"          // a choice with a default
env:   *"dev" | "prod" @tag(env)   // a tag
```

```sh run
pravo eval
pravo eval -t env=prod -e env
```

```text out
class: "standard" | "ia"
tier:  "standard"
env:   "dev"
"prod"
```

## Required, optional, regular

A required field must be supplied. An optional field may stay unset. A regular field is a field. [A7](a07-required-optional-regular.md)

```cue file=estate.cue
package estate

bucket: {
	name:   string   // regular
	owner!: string   // required
	note?:  string   // optional
}
```

```sh run
pravo eval
pravo eval -O
```

```text out
bucket: {
	name:   string
	owner!: string
}
bucket: {
	name:   string
	owner!: string
	note?:  string
}
```

## Definitions

A definition starts with `#`. It is closed: a value built from it may hold only declared fields. `...` opens it. Embedding includes the fields of another definition. [A8](a08-definitions-reject-typos.md)

```cue file=estate.cue
package estate

#Bucket: {name!: string, versioning: *false | bool}   // a closed shape
logs:    #Bucket & {name: "logs"}

#Loose: {name: string, ...}                           // an open shape
extra:  #Loose & {name: "x", anything: 1}

#Tagged: {owner!: string}
#Owned:  {#Tagged, name!: string}                     // embedding
owned:   #Owned & {name: "n", owner: "me"}
```

```sh run
pravo eval
```

```text out
logs: {
	name:       "logs"
	versioning: false
}
extra: {
	name:     "x"
	anything: 1
}
owned: {
	name:  "n"
	owner: "me"
}
```

`pravo eval` omits definitions. Name one with `-e '#Bucket'`.

## Patterns

A pattern applies a rule to every field of a struct, now or added later. `Name=` names the key. [A9](a09-patterns.md)

```cue file=estate.cue
package estate

buckets: [string]: {size: *1 | int}   // a rule for every entry
buckets: logs: {}

nodes: [Name=string]: {name: Name}    // Name stands for the key
nodes: web: {}

labels: [string]: string              // a map of strings
labels: owner: "platform"
```

```sh run
pravo eval
```

```text out
buckets: {
	logs: {
		size: 1
	}
}
nodes: {
	web: {
		name: "web"
	}
}
labels: {
	owner: "platform"
}
```

A pattern constrains entries that exist. It does not create them.

## Comprehensions

`for` builds fields or list items from other data. `if` filters. [A10](a10-comprehensions.md)

```cue file=estate.cue
package estate

zones: {a: 1, b: 2}

subnets: {for z, n in zones {"net-\(z)": n * 10}}   // fields from a struct
doubled: [for n in [1, 2, 3] {n * 2}]              // a list from a list
big:     [for n in [1, 2, 3] if n > 1 {n}]         // with a filter
```

```sh run
pravo eval
```

```text out
zones: {
	a: 1
	b: 2
}
subnets: {
	"net-a": 10
	"net-b": 20
}
doubled: [2, 4, 6]
big: [2, 3]
```

The key you build is the identity of the entry. Choose keys that do not move.

## Names

A field reads another by name. A `let` and a hidden field name a helper and stay out of the output. [A11](a11-references-let-hidden.md)

```cue file=estate.cue
package estate

base: "10.0"
cidr: "\(base).0.0/16"      // a reference

let prefix = "estate"       // a let: not printed
_zone:  "eu-west-1"         // a hidden field: not printed
name:   "\(prefix)-logs"
region: _zone
```

```sh run
pravo eval
```

```text out
base:   "10.0"
cidr:   "10.0.0.0/16"
name:   "estate-logs"
region: "eu-west-1"
```

A field must not share a name with the value it reads. `region: region` refers to itself.

## Conditions and strings

`if` includes fields when its condition is true. There is no `else`. `\( )` puts a value into a string. A struct indexed by a key looks a value up. [A12](a12-conditions-and-strings.md)

```cue file=estate.cue
package estate

env: *"dev" | "prod" @tag(env)

retention: *7 | int
if env == "prod" {retention: 90}   // a condition

name: "estate-\(env)"              // interpolation
full: name + "-bucket"             // join strings
days: {dev: 7, prod: 90}[env]      // a lookup
note: """
	one
	two
	"""
```

```sh run
pravo eval
pravo eval -t env=prod
```

```text out
env:       "dev"
retention: 7
name:      "estate-dev"
full:      "estate-dev-bucket"
days:      7
note: """
	one
	two
	"""
retention: 90
env:       "prod"
name:      "estate-prod"
full:      "estate-prod-bucket"
days:      90
note: """
	one
	two
	"""
```

## Packages and the standard library

A directory is a package. `import` brings in another one. Validators join a value with `&`. Functions compute a value. [A13](a13-packages-and-the-standard-library.md)

```cue file=estate.cue
package estate

import (
	"encoding/json"
	"list"
	"net"
	"strings"
)

cidr:  net.IPCIDR & "10.0.1.0/24"   // a validator
zones: list.MinItems(1) & ["a"]    // a validator

up:  strings.ToUpper("logs")       // a function
doc: json.Marshal({a: 1})
n:   len(zones)
```

```sh run
pravo eval
```

```text out
cidr: "10.0.1.0/24"
zones: ["a"]
up:  "LOGS"
doc: "{\"a\":1}"
n:   1
```

Your own package is imported by module path and directory.

```cue title="estate.cue"
import "estate.example/platform"

subnet: platform.#Subnet & {zone: "a", cidr: "10.0.1.0/24"}
```

## Commands

| Command | Does |
|---|---|
| `pravo eval` | Prints the evaluated specification. |
| `pravo eval -e path` | Prints one path or expression. |
| `pravo eval -c` | Fails unless every value is concrete. |
| `pravo eval -O` | Also prints optional fields. |
| `pravo eval -t env=prod` | Sets a tag. |
| `pravo validate` | Checks the specification against provider schemas. |
| `pravo fmt` | Rewrites the `.cue` files of the directory in the standard layout. |

[eval and validate](eval-and-validate.md) explains each flag.

```cue file=estate.cue
package estate
size:int
tags:   {owner:"platform",team:"infra"}
name:    "logs"
```

```sh run
pravo fmt
cat estate.cue
```

```text out
estate.cue
package estate

size: int
tags: {owner: "platform", team: "infra"}
name: "logs"
```

## Errors

An error names the path, the reason and the places that took part. [Reading a CUE error](reading-a-cue-error.md) reads one line by line. The [error catalogue](../reference/errors.md) lists the errors of pravo itself.

| Message | Meaning | Lesson |
|---|---|---|
| `conflicting values 4 and 3` | Two concrete values for one field. | [A3](a03-writing-a-field-twice.md) |
| `invalid value 70000 (out of bound <=65535)` | A value outside a bound. | [A5](a05-constraints-narrow-a-value.md) |
| `3 errors in empty disjunction` | No choice matches the value. | [A6](a06-choices-and-defaults.md) |
| `field is required but not present` | A field marked `!` has no value. | [A7](a07-required-optional-regular.md) |
| `field not allowed` | A closed definition has no such field. | [A8](a08-definitions-reject-typos.md) |
| `reference "bse" not found` | No field or `let` of that name is in reach. | [A11](a11-references-let-hidden.md) |
| `unreferenced alias or let clause x` | A `let` that nothing reads. | [A11](a11-references-let-hidden.md) |
| `non-concrete value string in operand to ==` | A condition on a value that is still a type. | [A12](a12-conditions-and-strings.md) |
| `imported and not used: "strings"` | An import that nothing uses. | [A13](a13-packages-and-the-standard-library.md) |
| `does not satisfy net.IPCIDR` | A validator refused the value. | [A13](a13-packages-and-the-standard-library.md) |
| `incomplete value string` | With `-c`: the field is still a type. | [Why is my value incomplete?](why-is-my-value-incomplete.md) |
| `no tag for "stage"` | `-t` names a tag that no field declares. | [eval and validate](eval-and-validate.md) |
