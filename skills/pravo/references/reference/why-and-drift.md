<!-- Generated from docs/reference/why-and-drift.md by tools/skillgen. Do not edit. -->

# pravo why and pravo drift

Explain why a plan creates, changes, replaces or skips a resource, and read the records that a plan does not read to report what changed outside pravo.

## Quick reference

| | `pravo why` | `pravo drift` |
|---|---|---|
| Synopsis | `pravo why ADDR... [--refresh=age\|expired\|false\|all]` | `pravo drift [--due [--limit N]] [--webhook URL]` |
| Does | Makes a plan and explains, for each address, the action, the reasons and what the change reaches. | Reads the real state of every resource, whatever its refresh age, and reports the ones that changed or vanished. With `--due`, reads only the records that are older than their refresh age. |
| Changes infrastructure | No. | No. |
| Writes | Refreshed records, as `pravo plan` does. | Refreshed records of resources that still exist, and a mark on every record that differs from its object or whose object is gone. |
| Exit code 2 | never | drift found |
| Taught in | [B2, Reading a plan](../learn/b02-reading-a-plan.md) | [B9, Drift](../learn/b09-drift.md) |

### Flags

| Flag | Command | Default | Meaning |
|---|---|---|---|
| `--refresh VALUE` | `why` | `age` | `age`, `expired`, `false` or `all`, with the meaning they have for [`pravo plan`](plan.md#refresh). `why` needs a value: `--refresh` alone is an error. |
| `--due` | `drift` | off | Read only the records that are older than their refresh age, the oldest first, and plan nothing. A record that was never refreshed comes first. |
| `--limit N` | `drift` | none | With `--due`: read at most `N` records. It is an error without `--due`. |
| `--webhook URL` | `drift` | none | POST the report as JSON to `URL`. |
| `-t`, `--tag key=value`, `-T` | both | none | Tags, as for `pravo plan`. |

The [global flags](global-flags.md) apply.

### Exit codes

| Code | Command | Meaning |
|---|---|---|
| 0 | both | `why` explained. `drift` found nothing. |
| 1 | both | An error: a provider failed, or the webhook could not be reached. |
| 2 | `drift` | Drift was found. |
| 4 | both | Rejected: a node is reserved by another actor. |
| 6 | both | The specification does not load or evaluate. |

## Examples

Work in an empty directory named `demo`. `note` reads `base`.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "base \(base.result)\n"
}
```

```sh
pravo init > /dev/null
pravo apply -auto-approve > /dev/null
pravo why base note
```

Output:

```text
base: no-op
  - skipped: nothing the provider saw has changed
  - not read: the plan did not reach it; the record is <age> old, its refresh age is always
note: no-op
  - skipped: nothing the provider saw has changed
  - not read: the plan did not reach it; the record is <age> old, its refresh age is always
```

Each address gets its action and one line per reason. Nothing changed, so both are `no-op`. The first reason says that the plan skipped the resource. The second says that the plan did not reach it, how old its record is and what its refresh age is: the plan did not read it.

### A change

Narrow `base`.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 8, seed: "base"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "base \(base.result)\n"
}
```

```sh
pravo why base note
```

Output:

```text
base: replace
  - the specification changed since the last apply (hash <old> -> <new>)
  - refreshed because the record is older than its refresh age; no drift
  - the provider cannot change max in place; replacement required
  reaches: note
note: replace
  - inputs depend on values known only after apply: base
  - refreshed because the record is older than its refresh age; no drift
  - the provider cannot change content in place; replacement required
```

`reaches` lists the resources that read the changed one. `note` is replaced because its `content` reads `base.result`, which is unknown until `base` is replaced.

The reasons that `why` can print:

| Reason | Meaning |
|---|---|
| `not in the state: a new resource` | The resource is created. |
| `the specification changed since the last apply (hash A -> B)` | The configuration the provider saw differs from the one recorded. |
| `inputs depend on values known only after apply: ADDR` | An attribute reads a value that does not exist yet. |
| `the provider cannot change ATTR in place; replacement required` | `ATTR` forces replacement. |
| `provider version changed (A -> B)`, `schema version changed (N -> M)` | The installed provider is not the one that wrote the record. |
| `provider instance changed (A -> B)` | The resource is built through another provider struct than the one that wrote the record. The plan proposes no change to the object, and the next apply records the key `B`. |
| `-full: every resource is planned`, `replacement requested with -replace` | A flag asked for it. |
| `refreshed because the record is older than its refresh age; no drift` | The provider was read under `-refresh=age` or `-refresh=expired`. |
| `refresh skipped: the record is within its refresh age` | The provider was not read under `-refresh=age`, though the plan reached the resource. |
| `not read: the plan did not reach it; the record is AGE old, its refresh age is AGE` | The plan did not look at the resource and did not read it. |
| `refreshed because a node that the plan reaches reads it; no drift` | The plan read the resource because a resource that it reaches reads it. |
| `refreshed because -refresh=all was given; no drift` | The provider was read under `-refresh=all`. |
| `refresh skipped: -refresh=false was given` | The provider was not read under `-refresh=false`. |
| `refresh found the real state differs from the state (drift)` | The provider was read, and the object differs. |
| `skipped: nothing the provider saw has changed` | No provider planned the resource. |
| `the last apply of it did not finish` | An apply stopped halfway. The record holds the state the provider had reached, and no configuration hash, so the plan changes the resource again. |
| `an earlier read found the real state differs from the state (drift); it is planned until an apply writes the record`, `an earlier read did not find the object; it is planned until an apply writes the record` | A read left a mark on the record. |
| `the non-sensitive configuration of its provider changed`, `an apply (...) stopped while this node was locked; its real state is read again whatever the refresh age`, `the record has no time of a read: an apply could not tell what it did to the node; its real state is read again whatever the refresh age` | The plan read the record whatever its age. See [Plans](../concepts/plans.md#records-a-plan-reads-whatever-their-age). |
| `removed from the specification: the state still records it` | The state holds a record that the specification does not declare. The apply deletes the object. |
| `destroy requested: every resource in the state is deleted` | The plan is a destroy plan: `pravo destroy` or `pravo plan --destroy`. |

An address that the specification does not declare and the state does not hold is answered, not refused. The exit code stays 0.

```sh
pravo why nope
pravo why base --refresh=false
```

Output:

```text
nope: not declared in the specification and not in the state
base: replace
  - the specification changed since the last apply (hash <old> -> <new>)
  - refresh skipped: -refresh=false was given
  - the provider cannot change max in place; replacement required
  reaches: note
```

### A resource that left the specification

A record that the state holds and the specification does not declare is deleted by the next apply. `why` says so. Remove `note` from the specification:

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 8, seed: "base"}
```

```sh
pravo why note
```

Output:

```text
note: delete
  - removed from the specification: the state still records it
```

`why` plans without `--destroy`. The reason of a deletion in a destroy plan is in the plan document:

```sh
pravo plan --destroy --json 2>/dev/null | jq -r '.changes[] | .addr + ": " + .reasons[0]'
```

Output:

```text
note: destroy requested: every resource in the state is deleted
base: destroy requested: every resource in the state is deleted
```

Put `note` back before the next step.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 8, seed: "base"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "base \(base.result)\n"
}
```

### JSON

With `--json`, the answer is an array with one object per address: `addr`, `action`, `reasons`, `dependents` and `found`.

```sh
pravo why note --json | grep -E '"(addr|action|found)"'
```

Output:

```text
    "addr": "note",
    "action": "replace",
    "found": true
```

### Drift

Restore the specification, so that no change is pending, and apply it.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "base \(base.result)\n"
}
```

```sh
pravo apply -auto-approve > /dev/null
pravo drift
```

Output:

```text
No drift. 2 resources refreshed; 0 pending changes from the specification.
```

Remove the file outside pravo, and ask again.

```sh
rm note.txt
pravo drift
```

Output:

```text
Drift. 1 of 2 resources changed outside pravo:
  note: create
    - refresh found the real state differs from the state (drift)
```

The action is the one a plan reports. A resource that vanished shows `create`. `drift` writes what it found as a mark on the record, so `pravo plan` shows the same change without reading the object again, a saved plan and `pravo apply` act on it too, and `pravo apply` restores the file. The mark stays until an apply writes the resource.

```sh
pravo drift --json | grep -E '"(addr|action|gone|pending_changes)"'
```

Output:

```text
      "addr": "note",
      "action": "create",
      "gone": true
  "pending_changes": 1,
```

`pravo drift --due` reads only the records that are older than their refresh age. With the age `always`, every record is. It plans nothing, and it exits 2 when it finds drift.

```sh
pravo drift --due
```

Output:

```text
Drift. 1 of 2 records changed outside pravo. 2 of 2 records read.
  note: the object does not exist
```

A provider that a due record uses is set up as it is for a plan. Its configuration may read the environment (`pravo.#Env`, `pravo.#SecretEnv`), the outputs of another root, an ephemeral resource, a function call, or a resource or data source of the state. An ephemeral resource that the configuration needs is opened before the reads and closed after them. A provider that is set up from a resource or data source that has no record cannot be configured: the run fails with `provider NAME is set up from ADDR, which the state does not hold`.

`--limit N` reads at most `N` records, the oldest first, and the report says how many more are older than their refresh age. A job that runs `pravo drift --due --limit N` on a schedule reads the estate in turns, and each run costs what it reads. [CI](../how-to/ci.md) shows the job. With `--json`, the report of `--due` has `time`, `root`, `actor`, `records`, `due`, `read` and `drifted`: the number of records, the number that are due, the addresses that were read and the ones that differ.

The report of a plain `drift` has `time`, `root`, `actor`, `drifted`, `pending_changes` and `summary`. An item has `addr`, `action`, `reasons` and, for a resource that has vanished, `gone: true`. With `--webhook`, the same document is sent as a POST with the content type `application/json`:

```sh
pravo drift --actor drift-cron --webhook https://ops.example/hooks/pravo
```

The webhook is called before the report is printed. A refused connection, or an answer with a status of 300 or more, ends the command with exit code 1 and no report. A scheduler runs `drift` and hands the report to whoever triages it.

## See also

- [`pravo plan`](plan.md) prints the whole plan.
- [Plans](../concepts/plans.md) explains refresh ages and scoped plans.
- [`pravo state`](state.md) shows the records that a refresh writes.
