---
title: pravo plan
description: Show the actions that apply takes. A plan changes no infrastructure, and it can be saved, stored in the state, judged by a policy and applied later.
order: 2
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(Z|[+-]\d\d:\d\d)'
    replace: '<time>'
  - pattern: '\d{4}-\d\d-\d\dT[0-9:.]+Z'
    replace: '<time>'
  - pattern: 'plan:\d+:\d+'
    replace: 'plan:1:<col>'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
  - pattern: 'created \d{4}-\d\d-\d\d \d\d:\d\d:\d\d'
    replace: 'created <time>'
  - pattern: 'txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+'
    replace: 'txn <id>'
keywords: "-target scoped plan saved plan -out stored plan plan id plan list plan gc"
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo plan [flags]` |
| | `pravo plan --store [--ttl DURATION] [flags]` |
| | `pravo plan list [--pending\|--all] [--touching ADDR]` |
| | `pravo plan show ID`, `pravo plan pull ID FILE` |
| | `pravo plan gc [--dry-run] [--older-than DURATION]` |
| Changes infrastructure | No. |
| Writes | Refreshed records to the state, a mark on a record whose object differs or is gone, `$from` moves, `$import` adoptions, and with `--reserve` a reservation. With `--store`, the objects of a stored plan, and the removal of a few that nobody needs. Data sources and ephemeral resources run while the plan is made. |
| Prints | The plan on stdout and one progress line per refreshed resource. With `--json`, the plan document on stdout and the progress on stderr. |
| Taught in | [B2, Reading a plan](../learn/b02-reading-a-plan.md) and [B7, Changing live infrastructure](../learn/b07-changing-live-infrastructure.md). |

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--out FILE` | none | Save the plan to `FILE`, to apply later with `pravo apply FILE`. A plan that a policy refuses is not saved. |
| `--store` | off | Write the plan into the state, where anyone who can read the state can list it and apply it with `pravo apply ID`. A plan that a policy refuses, and a plan with no changes, are not stored. See [Store a plan](#store-a-plan). With `--out`, the file is written first. |
| `--ttl DURATION` | `7d` | With `--store`: how long an unapplied plan is kept. `90m`, `36h` or `7d`. The message that follows says the length as you typed it. An error without `--store`. |
| `--refresh=age\|expired\|false\|all` | `age` | When to read real state from the providers. See [Refresh](#refresh). `--refresh` alone means `all`. |
| `--full` | off | Plan every resource with its provider instead of skipping resources that did not change. |
| `--destroy` | off | Plan the destruction of every resource in the state. Refused while a declared resource in the state has [`prevent_destroy`](spec-meta-fields.md#prevent-destroy). |
| `--replace ADDR` | none | Destroy and create `ADDR` again though the specification did not change. Repeatable, or comma-separated. Refused with `--destroy`. |
| `--detailed-exitcode` | off | Exit with code 2 when the plan has changes. |
| `--include-skipped` | off | With `--json`, list the nodes the plan left alone too, as entries with their address, mode, type and name, the action `no-op` and `skipped`. See [The plan document](plan-document.md). |
| `--reserve DURATION` | none | Reserve the nodes that have pending changes for `DURATION`, for example `15m`. |
| `--policy FILE` | none | A CUE file the plan must satisfy. Repeatable. See [Policy](#policy). |
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field. Repeatable. The subcommands take it too: the tags select the state. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE into tags written `@tag(name,var=os)`. The subcommands take it too. |

The [global flags](global-flags.md) apply. A single dash works as well: `-refresh=false`. The value of `--refresh` follows `=`. A word after the flag is an argument, and `plan` takes none, so `-refresh false` is an error.

### Subcommands

| Subcommand | Prints or does | Writes the state |
|---|---|---|
| `list` | The stored plans: id, status, author, age, counts and a note. See [List stored plans](#list-stored-plans). | no |
| `show ID` | A stored plan after a header. `pravo show ID` prints the same. See [Show and pull](#show-and-pull). | no |
| `pull ID FILE` | Writes a stored plan to a plan file. | no |
| `gc` | Removes the stored plans that nobody needs. See [Collect stored plans](#collect-stored-plans). | yes, not with `--dry-run` |

| Flag | Subcommand | Meaning |
|---|---|---|
| `--pending` | `list` | List the plans that can still be applied: `pending` and `partial`. This is the default. |
| `--all` | `list` | Also list the stale, applied and expired plans that no collection has removed. Refused with `--pending`. |
| `--touching ADDR` | `list` | Only the plans that change the resource at `ADDR`. |
| `--dry-run` | `gc` | Say what a collection would do and change nothing, not even a mark. |
| `--older-than DURATION` | `gc` | Also remove the unapplied plans that were made longer ago than `DURATION`. |

`plan` takes no argument of its own. A word that is not a subcommand is an error: `pravo plan takes no arguments, but got "x"`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The plan was made. With `--detailed-exitcode`, it has no changes. |
| 1 | An error: a provider failed, `--replace` names a resource that cannot be replaced, an argument was given: `plan` takes none, `--ttl` without `--store`, an id that no stored plan has. |
| 2 | With `--detailed-exitcode`: the plan has changes. |
| 4 | Rejected: another actor holds a lock or a reservation on a node. |
| 5 | A policy refused the plan, a `--policy` file cannot be used, or a module does not match its lock. |
| 6 | The specification does not load or evaluate. |

## Examples

Work in an empty directory named `demo`. The specification has two resources, and `port` reads `base`.

```cue file=main.cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}

port: $random.#Resource.random_integer & {
	min:  20000 + base.result
	max:  20100
	seed: "port"
}

url: pravo.#Output & {value: "http://localhost:\(port.result)"}
```

### A new root

```sh run
pravo init > /dev/null
pravo plan
```

```text out
pravo will perform the following actions:

  # base (random_integer) will be created
  + base: {
  +     id:     (known after apply)
  +     max:    9
  +     min:    1
  +     result: (known after apply)
  +     seed:   "base"
    }

  # port (random_integer) will be created
  + port: {
  +     id:     (known after apply)
  +     max:    20100
  +     min:    (known after apply)
  +     result: (known after apply)
  +     seed:   "port"
    }

Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 2.

Outputs:
  url: (known after apply)
```

Each change has a header with the address and type, a marker, and the attributes.

| Marker | Header says | Meaning |
|---|---|---|
| `+` | will be created | The resource is not in the state. |
| `~` | will be updated in-place | The provider changes the object and keeps it. Each changed attribute carries `~` too. |
| `-/+` | must be replaced | The provider destroys the object and creates a new one. `# forces replacement` marks the attribute that requires it. |
| `-` | will be destroyed | The resource is in the state and not in the specification, or `--destroy` was given. |
| `<=` | will be read | A data source whose arguments are unknown until apply. |

Other lines:

| Text | Meaning |
|---|---|
| `(known after apply)` | The provider supplies the value. Everything computed from it is unknown too. |
| `(sensitive)` | The attribute is sensitive. |
| `# a was moved to b` | The record of `a` is renamed because of `$from`. The object does not change. |
| `# a was imported from id "..."` | The object was adopted because of `$import`. |
| `Plan: N to add, N to change, N to destroy.` | A replacement counts once under add and once under destroy. |
| `Hint: ...` | A resource looks like one that was renamed without `$from`. |
| `Graph: ...` | How much of the specification the plan reached. See below. |
| `Outputs:` | The outputs, known or `(known after apply)`. |

The `Graph:` line lists these parts, in this order, and leaves out a part that is zero.

| Part | Meaning |
|---|---|
| `planned N of M` | N resources went to a provider out of M in the specification. With `--destroy`: `N resources in state`. |
| `N unchanged and skipped` | Nothing changed for these, so no provider planned them. |
| `N removed from the specification` | The records of resources that the specification does not declare and that the plan deletes. They are not among the `M`. |
| `N refreshed` | The provider reported their real state. |
| `N drifted` | A refresh found a difference. |
| `N within refresh age` | Their refresh was skipped because the record is within its refresh age. |
| `N not read` | The plan did not read them, and their record is older than its refresh age. |
| `N ephemeral opened`, `N functions called`, `N imported`, `N moved` | The count of each. |

### Save a plan

```sh run
pravo plan --out change.plan | tail -n 3
```

```text out
Saved the plan to change.plan. To apply exactly these actions, run:
  pravo apply change.plan
```

`pravo show change.plan` prints it again, and `pravo apply change.plan` applies exactly those actions. See [`pravo show`](show.md) and [`pravo apply`](apply.md).

### Refresh

After an apply, a plan that changes nothing reads nothing, and says so.

```sh run
pravo apply -auto-approve > /dev/null
pravo plan
```

```text out
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 not read.

Outputs:
  url: "http://localhost:20093"
```

The four values of `--refresh` differ in what the provider is asked to read.

| Value | Reads | Reason shown by `pravo why` |
|---|---|---|
| `age` | The resources the plan reaches and the resources they read, when their records are older than their refresh age. A resource that sets its own `$lifecycle: refresh` is read once its record is older than that age, reached or not. | `refreshed because the record is older than its refresh age; no drift`, `refresh skipped: the record is within its refresh age`, or `not read: the plan did not reach it` |
| `expired` | Every resource whose record is older than its refresh age, reached or not. | `refreshed because the record is older than its refresh age; no drift` |
| `false` | Nothing. Every record is trusted. A record with a mark is still planned. | `refresh skipped: -refresh=false was given` |
| `all` | Every resource, whatever its age. | `refreshed because -refresh=all was given; no drift` |

```sh run
pravo plan -refresh=false | grep '^Graph'
pravo plan -refresh=expired | grep '^Graph'
pravo plan -refresh=all | grep '^Graph'
```

```text out
Graph: planned 0 of 2, 2 unchanged and skipped.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 refreshed.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 refreshed.
```

Write the value after `=`. `-refresh false` is read as `-refresh` followed by the word `false`, and means `all`. Any other value is an error: `invalid -refresh value "soon": want age, expired, false or all`.

The refresh age of a resource comes from `$lifecycle: refresh` or from `$settings: refresh`. The default is `always`, an age of zero, so `age` reads every resource that the plan looks at, and `expired` reads every resource. See [Settings](spec-settings.md) and [Meta fields](spec-meta-fields.md).

A plan with `age` does not read a resource that it neither reaches nor needs as an input of one it reaches, so `--detailed-exitcode` exits 0 for a plan that found no change among what it looked at, and does not detect drift in the rest. The `Graph:` line counts them as `not read` or `within refresh age`, and a plan that has `not read` records ends with `No changes. The specification matches the state.` and the number of records it did not read. [`pravo drift`](why-and-drift.md) reads them. `--refresh=expired` is the plan that reads every record past its age; a job that runs it, or `pravo drift --due`, finds drift in the resources that other plans do not read. [Plans](../concepts/plans.md#refresh-age) describes which records each value reads.

### Replace a resource

```sh run
pravo plan --replace base | grep -E '^(Plan|Graph|  #)'
```

```text out
  # base (random_integer) must be replaced
  # port (random_integer) must be replaced
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 of 2, 2 refreshed.
```

`port` is replaced too, because its `min` reads `base.result`, which is unknown after the replacement.

### Change the specification

Narrow `base` to `max: 8`.

```cue file=main.cue hl="10"
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 8, seed: "base"}

port: $random.#Resource.random_integer & {
	min:  20000 + base.result
	max:  20100
	seed: "port"
}

url: pravo.#Output & {value: "http://localhost:\(port.result)"}
```

```sh run
pravo plan
```

```text out
base: Refreshing state... [id=9]
port: Refreshing state... [id=20093]

pravo will perform the following actions:

  # base (random_integer) must be replaced
-/+ base: {
  ~     id:      "9" -> (known after apply)
  ~     max:     9 -> 8 # forces replacement
        min:     1
  ~     result:  9 -> (known after apply)
        seed:    "base"
    }

  # port (random_integer) must be replaced
-/+ port: {
  ~     id:      "20093" -> (known after apply)
        max:     20100
  ~     min:     20009 -> (known after apply) # forces replacement
  ~     result:  20093 -> (known after apply)
        seed:    "port"
    }

Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 of 2, 2 refreshed.

Outputs:
  url: (known after apply)
```

`# forces replacement` marks `max` on `base` and `min` on `port`. The `Graph:` line shows 2 of 2 planned.

### Exit code 2

```sh run
pravo plan --detailed-exitcode > /dev/null
echo "exit $?"
```

```text out
exit 2
```

### Reserve nodes

`--reserve` claims the nodes that have pending changes for the actor named by `--actor`. Another actor's plan or apply of those nodes is refused with exit code 4 until the reservation expires or `pravo state unlock` releases it.

```sh run
pravo plan --reserve=10m --actor alice | tail -n 1
```

```text out
Reserved 2 nodes until <time>.
```

```sh run exit=4
pravo plan --actor bob
```

```text out
base: Refreshing state... [id=9]
port: Refreshing state... [id=20093]

Error: plan rejected: base is reserved by alice (pravo plan --reserve=10m --actor alice until <time>, since <time>)
```

```sh run
pravo state locks
pravo state unlock base
pravo state unlock port
```

```text out
base  reserved by alice until <time>: pravo plan --reserve=10m --actor alice
port  reserved by alice until <time>: pravo plan --reserve=10m --actor alice
Unlocked base
Unlocked port
```

A reservation is coordination. It does not protect the state from anyone who writes it. See [`pravo state locks`](state.md#locks-and-reservations).

### Policy

A policy is one CUE file that the plan document must satisfy. Keep it outside the directory of the specification.

```cue file=policy/keep.cue
summary: destroy: 0
```

```sh run exit=5
pravo plan --destroy --policy policy/keep.cue
```

```text out
base: Refreshing state... [id=9]
port: Refreshing state... [id=20093]
...
Plan: 0 to add, 0 to change, 2 to destroy.
Graph: 2 resources in state, 2 refreshed.

Error: the plan violates the policy policy/keep.cue (sha256:238d99a0ac31):
summary.destroy: conflicting values 2 and 0:
    plan:1:<col>
    policy/keep.cue:1:19
```

The plan is printed first and then judged. A refused plan exits with code 5, and `--out` and `--store` write nothing. When every policy holds, one line follows the outputs: `Policy policy/keep.cue (sha256:...) is satisfied.` The 12 hex digits identify the bytes of the file.

`pravo plan --json` prints the document that a policy is judged against. [Policies](policies-overview.md) describes what a policy can say and what it does not bind.

### JSON

```sh run
pravo plan --json 2>/dev/null | grep -A6 '"summary"'
```

```text out
  "summary": {
    "add": 2,
    "change": 0,
    "destroy": 2,
    "read": 0,
    "blast_radius": 2
  },
```

The fields are listed in [The plan document](plan-document.md).

### Store a plan

`--store` writes the plan and its summary into the state, below `plans/`, and prints the id of the plan. Anyone who can read the state can then list, show and apply it. The plan is printed and judged first, as for any plan. A plan that a policy refuses is not stored. [State format](state-format.md#plans) describes the objects.

A plan has changes when it creates, updates, replaces or destroys a resource. A plan that only reads data sources, moves or imports records or writes outputs has nothing to store. pravo says `Nothing to store: the plan has no changes.` and stores nothing, and `pravo apply` applies the plan directly.

With `--out FILE`, the file is written before the plan is stored, with or without `--json`. A plan that cannot be saved to `FILE` is not stored.

The specification has pending changes. Alice stores the plan with a reason. `--actor` names the author that a listing shows. It is a label that nobody verifies. Without `--actor`, the author is the operating system user.

```sh run
pravo plan --store --actor alice --reason "narrow base" | tail -n 2
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
```

An unapplied plan is kept for seven days. `--ttl` takes a number and a unit: `90m`, `36h` or `7d`. Bob stores a plan for the same resources and keeps it for 36 hours. With `--json`, `--store` still prints one document: the plan document and a field `stored`.

```sh run
pravo plan --store --json --ttl 36h --actor bob 2>/dev/null | jq '.stored'
```

```text out
{
  "id": "plan-<id>",
  "expires": "<time>",
  "apply_command": "pravo apply plan-<id>",
  "overlaps": [
    {
      "id": "plan-<id>",
      "author": "alice",
      "addrs": [
        "base",
        "port"
      ]
    }
  ]
}
```

| Field of `stored` | Meaning |
|---|---|
| `id` | The id of the plan. |
| `expires` | When an unapplied plan is collected. |
| `apply_command` | The command that applies the plan, with the `-t` and `-T` that it needs. |
| `overlaps` | The open plans that change a resource of this plan: `id`, `author` and the addresses, `addrs`. Absent when there are none. |
| `removed` | The plans that the small collection of `--store` removed: `id`, `author` and `reason`. Absent when there are none. |

A plan that is not stored has no `stored` field.

### Overlapping plans

pravo compares a plan with the plans that can still be applied, and warns when they change the same resources. The warning names each resource once, and leaves out a change that the other plan has applied already. The warning informs and blocks nothing. A plan that is not stored warns too. `plan list --touching` counts a change in the same way.

```sh run
pravo plan --actor carol | tail -n 2
```

```text out
Warning: plan-<id> by bob (just now) also changes 2 of these resources: base, port
Warning: plan-<id> by alice (just now) also changes 2 of these resources: base, port
```

### List stored plans

```sh run
pravo plan list
```

```text out
plan-<id>  pending  bob    just now  +2 ~0 -2  expires in 1 day 12 hours
plan-<id>  pending  alice  just now  +2 ~0 -2  expires in 7 days
```

The columns are the id, the status, the author, the age, the counts of resources to add, change and destroy, and a note. Each column is as wide as its longest value. A length of time is written in up to two units, rounded to the minute: `7 days`, `1 day 12 hours`, `1 hour 30 minutes`. The note says when an open plan expires, why a stale plan is stale, when an applied plan was applied and, for a partial plan, how many changes were applied. That count is of the changes that the plan lists. A plan whose summary or progress object cannot be read is not listed. A listing shows the plans that can be applied. `--all` adds the others, until a collection removes them. `--touching ADDR` keeps the plans that change one resource, and `--json` prints `{"plans": [...]}` with the same fields and the changes of each plan.

| Status | Meaning |
|---|---|
| `pending` | The plan can be applied. |
| `partial` | `pravo apply ID --only ADDR` applied some of the changes. The rest can be applied. |
| `stale` | The plan cannot be applied. The state has another lineage, or a record that a change not applied yet writes or reads has another revision or generation. One such record makes the whole plan stale, though `pravo apply ID --only ADDR` could still apply a part that does not use it. |
| `applied` | Every change was applied. |
| `expired` | The time to live ended before the plan was applied. |

A listing judges a plan by the state alone. The configuration and the providers are compared when the plan is shown or applied, because they belong to a checkout.

```sh run
pravo plan list --touching port --json | jq '.plans | length'
pravo plan list --touching nothing
```

```text out
2
No stored plan changes nothing.
```

### Show and pull

`pravo plan show ID` and `pravo show ID` print a stored plan after a header. [`pravo show`](show.md#show-a-stored-plan) describes it. An id is `plan-` and eight hexadecimal digits, and the first six digits are enough when no other plan starts with them.

`pravo plan pull ID FILE` writes the plan to a plan file, with the changes that were applied already marked. Applying the file records its progress in the file and not in the state.

```sh run
id=$(pravo plan list --json | jq -r '.plans[] | select(.author == "bob") | .id')
pravo plan show $(echo $id | cut -c1-11) | head -n 1
pravo plan pull $id bob.plan
pravo show bob.plan | head -n 1
```

```text out
Stored plan plan-<id> by bob, just now (pending)
Wrote plan-<id> to bob.plan. To apply exactly these actions, run:
  pravo apply bob.plan
Saved plan created <time> by "pravo plan --store --json --ttl 36h --actor bob" (actor bob)
```

An id that no plan has is an error with exit code 1.

```sh run exit=1
pravo plan show plan-000000
```

```text out
Error: no such stored plan: plan-000000 (see: pravo plan list --all)
```

### Collect stored plans

`pravo plan gc` removes the plans that nobody needs. `pravo plan --store` runs the same rules on its way, removes at most 25 plans and says how many.

| A plan is removed when | Rule |
|---|---|
| It expires | An unapplied plan, seven days after it was made, or when `--ttl` says. |
| It is stale | The first collection that finds it stale marks it. A collection that runs more than one hour after the mark removes it. A plan that is not stale any more loses the mark. The whole plan is stale when one change that is not applied yet expects a record that moved, so a plan whose other parts could still be applied is marked and removed too. |
| It was applied completely | 24 hours after the last change was applied. |
| `--older-than DURATION` is given | It was not applied and was made longer ago than `DURATION`, stale or not. |
| Its objects are not whole | One hour after it was written: the plan was not stored whole. |

A plan is never removed while an apply from it runs (a claim that lapses after 24 hours), while another collection removes it (a claim that lapses after 10 minutes), or while it holds a reservation that has not ended. `plan gc` lists such a plan under `Kept`. Only the objects below `plans/` that have the names of plan objects are removed, and an object only if it is what its name says it is.

A summary that exists and cannot be read, because a newer pravo wrote it, because it is damaged or because the read failed, leaves the plan alone: it is never removed. A progress object that cannot be parsed is made empty by a collection once the plan is older than one hour, so that the plan can be listed and applied again. The small collection of `--store` never removes the plan that it has just stored. A collection lists a plan as removed only when it removed it. `plan gc` in a project whose state does not exist makes no state.

Alice applies her plan by its id. [`pravo apply`](apply.md#apply-a-stored-plan) describes the checks. Bob's plan assumed the state before her apply, so it is stale, and the plan that follows has no changes, so it is not stored.

```sh run
id=$(pravo plan list --json | jq -r '.plans[] | select(.author == "alice") | .id')
pravo apply $id | grep '^Apply complete'
pravo plan --store | tail -n 1
pravo plan list --all
```

```text out
Apply complete! Resources: 2 added, 0 changed, 2 destroyed. [txn <id>]
Nothing to store: the plan has no changes.
plan-<id>  stale    bob    just now  +2 ~0 -2  base changed in state (revision 2, plan expected 1)
plan-<id>  applied  alice  just now  +2 ~0 -2  applied just now
```

`plan gc --dry-run` says what a collection would do and changes nothing, not even a mark. For a stale plan it says that a collection would mark it, and that one which runs an hour after the mark would remove it. `--json` prints the result as a document: `looked`, `removed`, `marked` and `kept`, each plan with `id`, `author` and `reason`.

```sh run
pravo plan gc --dry-run
pravo plan gc --json --dry-run | jq -c '{looked, marked: [.marked[].author]}'
```

```text out
1 plan can no longer be applied; a collection would mark it, and one that runs 1 hour after it would remove it:
  plan-<id>  bob        stale, base changed in state (revision 2, plan expected 1)
{"looked":2,"marked":["bob"]}
```

The first real collection marks the stale plan. It says that a collection that runs an hour later removes it.

```sh run
pravo plan gc
```

```text out
1 plan can no longer be applied; a collection that runs 1 hour later removes it:
  plan-<id>  bob        stale, base changed in state (revision 2, plan expected 1)
```

`--older-than` removes the unapplied plans that were made longer ago than a length of time, without waiting for the hour.

```sh run
sleep 3
pravo plan gc --older-than 2s
```

```text out
Removed 1 stored plan:
  plan-<id>  bob        made more than a moment ago
```

A plan that expired is removed by the next collection, and the command that stores a plan runs one. `--out` writes its file first, and the plan is stored after it.

```sh run
pravo plan --store --replace base --ttl 2s --actor dave > /dev/null
sleep 3
pravo plan --store --replace base --out erin.plan --actor erin | grep -E '^(Saved|Removed)'
```

```text out
Saved the plan to erin.plan. To apply exactly these actions, run:
Removed 1 stored plan that was no longer needed.
```

## See also

- [`pravo apply`](apply.md) applies a plan, from a file or from the state.
- [`pravo show`](show.md) prints a saved plan or a stored plan.
- [Share a plan before you apply it](../how-to/share-a-plan-before-you-apply-it.md) follows two people through a stored plan.
- [`pravo why`](why-and-drift.md) explains a single change.
- [Plans](../concepts/plans.md) explains scoped plans, unknown values and stored plans.
