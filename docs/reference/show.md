---
title: pravo show
description: Print a saved plan or a plan stored in the state, with the time, the command and the actor that made it, and a warning when it cannot be applied.
order: 5
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'created \d{4}-\d\d-\d\d \d\d:\d\d:\d\d'
    replace: 'created <time>'
  - pattern: '"created": "[^"]+"'
    replace: '"created": "<time>"'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo show PLANFILE` |
| | `pravo show ID [-t KEY=VALUE]...` |
| Changes infrastructure | No. |
| Reads | The plan file or the stored plan, the specification and the state. It starts no provider. |
| Taught in | [B7, Changing live infrastructure](../learn/b07-changing-live-infrastructure.md). |

`show` has no flags of its own besides `-t` and `-T`. A plan file carries its tags, so they do not apply to it. A stored plan is found through the state, which the tags select, so they must be the ones the plan was made with: see [The tags of a stored plan](apply.md#the-tags-of-a-stored-plan). `pravo plan show ID` is the same command. The [global flags](global-flags.md) apply.

`ID` is `plan-` and eight hexadecimal digits, as `pravo plan --store` printed them. The first six digits are enough when no other plan starts with them. A file with that name is a file.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The plan was printed. A stale plan prints a warning and still exits with 0. |
| 1 | The file is missing, is not a plan or was written by a newer pravo. No stored plan has the id, or `-t` and `-T` differ from the ones the stored plan was made with. |
| 6 | The specification does not load or evaluate. |

## Examples

Work in an empty directory named `demo`.

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}
```

Save a plan with an actor and a reason, then print it.

```sh run
pravo init > /dev/null
pravo plan --out change.plan --actor ci --reason "first base" > /dev/null
pravo show change.plan
```

```text out
Saved plan created <time> by "pravo plan --out change.plan --actor ci --reason first base" (actor ci)

pravo will perform the following actions:

  # base (random_integer) will be created
  + base: {
  +     id:     (known after apply)
  +     max:    9
  +     min:    1
  +     result: (known after apply)
  +     seed:   "base"
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 of 1.
```

The first line gives the time, the command line and the actor. The rest is the plan as `pravo plan` printed it. A change that `--only` has applied carries `(already applied in txn ...)`.

### A stale plan

A plan is stale when the specification, a provider version or a state node it touches changed after the plan was made. `show` says so and still prints the plan.

```sh run
echo "// edited" >> main.cue
pravo show change.plan | head -n 2
```

```text out
Warning: this plan can no longer be applied: the configuration changed since the plan was made (main.cue changed)
Saved plan created <time> by "pravo plan --out change.plan --actor ci --reason first base" (actor ci)
```

`pravo apply change.plan` refuses the same plan with exit code 3 and the same reason.

### JSON

With `--json`, the document has three fields: `plan`, `planfile` and `stale`. `stale` is `null` for a plan that can be applied. Otherwise it is an object whose `reasons` field lists why not. The warning goes to stderr.

```sh run
pravo show change.plan --json 2>/dev/null | grep -A4 '"stale"'
```

```text out
  "stale": {
    "reasons": [
      "the configuration changed since the plan was made (main.cue changed)"
    ]
  }
```

The `plan` field is the document that [`pravo plan --json`](plan.md#json) prints. It adds `reservation` when the plan reserved nodes. See [The plan document](plan-document.md).

### Show a stored plan

`pravo show ID` prints a plan that `pravo plan --store` wrote into the state. A header comes first. The plan follows as it does for a plan file.

```sh run
pravo plan --store --actor ci --reason "first base" > /dev/null
id=$(pravo plan list --json | jq -r '.plans[0].id')
pravo show $id | head -n 7
```

```text out
Stored plan plan-<id> by ci, just now (pending)
  reason:  first base
  command: pravo plan --store --actor ci --reason first base
  expires in 7 days
  apply:   pravo apply plan-<id>

pravo will perform the following actions:
```

| Line | Meaning |
|---|---|
| `Stored plan ID by AUTHOR, AGE (STATUS)` | Who made the plan, how long ago, and where it stands: `pending`, `partial`, `stale`, `applied` or `expired`. |
| `reason:` | The `--reason`, when there is one. |
| `command:` | The command line that made the plan. |
| `git:` | The revision of the repository, when the plan was made in a git repository. |
| `expires in ...` | When the plan is collected, in up to two units, such as `1 day 12 hours`. Shown for a plan that can be applied and for a stale one. |
| `N of M changes applied` | Shown after a part of the plan was applied. M is the number of changes that the plan lists, and N counts only those. |
| `reserved until ...` | Shown while a reservation that the plan took has not ended, with the resources it holds. |
| `apply:` | The command that applies the plan. Shown for a plan that can be applied. |

The status comes from the state alone. The warning comes from the comparison with the checkout, its configuration and the providers, which only `show` and `apply` make. A change to a file of the checkout leaves the plan pending in the listing and makes it stale here.

```sh run
echo "// edited again" >> main.cue
id=$(pravo plan list --json | jq -r '.plans[0].id')
pravo show $id | grep '^Warning'
pravo plan list
```

```text out
Warning: this plan can no longer be applied: the configuration changed since the plan was made (main.cue changed)
plan-<id>  pending  ci  just now  +1 ~0 -0  expires in 7 days
```

With `--json`, a stored plan gives the fields `plan_id`, `stored`, `stale` and `plan`. `stored` has the fields of the listing: [`pravo plan list --json`](plan.md#list-stored-plans). `stale` is `null` for a plan that can be applied, and `plan` is the document of the plan.

```sh run
id=$(pravo plan list --json | jq -r '.plans[0].id')
pravo show $id --json 2>/dev/null | jq -c 'keys, .stale.reasons, (.stored | {id, status, author})'
```

```text out
["plan","plan_id","stale","stored"]
["the configuration changed since the plan was made (main.cue changed)"]
{"id":"plan-<id>","status":"pending","author":"ci"}
```

## See also

- [`pravo plan`](plan.md) with `--out` saves a plan, and with `--store` writes it into the state.
- [`pravo apply`](apply.md) applies a saved plan or a stored plan, in whole or in closed parts.
