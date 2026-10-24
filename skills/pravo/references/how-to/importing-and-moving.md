<!-- Generated from docs/how-to/importing-and-moving.md by tools/skillgen. Do not edit. -->

# Rename, move and adopt resources

Rename a resource without recreating it, move a group of resources, adopt an object that already exists and forget one without destroying it.

The address of a resource is its path in the specification. A rename in the specification changes the address, and pravo reads the old address as a resource that is gone. This page keeps the object through a rename, moves a group, adopts an object and forgets one.

Work in an empty directory.

File `main.cue`:

```cue
package moves

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

server: $random.#Resource.random_pet & {length: 2}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(server.id)\n"
}
```

```sh
pravo init > /dev/null
pravo apply -auto-approve | grep '^Apply complete'
pravo state list
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
note
server
```

## 1. Rename a resource and keep its object

Rename `server` to `primary` and update the reference. Without anything else, pravo plans a new resource, a replacement of the file that depends on it and the destruction of the old one.

File `main.cue`:

```cue
package moves

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

primary: $random.#Resource.random_pet & {length: 2}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(primary.id)\n"
}
```

```sh
pravo plan | grep -E '^(  #|Plan|Hint)'
```

Output:

```text
  # primary (random_pet) will be created
  # note (local_file) must be replaced
  # server (random_pet) will be destroyed
Plan: 2 to add, 0 to change, 2 to destroy.
Hint: primary looks like server under a new address; to keep the existing object, add $from: "server" to primary
```

The `Hint` appears when a plan creates one resource and destroys another of the same type with the same configuration. Say where the record used to be, on the resource that has the new address.

File `main.cue`:

```cue
package moves

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

primary: $random.#Resource.random_pet & {$from: "server", length: 2}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(primary.id)\n"
}
```

```sh
pravo plan
```

Output:

```text
pravo will perform the following actions:

  # server will be moved to primary (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 1 to move.
Graph: planned 0 · skipped 2 · not read 2 · to move 1
```

The plan lists the move and writes nothing. The nodes that it plans see the record at `primary`, so `note` is compared with the right record. The state still holds `server` until the apply, which renames the record and does not touch the object:

```sh
pravo state list
pravo apply -auto-approve | grep '^Apply complete'
pravo state list
```

Output:

```text
note
server
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
note
primary
```

Leave `$from` in the file or remove it in a later change. Once the old address is gone it does nothing.

## 2. Move a group

Resources inside one struct move together. A definition is closed, so it accepts `$from` only when it embeds `pravo.#Movable`. Add a definition with two pets and an instance of it.

File `names.cue`:

```cue
package moves

import "pravo.dev/pravo"

#Naming: {
	pravo.#Movable
	first: $random.#Resource.random_pet & {length: 2}
	second: $random.#Resource.random_pet & {length: 3}
}

names: #Naming
```

```sh
pravo apply -auto-approve | grep '^Apply complete'
pravo state list
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
names.first
names.second
note
primary
```

Rename the instance and say where it used to be.

File `names.cue`:

```cue
package moves

import "pravo.dev/pravo"

#Naming: {
	pravo.#Movable
	first: $random.#Resource.random_pet & {length: 2}
	second: $random.#Resource.random_pet & {length: 3}
}

ids: #Naming & {$from: "names"}
```

```sh
pravo plan | grep -E '^(names|  #|Plan)'
pravo apply -auto-approve | grep '^Apply complete'
```

Output:

```text
  # names.first will be moved to ids.first (record renamed; no change to the object)
  # names.second will be moved to ids.second (record renamed; no change to the object)
Plan: 0 to add, 0 to change, 0 to destroy, 2 to move.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
```

Every record below `names` moves to the same place below `ids`. This is how to rename a module instance, or to move a resource into a module. The instance carries the old path. pravo refuses a `$from` that cannot be a move.

| Message | Cause |
|---|---|
| `e: $from on a struct that holds no resources` | The struct has no resource below it. |
| `server: $from is the address of the resource itself` | The old address equals the new one. |
| `$from server -> copy: the record is a managed random_pet but copy is declared as a managed local_file; the type must stay the same` | A move keeps the type of the resource. |

Each root has its own state, so a rename in shared code needs a `$from` in every root that holds the old address.

## 3. Adopt an object that exists

Put the provider's id for the object in `$import` on the resource that describes it. For `random_integer` the id is the result, the minimum and the maximum.

File `dice.cue`:

```cue
package moves

dice: $random.#Resource.random_integer & {min: 1, max: 10, $import: "5,1,10"}
```

```sh
pravo plan | grep -E '^(dice|  #|Plan)'
```

Output:

```text
dice: Reading the object of id "5,1,10"...
  # dice will be imported from id "5,1,10" and matches the specification
Plan: 0 to add, 0 to change, 0 to destroy, 1 to import.
```

The provider read the object, and the plan compares it with the specification. Here they match, so there is no change to make to the object. With `max: 20` in the specification, the plan shows the difference and the change it needs. The plan writes no record; the apply writes the record for `dice`, and a later plan does not import again, because the record exists. Only managed resources can be imported.

```sh
pravo apply -auto-approve | grep '^Apply complete'
pravo state show dice | sed '2d'
```

Output:

```text
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
// dice (provider registry.opentofu.org/hashicorp/random v3.9.1 as random, schema version 0, revision 2)
{
	id:      "5"
	keepers: null
	max:     10
	min:     1
	result:  5
	seed:    null
}
```

The id is whatever the provider documents for that resource type. `pravo import <address> <id>` does the same from the command line, for a resource that is already declared.

File `dice.cue`:

```cue
package moves

dice: $random.#Resource.random_integer & {min: 1, max: 10, $import: "5,1,10"}

other: $random.#Resource.random_integer & {min: 1, max: 10}
```

```sh
pravo import other 7,1,10
pravo state list
```

Output:

```text
other: Importing from ID "7,1,10"...
other: Import complete. Run "pravo plan" to compare it with the configuration.
dice
ids.first
ids.second
note
other
primary
```

Prefer `$import`. It is in the specification, so a reviewer sees it.

## 4. Edit the state directly

Two commands change the state without a plan. Neither is reviewed, and no policy sees either, because they are not plans.

- `pravo state mv <from> <to>` renames one record. `$from` does the same in the specification.
- `pravo state rm <address>` forgets a record without destroying the object. The next plan creates it again unless you remove it from the specification too.

```sh
pravo state rm other
pravo state list
```

Output:

```text
Removed other
dice
ids.first
ids.second
note
primary
```

The `pravo state` commands take `-t`. They act on the state of the environment that the tags select, and on the default environment without them.

## 5. After a conversion

`pravo state import-tfstate` converts an OpenTofu state and keeps the OpenTofu addresses. Point each record at its new place with `$from`, then plan until it is empty. [Convert the state](../migrate/m5-convert-the-state.md) runs the whole sequence.

## What next

- [B8, Renames, moves, imports](../learn/b08-renames-moves-imports.md) teaches the idea with exercises.
- [Share code between roots in one repository](modules-in-a-repository.md) shows why a rename in shared code reaches every root.
- [Write and roll out plan policies](policies.md) judges the plan that a move or an import produces.
- [Moves and imports](../reference/spec-moves-and-imports.md), [pravo import](../reference/import.md) and [state](../reference/state.md) are the reference.
