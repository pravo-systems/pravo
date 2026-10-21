---
title: pravo show
description: Print a saved plan, with the time, the command and the actor that made it, and a warning when it cannot be applied.
order: 5
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'created \d{4}-\d\d-\d\d \d\d:\d\d:\d\d'
    replace: 'created <time>'
  - pattern: '"created": "[^"]+"'
    replace: '"created": "<time>"'
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo show PLANFILE` |
| Changes infrastructure | No. |
| Reads | The plan file, the specification and the state. It starts no provider. |
| Taught in | [B7, Changing live infrastructure](../learn/b07-changing-live-infrastructure.md). |

`show` has no flags of its own. The plan file carries its tags, so `-t` and `-T` do not apply. The [global flags](global-flags.md) apply.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The plan was printed. A stale plan prints a warning and still exits with 0. |
| 1 | The file is missing, is not a plan or was written by a newer pravo. |
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
Warning: this plan can no longer be applied: the configuration changed since the plan was made
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
      "the configuration changed since the plan was made"
    ]
  }
```

The `plan` field is the document that [`pravo plan --json`](plan.md#json) prints. It adds `reservation` when the plan reserved nodes. See [The plan document](plan-document.md).

## See also

- [`pravo plan`](plan.md) with `--out` saves a plan.
- [`pravo apply`](apply.md) applies a saved plan, in whole or in closed parts.
