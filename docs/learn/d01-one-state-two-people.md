---
title: One state, two people
description: Keep the state in a store that a team shares, and see what two checkouts of one root share and what each keeps to itself. Who did what, and the mistake that makes two estates out of one.
order: 61
group: Track D · A team on one state
minutes: 12
needs: B7 and Lab 2
learn: Where does the state live when two people work on one root? What does the state record about who changed what? How does a checkout end up with an estate of its own?
done_when: "Dan's checkout plans no changes after Ada applied, pravo state log names both actors, and you can say why a wrong prefix is a second estate."
level: intermediate
services:
  - fakes3:9000
env:
  AWS_ACCESS_KEY_ID: test
  AWS_SECRET_ACCESS_KEY: test
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}"
    replace: "<txn>"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
  - pattern: "\\[id=[^\\]]+\\]"
    replace: "[id=<id>]"
  - pattern: "[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2} [0-9][0-9.a-z]*"
    replace: "<time> <duration>"
---

Track B ran everything on one machine, with the state in a directory next to the specification. A team does not work that way. Two people have two checkouts of one repository, and the state has to be where both can reach it. In OpenTofu that is a backend block, and the state is one file that every apply downloads, rewrites and uploads under one lock.

:::compare
```hcl title="backend.tf"
terraform {
  backend "s3" {
    bucket = "northwind-state"
    key    = "prod/terraform.tfstate"
    region = "eu-west-1"
  }
}
```

```cue title="main.cue"
$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend: "s3"
		bucket:  "northwind-state"
		prefix:  "prod"
		region:  "eu-west-1"
	}
}
```
:::

pravo keeps the state as one record per resource, in a store that the specification names. Two checkouts of the same root read and write the same records, each apply locks the records it writes and no others, and every change is a transaction that says who ran what. This track follows a team of three, Ada, Dan and Eve, and in this lesson two of them share a state for the first time. Every lesson of the track runs against `tools/fakes3`, the in-memory S3 server in pravo's source, on port 9000, so nothing here needs an account.

:::figure Two checkouts of one root. The specification is in each checkout, the records are in the bucket, and a transaction names the actor that wrote it.
<svg font-size="13" viewBox="0 0 560 190" role="img" aria-label="Two boxes on the left, ada's checkout and dan's checkout, each holding main.cue. Arrows from both point at one box on the right, the bucket northwind-state under prod, which holds one record per resource and the transactions, each with an actor.">
<g fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="20" width="150" height="52"/><rect x="2" y="112" width="150" height="52"/><rect x="330" y="20" width="228" height="144"/><path d="M152 46H330M152 138H330"/></g>
<g fill="currentColor"><path d="M323 42l7 4-7 4zM323 134l7 4-7 4z"/></g>
<text x="14" y="42" style="fill: var(--text)">ada/</text>
<text x="14" y="60">main.cue, cue.mod</text>
<text x="14" y="134" style="fill: var(--text)">dan/</text>
<text x="14" y="152">main.cue, cue.mod</text>
<text x="342" y="42" style="fill: var(--text)">s3://northwind-state/prod</text>
<text x="342" y="66">resources/port.json</text>
<text x="342" y="84">resources/pet.json</text>
<text x="342" y="108">txn/…  actor ada</text>
<text x="342" y="126">txn/…  actor dan</text>
<text x="342" y="150">locks/  while an apply runs</text>
</svg>
:::

## Point the root at a store

Work in an empty directory. Ada's checkout is `ada/`. The `$settings` block names the store, and everything else is a root as in Track B. The endpoint and `path_style` are for the in-memory server; a real bucket needs the bucket, the prefix and the region, and the credentials that the AWS tools already use.

```cue file=ada/main.cue title="ada/main.cue" hl="8-16"
package team

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "northwind-state"
		prefix:     "prod"
		region:     "eu-west-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}

$random: rnd.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29999, seed: "web"}
```

Ada initialises the root and applies. `--actor` names her in the transaction and `--reason` says why. pravo records both and verifies neither; the lesson [Seal the state](d03-seal-the-state.md) adds a name that is verified.

```sh run
pravo -C ada init > /dev/null
pravo -C ada --actor ada --reason "Reserve a port for the web server" apply --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

Nothing was written next to `main.cue`. The records went to the bucket, and `pravo state pull` copies them to a directory to look at:

```sh run
pravo -C ada state pull copy
find ada/copy -type f | sort
```

```text out
Copied 4 objects from s3://northwind-state/prod to copy
ada/copy/meta.json
ada/copy/outputs.json
ada/copy/resources/port.json
ada/copy/txn/<txn>.json
```

One object for the record of `port`, one for the transaction that wrote it, one for the outputs, which are empty so far, and `meta.json` for the state as a whole. A second resource would be a second object. That is the whole difference from one state file, and everything in this track follows from it.

## A second checkout

Dan clones the repository. Here a copy of Ada's checkout stands for the clone, `cue.mod` included: a root is the files, and the store is named in them.

```sh run
cp -r ada dan
rm -r dan/copy
```

:::predict Dan runs `pravo plan` in his checkout. Ada's apply wrote nothing into his directory. What does the plan say?
Ask where the plan reads the records from.

::reveal
`No changes`. The plan reads the records from the bucket, and the bucket already holds `port`. Dan's directory holds the specification, which is the same as Ada's.
:::

```sh run
pravo -C dan plan | tail -n 2
pravo -C dan state list
```

```text out
No changes. The specification matches the state. 1 record was not read; run pravo drift to compare it with the infrastructure.
Graph: planned 0 · skipped 1 · not read 1
port
```

Dan adds a resource and applies it under his own name.

```cue file=dan/main.cue title="dan/main.cue" hl="21"
package team

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "northwind-state"
		prefix:     "prod"
		region:     "eu-west-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}

$random: rnd.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29999, seed: "web"}
pet:  $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo -C dan --actor dan --reason "Name the web server" apply --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

## Who did what

Ada has not pulled Dan's commit yet. Her checkout still says one resource, and the state says two. `pravo state log` lists every transaction, newest first, with the actor, the reason and the records it wrote:

```sh run
pravo -C ada state log | head -n 12
```

```text out
<txn>  apply    applied   <time> <duration>
    pravo -C dan --actor dan --reason Name the web server apply --auto-approve
    actor dan  Name the web server
    1 added, 0 changed, 0 destroyed
    create   pet  r0 -> r1
<txn>  apply    applied   <time> <duration>
    pravo -C ada --actor ada --reason Reserve a port for the web server apply --auto-approve
    actor ada  Reserve a port for the web server
    1 added, 0 changed, 0 destroyed
    create   port  r0 -> r1
```

Each line of the form `create pet r0 -> r1` is a record and its revision: `pet` went from not existing, revision 0, to revision 1. A later apply that changes `pet` writes revision 2, and a plan that was made against revision 1 is stale when it meets revision 2. That is the check that [Lab 2](lab2-two-applies-at-once.md) showed with exit code 3, and it works across checkouts because the revisions live in the bucket.

Ada's plan, made from a specification that does not have `pet`, wants to remove it. The state is right and her checkout is behind:

```sh run
pravo -C ada plan 2>&1 | grep -E '^(Plan|Graph):'
```

```text out
Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 · skipped 1 · removed 1 · refreshed 1 · not read 1
```

Pull before you plan, as you commit before you apply. A plan that destroys something you did not touch is the first sign that the checkout is behind the state.

```sh run
cp dan/main.cue ada/main.cue
pravo -C ada plan | tail -n 2
```

```text out
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
```

## Break it: a prefix of its own

Dan edits the settings and gets the prefix wrong. The block carries `status=unintended`: the specification is valid, and it names a place where nothing is.

```cue file=dan/main.cue title="dan/main.cue" status=unintended hl="12"
package team

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "northwind-state"
		prefix:     "prd"
		region:     "eu-west-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}

$random: rnd.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29999, seed: "web"}
pet:  $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo -C dan plan | grep -E '^(Plan|Graph):'
```

```text out
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2
```

Nothing is wrong, as far as pravo can tell. The prefix `prd` holds no records, so the two resources do not exist there, and the plan creates them. An apply would create a second port and a second pet in the cloud, under a second estate that nobody meant to have. The store is named in the specification, so the mistake is in a file, and a review catches it. Keep the settings in a file of their own, change it rarely, and when a plan wants to create everything you already have, check the prefix before anything else.

```sh run
sed -i 's/prefix:     "prd"/prefix:     "prod"/' dan/main.cue
pravo -C dan plan | tail -n 1
```

```text out
Graph: planned 0 · skipped 2 · not read 2
```

## Exercise

:::exercise Add an output and read it from the other checkout
Add an output `address` to Dan's checkout, the pet's id and the port, and apply it as Dan. Then read the output from Ada's checkout, without copying Dan's file first.

::hint
B3 wrote outputs: `address: pravo.#Output & {value: ...}`. The pet's name is `pet.id`, and `strconv.FormatInt` makes a string of the port, or use a string interpolation `"\(pet.id):\(port.result)"`.

::hint
Outputs live in the state with the records, so `pravo output` in Ada's checkout reads what Dan's apply wrote.

::solution
```cue file=dan/outputs.cue title="dan/outputs.cue"
package team

import "pravo.dev/pravo"

address: pravo.#Output & {value: "\(pet.id):\(port.result)"}
```

```sh run
pravo -C dan --actor dan apply --auto-approve > /dev/null
pravo -C ada output address | sed 's/"[a-z-]*:/"<pet>:/'
```

```text out
"<pet>:26720"
```

The name of the pet differs on every run, and the port does not: `seed` makes `random_integer` repeatable. Ada's checkout has no `outputs.cue`, and the value is there all the same, because the outputs are a record of the state.

```sh run check
pravo -C ada output address | grep -q ':2'
```
:::

## Clean up

Destroy as Dan, from the checkout that has every resource:

```sh run
pravo -C dan destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- The specification names the store, so every checkout of the root reads and writes the same records. A plan reads the records from the store, and nothing from the directory of another checkout.
- Every change is a transaction that names its actor, its reason and the records it wrote with their revisions. `pravo state log` lists them, newest first.
- A wrong prefix is not an error. It is an empty estate, and a plan that wants to create everything you already have is the sign of it.

:::check
Dan applied a resource from his checkout. Why does Ada's checkout know about it without a pull?
::answer
The record went to the bucket, not to Dan's directory, and Ada's plan reads the bucket. Her specification is behind, which is why her plan wants to remove the resource until she pulls.

What does `create pet r0 -> r1` in the log say?
::answer
The transaction wrote the record of `pet`, which did not exist before, revision 0, and has revision 1 now. A plan that was made against revision 0 is stale.
:::

The how-to page [Keep the state in a shared store](../how-to/keep-the-state-in-a-shared-store.md) sets up a real bucket, moves an existing local state into it and lists what the credentials need. [State](../concepts/state.md) explains the records, the revisions and the locks. The next lesson, [Store a plan](d02-store-a-plan.md), puts a plan in the state so that the other person can read it before anyone applies it.
