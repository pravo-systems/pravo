<!-- Generated from docs/reference/plan.md by tools/skillgen. Do not edit. -->

# pravo plan

Show the actions that apply takes. A plan changes no infrastructure, and it can be saved, judged by a policy and applied later.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo plan [flags]` |
| Changes infrastructure | No. |
| Writes | Refreshed records to the state, a mark on a record whose object differs or is gone, `$from` moves, `$import` adoptions, and with `--reserve` a reservation. Data sources and ephemeral resources run while the plan is made. |
| Prints | The plan on stdout and one progress line per refreshed resource. With `--json`, the plan document on stdout and the progress on stderr. |
| Taught in | [B2, Reading a plan](../learn/b02-reading-a-plan.md) and [B7, Changing live infrastructure](../learn/b07-changing-live-infrastructure.md). |

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--out FILE` | none | Save the plan to `FILE`, to apply later with `pravo apply FILE`. A plan that a policy refuses is not saved. |
| `--refresh=age\|expired\|false\|all` | `age` | When to read real state from the providers. See [Refresh](#refresh). `--refresh` alone means `all`. |
| `--full` | off | Plan every resource with its provider instead of skipping resources that did not change. |
| `--destroy` | off | Plan the destruction of every resource in the state. Refused while a declared resource in the state has [`prevent_destroy`](spec-meta-fields.md#prevent-destroy). |
| `--replace ADDR` | none | Destroy and create `ADDR` again though the specification did not change. Repeatable, or comma-separated. Refused with `--destroy`. |
| `--detailed-exitcode` | off | Exit with code 2 when the plan has changes. |
| `--include-skipped` | off | With `--json`, list the nodes the plan left alone too, as entries with their address, mode, type and name, the action `no-op` and `skipped`. See [The plan document](plan-document.md). |
| `--reserve DURATION` | none | Reserve the nodes that have pending changes for `DURATION`, for example `15m`. |
| `--policy FILE` | none | A CUE file the plan must satisfy. Repeatable. See [Policy](#policy). |
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field. Repeatable. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE into tags written `@tag(name,var=os)`. |

The [global flags](global-flags.md) apply. A single dash works as well: `-refresh=false`. The value of `--refresh` follows `=`. A word after the flag is an argument, and `plan` takes none, so `-refresh false` is an error.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The plan was made. With `--detailed-exitcode`, it has no changes. |
| 1 | An error: a provider failed, `--replace` names a resource that cannot be replaced, an argument was given: `plan` takes none. |
| 2 | With `--detailed-exitcode`: the plan has changes. |
| 4 | Rejected: another actor holds a lock or a reservation on a node. |
| 5 | A policy refused the plan, a `--policy` file cannot be used, or a module does not match its lock. |
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

```sh
pravo plan -refresh=false | grep '^Graph'
pravo plan -refresh=expired | grep '^Graph'
pravo plan -refresh=all | grep '^Graph'
```

Output:

```text
Graph: planned 0 of 2, 2 unchanged and skipped.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 refreshed.
Graph: planned 0 of 2, 2 unchanged and skipped, 2 refreshed.
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
Graph: planned 2 of 2, 2 refreshed.
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
Graph: planned 2 of 2, 2 refreshed.

Outputs:
  url: (known after apply)
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

A policy is one CUE file that the plan document must satisfy. Keep it outside the directory of the specification.

File `policy/keep.cue`:

```cue
summary: destroy: 0
```

```sh
pravo plan --destroy --policy policy/keep.cue
```

Output:

```text
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

The plan is printed first and then judged. A refused plan exits with code 5 and `--out` writes no file. When every policy holds, one line follows the outputs: `Policy policy/keep.cue (sha256:...) is satisfied.` The 12 hex digits identify the bytes of the file.

`pravo plan --json` prints the document that a policy is judged against. [Policies](policies-overview.md) describes what a policy can say and what it does not bind.

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

## See also

- [`pravo apply`](apply.md) applies a plan.
- [`pravo why`](why-and-drift.md) explains a single change.
- [Plans](../concepts/plans.md) explains scoped plans and unknown values.
