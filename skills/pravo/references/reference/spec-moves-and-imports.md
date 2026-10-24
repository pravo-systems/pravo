<!-- Generated from docs/reference/spec-moves-and-imports.md by tools/skillgen. Do not edit. -->

# Moves and imports

$from renames or moves a record to a new address. $import adopts an existing object. Both are fields of the resource they describe.

An address is where a resource sits in the specification. A rename, or a move to another place, gives the resource a new address. A resource keeps its record by naming the old address in `$from`, and it adopts an existing object by naming its id in `$import`.

| Field | Set on | Value | Effect at the next plan |
|---|---|---|---|
| `$from` | a resource or data source | the old address | The record is renamed. The object is not touched. |
| `$from` | a struct that holds resources | the old path | Every record below the old path moves to the same place below the struct. |
| `$from` | a provider | the old registry address | Records written under that address are relabelled. |
| `$import` | a managed resource | the provider's id for the object | The provider reads the object, and its state becomes the record. |

`pravo state mv` and `pravo import` do the same for one-off use. See [state](state.md) and [import](import.md).

## $from on a resource

Work in an empty directory. Apply a root with two resources:

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

hello: $local.#Resource.local_file & {filename: "hello.txt", content: "hello"}
pet:   $random.#Resource.random_pet & {length: 2}
```

```sh
pravo init > /dev/null
pravo apply --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

Rename `hello` to `greeting`. Without more, the record at `hello` is destroyed and a new one is created at `greeting`. When a plan creates one resource and deletes another of the same type with identical configuration, it prints a hint and changes nothing:

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

greeting: $local.#Resource.local_file & {filename: "hello.txt", content: "hello"}
pet:      $random.#Resource.random_pet & {length: 2}
```

```sh
pravo plan 2>/dev/null | grep -E '^  #|^Plan|^Hint'
```

Output:

```text
  # greeting (local_file) will be created
  # hello (local_file) will be destroyed
Plan: 1 to add, 0 to change, 1 to destroy.
Hint: greeting looks like hello under a new address; to keep the existing object, add $from: "hello" to greeting
```

Name the old address in `$from`. The plan renames the record. The type must stay the same.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

greeting: $local.#Resource.local_file & {$from: "hello", filename: "hello.txt", content: "hello"}
pet:      $random.#Resource.random_pet & {length: 2}
```

```sh
pravo plan 2>&1 | grep -v 'Refreshing state'
pravo state list
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
hello: moved to greeting

pravo will perform the following actions:

  # hello was moved to greeting (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 0 · skipped 2 · not read 2 · moved 1
greeting
pet
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

The plan carries out the move first, before it refreshes any record. `$from` is harmless after use. Leave it or remove it in a later change. If the old record is gone and the new one exists, the move is complete and nothing happens. Data sources take `$from` too.

## $from on a group

`$from` on a struct that holds resources moves every record below the old path to the same place below the struct. This moves a module instance. The definition must accept the field: embed `pravo.#Movable`, which declares `$from?: string`. The definitions generated for resources and data sources declare it already.

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

#Naming: {
	pravo.#Movable
	codename: $random.#Resource.random_pet & {length: 2}
	suffix:   $random.#Resource.random_integer & {min: 1, max: 9}
}

names: #Naming
```

```sh
pravo init > /dev/null
pravo apply --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

Rename the instance and say where it was:

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

#Naming: {
	pravo.#Movable
	codename: $random.#Resource.random_pet & {length: 2}
	suffix:   $random.#Resource.random_integer & {min: 1, max: 9}
}

ids: #Naming & {$from: "names"}
```

```sh
pravo plan 2>/dev/null | grep -E '^  #|^Plan'
pravo state list
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
  # names.codename was moved to ids.codename (record renamed; no change to the object)
  # names.suffix was moved to ids.suffix (record renamed; no change to the object)
Plan: 0 to add, 0 to change, 0 to destroy.
ids.codename
ids.suffix
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

## Errors

A move that cannot be carried out refuses the plan. Each message names both addresses.

| Cause | Message |
|---|---|
| Both addresses have a record. | `$from a -> b: both addresses exist in state; remove one with "pravo state rm"` |
| The types differ. | `$from b -> c: the record is a managed random_pet but c is declared as a managed local_file; the type must stay the same` |
| A record moves to a place where nothing is declared. | `$from k -> g: the record k.y would move to g.y, but no resource is declared there` |
| `$from` on a struct that holds no resources. | `q: $from on a struct that holds no resources` |
| `$from` names the address it sits at. | `n: $from is the address of the resource itself` |

A group whose definition lacks `pravo.#Movable` is rejected by CUE:

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

#Naming: {
	codename: $random.#Resource.random_pet & {length: 2}
}

ids: #Naming & {$from: "names"}
```

```sh
pravo validate
```

Output:

```text
Error: evaluating .:
ids.$from: field not allowed:
    ./main.cue:11:17
```

A group with records that have no counterpart:

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

k: {
	x: $random.#Resource.random_pet & {length: 1}
	y: $random.#Resource.random_pet & {length: 2}
}
```

```sh
pravo apply --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

File `main.cue`:

```cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

#Naming: {
	pravo.#Movable
	x: $random.#Resource.random_pet & {length: 1}
}

g: #Naming & {$from: "k"}
```

```sh
pravo plan
```

Output:

```text
Error: $from k -> g: the record k.y would move to g.y, but no resource is declared there
```

```sh
pravo state rm k.x
pravo state rm k.y
```

Output:

```text
Removed k.x
Removed k.y
```

## $import

`$import` brings an existing object under management as part of the specification. The value is the id as the provider understands it.

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

n: $random.#Resource.random_integer & {min: 1, max: 10, $import: "5,1,10"}
```

```sh
pravo plan
```

Output:

```text
n: Importing from id "5,1,10"...

pravo will perform the following actions:

  # n was imported from id "5,1,10" and matches the specification

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 1 · imported 1
```

At the plan, the provider reads the object by id and its state becomes the record of `n`. The plan compares the record with the configuration and shows the import with any update the specification implies. When the configuration differs, the header ends `and must be replaced` or `and will be updated`, followed by the changes. After the plan the record exists, so the field is not used again, and the same specification works from an empty state. Leave `$import` or remove it in a later change.

```sh
pravo apply --auto-approve
pravo state show n | head -n 1
```

Output:

```text
n: Refreshing state... [id=5]

No changes. Your infrastructure matches the configuration.
Graph: planned 1 · refreshed 1
// n (provider registry.opentofu.org/hashicorp/random v3.9.1 as random, schema version 0, revision 2)
```

Only managed resources can be imported. The generated data definitions do not declare `$import`. An id the provider cannot use stops the plan:

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

n: $random.#Resource.random_integer & {min: 1, max: 10, $import: "zzz"}
```

```sh
pravo state rm n
pravo plan
```

Output:

```text
Removed n
n: Importing from id "zzz"...

Error: n: importing "zzz": Import Random Integer Error: Invalid import usage: expecting {result},{min},{max} or {result},{min},{max},{seed}
```

## Where the plan writes

A plan is not read-only. It renames records for `$from` and adopts the objects named by `$import` before it prints. The apply that follows finds nothing left to do for them. A saved plan lists the move or the import, and `pravo show <planfile>` prints it again.

## See also

- [Rename, move and adopt resources](../how-to/importing-and-moving.md) shows the whole sequence.
- [`pravo import`](import.md) and [`pravo state`](state.md) do the same from the command line.
- [Resources and addresses](../concepts/resources-and-addresses.md) explains why a rename changes the address.
