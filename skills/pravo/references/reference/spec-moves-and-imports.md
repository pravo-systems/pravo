<!-- Generated from docs/reference/spec-moves-and-imports.md by tools/skillgen. Do not edit. -->

# Moves and imports

$from renames or moves a record to a new address. $import adopts an existing object. Both are fields of the resource they describe.

An address is where a resource sits in the specification. A rename, or a move to another place, gives the resource a new address. A resource keeps its record by naming the old address in `$from`, and it adopts an existing object by naming its id in `$import`.

| Field | Set on | Value | Effect, when the plan is applied |
|---|---|---|---|
| `$from` | a resource or data source | the old address | The record is renamed. The object is not touched. |
| `$from` | a struct that holds resources | the old path | Every record below the old path moves to the same place below the struct. |
| `$from` | a provider | the old registry address | Records written under that address are relabelled. |
| `$import` | a managed resource | the provider's id for the object, or its identity | The provider reads the object at the plan, and what it read becomes the record. |

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

Name the old address in `$from`. The plan lists the rename, and the apply carries it out. The type must stay the same.

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
pravo apply --auto-approve | tail -n 1
pravo state list
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
pravo will perform the following actions:

  # hello will be moved to greeting (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 1 to move.
Graph: planned 0 · skipped 2 · not read 2 · to move 1
hello
pet
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
greeting
pet
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

The plan writes nothing: the state holds `hello` until the apply, which renames the record before it walks any node. The nodes that the plan plans see the record at `greeting`, so it compares the right record with the specification. `$from` is harmless after use. Leave it or remove it in a later change. If the old record is gone and the new one exists, the move is complete and nothing happens. Data sources take `$from` too.

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
pravo apply --auto-approve | tail -n 1
pravo state list
pravo destroy --auto-approve | tail -n 1
```

Output:

```text
  # names.codename will be moved to ids.codename (record renamed; no change to the object)
  # names.suffix will be moved to ids.suffix (record renamed; no change to the object)
Plan: 0 to add, 0 to change, 0 to destroy, 2 to move.
names.codename
names.suffix
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
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

`$import` brings an existing object under management as part of the specification. The value is the id as the provider understands it, a string, or the identity of the object, a struct.

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
n: Reading the object of id "5,1,10"...

pravo will perform the following actions:

  # n will be imported from id "5,1,10" and matches the specification

Plan: 0 to add, 0 to change, 0 to destroy, 1 to import.
Graph: planned 1 · to import 1
```

At the plan, the provider reads the object by id. The plan compares what it read with the configuration and shows the import with any update the specification implies. When the configuration differs, the header ends `and must be replaced` or `and will be updated`, followed by the changes. The plan writes no record: the state is as it was, and a second plan says the same. The apply writes the record of `n`, and carries out the update, if there is one. After the apply the record exists, so the field is not used again, and the same specification works from an empty state. Leave `$import` or remove it in a later change.

```sh
pravo state list
pravo apply --auto-approve | grep -E '^  #|^Apply complete'
pravo state show n | head -n 1
```

Output:

```text
  # n will be imported from id "5,1,10" and matches the specification
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
// n (provider registry.opentofu.org/hashicorp/random v3.9.1 as random, schema version 0, revision 2)
```

### By identity

A provider that gives resources an identity can import an object by it. The identity is a set of attributes that the resource type lists in its identity schema, such as an account, a name and a region. It is what a [discovery](../concepts/discovery.md) lists an object by, and it is written as a struct:

```cue
logs: $aws.#Resource.aws_s3_bucket & {
	$import: {account_id: "000000000000", bucket: "logs", region: "eu-north-1"}
	bucket: "logs"
}
```

pravo asks the provider for the identity schema of the type and sends the attributes as the identity of the import. An attribute that the schema marks optional for import may be left out, and the provider fills it in from its configuration. An attribute that the type does not have, or a required one that is missing, stops the plan and names the attributes the identity is made of. A provider that has no identity for the type cannot import by it, and the plan says so. An id and an identity are alternatives: `$import` is one or the other. The plan header says `will be imported from the identity "…"` for the second.

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
n: Reading the object of id "zzz"...

Error: n: importing "zzz": Import Random Integer Error: Invalid import usage: expecting {result},{min},{max} or {result},{min},{max},{seed}
```

## What the plan writes

A plan writes no record for what the specification declares: not for a move, not for an import, and not for a provider that `$from` renames. It reads the object of an `$import`, lists what the apply will do, and holds the new records in memory, so that the nodes it plans see the record at its new address and the object that was read. A move and an import are not changes to an object: a plan that has nothing but these exits with 0 under `--detailed-exitcode`, as it did when it wrote them, so a migration that must show only moves can still be checked by the exit code. `pravo apply` asks before it writes them.

The apply writes them first, before it locks and walks any node, from what the plan says. A saved plan lists the move or the import, and `pravo show <planfile>` prints it again. A move is checked when the plan is applied: when the record at the old address changed since the plan, or is gone, the plan is stale, and nothing is written.

A plan still refreshes the records it reads and marks a record whose object differs or is gone. The moves and imports are the part that the specification asks for, and they wait for an apply.

## See also

- [Rename, move and adopt resources](../how-to/importing-and-moving.md) shows the whole sequence.
- [`pravo import`](import.md) and [`pravo state`](state.md) do the same from the command line.
- [Resources and addresses](../concepts/resources-and-addresses.md) explains why a rename changes the address.
