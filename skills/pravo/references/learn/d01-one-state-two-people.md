<!-- Generated from docs/learn/d01-one-state-two-people.md by tools/skillgen. Do not edit. -->

# One state, two people

Keep the state in a store that a team shares, and see what two checkouts of one root share and what each keeps to itself. Who did what, and the mistake that makes two estates out of one.

Track B ran everything on one machine, with the state in a directory next to the specification. A team does not work that way. Two people have two checkouts of one repository, and the state has to be where both can reach it. In OpenTofu that is a backend block, and the state is one file that every apply downloads, rewrites and uploads under one lock.

```hcl
terraform {
  backend "s3" {
    bucket = "northwind-state"
    key    = "prod/terraform.tfstate"
    region = "eu-west-1"
  }
}
```

```cue
$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend: "s3"
		bucket:  "northwind-state"
		prefix:  "prod"
		region:  "eu-west-1"
	}
}
```

pravo keeps the state as one record per resource, in a store that the specification names. Two checkouts of the same root read and write the same records, each apply locks the records it writes and no others, and every change is a transaction that says who ran what. This track follows a team of three, Ada, Dan and Eve, and in this lesson two of them share a state for the first time. Every lesson of the track runs against `tools/fakes3`, the in-memory S3 server in pravo's source, on port 9000, so nothing here needs an account.

*Figure: Two checkouts of one root. The specification is in each checkout, the records are in the bucket, and a transaction names the actor that wrote it.*

## Point the root at a store

Work in an empty directory. Ada's checkout is `ada/`. The `$settings` block names the store, and everything else is a root as in Track B. The endpoint and `path_style` are for the in-memory server; a real bucket needs the bucket, the prefix and the region, and the credentials that the AWS tools already use.

File `ada/main.cue`:

```cue
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

```sh
pravo -C ada init > /dev/null
pravo -C ada --actor ada --reason "Reserve a port for the web server" apply --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

Nothing was written next to `main.cue`. The records went to the bucket, and `pravo state pull` copies them to a directory to look at:

```sh
pravo -C ada state pull copy
find ada/copy -type f | sort
```

Output:

```text
Copied 4 objects from s3://northwind-state/prod to copy
ada/copy/meta.json
ada/copy/outputs.json
ada/copy/resources/port.json
ada/copy/txn/<txn>.json
```

One object for the record of `port`, one for the transaction that wrote it, one for the outputs, which are empty so far, and `meta.json` for the state as a whole. A second resource would be a second object. That is the whole difference from one state file, and everything in this track follows from it.

## A second checkout

Dan clones the repository. Here a copy of Ada's checkout stands for the clone, `cue.mod` included: a root is the files, and the store is named in them.

```sh
cp -r ada dan
rm -r dan/copy
```

```sh
pravo -C dan plan | tail -n 2
pravo -C dan state list
```

Output:

```text
No changes. The specification matches the state. 1 record was not read; run pravo drift to compare it with the infrastructure.
Graph: planned 0 · skipped 1 · not read 1
port
```

Dan adds a resource and applies it under his own name.

File `dan/main.cue`:

```cue
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

```sh
pravo -C dan --actor dan --reason "Name the web server" apply --auto-approve | tail -n 1
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

## Who did what

Ada has not pulled Dan's commit yet. Her checkout still says one resource, and the state says two. `pravo state log` lists every transaction, newest first, with the actor, the reason and the records it wrote:

```sh
pravo -C ada state log | head -n 12
```

Output:

```text
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

Each line of the form `create pet r0 -> r1` is a record and its revision: `pet` went from not existing, revision 0, to revision 1. A later apply that changes `pet` writes revision 2, and a plan that was made against revision 1 is stale when it meets revision 2. That is the check that [Lab 2](https://pravo.systems/learn/lab2-two-applies-at-once.html) showed with exit code 3, and it works across checkouts because the revisions live in the bucket.

Ada's plan, made from a specification that does not have `pet`, wants to remove it. The state is right and her checkout is behind:

```sh
pravo -C ada plan 2>&1 | grep -E '^(Plan|Graph):'
```

Output:

```text
Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 · skipped 1 · removed 1 · refreshed 1 · not read 1
```

Pull before you plan, as you commit before you apply. A plan that destroys something you did not touch is the first sign that the checkout is behind the state.

```sh
cp dan/main.cue ada/main.cue
pravo -C ada plan | tail -n 2
```

Output:

```text
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
```

## Break it: a prefix of its own

Dan edits the settings and gets the prefix wrong. The block carries `status=unintended`: the specification is valid, and it names a place where nothing is.

File `dan/main.cue`:

```cue
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

```sh
pravo -C dan plan | grep -E '^(Plan|Graph):'
```

Output:

```text
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2
```

Nothing is wrong, as far as pravo can tell. The prefix `prd` holds no records, so the two resources do not exist there, and the plan creates them. An apply would create a second port and a second pet in the cloud, under a second estate that nobody meant to have. The store is named in the specification, so the mistake is in a file, and a review catches it. Keep the settings in a file of their own, change it rarely, and when a plan wants to create everything you already have, check the prefix before anything else.

```sh
sed -i 's/prefix:     "prd"/prefix:     "prod"/' dan/main.cue
pravo -C dan plan | tail -n 1
```

Output:

```text
Graph: planned 0 · skipped 2 · not read 2
```

## Exercise

## Clean up

Destroy as Dan, from the checkout that has every resource:

```sh
pravo -C dan destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- The specification names the store, so every checkout of the root reads and writes the same records. A plan reads the records from the store, and nothing from the directory of another checkout.
- Every change is a transaction that names its actor, its reason and the records it wrote with their revisions. `pravo state log` lists them, newest first.
- A wrong prefix is not an error. It is an empty estate, and a plan that wants to create everything you already have is the sign of it.

The how-to page [Keep the state in a shared store](../how-to/keep-the-state-in-a-shared-store.md) sets up a real bucket, moves an existing local state into it and lists what the credentials need. [State](../concepts/state.md) explains the records, the revisions and the locks. The next lesson, [Store a plan](d02-store-a-plan.md), puts a plan in the state so that the other person can read it before anyone applies it.
