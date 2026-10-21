---
title: pravo destroy
description: Destroy every resource recorded in the state, dependents first, after a plan and a confirmation.
order: 4
group: Commands
normalize:
  - pattern: 'after [0-9.]+m?s'
    replace: 'after 0s'
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+'
    replace: 'txn <txn>'
  - pattern: 'plan:\d+:\d+'
    replace: 'plan:1:<col>'
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo destroy [flags]` |
| Changes infrastructure | Yes. It deletes every object in the state. |
| Scope | Every node in the state, whether or not the specification still declares it. |
| Refused when | A resource that the specification still declares has `$lifecycle: prevent_destroy: true`. Nothing is changed. |
| Order | Dependents first. A resource is deleted once every resource that depends on it is gone, and independent resources are deleted at the same time. The plan reads the records at the same time as well. |
| Confirmation | `Do you really want to destroy all resources?` Only `yes` is accepted. |
| Preview | `pravo plan --destroy` prints the same plan and changes nothing. |
| Taught in | [Quick start](../how-to/quick-start.md), step 9, and [B3, Apply, outputs, state](../learn/b03-apply-outputs-state.md). |

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--auto-approve` | off | Destroy without asking. |
| `--refresh=age\|expired\|false\|all` | `age` | When to read real state before the plan. See [`pravo plan`](plan.md#refresh). |
| `--policy FILE` | none | A CUE file the plan of deletions must satisfy. Repeatable. |
| `-t`, `--tag key=value`, `-T` | none | Tags, as for [`pravo plan`](plan.md). |

`destroy` takes no `--out`, `--only` or `--replace`. To destroy in parts, save the plan with `pravo plan --destroy --out FILE` and apply it with `pravo apply FILE --only ADDR,...`. [Outputs of a partial destroy](apply.md#outputs-of-a-partial-destroy) says which outputs a part removes. The [global flags](global-flags.md) apply.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Everything in the state was destroyed, or the state was empty. |
| 1 | An error: the prompt was answered with anything but `yes`, a provider failed, `--json` without `--auto-approve`, a resource has `prevent_destroy`. |
| 4 | Rejected: a node is locked or reserved by another actor. |
| 5 | A policy refused the plan, or a `--policy` file cannot be used. |
| 6 | The specification does not load or evaluate. |

## Examples

Work in an empty directory named `demo`. `port` reads `base`.

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}

port: $random.#Resource.random_integer & {
	min:  20000 + base.result
	max:  20100
	seed: "port"
}
```

```sh run
pravo init > /dev/null
pravo apply -auto-approve > /dev/null
pravo state list
```

```text out
base
port
```

### Preview

```sh run
pravo plan --destroy
```

```text out
base: Refreshing state... [id=9]
port: Refreshing state... [id=20093]

pravo will perform the following actions:

  # port (random_integer) will be destroyed
  - port: {
  -     id:     "20093"
  -     max:    20100
  -     min:    20009
  -     result: 20093
  -     seed:   "port"
    }

  # base (random_integer) will be destroyed
  - base: {
  -     id:     "9"
  -     max:    9
  -     min:    1
  -     result: 9
  -     seed:   "base"
    }

Plan: 0 to add, 0 to change, 2 to destroy.
Graph: 2 resources in state, 2 refreshed.
```

The `Graph:` line counts the nodes in the state. `port` comes first, because it depends on `base`.

### Refuse deletions with a policy

A plan policy that allows no destroy action refuses the command before anything is deleted. The state is untouched.

```cue file=policy/keep.cue
summary: destroy: 0
```

```sh run exit=5
pravo destroy --policy policy/keep.cue
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

The plan is printed first and judged second. The prompt never appears. Both nodes are still in the state:

```sh run
pravo state list
```

```text out
base
port
```

See [Policies](policies-overview.md).

### Destroy

```sh run
pravo destroy -auto-approve
```

```text out
base: Refreshing state... [id=9]
port: Refreshing state... [id=20093]
...
Plan: 0 to add, 0 to change, 2 to destroy.
Graph: 2 resources in state, 2 refreshed.
port: Destroying... [id=20093]
port: Destruction complete after 0s
base: Destroying... [id=9]
base: Destruction complete after 0s

Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

The state is empty afterwards. The specification and the lock file stay.

```sh run
pravo state list
```

### Protect a resource

`$lifecycle: prevent_destroy: true` on a resource that the specification declares refuses `pravo destroy` and `pravo plan --destroy` while the resource is in the state. Add it to `port`:

```cue file=main.cue hl="10"
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}

port: $random.#Resource.random_integer & {
	$lifecycle: prevent_destroy: true
	min:        20000 + base.result
	max:        20100
	seed:       "port"
}
```

```sh run exit=1
pravo apply -auto-approve > /dev/null
pravo destroy -auto-approve
```

```text out
Error: destroy refused: $lifecycle.prevent_destroy is set on port; remove the setting from the specification to destroy the resource
```

The refusal names each resource that has the setting. It comes before the plan, so nothing is deleted, not even the resources without the setting:

```sh run
pravo state list
```

```text out
base
port
```

The setting also refuses a replacement of the resource, because a replacement deletes it:

```sh run exit=1
pravo plan --replace port
```

```text out
port: Refreshing state... [id=20093]

Error: port: replacement requires destroying the resource, but $lifecycle.prevent_destroy is set
```

A resource that the specification does not declare has no `$lifecycle` to read. Remove `port` from the specification and `pravo destroy` deletes it with `base`:

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}
```

```sh run
pravo destroy -auto-approve | grep '^Destroy complete'
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

To refuse deletions of every kind, name a plan policy with `--policy`, as above.

## See also

- [`pravo plan`](plan.md) with `--destroy` previews the deletions.
- [`pravo state rm`](state.md#mv-and-rm) forgets a node without destroying the object.
- [Meta fields](spec-meta-fields.md) describes `$lifecycle`.
