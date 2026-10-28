<!-- Generated from docs/reference/plan.md by tools/skillgen. Do not edit. -->

# pravo plan

Show the actions that apply takes. A plan changes no infrastructure, and it can be saved, stored in the state, judged by a policy and applied later.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo plan [flags]` |
| | `pravo plan --store [--ttl DURATION] [flags]` |
| | `pravo plan list [--pending\|--all] [--touching ADDR]` |
| | `pravo plan show ID`, `pravo plan pull ID FILE`, `pravo plan approve ID` |
| | `pravo plan gc [--dry-run] [--older-than DURATION]` |
| Changes infrastructure | No. |
| Writes | Refreshed records to the state, a mark on a record whose object differs or is gone, and with `--reserve` a reservation. No record for a `$from` move or an `$import`: it lists them, and the apply writes them. With `--store`, the objects of a stored plan, and the removal of a few that nobody needs. Data sources and ephemeral resources run while the plan is made. |
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
| `--detailed-exitcode` | off | Exit with code 2 when the plan has changes. A new, changed or removed policy counts as a change. |
| `--include-skipped` | off | With `--json`, list the nodes the plan left alone too, as entries with their address, mode, type and name, the action `no-op` and `skipped`. See [The plan document](plan-document.md). |
| `--reserve DURATION` | none | Reserve the nodes that have pending changes for `DURATION`, for example `15m`. |
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
| `--pending` | `list` | List the plans that can still be applied, all of them or a part: `pending`, `partial` and `partly-stale`. This is the default. |
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
| 5 | A policy refused the plan, the root declares a policy and the state is not sealed, or a CUE module does not match its lock. |
| 6 | The specification does not load or evaluate. |

## Examples

Work in an empty directory named `demo`. The specification has two resources, and `port` reads `base`.

File `main.cue`:

```cue
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

```sh
pravo init > /dev/null
pravo plan
```

Output:

```text
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
Graph: planned 2

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
| `# a will be moved to b` | The record of `a` is renamed, because of `$from`, when the plan is applied. The object does not change. |
| `# a will be imported from id "..."` | The object that `$import` names is read, and the apply adopts it. With an identity in `$import` the header says `from the identity "..."`, followed by the attributes as `name=value`. |
| `Plan: N to add, N to change, N to destroy.` | A replacement counts once under add and once under destroy. `, N to import` and `, N to move` follow when the plan has `$import` objects and `$from` records for the apply to write. |
| `Hint: ...` | A resource looks like one that was renamed without `$from`. |
| `Graph: ...` | How much of the specification the plan reached. See below. |
| `Outputs:` | The outputs that the plan changes, as `old → new` or just the new value when none is recorded, and `(known after apply)` for a value that is not known yet. Sensitive outputs show `(sensitive)`. The others are counted. |

The `Graph:` line lists these parts, in this order, separated by `·`, and leaves out a part that is zero.

| Part | Meaning |
|---|---|
| `planned N` | N resources went to a provider. A data source is read, not planned, and counts in `refreshed`. With `--destroy`: `N resources in state`. |
| `skipped N` | Nothing changed for these, so no provider planned them. |
| `removed N` | The records of resources that the specification does not declare and that the plan deletes. |
| `refreshed N` | The provider reported their real state. |
| `drifted N` | A refresh found a difference. |
| `within refresh age N` | Their refresh was skipped because the record is within its refresh age. |
| `not read N` | The plan did not read them, and their record is older than its refresh age. |
| `opened N` (ephemeral resources), `called N` (functions), `to import N`, `to move N` | The count of each. The last two are written by the apply. |

### Save a plan

```sh
pravo plan --out change.plan | tail -n 3
```

Output:

```text
Saved the plan to change.plan. To apply exactly these actions, run:
  pravo apply change.plan
```

`pravo show change.plan` prints it again, and `pravo apply change.plan` applies exactly those actions. See [`pravo show`](show.md) and [`pravo apply`](apply.md).

### Refresh

After an apply, a plan that changes nothing reads nothing, and says so.

```sh
pravo apply -auto-approve > /dev/null
pravo plan
```

Output:

```text
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2

Outputs: 1 output unchanged.
```

The four values of `--refresh` differ in what the provider is asked to read.

| Value | Reads | Reason shown by `pravo why` |
|---|---|---|
| `age` | The resources the plan reaches and the resources they read, when their records are older than their refresh age. A resource that sets its own `$lifecycle: refresh` is read once its record is older than that age, reached or not. | `refreshed because the record is older than its refresh age; no drift`, `refresh skipped: the record is within its refresh age`, or `not read: the plan did not reach it` |
| `expired` | Every resource whose record is older than its refresh age, reached or not. | `refreshed because the record is older than its refresh age; no drift` |
| `false` | Nothing. Every record is trusted. A record with a mark is still planned. | `refresh skipped: -refresh=false was given` |
| `all` | Every resource, whatever its age. | `refreshed because -refresh=all was given; no drift` |

```sh
pravo plan -refresh=false | grep '^Graph'
pravo plan -refresh=expired | grep '^Graph'
pravo plan -refresh=all | grep '^Graph'
```

Output:

```text
Graph: planned 0 · skipped 2
Graph: planned 0 · skipped 2 · refreshed 2
Graph: planned 0 · skipped 2 · refreshed 2
```

Write the value after `=`. `-refresh false` is read as `-refresh` followed by the word `false`, and means `all`. Any other value is an error: `invalid -refresh value "soon": want age, expired, false or all`.

The refresh age of a resource comes from `$lifecycle: refresh` or from `$settings: refresh`. The default is `always`, an age of zero, so `age` reads every resource that the plan looks at, and `expired` reads every resource. See [Settings](spec-settings.md) and [Meta fields](spec-meta-fields.md).

A plan with `age` does not read a resource that it neither reaches nor needs as an input of one it reaches, so `--detailed-exitcode` exits 0 for a plan that found no change among what it looked at, and does not detect drift in the rest. The `Graph:` line counts them as `not read` or `within refresh age`, and a plan that has `not read` records ends with `No changes. The specification matches the state.` and the number of records it did not read. [`pravo drift`](why-and-drift.md) reads them. `--refresh=expired` is the plan that reads every record past its age; a job that runs it, or `pravo drift --due`, finds drift in the resources that other plans do not read. [Plans](../concepts/plans.md#refresh-age) describes which records each value reads.

### Replace a resource

```sh
pravo plan --replace base | grep -E '^(Plan|Graph|  #)'
```

Output:

```text
  # base (random_integer) must be replaced
  # port (random_integer) must be replaced
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 · refreshed 2
```

`port` is replaced too, because its `min` reads `base.result`, which is unknown after the replacement.

### Change the specification

Narrow `base` to `max: 8`.

File `main.cue`:

```cue
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

```sh
pravo plan
```

Output:

```text
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
Graph: planned 2 · refreshed 2

Outputs:
  url: "http://localhost:20093" → (known after apply)
```

`# forces replacement` marks `max` on `base` and `min` on `port`. The `Graph:` line shows 2 of 2 planned.

### Exit code 2

```sh
pravo plan --detailed-exitcode > /dev/null
echo "exit $?"
```

Output:

```text
exit 2
```

### Reserve nodes

`--reserve` claims the nodes that have pending changes for the actor named by `--actor`. Another actor's plan or apply of those nodes is refused with exit code 4 until the reservation expires or `pravo state unlock` releases it.

```sh
pravo plan --reserve=10m --actor alice | tail -n 1
```

Output:

```text
Reserved 2 nodes until <time>.
```

```sh
pravo plan --actor bob
```

Output:

```text
base: Refreshing state... [id=9]
port: Refreshing state... [id=20093]

Error: plan rejected: base is reserved by alice (pravo plan --reserve=10m --actor alice until <time>, since <time>)
```

```sh
pravo state locks
pravo state unlock base
pravo state unlock port
```

Output:

```text
base  reserved by alice until <time>: pravo plan --reserve=10m --actor alice
port  reserved by alice until <time>: pravo plan --reserve=10m --actor alice
Unlocked base
Unlocked port
```

A reservation is coordination. It does not protect the state from anyone who writes it. See [`pravo state locks`](state.md#locks-and-reservations).

### Policy

A policy is a `pravo.#Policy` node of the specification. The plan prints each new, changed or removed policy as `Policies: +N new, ~N changed, -N removed.` followed by one line for each. A plan that carries only policy entries counts as something to apply.

When a plan creates, updates, replaces or deletes a resource, it is judged by the policies in force in the state, after it is printed. A refused plan exits with code 5, and `--out` and `--store` write nothing. When every policy holds, one line follows the outputs: `Policies: 2 satisfied.` The versions that judged, with their digests, are in the stored plan and in `--json`. A new policy, and a change to a policy, are not judged. See [Policies](policies-overview.md) for what a policy is, [Operating policies](policies-operations.md) for where each command judges, and [Writing policies](policies-writing.md) for a run.

`pravo plan --json` prints the document with the fields that the judged document leaves out: see [The plan document in a policy](policies-plan-document.md#what-the-judged-document-leaves-out).

### JSON

```sh
pravo plan --json 2>/dev/null | grep -A6 '"summary"'
```

Output:

```text
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

```sh
pravo plan --store --actor alice --reason "narrow base" | tail -n 2
```

Output:

```text
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
```

An unapplied plan is kept for seven days. `--ttl` takes a number and a unit: `90m`, `36h` or `7d`. Bob stores a plan for the same resources and keeps it for 36 hours. With `--json`, `--store` still prints one document: the plan document and a field `stored`.

```sh
pravo plan --store --json --ttl 36h --actor bob 2>/dev/null | jq '.stored'
```

Output:

```text
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

pravo compares a plan with the plans that can still be applied, and warns when they change the same resources. The warning names each resource once, and leaves out a change that the other plan has applied already and a change that a partly stale plan cannot apply. A move changes the address that it takes the record from, so a plan that changes that address overlaps with it. A plan that only reads a record is made stale by the plan that changes it, and the warning says so in each direction: `changes X, which this plan reads` and `reads X, which this plan changes`. The warning informs and blocks nothing. A plan that is not stored warns too. `plan list` ends the note of a plan that holds a reservation with `reserved until HH:MM`. `plan list --touching` counts a change in the same way.

```sh
pravo plan --actor carol | tail -n 2
```

Output:

```text
Warning: plan-<id> by bob (just now) also changes 2 of these resources: base, port
Warning: plan-<id> by alice (just now) also changes 2 of these resources: base, port
```

### List stored plans

```sh
pravo plan list
```

Output:

```text
plan-<id>  pending  bob    just now  +2 ~0 -2  expires in 1 day 12 hours
plan-<id>  pending  alice  just now  +2 ~0 -2  expires in 7 days
```

The columns are the id, the status, the author, the age, the counts of resources to add, change and destroy, and a note. A plan that changes policies adds `, N policies` to its counts, so that a plan that changes nothing else is not read as one that does nothing, and `--json` lists them as `policies`. Each column is as wide as its longest value. A length of time is written in up to two units, rounded to the minute: `7 days`, `1 day 12 hours`, `1 hour 30 minutes`. The note says when an open plan expires, why a stale plan is stale, when an applied plan was applied, for a partial plan how many changes were applied, and for a partly stale plan which changes conflict and the `--only` set that applies the others. That count is of the changes that the plan lists. A plan whose summary or progress object cannot be read is not listed, and neither is a plan file with no summary whose digest is not its id: the listing warns of each of them. A listing shows the plans that can be applied. `--all` adds the others, until a collection removes them. `--touching ADDR` keeps the plans that change one resource, and `--json` prints `{"plans": [...]}` with the same fields and the changes of each plan.

| Status | Meaning |
|---|---|
| `pending` | The plan can be applied. |
| `partial` | Some of the changes were applied, by `pravo apply ID --only ADDR` or by an apply that failed after it finished them. The rest can be applied. |
| `partly-stale` | A record that some changes not applied yet write has another revision or generation, and the other changes can still be applied. `pravo apply ID` is refused with exit code 3. `pravo apply ID --only ADDR,...` with the live set that the listing prints applies the others. |
| `stale` | No part of the plan can be applied. The state has another lineage, a record that the plan only reads moved, or every change not applied yet conflicts. |
| `applied` | Every change was applied. |
| `expired` | The time to live ended before the plan was applied. |

A listing judges a plan by the state alone. The configuration and the providers are compared when the plan is shown or applied, because they belong to a checkout.

A plan is judged change by change. A change conflicts when the record that it writes has another revision or generation than the plan expected, and a change conflicts when it has to be applied with one that does. A change that is applied is left out of the judgement. The changes that do not conflict are the live part, with the changes that they need. A plan with conflicts and a live part is `partly-stale`. A plan with no live part is `stale`. Two things make the whole plan stale: another lineage of the state, and a moved record that the plan reads and does not write. A summary without a `needs` field says nothing about which changes depend on which, so a record that moved makes such a plan stale as a whole. [State format](state-format.md#the-summary) describes `needs`.

With `--json`, a plan has these fields besides those of every plan.

| Field | Meaning |
|---|---|
| `stale_reasons` | Why a `stale` plan is stale. Absent for a `partly-stale` plan. |
| `conflicts` | For a `partly-stale` plan: each address that cannot be applied, with its reasons. A change that waits for a conflict has the reason `x must be applied with y, which cannot be applied`. |
| `apply_only` | For a `partly-stale` plan: the live addresses, sorted. The set is closed, so `--only` accepts it. |
| `apply_only_command` | For a `partly-stale` plan: the command that applies the live part, with the `-t` and `-T` of the plan. |

The root `moved` has three files. Alice stores a plan that replaces them, and another command replaces `a`. The listing names the conflict and the flag that applies the others.

File `moved/main.cue`:

```cue
package moved

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

a: $local.#Resource.local_file & {filename: "a.txt", content: "a"}
b: $local.#Resource.local_file & {filename: "b.txt", content: "b"}
c: $local.#Resource.local_file & {filename: "c.txt", content: "c"}
```

```sh
pravo -C moved init > /dev/null
pravo -C moved apply --auto-approve > /dev/null
pravo -C moved plan --store --replace a,b,c --actor alice > /dev/null
pravo -C moved apply --auto-approve --replace a > /dev/null
pravo -C moved plan list
pravo -C moved plan list --json | jq -c '.plans[] | {status, stale_reasons, conflicts, apply_only, apply_only_command}'
```

Output:

```text
plan-<id>  partly-stale  alice  just now  +3 ~0 -3  1 of 3 unapplied changes conflict: a changed in state (revision 2, plan expected 1); apply with --only b,c
{"status":"partly-stale","stale_reasons":null,"conflicts":{"a":["a changed in state (revision 2, plan expected 1)"]},"apply_only":["b","c"],"apply_only_command":"pravo apply plan-<id> --only b,c"}
```

```sh
pravo plan list --touching port --json | jq '.plans | length'
pravo plan list --touching nothing
```

Output:

```text
2
No stored plan changes nothing.
```

### Show and pull

`pravo plan show ID` and `pravo show ID` print a stored plan after a header. [`pravo show`](show.md#show-a-stored-plan) describes it. An id is `plan-` and eight hexadecimal digits, and the first six digits are enough when no other plan starts with them.

`pravo plan pull ID FILE` writes the plan to a plan file, with the changes that were applied already marked. Applying the file records its progress in the file and not in the state.

```sh
id=$(pravo plan list --json | jq -r '.plans[] | select(.author == "bob") | .id')
pravo plan show $(echo $id | cut -c1-11) | head -n 1
pravo plan pull $id bob.plan
pravo show bob.plan | head -n 1
```

Output:

```text
Stored plan plan-<id> by bob, just now (pending)
Wrote plan-<id> to bob.plan. To apply exactly these actions, run:
  pravo apply bob.plan
Saved plan created <time> by "pravo plan --store --json --ttl 36h --actor bob" (actor bob)
```

An id that no plan has is an error with exit code 1.

```sh
pravo plan show plan-000000
```

Output:

```text
Error: no such stored plan: plan-000000 (see: pravo plan list --all)
```

### Approve a stored plan

`pravo plan approve ID` signs an approval of a stored plan with your identity, in a [sealed state](../concepts/signatures-and-roles.md). A [policy can ask for approvals](policies-operations.md#ask-for-approvals) before a plan is applied; the apply counts the approvals of keys in the readers file whose role suffices, and not the key that stored the plan unless the policy allows it. Give the `-t` and `-T` that the plan was made with. `--json` prints the plan id, the signer and the time.

### Collect stored plans

`pravo plan gc` removes the plans that nobody needs. `pravo plan --store` runs the same rules on its way, removes at most 25 plans and says how many.

| A plan is removed when | Rule |
|---|---|
| It expires | An unapplied plan, seven days after it was made, or when `--ttl` says. |
| It is stale | The first collection that finds it stale marks it. A collection that runs more than one hour after the mark removes it. A plan that is not stale any more loses the mark. |
| It is partly stale | It is neither marked nor removed for this reason. It stays until its time to live ends, and it loses a mark that it carries. When the live part is applied, the plan is stale, and the rule above applies. |
| It was applied completely | 24 hours after the last change was applied. |
| `--older-than DURATION` is given | It was not applied and was made longer ago than `DURATION`, stale or not. |
| Its objects are not whole | One hour after it was written: the plan was not stored whole. |
| Its plan file is not the plan its name says | The plan file has no summary and its digest is not the id (a file that was planted or damaged). The first collection that finds it marks it, with a progress object. A collection that runs more than one hour after the mark removes it. |

A plan is never removed while an apply from it runs (a claim that the apply renews every 30 minutes, and that lapses after 24 hours when nothing renews it), while another collection removes it (a claim that lapses after 10 minutes until the collection commits to the removal, and then keeps every apply out, so that a collection that is slow, or whose clock differs, cannot remove a plan that an apply has begun on), or while it holds a reservation that has not ended. `plan gc` lists such a plan under `Kept`. Only the objects below `plans/` that have the names of plan objects are removed, and an object only if it is what its name says it is.

A summary that exists and cannot be read, because a newer pravo wrote it, because it is damaged or because the read failed, leaves the plan alone: it is never removed. A progress object that cannot be parsed is made empty by a collection once the plan is older than one hour, so that the plan can be listed and applied again. The small collection of `--store` never removes the plan that it has just stored. A collection lists a plan as removed only when it removed it. `plan gc` in a project whose state does not exist makes no state.

Alice applies her plan by its id. [`pravo apply`](apply.md#apply-a-stored-plan) describes the checks. Bob's plan assumed the state before her apply, so it is stale, and the plan that follows has no changes, so it is not stored.

```sh
id=$(pravo plan list --json | jq -r '.plans[] | select(.author == "alice") | .id')
pravo apply $id | grep '^Apply complete'
pravo plan --store | tail -n 1
pravo plan list --all
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 2 destroyed. [txn <id>]
Nothing to store: the plan has no changes.
plan-<id>  stale    bob    just now  +2 ~0 -2  base changed in state (revision 2, plan expected 1)
plan-<id>  applied  alice  just now  +2 ~0 -2  applied just now
```

`plan gc --dry-run` says what a collection would do and changes nothing, not even a mark. For a stale plan it says that a collection would mark it, and that one which runs an hour after the mark would remove it. `--json` prints the result as a document: `looked`, `removed`, `marked` and `kept`, each plan with `id`, `author` and `reason`.

```sh
pravo plan gc --dry-run
pravo plan gc --json --dry-run | jq -c '{looked, marked: [.marked[].author]}'
```

Output:

```text
1 plan can no longer be applied; a collection would mark it, and one that runs 1 hour after it would remove it:
  plan-<id>  bob        stale, base changed in state (revision 2, plan expected 1)
{"looked":2,"marked":["bob"]}
```

The first real collection marks the stale plan. It says that a collection that runs an hour later removes it.

```sh
pravo plan gc
```

Output:

```text
1 plan can no longer be applied; a collection that runs 1 hour later removes it:
  plan-<id>  bob        stale, base changed in state (revision 2, plan expected 1)
```

A partly stale plan is not marked and not removed, however often a collection runs. The plan of `moved` stays until its time to live ends.

```sh
pravo -C moved plan gc
pravo -C moved plan gc --json | jq -c '{looked, removed: (.removed | length), marked: (.marked | length)}'
```

Output:

```text
No stored plan needs to be removed.
{"looked":1,"removed":0,"marked":0}
```

`--older-than` removes the unapplied plans that were made longer ago than a length of time, without waiting for the hour.

```sh
sleep 3
pravo plan gc --older-than 2s
```

Output:

```text
Removed 1 stored plan:
  plan-<id>  bob        made more than a moment ago
```

A plan that expired is removed by the next collection, and the command that stores a plan runs one. `--out` writes its file first, and the plan is stored after it.

```sh
pravo plan --store --replace base --ttl 2s --actor dave > /dev/null
sleep 3
pravo plan --store --replace base --out erin.plan --actor erin | grep -E '^(Saved|Removed)'
```

Output:

```text
Saved the plan to erin.plan. To apply exactly these actions, run:
Removed 1 stored plan that was no longer needed.
```

## See also

- [`pravo apply`](apply.md) applies a plan, from a file or from the state.
- [`pravo show`](show.md) prints a saved plan or a stored plan.
- [Store a plan and apply it later](../how-to/store-a-plan-and-apply-it-later.md) follows two people through a stored plan.
- [`pravo why`](why-and-drift.md) explains a single change.
- [Plans](../concepts/plans.md) explains scoped plans, unknown values and stored plans.
