---
title: Renames, moves, imports
description: Keep the object of a resource when its address changes, and adopt an object that already exists.
order: 38
group: Track B · pravo
minutes: 15
needs: B7
learn: What does a rename do to a resource? How do you keep the object, and how do you adopt one that already exists?
done_when: "A renamed resource keeps its object, and an existing object is adopted with `$import`."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
  - pattern: "refreshed [0-9a-z.]+ ago"
    replace: "refreshed 0s ago"
keywords: "moved block import state mv rename"
---

You rename a resource in the file. In HCL the next plan destroys the old object and creates a new one. A `moved` block prevents that, and you keep it until every state has seen it. An object that exists and has no record needs an `import` block. Both blocks are separate from the resource, and both name addresses as text.

:::compare
```hcl title="main.tf"
moved {
  from = random_integer.server
  to   = random_integer.primary
}

import {
  to = random_integer.dice
  id = "5,1,10"
}
```

```cue title="main.cue"
primary: $random.#Resource.random_integer & {
	$from: "server"
	min:   1
	max:   99
}

dice: $random.#Resource.random_integer & {
	$import: "5,1,10"
	min:     1
	max:     10
}
```
:::

In pravo the address of a resource is the path where its struct sits. Rename the field and you have a new address. `$from` and `$import` are fields of the resource they describe.

## An address is an identity

Work in an empty directory. This root has a number and a file that reads it.

```cue file=main.cue title="main.cue"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

server: $random.#Resource.random_integer & {min: 1, max: 99, seed: "s1"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(server.result)\n"
}
```

```sh run
pravo init
pravo apply --auto-approve
pravo state list
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
...
pravo has been initialised. Run "pravo plan" to see what it would do.
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
note
server
```

The state holds two records, at the addresses `server` and `note`. Rename `server` to `primary` and update the reference. The object is the same. Only the name changes.

```cue file=main.cue title="main.cue" status=unintended hl="11,15"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

primary: $random.#Resource.random_integer & {min: 1, max: 99, seed: "s1"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(primary.result)\n"
}
```

:::predict What does pravo plan do with this rename?
Count the records in the state, and the addresses the specification now declares.

::reveal
The state has `server`, and the specification has `primary`. pravo plans to create `primary` and to destroy `server`. The file reads `primary`, so it is replaced as well.

```sh run
pravo plan
```

```text out
...
  # primary (random_integer) will be created
  + primary: {
  +     id:     (known after apply)
  +     max:    99
  +     min:    1
  +     result: (known after apply)
  +     seed:   "s1"
    }

  # note (local_file) must be replaced
-/+ note: {
  ~     content:              "server 3\n" -> (known after apply) # forces replacement
...
    }

  # server (random_integer) will be destroyed
  - server: {
  -     id:     "3"
  -     max:    99
  -     min:    1
  -     result: 3
  -     seed:   "s1"
    }

Plan: 2 to add, 0 to change, 2 to destroy.
Hint: primary looks like server under a new address; to keep the existing object, add $from: "server" to primary
Graph: planned 2 · removed 1 · refreshed 2
```

The plan creates two resources and destroys two. A line starting with `Hint` says that `primary` looks like `server` under a new address. It appears when a plan creates one resource and destroys another of the same type with the same configuration.
:::

Applying this plan destroys the number, creates another and replaces the file that reads it.

## Keep the object with $from

Name the old address on the resource that has the new one.

```cue file=main.cue title="main.cue" hl="11"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

primary: $random.#Resource.random_integer & {$from: "server", min: 1, max: 99, seed: "s1"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(primary.result)\n"
}
```

```sh run
pravo plan
```

```text out
pravo will perform the following actions:

  # server will be moved to primary (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 1 to move.
Graph: planned 0 · skipped 2 · not read 2 · to move 1
```

The plan lists the rename. It changes nothing in the world: `0 to add, 0 to change, 0 to destroy`, and it writes nothing to the state, which still holds the old address. The apply renames the record.

Saved to a file and opened in the viewer, the plan draws the move: `primary` carries the badge of a move, and its Change tab says that no attribute changes. `server`, the record that the move renames, is drawn faint beside it until the apply.

```sh
pravo plan --out change.plan
pravo serve --plan change.plan
```

:::figure The move as a plan. `primary` carries the badge of a move and nothing else changes; `server`, the old address, is drawn faint beside it.
<a class="shot shot-dark" href="../img/serve/b08-move-dark.png"><img src="../img/serve/b08-move-dark.png" width="1440" height="900" alt="The viewer with the plan change.plan laid over the graph: a strip under the bar names the file, with 1 change. The resource primary, a random_integer, is selected and carries the badge of a move, with a dashed outline, and is wired to the file note, outlined in amber. Under it, server, the old address, is drawn faint with a crossed circle. The inspector shows primary with the plan action move, the record declared only and no revision, and its Change tab reads move and says no attribute is listed for this change. The status bar says planned 1 of 3, 2 not read." loading="lazy"></a>
<a class="shot shot-light" href="../img/serve/b08-move-light.png"><img src="../img/serve/b08-move-light.png" width="1440" height="900" alt="The viewer with the plan change.plan laid over the graph: a strip under the bar names the file, with 1 change. The resource primary, a random_integer, is selected and carries the badge of a move, with a dashed outline, and is wired to the file note, outlined in amber. Under it, server, the old address, is drawn faint with a crossed circle. The inspector shows primary with the plan action move, the record declared only and no revision, and its Change tab reads move and says no attribute is listed for this change. The status bar says planned 1 of 3, 2 not read." loading="lazy"></a>
:::

> [!NOTE]
> A plan writes no record for a `$from` or an `$import`: it says what the apply will write. The apply writes it first. No object changes, and only the records move.

```sh run
pravo state list
pravo apply --auto-approve | grep '^Apply complete'
pravo state list
pravo plan
```

```text out
note
server
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
note
primary
...
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
```

A second plan finds nothing to do. The `$from` can stay in the file or go. Once the old address is gone from the state, the field does nothing.

A move keeps the type. A `$from` that names a record of another type is refused.

:::deep-dive Move a record to a different type
Replace the root with one resource that claims the record of the number as a file.

```cue file=main.cue title="main.cue"
package main

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

copy: $local.#Resource.local_file & {$from: "primary", filename: "copy.txt", content: "x\n"}
```

```sh run exit=1
pravo plan
```

```text out
Error: $from primary -> copy: the record is a managed random_integer but copy is declared as a managed local_file; the type must stay the same
```

The plan stops before it plans anything. The record of `primary` is a `random_integer`, and `copy` is a `local_file`.
:::

## Move a group

A struct that holds resources moves as one. Every record below the old path moves to the same place below the new one. A definition is closed, so it must accept the field. Embed `pravo.#Movable`.

```cue file=main.cue title="main.cue" hl="4,19-23,25"
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

primary: $random.#Resource.random_integer & {$from: "server", min: 1, max: 99, seed: "s1"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(primary.result)\n"
}

#Naming: {
	pravo.#Movable
	first:  $random.#Resource.random_integer & {min: 1, max: 9, seed: "first"}
	second: $random.#Resource.random_integer & {min: 1, max: 9, seed: "second"}
}

names: #Naming
```

```sh run
pravo apply --auto-approve
pravo state list
```

```text out
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 · skipped 2 · not read 2
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
names.first
names.second
note
primary
```

Rename the instance from `names` to `ids` and name the address its records have now.

```cue file=main.cue title="main.cue" hl="25"
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

primary: $random.#Resource.random_integer & {$from: "server", min: 1, max: 99, seed: "s1"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(primary.result)\n"
}

#Naming: {
	pravo.#Movable
	first:  $random.#Resource.random_integer & {min: 1, max: 9, seed: "first"}
	second: $random.#Resource.random_integer & {min: 1, max: 9, seed: "second"}
}

ids: #Naming & {$from: "names"}
```

```sh run
pravo plan
```

```text out
pravo will perform the following actions:

  # names.first will be moved to ids.first (record renamed; no change to the object)

  # names.second will be moved to ids.second (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 2 to move.
Graph: planned 0 · skipped 4 · not read 4 · to move 2
```

Both records move when the plan is applied. This is how you rename an instance of a definition, or move resources into one.

```sh run
pravo apply --auto-approve | grep '^Apply complete'
pravo state list
```

```text out
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
ids.first
ids.second
note
primary
```

## Break it: a definition that is closed

Leave `pravo.#Movable` out of the definition. A small root of its own shows the refusal. The block carries `status=rejected`.

```cue file=closed/main.cue title="closed/main.cue" status=rejected hl="7-9,11"
package closed

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

#Naming: {
	first: $random.#Resource.random_integer & {min: 1, max: 9, seed: "first"}
}

ids: #Naming & {$from: "names"}
```

```sh run exit=6
pravo -C closed init > /dev/null
pravo -C closed validate
```

```text out
Error: evaluating ./closed:
ids.$from: field not allowed:
    ./closed/main.cue:11:17
```

A definition is closed, as in [A8](a08-definitions-reject-typos.md). `$from` is a field the definition does not declare. `pravo.#Movable` declares it.

## Adopt an object with $import

An object that exists can be brought under management without being created again. Put the provider's id for it in `$import`. For `random_integer` the id is the result, the minimum and the maximum, joined by commas.

```cue file=main.cue title="main.cue" hl="27"
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

primary: $random.#Resource.random_integer & {$from: "server", min: 1, max: 99, seed: "s1"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(primary.result)\n"
}

#Naming: {
	pravo.#Movable
	first:  $random.#Resource.random_integer & {min: 1, max: 9, seed: "first"}
	second: $random.#Resource.random_integer & {min: 1, max: 9, seed: "second"}
}

ids: #Naming & {$from: "names"}

dice: $random.#Resource.random_integer & {min: 1, max: 10, $import: "5,1,10"}
```

```sh run
pravo plan
```

```text out
...
dice: Reading the object of id "5,1,10"...
...
pravo will perform the following actions:

  # dice will be imported from id "5,1,10" and matches the specification

Plan: 0 to add, 0 to change, 0 to destroy, 1 to import.
Graph: planned 1 · skipped 4 · not read 4 · to import 1
```

The provider read the object, and the plan compared it with the specification. They match, so there is nothing to change. With `max: 20` the plan reports the import and a replacement, because a new `max` forces one.

The plan writes no record for `dice`. The apply does, and then the record holds the object as the provider reported it.

```sh run
pravo apply --auto-approve | grep '^Apply complete'
pravo state show dice | sed '2d'
```

```text out
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
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

A later plan does not import again, because the record exists. Leave `$import` in the file. The id format is whatever the provider documents for the resource type. Only managed resources can be imported.

`pravo import` does the same from the command line. Declare the resource first, without `$import`, and pass the address and the id.

```cue file=main.cue title="main.cue" hl="29"
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

primary: $random.#Resource.random_integer & {$from: "server", min: 1, max: 99, seed: "s1"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(primary.result)\n"
}

#Naming: {
	pravo.#Movable
	first:  $random.#Resource.random_integer & {min: 1, max: 9, seed: "first"}
	second: $random.#Resource.random_integer & {min: 1, max: 9, seed: "second"}
}

ids: #Naming & {$from: "names"}

dice: $random.#Resource.random_integer & {min: 1, max: 10, $import: "5,1,10"}

coin: $random.#Resource.random_integer & {min: 0, max: 1}
```

```sh run
pravo import coin 1,0,1
pravo state list
```

```text out
coin: Importing from ID "1,0,1"...
coin: Import complete. Run "pravo plan" to compare it with the configuration.
coin
dice
ids.first
ids.second
note
primary
```

The specification carries `$import`, so a reviewer sees it in the diff. The command changes the state at once and leaves no trace in the files. Prefer `$import`.

## Edit the state directly

Two commands change the state without a plan.

| Command | Effect |
|---|---|
| `pravo state mv <from> <to>` | Renames one record. |
| `pravo state rm <addr>` | Forgets a record. The object stays. |

Both act at once and nobody reviews them. `$from` does the same work in a plan. Rename `coin` to `flip` and back to see the effect.

```sh run
pravo state mv coin flip
pravo state list
pravo state mv flip coin
```

```text out
Moved coin to flip
dice
flip
ids.first
ids.second
note
primary
Moved flip to coin
```

## Exercise

:::exercise Rename without recreating
Rename `note` to `readme` and keep the file `note.txt`. The plan must say that the record was renamed and must change nothing.

::hint
Put `$from` on the resource that has the new name. It holds the old address as a string.

::hint
The plan prints `will be moved to` for each record it renames, and the apply renames them. See [Moves and imports](../reference/spec-moves-and-imports.md).

::solution
```cue file=main.cue title="main.cue"
package main

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

primary: $random.#Resource.random_integer & {$from: "server", min: 1, max: 99, seed: "s1"}

readme: $local.#Resource.local_file & {
	$from:    "note"
	filename: "note.txt"
	content:  "server \(primary.result)\n"
}

#Naming: {
	pravo.#Movable
	first:  $random.#Resource.random_integer & {min: 1, max: 9, seed: "first"}
	second: $random.#Resource.random_integer & {min: 1, max: 9, seed: "second"}
}

ids: #Naming & {$from: "names"}

dice: $random.#Resource.random_integer & {min: 1, max: 10, $import: "5,1,10"}

coin: $random.#Resource.random_integer & {min: 0, max: 1}
```

```sh run
pravo plan
```

```text out
...
  # note will be moved to readme (record renamed; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy, 1 to move.
Graph: planned 1 · skipped 5 · refreshed 1 · not read 5 · to move 1
```

```sh run check
pravo apply --auto-approve > /dev/null
pravo state list | grep -qx readme
! pravo state list | grep -qx note
pravo plan | grep -q 'No changes.'
```
:::

## Clean up

```sh run
pravo destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 6 destroyed. [txn <txn>]
```

## Recap

- An address is the path where a struct sits, and it is the identity of a resource. A rename without `$from` destroys and creates.
- `$from` names the old address. On a struct that holds resources it moves every record below it.
- `$import` adopts an existing object by its provider id. `pravo import` does the same from the command line.

:::check
What does a plan print when it creates one resource and destroys another of the same type and configuration?
::answer
A `Hint` line that names the new address, the old one, and the `$from` that keeps the object.

Why does a definition reject `$from` unless it embeds `pravo.#Movable`?
::answer
A definition is closed. It accepts only the fields it declares, and `pravo.#Movable` declares `$from`.
:::

To do this on your own project, use the how-to [Rename, move and adopt resources](../how-to/importing-and-moving.md). [B9](b09-drift.md) finds the changes that someone made outside pravo. [Migrate](../migrate/m5-convert-the-state.md) uses `$from` to point converted state at new addresses.
