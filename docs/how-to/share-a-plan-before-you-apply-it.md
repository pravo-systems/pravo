---
title: Share a plan before you apply it
description: Store a plan in the state so that a team can list it, read it, see who else plans the same resources and apply it by its id.
order: 12
minutes: 15
needs: A root that you can plan, and `jq`. B7 teaches saved plans. The last section uses the in-memory S3 server from pravo's source.
done_when: "`pravo plan list` in one checkout shows the plan that another checkout stored, and `pravo apply plan-ID` applies it."
level: intermediate
group: Operate
keywords: "stored plan plan store plan id share plan list plans handoff review"
services:
  - fakes3:9000
env:
  AWS_ACCESS_KEY_ID: test
  AWS_SECRET_ACCESS_KEY: test
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: 'created [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8}'
    replace: 'created <time>'
---

A plan file belongs to the person who made it. Nobody else knows that it exists, and two people can plan the same resource without noticing. `pravo plan --store` writes the plan into the state instead. Anyone who can read the state can list the plan, read it and apply it.

This page follows two people, `alice` and `bob`. Each has a checkout of one repository, and the two checkouts share one state.

> [!WARNING]
> A stored plan holds the same values as a plan file, sensitive values included. Whoever can read the state can read its plans, as they can read its records. `pravo serve` withholds them, as it does for a plan file.

## 1. Share a state

Work in an empty directory. It holds the checkout of alice, the checkout of bob and the state they share. A team keeps the state in a bucket, as [section 9](#9-use-a-bucket) shows. A directory next to both checkouts stands in for it.

```cue file=alice/main.cue title="alice/main.cue" hl="8"
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {state: path: "../state"}

$random: rnd.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29999, seed: "web"}
```

Bob's checkout is a copy of alice's. A plan applies only to the files it was made from, `cue.mod` included.

```sh run
pravo -C alice init > /dev/null
cp -r alice bob
```

## 2. Store a plan

Alice makes a plan and stores it. `--actor` names her and `--reason` says why. pravo records both and does not verify them. Without `--actor`, the author of a plan is the operating system user.

```sh run
pravo -C alice --actor alice --reason "Reserve a port for the web server" plan --store | tail -n 4
```

```text out
Graph: planned 1

Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
```

The plan is the object that `plan --out` writes to a file. pravo writes it below `plans/` in the state and prints its id: `plan-` and the first eight hexadecimal digits of its digest. Nothing is applied. A plan that creates, changes or destroys no resource is not stored: `pravo apply` applies it.

The id differs on every run. This page keeps it in a file, and `$(cat alice.id)` stands for the id that pravo printed.

```sh run
pravo -C alice plan list --json | jq -r '.plans[] | select(.author == "alice") | .id' > alice.id
```

## 3. Find the plan and read it

Bob lists the plans of the state.

```sh run
pravo -C bob plan list
```

```text out
plan-<id>  pending  alice  just now  +1 ~0 -0  expires in 7 days
```

The columns are the id, the status, the author and the age. Then come the counts of resources to add, change and destroy, and a note. `pending` means that the plan can be applied. The note says when it expires.

`plan show` prints the plan after a header. `pravo show ID` prints the same.

```sh run
pravo -C bob plan show $(cat alice.id) | head -n 7
```

```text out
Stored plan plan-<id> by alice, just now (pending)
  reason:  Reserve a port for the web server
  command: pravo -C alice --actor alice --reason Reserve a port for the web server plan --store
  expires in 7 days
  apply:   pravo apply plan-<id>

pravo will perform the following actions:
```

The header names the author, the age, the status and the reason. It adds the command that made the plan, its expiry and the command that applies it. Six hexadecimal digits are enough to find a plan, as in `plan-2f9c1a`, when no other plan starts with them. `pravo serve` lists the same plans in a browser and shows which resources they share: [Explore an estate in a browser](explore-your-estate.md#7-see-what-your-team-has-planned).

## 4. See who else plans the same resource

Bob wants a narrower range. He changes his checkout and stores a plan.

```cue file=bob/main.cue title="bob/main.cue" hl="11"
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {state: path: "../state"}

$random: rnd.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29998, seed: "web"}
```

```sh run
pravo -C bob --actor bob --reason "Keep 29999 free" plan --store | tail -n 5
pravo -C bob plan list --json | jq -r '.plans[] | select(.author == "bob") | .id' > bob.id
```

```text out
Graph: planned 1

Warning: plan-<id> by alice (just now) also changes 1 of these resources: port
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
```

pravo compares the new plan with the stored plans that can still be applied. Alice's plan changes `port` too, so pravo warns bob. The warning is information. It does not stop bob and does not change alice's plan.

A plan that is not stored warns in the same way. It names every open plan that changes one of its resources.

```sh run
pravo -C bob plan | tail -n 2
```

```text out
Warning: plan-<id> by bob (just now) also changes 1 of these resources: port
Warning: plan-<id> by alice (just now) also changes 1 of these resources: port
```

`--touching` lists the plans that change one resource.

```sh run
pravo -C bob plan list --touching port
```

```text out
plan-<id>  pending  bob    just now  +1 ~0 -0  expires in 7 days
plan-<id>  pending  alice  just now  +1 ~0 -0  expires in 7 days
```

`plan list` judges a plan by the state alone. `plan show` and `apply` also compare it with the checkout and with the providers. Bob's checkout differs from the files alice planned from, and `plan show` says so.

```sh run
pravo -C bob plan show $(cat alice.id) | grep '^Warning'
```

```text out
Warning: this plan can no longer be applied: the configuration changed since the plan was made (main.cue changed)
```

Bob cannot apply alice's plan from this checkout. Alice can apply it from hers. The reason names the files that differ. When it names `cue.mod/module.cue`, the two checkouts have different module files, as they do when `pravo init` wrote the module in directories with different names: commit `cue.mod/module.cue` and use the same commit.

## 5. Apply by id

Alice applies her plan by its id. The checks are those of a plan file. pravo compares the configuration, the providers, the lineage of the state and the revision of every record the plan uses. A policy named with `--policy` judges the whole plan. `--only` applies a closed part.

```sh run
pravo -C alice apply $(cat alice.id)
```

```text out
port: Creating...
port: Creation complete after 0s [id=<id>]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
```

The plan is now `applied`. Bob's plan assumed that `port` does not exist, so it is `stale`: no part of it can be applied any more. Its only change conflicts with the state. A plan with several changes is judged change by change. When the state moved under some of them and not under the others, it is `partly-stale`, and `plan list` prints the `--only` set that applies the rest: see [A partly stale plan](../reference/apply.md#a-partly-stale-plan). `plan list` shows the plans that can be applied, a partly stale one among them, and `--all` adds the others.

```sh run
pravo -C bob plan list
pravo -C bob plan list --all
```

```text out
No stored plan can be applied. "pravo plan list --all" lists the others.
plan-<id>  stale    bob    just now  +1 ~0 -0  port changed in state (revision 1, plan expected 0)
plan-<id>  applied  alice  just now  +1 ~0 -0  applied just now
```

An apply of a stale plan changes nothing and exits with code 3. An apply that fails part-way is different: it records the changes it finished, the plan is `partial`, and the same command applies the rest once the cause is gone. See [Apply a stored plan](../reference/apply.md#apply-a-stored-plan).

```sh run exit=3
pravo -C bob apply $(cat bob.id)
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - port changed in state (revision 1, plan expected 0)
```

Bob plans again against the new state.

## 6. Collect what nobody needs

`pravo plan gc` removes the plans that nobody will apply. The first collection that finds a plan stale marks it and removes nothing.

```sh run
pravo -C bob plan gc
```

```text out
1 plan can no longer be applied; a collection that runs 1 hour later removes it:
  plan-<id>  bob        stale, port changed in state (revision 1, plan expected 0)
```

A collection that runs more than an hour later removes the plan. Until then, a stale plan stays readable. A plan that loses its staleness loses the mark.

| A plan is removed when | Rule |
|---|---|
| It expires | Seven days after it was made, or when `--ttl` says. |
| It is stale | By the first collection that runs more than one hour after a collection saw it stale. A partly stale plan is not removed for this reason. |
| It was applied completely | 24 hours after the last change was applied. |
| `--older-than DURATION` is given | It was not applied and was made longer ago than `DURATION`. |

A plan that an apply is running from is never removed. Nor is a plan that another collection is removing, or one that holds a reservation made with `plan --reserve`. `pravo plan --store` runs a small collection of its own.

## 7. Let a plan expire

A stored plan that nobody applies expires after seven days. `--ttl` sets another time: `90m`, `36h` or `7d`. A short time shows what expiry does.

```sh run
pravo -C bob --actor bob plan --store --ttl 2s | tail -n 2
sleep 3
pravo -C bob plan list
pravo -C bob plan list --all
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after a moment. To apply exactly these actions, run:
  pravo apply plan-<id>
No stored plan can be applied. "pravo plan list --all" lists the others.
plan-<id>  expired  bob    just now  +1 ~0 -1  expired a moment ago
plan-<id>  stale    bob    just now  +1 ~0 -0  port changed in state (revision 1, plan expected 0)
plan-<id>  applied  alice  just now  +1 ~0 -0  applied just now
```

An expired plan is listed only with `--all`. `apply` refuses it with exit code 3, even before a collection has removed it. `--dry-run` says what a collection would remove and changes nothing, not even a mark.

```sh run
pravo -C bob plan gc --dry-run
pravo -C bob plan gc
```

```text out
Would remove 1 stored plan:
  plan-<id>  bob        expired a moment ago
Removed 1 stored plan:
  plan-<id>  bob        expired a moment ago
```

## 8. Keep a plan longer, or take it with you

Bob stores a plan that he wants to keep for a day and a half.

```sh run
pravo -C bob --actor bob plan --store --ttl 36h | tail -n 2
pravo -C bob plan list --json | jq -r '.plans[0].id' > keep.id
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 36 hours. To apply exactly these actions, run:
  pravo apply plan-<id>
```

`plan pull` writes a stored plan to a plan file. Send the file, attach it to a ticket or apply it elsewhere.

```sh run
pravo -C bob plan pull $(cat keep.id) bob.plan
pravo -C bob show bob.plan | head -n 1
```

```text out
Wrote plan-<id> to bob.plan. To apply exactly these actions, run:
  pravo apply bob.plan
Saved plan created <time> by "pravo -C bob --actor bob plan --store --ttl 36h" (actor bob)
```

Applying the file records its progress in the file. The stored plan does not learn of it. Its status follows the state, which has moved, and it is `stale`.

```sh run
pravo -C bob apply bob.plan | tail -n 1
pravo -C bob plan list --all | head -n 1
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
plan-<id>  stale    bob    just now  +1 ~0 -1  port changed in state (revision 2, plan expected 1)
```

## 9. Use a bucket

The commands are the same when the state is in an S3 bucket. The objects below `plans/` sit next to the records, under the same prefix. The commands below run against `tools/fakes3`, the in-memory S3 server in pravo's source, on port 9000. [Keep the state in a shared bucket](remote-state.md) sets up a real bucket.

```cue file=team/main.cue title="team/main.cue"
package team

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "pravo-state"
		prefix:     "demo/team"
		region:     "us-east-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}

$random: rnd.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29999, seed: "web"}
```

```sh run
pravo -C team init > /dev/null
pravo -C team --actor alice plan --store | tail -n 2
pravo -C team plan list
pravo -C team state pull copy
ls team/copy/plans
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
plan-<id>  pending  alice  just now  +1 ~0 -0  expires in 7 days
Copied 3 objects from s3://pravo-state/demo/team to copy
plan-<id>.meta.json
plan-<id>.plan.json
```

`state pull` and `state push` copy the plans with the records. Storing, listing and collecting plans needs the permissions that the state already needs: get, put, delete and list on the objects of the bucket. pravo creates the plan and its summary with `If-None-Match: *`, and replaces the progress object and deletes objects with `If-Match`. [S3-compatible stores](../reference/s3-stores.md) says how to test that a store honours them.

## What next

- [`pravo plan`](../reference/plan.md) lists every flag and subcommand, and the fields of the JSON document.
- [Plans](../concepts/plans.md#stored-plans) explains the life of a stored plan.
- [Run pravo in a pipeline](ci.md) uses plans in a job.
- [State format](../reference/state-format.md#plans) lists the objects of a stored plan.
