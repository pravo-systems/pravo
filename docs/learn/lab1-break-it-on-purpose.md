---
title: "Lab 1: break it"
description: Cause ten failures one at a time and read each message. Match each failure to the exit code that a pipeline sees.
order: 51
group: Track B · Labs and project
minutes: 30
needs: B10
learn: Which stage of a run produces which failure? Which exit code does each one give?
done_when: "You have caused a failure with each of the exit codes 1 to 6. You can say which stage produced it."
level: intermediate
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
  - pattern: "<sealed:[0-9a-f]+>"
    replace: "<sealed>"
  - pattern: "[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})"
    replace: "<time>"
---

A pipeline reads exit codes. A person reads messages. This lab produces both, one failure at a time, on a root that works.

Each fault is declared before it runs. A badge says what to expect. `conflict` marks a value that disagrees. `rejected` marks a specification that is refused before any provider starts. Each fault ends with a revert, so the root is whole again before the next one.

## The root that works

Work in an empty directory. The root lives in `root`, and every command names it with `-C`.

```cue file=root/main.cue title="root/main.cue"
package main

import (
	loc "pravo.dev/providers/hashicorp/local"
	rnd "pravo.dev/providers/hashicorp/random"
)

$local:  loc.#Provider
$random: rnd.#Provider

env: *"dev" | "prod" @tag(env)

release: $random.#Resource.random_integer & {min: 1, max: 99, seed: "lab"}

manifest: $local.#Resource.local_file & {
	filename: "manifest-\(env).txt"
	content:  "release \(release.result)\n"
}
```

```sh run
pravo -C root init
pravo -C root apply --auto-approve
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
...
pravo has been initialised. Run "pravo plan" to see what it would do.
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

Every file in `root` that ends in `.cue` is part of one package. The faults below are files that you add to it, so that the revert is deleting the file.

## Before any provider starts

These faults are found while the specification is evaluated. Every command that loads the specification reports the same error with exit code 6.

### Fault 1: two files disagree

A second file sets `min` to a different number. The block carries `status=conflict`.

```cue file=root/fault.cue title="root/fault.cue" status=conflict
package main

release: min: 5
```

```sh run exit=6
pravo -C root validate
```

```text out
Error: evaluating ./root:
release.min: conflicting values 1 and 5:
    ./root/fault.cue:3:15
    ./root/main.cue:13:51
```

Read it in four parts. The path is `release.min`. The values are `1` and `5`. The rule is that two concrete values must be equal. The locations are the two files. See [A3](a03-writing-a-field-twice.md).

### Fault 2: a misspelt field

The next file misspells `filename`. The block carries `status=rejected`.

```cue file=root/fault.cue title="root/fault.cue" status=rejected
package main

manifest: filenam: "x"
```

```sh run exit=6
pravo -C root validate
```

```text out
Error: evaluating ./root:
manifest.filenam: field not allowed:
    ./root/fault.cue:3:11
```

The provider's definition is closed, so the field is refused. The only location is the one you typed. See [A8](a08-definitions-reject-typos.md).

### Fault 3: the wrong type

```cue file=root/fault.cue title="root/fault.cue" status=rejected
package main

release: max: "ninety"
```

```sh run exit=6
pravo -C root validate
```

```text out
Error: evaluating ./root:
release.max: conflicting values "ninety" and 99 (mismatched types string and int):
    ./root/fault.cue:3:15
    ./root/main.cue:13:59
```

The message names both values and says why they cannot meet: `mismatched types string and int`.

### Fault 4: a reference to nothing

```cue file=root/fault.cue title="root/fault.cue" status=rejected
package main

extra: $local.#Resource.local_file & {filename: "extra.txt", content: relase.result}
```

```sh run exit=6
pravo -C root validate
```

```text out
Error: evaluating ./root:
extra.content: reference "relase" not found:
    ./root/fault.cue:3:71
```

`relase` is not a field in scope. The message names the path that holds the reference and the word that failed to resolve. Revert the file.

```sh run
rm root/fault.cue
```

### Fault 5: a tag outside the set

No file changes this time. The `env` tag accepts `"dev"` or `"prod"`.

```sh run exit=6
pravo -C root validate -t env=qa
```

```text out
Error: evaluating ./root:
env: 2 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./root/main.cue:11:7
env: conflicting values "prod" and "qa":
    ./root/main.cue:11:15
```

CUE tries each alternative and lists every one that failed: `qa` is neither `dev` nor `prod`. The same specification run through `plan` gives the same text and the same code.

```sh run exit=6
pravo -C root plan -t env=qa
```

```text out
Error: evaluating ./root:
env: 2 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./root/main.cue:11:7
env: conflicting values "prod" and "qa":
    ./root/main.cue:11:15
```

Every command that loads the specification reports the error in this form, with exit code 6. A job needs no special case for the command that found it.

## After a plan exists

These faults need a plan, a state and a world. The root is valid in every one of them.

:::predict Match exit codes 2, 3, 4 and 5 to a stale plan, a policy refusal, a reserved resource and drift.
Code 6 is used above. Code 1 comes last.

::reveal
A stale plan exits with 3. A reserved resource exits with 4. A plan refused by a policy exits with 5. Drift exits with 2.
:::

### Fault 6: a stale plan

Add a valid resource and save a plan.

```cue file=root/extra.cue title="root/extra.cue"
package main

extra: $local.#Resource.local_file & {
	filename: "extra-\(env).txt"
	content:  "extra \(release.result)\n"
}
```

```sh run
pravo -C root plan --out change.plan
```

```text out
...
  # extra (local_file) will be created
  + extra: {
  +     content:              "extra 51\n"
...
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 2 · refreshed 1 · not read 1

Saved the plan to change.plan. To apply exactly these actions, run:
  pravo apply change.plan
```

Edit the file after the plan was saved. A comment is enough.

```sh run
echo "// edited" >> root/extra.cue
```

```sh run exit=3
pravo -C root apply change.plan
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (extra.cue changed)
```

Exit code 3 is a saved plan that does not match its files. Revert by planning again. This time apply the specification, so that `extra` exists.

```sh run
pravo -C root apply --auto-approve
```

```text out
...
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 2 · refreshed 1 · not read 1
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

### Fault 7: a plan that a policy refuses

A policy version is signed by whoever applies it, so the state must be sealed. Make an identity, list it as a reader, and seal the state.

```sh run
mkdir -p root/.pravo/access
pravo -C root id new --name ada | grep '^  ada ssh' | sed 's/^  //' > root/.pravo/access/readers.txt
pravo -C root protect init --encrypt --yes --allow-local > /dev/null
```

Add a policy to the root and apply it. The policy does not judge the plan that adds it.

```cue file=root/no_delete.cue title="root/no_delete.cue"
package main

import "pravo.dev/pravo"

no_delete: pravo.#Policy & {
	rule: {
		// A plan may create and change. It may not delete.
		changes: [...{
			addr:   string
			mode:   string
			action: string
			if mode == "managed" && action == "delete" {
				error("\(addr): delete is not allowed")
			}
		}]
	}
}
```

```sh run
pravo -C root apply --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
```

Delete `extra.cue`. The specification is valid, and the plan now deletes `extra`.

```sh run
rm root/extra.cue
```

```sh run exit=5
pravo -C root plan
```

```text out
...
  # extra (local_file) will be destroyed
  - extra: {
...
    }

Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 · skipped 2 · removed 1 · refreshed 1 · not read 2

Error: the plan violates the policy no_delete@1 (sha256:a1757b6a34e5):
changes.0: extra: delete is not allowed:
    no_delete:7:3
```

Exit code 5 is a policy. Nothing was applied. Revert by writing the file again.

```cue file=root/extra.cue title="root/extra.cue"
package main

extra: $local.#Resource.local_file & {
	filename: "extra-\(env).txt"
	content:  "extra \(release.result)\n"
}
```

### Fault 8: a resource that someone else reserved

Add one more resource. Ana plans it and reserves it for fifteen minutes. A reservation is a claim in the state, and it needs no running process.

```cue file=root/late.cue title="root/late.cue"
package main

late: $local.#Resource.local_file & {
	filename: "late-\(env).txt"
	content:  "late\n"
}
```

```sh run
pravo -C root plan --reserve 15m --actor ana
```

```text out
...
  # late (local_file) will be created
  + late: {
  +     content:              "late\n"
...
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 3 · not read 3
Reserved 1 node until <time>.
Policy no_delete@1:a1757b6a34e5fdef6f0a38ee1fd2cc1ed043e40432cb209fa571fa3b5ad548fc is satisfied.
```

`pravo state locks` lists the claim.

```sh run
pravo -C root state locks
```

```text out
late  reserved by ana until <time>: pravo -C root plan --reserve 15m --actor <sealed>
```

Bob tries to apply. The actor label is not verified, and it is how reservations tell people apart.

```sh run exit=4
pravo -C root apply --auto-approve --actor bob
```

```text out
...
Error: plan rejected: late is reserved by ana (pravo -C root plan --reserve 15m --actor <sealed> until <time>, since <time>)
```

Exit code 4 is a resource that another actor holds. Ana releases the claim, and Bob applies.

```sh run
pravo -C root state unlock late
pravo -C root apply --auto-approve --actor bob
```

```text out
Unlocked late
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

### Fault 9: a file that someone deleted

Delete a file that pravo wrote, and ask for drift.

```sh run
rm root/manifest-dev.txt
```

```sh run exit=2
pravo -C root drift
```

```text out
Drift. 1 of 4 resources changed outside pravo:
  manifest: create
    - refresh found the real state differs from the state (drift)
```

Exit code 2 is drift. Revert by applying the specification.

```sh run
pravo -C root apply --auto-approve
pravo -C root drift
```

```text out
...
manifest: no longer exists; it will be created again
...
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
No drift. 4 resources refreshed; 0 pending changes from the specification.
```

### Fault 10: an output that was never recorded

The last fault is none of the above. Ask for an output that the state does not hold.

```sh run exit=1
pravo -C root output nosuch
```

```text out
Error: output "nosuch" is not recorded in state (run "pravo apply")
```

Exit code 1 is any failure that has no code of its own. Examples are a missing output, an unknown flag and a provider error. Read the message.

## What you caused

| Exit | Fault | Stage | Command |
|---|---|---|---|
| 1 | 10 | Any other failure. | Every command |
| 6 | 1 to 5 | The specification does not evaluate. | `validate`, `plan`, `apply` and every other command that loads it |
| 2 | 9 | The world differs from the state. | `drift` |
| 3 | 6 | The plan does not match its files. | `apply <planfile>` |
| 4 | 8 | Another actor holds the resource. | `plan`, `apply` |
| 5 | 7 | A policy refused the plan. | `plan`, `apply`, `destroy` |

Faults 1 to 5 stop before any provider starts. Faults 6 to 10 need a plan, a state or a world. The reference lists every exit code: [Exit codes](../reference/exit-codes.md).

Remove what the lab created. The policy refuses a destroy, so remove the policy first. A plan that changes no resource is not judged.

```sh run
rm root/no_delete.cue
pravo -C root apply --auto-approve | tail -n 1
pravo -C root destroy --auto-approve | tail -n 1
```

```text out
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
Destroy complete! Resources: 0 added, 0 changed, 4 destroyed. [txn <txn>]
```

## Recap

- A specification that does not evaluate exits with 6, whichever command loads it. Any other failure exits with 1.
- Codes 2, 3, 4 and 5 tell a pipeline what to do: report drift, plan again, wait or stop.
- Every message names a path, the values, the rule and the places in your files.

:::check
Which exit code tells a job to plan again?
::answer
Exit code 3. The saved plan does not match the files or the state it was made from.

What does exit code 4 mean?
::answer
Another actor holds the resource, by a running command or by a reservation.
:::

[Lab 2](lab2-two-applies-at-once.md) runs two applies against one state.
