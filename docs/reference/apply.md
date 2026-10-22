---
title: pravo apply
description: Plan, ask for confirmation and apply, or apply a saved plan or a plan stored in the state exactly as it was reviewed, in whole or in closed parts.
order: 3
group: Commands
normalize:
  - pattern: 'after [0-9.]+m?s'
    replace: 'after 0s'
  - pattern: ',"elapsed_ms":\d+'
    replace: ''
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+'
    replace: 'txn <txn>'
  - pattern: '"txn":"[0-9TZ]+-[0-9a-f]+"'
    replace: '"txn":"<txn>"'
  - pattern: '"time":"[^"]+"'
    replace: '"time":"<time>"'
  - pattern: 'plan:\d+:\d+'
    replace: 'plan:1:<col>'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo apply [flags]` |
| | `pravo apply PLANFILE [--only ADDR,...] [--policy FILE]...` |
| | `pravo apply ID [--only ADDR,...] [--policy FILE]... [-t KEY=VALUE]...` |
| Changes infrastructure | Yes. |
| Confirmation | A plain apply prints the plan and asks `Do you want to perform these actions?`. Only `yes` is accepted. A saved plan, from a file or from the state, asks nothing. |
| Locks | The nodes the plan writes are locked from the start of the apply. An apply whose nodes another process holds is rejected before it changes anything. |
| Taught in | [B3, Apply, outputs, state](../learn/b03-apply-outputs-state.md) and [B7, Changing live infrastructure](../learn/b07-changing-live-infrastructure.md). |

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--auto-approve` | off | Apply without asking. |
| `--refresh=age\|expired\|false\|all` | `age` | As for [`pravo plan`](plan.md#refresh). Refused with a plan file or a plan id. |
| `--full` | off | As for `pravo plan`. Refused with a plan file or a plan id. |
| `--replace ADDR` | none | As for `pravo plan`. Refused with a plan file or a plan id: the replacement is decided when the plan is made. |
| `--only ADDR,...` | none | With a plan file or a plan id: apply these changes now and leave the rest in the file or in the state. Each name must be a change in the plan: a node that the plan left alone has none. The selection must be closed under dependencies. Repeatable, or comma-separated. For a stored plan that is [partly stale](#a-partly-stale-plan), the set that `pravo plan list` and `pravo plan show` print. |
| `--policy FILE` | none | A CUE file the saved plan must satisfy before anything is applied. Repeatable. Without a plan file or a plan id the command is refused. |
| `-t`, `--tag key=value`, `-T` | none | Tags, as for `pravo plan`. Refused with a plan file: the plan carries its own tags. With a plan id they must be the ones the plan was made with: see [The tags of a stored plan](#the-tags-of-a-stored-plan). |

### Reads after the apply

When an apply has written its nodes, it reads each of them once more from its provider and records what the provider returns, with no drift mark. A resource that is made later in the same run can change one that was made earlier: a policy attached to a role changes the role, and a queue policy changes the queue. Without the read, the next one finds the records out of date and reports drift that no one caused.

The cost is one read for each node that the apply wrote, and none for the nodes that it only read, so it follows the change and not the size of the state. A read that fails, finds no object or takes more than 30 seconds leaves the record as the apply wrote it, and the apply still succeeds.

`ID` is `plan-` and eight hexadecimal digits, as `pravo plan --store` printed them. The first six digits are enough when no other plan starts with them. A file with that name is a file.

The [global flags](global-flags.md) apply. With `--json`, a plain apply needs `--auto-approve`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Applied, or the saved plan has nothing left to apply. |
| 1 | An error: the prompt was answered with anything but `yes`, a provider failed, the apply was interrupted, a flag does not combine with the form, `--only` names a node that has no change in the plan file, `--json` without `--auto-approve`, an id that no stored plan has, `-t` and `-T` that differ from the ones a stored plan was made with, or a stored plan that cannot be claimed for a reason other than a collection. |
| 3 | The saved plan is stale, or an apply found that a resource is not what the plan said: a node that another command wrote since, or a provider that plans the resource differently. The reasons follow the error. A stored plan whose time to live ended before it was applied, or that a collection is removing, is refused with this code, and so is a stored plan that is [partly stale](#a-partly-stale-plan) when the apply includes a change that conflicts, as the whole plan does. |
| 4 | Rejected: a node is locked or reserved by another actor, before anything changes, or a lock was taken over while the apply ran (`lock lost`). |
| 5 | A policy refused the plan, a `--policy` file cannot be used, `--policy` was given without a plan file, or a module does not match its lock. |
| 6 | The specification does not load or evaluate. |

## Examples

Work in an empty directory named `demo`. The specification has two resources, and `port` reads `base`.

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

### Apply with a prompt

```sh run
pravo init > /dev/null
echo yes | pravo apply
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

Do you want to perform these actions?
  Only 'yes' will be accepted to approve.

  Enter a value:
base: Creating...
base: Creation complete after 0s [id=9]
port: Creating...
port: Creation complete after 0s [id=20093]

Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

The plan comes first, as for `pravo plan`. The progress lines carry the address. The last line counts the actions and names the transaction. `pravo state log` shows it later.

Any answer but `yes` cancels. Nothing changes.

### Change the specification

Narrow `base` and add `solo`. The plan replaces `base` and `port`, and creates `solo`.

```cue file=main.cue hl="7,15"
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 8, seed: "base"}

port: $random.#Resource.random_integer & {
	min:  20000 + base.result
	max:  20100
	seed: "port"
}

solo: $random.#Resource.random_integer & {min: 1, max: 99, seed: "solo"}
```

```sh run
echo no | pravo apply > answer.txt
echo "exit $?"
tail -n 3 answer.txt
```

```text out
exit 1
  Enter a value:

Error: apply cancelled
```

### Apply a saved plan

```sh run
pravo plan --out change.plan > /dev/null
```

A saved plan runs without a prompt. Before it applies anything, pravo checks that the specification, the provider versions and every state node the plan touches are as they were when the plan was made. Each resource is planned again by its provider and compared with the saved change. A provider that plans something else at that point stops the apply with exit code 3. A data source that the plan read is not read again. A saved plan whose only change is an output, or the removal of one, is applied too: the apply records it, and a second apply of the file has nothing left to apply.

### Apply a part of a saved plan

`--only` takes a set that is closed under dependencies. `port` reads `base`, so `port` alone is refused. The message names the smallest set that works.

```sh run exit=1
pravo apply change.plan --only port
```

```text out
Error: the selection is not closed under dependencies; it also needs base
  (the smallest closed selection is -only base,port)
```

A name that is not a change in the file is refused. A node that the plan left alone has no entry in the file, so naming it is refused too.

```sh run exit=1
pravo apply change.plan --only absent
```

```text out
Error: absent is not part of the saved plan
```

### Judge a plan before applying it

A policy is judged against a saved plan. A plain apply plans again while it runs, and that second plan is not the one that was judged.

```cue file=policy/small.cue
summary: add: <=1
```

```sh run exit=5
pravo apply --policy policy/small.cue
```

```text out
Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"
```

The saved plan is judged whole, whatever `--only` selects. `solo` adds one resource, and the plan adds three. A limit on the size of a plan cannot be passed by applying it in stages.

```sh run exit=5
pravo apply change.plan --only solo --policy policy/small.cue
```

```text out
Error: the plan violates the policy policy/small.cue (sha256:267ca1f7c1b4):
summary.add: invalid value 3 (out of bound <=1):
    policy/small.cue:1:15
    plan:1:<col>
```

Nothing was applied. See [Policies](policies-overview.md) for what a policy can say.

### Apply the closed part

`solo` depends on nothing. A policy that allows three additions holds for the whole plan, and the command names it before the first change.

```cue file=policy/enough.cue
summary: add: <=3
```

```sh run
pravo apply change.plan --only solo --policy policy/enough.cue
```

```text out
Policy policy/enough.cue (sha256:6a60ce99537a) is satisfied.
Applying 1 of 3 remaining changes from change.plan.
solo: Creating...
solo: Creation complete after 0s [id=93]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
2 changes remain in change.plan; apply with "pravo apply change.plan".
```

The rest stays in the file. `pravo show change.plan` marks the applied change with `(already applied in txn ...)`. Running `pravo apply change.plan` again applies what is left.

Parts of one file may be applied at the same time when they share no node: each apply marks its own changes in the file, which it locks and writes whole, and the file records the marks of all of them. A part that shares a node with one that is running is refused with exit code 4, as any two overlapping applies are. A file that was replaced by another plan while a part was applied is not marked, and the apply reports that.

### Outputs of a part

An output that reads a resource that the selection leaves out, and that does not exist yet, has no value. A partial apply records the outputs that have one and leaves out the others. The apply of the rest records them. The root `outs` has two resources and an output for each:

```cue file=outs/main.cue
package outs

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider

first:  $random.#Resource.random_integer & {min: 1, max: 9, seed: "first"}
second: $random.#Resource.random_integer & {min: 1, max: 9, seed: "second"}

first_value:  pravo.#Output & {value: first.result}
second_value: pravo.#Output & {value: second.result}
```

```sh run
pravo -C outs init > /dev/null
pravo -C outs plan --out part.plan > /dev/null
pravo -C outs apply part.plan --only first
```

```text out
Applying 1 of 2 remaining changes from part.plan.
first: Creating...
first: Creation complete after 0s [id=7]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
1 change remains in part.plan; apply with "pravo apply part.plan".

Outputs:
  first_value: 7
```

The second change is still in the plan file. Applying it records the other output:

```sh run
pravo -C outs apply part.plan
```

```text out
second: Creating...
second: Creation complete after 0s [id=1]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]

Outputs:
  first_value: 7
  second_value: 1
```

### Outputs of a partial destroy

A saved destroy plan, made with `pravo plan --destroy --out`, is applied in parts like any other. A part leaves resources behind, and their outputs stay with them. After a part, an output that reads none of the resources the part destroyed stays as it is recorded. An output that reads one of them is removed. The part that destroys the last resources removes every output that is left, including an output that reads no resource. `pravo destroy` has one part and removes them all.

The root `outs` from above has both resources and both outputs. Save the destroy plan and destroy `second` alone:

```sh run
pravo -C outs plan --destroy --out down.plan > /dev/null
pravo -C outs apply down.plan --only second
pravo -C outs output
```

```text out
Applying 1 of 2 remaining changes from down.plan.
second: Destroying... [id=1]
second: Destruction complete after 0s

Destroy complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
1 change remains in down.plan; apply with "pravo apply down.plan".
first_value: 7
```

`second_value` read `second` and is gone. `first_value` reads `first`, which remains, and keeps its value. The rest of the plan destroys `first` and with it the last output:

```sh run
pravo -C outs apply down.plan | tail -n 1
pravo -C outs output --json
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
{}
```

### A stale plan

Any change to a file that was loaded for the root makes the plan stale. A comment counts.

```sh run exit=3
echo "// edited" >> main.cue
pravo apply change.plan
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (main.cue changed)
```

Plan again. The new plan holds what is left, and applies in one step.

```sh run
pravo plan --out change.plan > /dev/null
pravo apply change.plan
```

```text out
base: Destroying... [id=9]
base: Creating replacement...
base: Replacement complete after 0s [id=3]
port: Destroying... [id=20093]
port: Creating replacement...
port: Replacement complete after 0s [id=20061]

Apply complete! Resources: 2 added, 0 changed, 2 destroyed. [txn <txn>]
```

### Apply a stored plan

`pravo plan --store` writes a plan into the state, and `pravo apply ID` applies it. The checks are those of a plan file: the configuration, the providers, the lineage of the state, and the revision and generation of every record the plan writes or reads. A difference is exit code 3 and changes nothing. A policy named with `--policy` judges the whole plan, and `--only` applies a closed part. `--refresh`, `--full` and `--replace` are refused, as they are with a plan file: the plan decided them. [`pravo plan`](plan.md#store-a-plan) stores a plan, and [Share a plan before you apply it](../how-to/share-a-plan-before-you-apply-it.md) follows two people through it.

The root has no pending change. Store a plan that replaces `solo`, and apply it by its id.

```sh run
pravo plan --store --replace solo --actor alice | tail -n 2
id=$(pravo plan list --json | jq -r '.plans[0].id')
pravo apply $id
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id>
solo: Destroying... [id=93]
solo: Creating replacement...
solo: Replacement complete after 0s [id=93]

Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
```

When the apply ends, it writes the changes it finished and its transaction into the progress object of the plan. The plan object stays as it was written. The plan is `applied` when every change that was pending is applied. A second apply of it has nothing left to apply, also after the time to live of the plan.

```sh run
id=$(pravo plan list --all --json | jq -r '.plans[0].id')
pravo plan list --all
pravo apply $id
```

```text out
plan-<id>  applied  alice  just now  +1 ~0 -1  applied just now
No changes. The saved plan has nothing left to apply.
```

While an apply runs from the plan, the progress object holds a claim of that apply, so that no collection removes the plan. Applies of separate parts, with `--only`, run at the same time. Each holds its own claim and ends only its own, and the plan is `applied` when the last of them has applied what was pending. A claim that a killed apply left does not count after 24 hours. Only a collection that has claimed the plan to remove it makes the apply exit with code 3: the message reads `a collection is removing plan-ID; run "pravo plan --store" again`. Any other failure to claim the plan, such as a progress object that cannot be read or an error of the state, is exit code 1.

A plan that was not applied and whose time to live ended is refused with exit code 3, also when no collection has removed it yet.

```sh run exit=3
pravo plan --store --replace solo --ttl 2s --actor alice > /dev/null
sleep 3
id=$(pravo plan list --all --json | jq -r '.plans[0].id')
pravo apply $id
```

```text out
Error: plan-<id> expired a moment ago; plans are kept for a limited time: run "pravo plan --store" again
```

An apply that fails or is interrupted records the changes it finished. The progress object of a stored plan holds them, and a plan file is marked with them. A change that failed, a change whose result is not known and a change that was never started are not recorded. Before the error, the apply prints how many changes it recorded, where, and the command that applies the rest. The plan lists as `partial`, and the next apply of the plan applies what is left. The root `fail` has three resources. `bad` cannot be created because `blocker` is a file, and it waits for `first`. `after` waits for `bad`.

```cue file=fail/main.cue
package fail

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

first: $local.#Resource.local_file & {filename: "first.txt", content: "first"}
bad:   $local.#Resource.local_file & {filename: "blocker/x.txt", content: "after \(first.id)"}
after: $local.#Resource.local_file & {filename: "after.txt", content: "after \(bad.id)"}
```

```sh run
pravo -C fail init > /dev/null
echo blocker > fail/blocker
pravo -C fail plan --store --actor ci > /dev/null
id=$(pravo -C fail plan list --json | jq -r '.plans[0].id')
pravo -C fail apply $id > apply.txt 2>&1; echo "exit $?"
grep -E '^(1 change|Error)' apply.txt
```

```text out
exit 1
1 change was applied before the error and is recorded in plan-<id>; run "pravo apply plan-<id>" again to apply the rest.
Error: bad: Create local file error: An unexpected error occurred while writing the file
```

`first` was created, and `bad` and `after` were not. The progress object names `first`, and the plan is `partial`. The exit code is that of the error.

```sh run
id=$(pravo -C fail plan list --json | jq -r '.plans[0].id')
pravo -C fail plan list
jq -c '{applied: (.applied | keys), done_at}' fail/pravo.state/plans/$id.progress.json
```

```text out
plan-<id>  partial  ci  just now  +3 ~0 -0  1 of 3 changes applied; expires in 7 days
{"applied":["first"],"done_at":null}
```

Remove the cause and apply the plan again. The apply does what is left. `first` is not created a second time.

```sh run
id=$(pravo -C fail plan list --json | jq -r '.plans[0].id')
rm fail/blocker
pravo -C fail apply $id | tail -n 1
pravo -C fail plan list --all
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
plan-<id>  applied  ci  just now  +3 ~0 -0  applied just now
```

If the changes were applied and the record could not be written, the message says so, and names the transaction. An apply of a plan file marks the changes in the file in the same way, and prints the same note with `pravo apply FILE` as the command. With `--json`, the note goes to stderr, and stdout stays a stream of JSON lines. An apply that is stopped by a third signal exits without recording anything.

A file whose name is an id is a file: `pravo apply plan-123456` applies that file when it exists.

```sh run
pravo plan --replace solo --out plan-123456 > /dev/null
pravo apply plan-123456 | grep '^Apply complete'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
```

With `--json`, the last line is the one that a plan file gives, with the key `plan` and the id in place of `planfile`.

```sh run
pravo plan --store --replace solo --actor alice > /dev/null
id=$(pravo plan list --json | jq -r '.plans[0].id')
pravo apply $id --json 2>/dev/null | tail -n 1
```

```text out
{"added":1,"changed":0,"destroyed":1,"kind":"apply","outputs":{},"phase":"done","plan":"plan-<id>","remaining":0,"txn":"<txn>"}
```

### A partly stale plan

A stored plan is judged change by change. When another command wrote the record of a change that is not applied yet, that change conflicts, and so does every change that has to be applied with it. The other changes are live. A plan with conflicts and a live part is `partly-stale`. [`pravo plan list`](plan.md#list-stored-plans) and [`pravo plan show`](show.md#show-a-stored-plan) name the conflicts and print the `--only` set that applies the live part. The lineage of the state and the records that the plan only reads are not judged change by change: when one of them moved, the whole plan is stale. So is a plan whose configuration or providers differ from the checkout, which `plan show` and `apply` compare.

The root `moved` has three files that do not depend on each other. Create them, store a plan that replaces all three, and let another command replace `a`. The record of `a` is then at revision 2, and the plan expects revision 1.

```cue file=moved/main.cue
package moved

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

a: $local.#Resource.local_file & {filename: "a.txt", content: "a"}
b: $local.#Resource.local_file & {filename: "b.txt", content: "b"}
c: $local.#Resource.local_file & {filename: "c.txt", content: "c"}
```

```sh run
pravo -C moved init > /dev/null
pravo -C moved apply --auto-approve | tail -n 1
pravo -C moved plan --store --replace a,b,c --actor alice > /dev/null
pravo -C moved apply --auto-approve --replace a | tail -n 1
pravo -C moved plan list
```

```text out
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <txn>]
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
plan-<id>  partly-stale  alice  just now  +3 ~0 -3  1 of 3 unapplied changes conflict: a changed in state (revision 2, plan expected 1); apply with --only b,c
```

Applying the whole plan is refused with exit code 3, and nothing changes.

```sh run exit=3
id=$(pravo -C moved plan list --json | jq -r '.plans[0].id')
pravo -C moved apply $id
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - a changed in state (revision 2, plan expected 1)
```

So is a selection that holds the conflict.

```sh run exit=3
id=$(pravo -C moved plan list --json | jq -r '.plans[0].id')
pravo -C moved apply $id --only a,b
```

```text out
Error: saved plan is stale; run "pravo plan" again:
  - a changed in state (revision 2, plan expected 1)
```

The live set applies. The plan keeps the conflict, and it lists as `partly-stale` while a live change is left.

```sh run
id=$(pravo -C moved plan list --json | jq -r '.plans[0].id')
pravo -C moved apply $id --only b | grep '^Apply complete'
pravo -C moved plan list
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
plan-<id>  partly-stale  alice  just now  +3 ~0 -3  1 of 2 unapplied changes conflict: a changed in state (revision 2, plan expected 1); apply with --only c
```

The listing gave the flag that applies the rest. When only the conflict is left, no part of the plan can be applied, and the plan is `stale`. Plan again for `a`.

```sh run
id=$(pravo -C moved plan list --json | jq -r '.plans[0].id')
pravo -C moved apply $id --only c | grep '^Apply complete'
pravo -C moved plan list --all
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
plan-<id>  stale    alice  just now  +3 ~0 -3  a changed in state (revision 2, plan expected 1)
```

### The tags of a stored plan

A plan records the `-t` and `-T` of the command that made it. `apply` and `plan show` need the same, and name the command to run when they differ. The root `tagged` takes a tag.

```cue file=tagged/main.cue
package tagged

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

color: *"web" | string @tag(color)

port: $random.#Resource.random_integer & {min: 1, max: 9, seed: color}
```

```sh run
pravo -C tagged init > /dev/null
pravo -C tagged plan --store -t color=blue | tail -n 2
```

```text out
Stored the plan as plan-<id>; unless it is applied it is removed after 7 days. To apply exactly these actions, run:
  pravo apply plan-<id> -t color=blue
```

```sh run exit=1
id=$(pravo -C tagged plan list --json | jq -r '.plans[0].id')
pravo -C tagged apply $id
```

```text out
Error: plan-<id> was made with other -t and -T than these: run pravo apply plan-<id> -t color=blue
```

```sh run
id=$(pravo -C tagged plan list --json | jq -r '.plans[0].id')
pravo -C tagged apply $id -t color=blue | grep '^Apply complete'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

### Interrupting an apply

A node that fails stops the apply from starting any other node. A node whose provider is already at work finishes, its record is written, and only then are the locks released. The root `flight` has a resource that cannot be written and one that takes a second. `bad` waits for two other resources and then cannot create its file, because `blocker` is a file and nothing can be created below it. `slow` waits for nothing, and its file is a named pipe: opening a named pipe for writing waits until something opens it for reading. The command in the background does that after a second.

```cue file=flight/main.cue
package flight

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

first:  $local.#Resource.local_file & {filename: "first.txt", content: "first"}
second: $local.#Resource.local_file & {filename: "second.txt", content: "after \(first.id)"}
bad:    $local.#Resource.local_file & {filename: "blocker/x.txt", content: "after \(second.id)"}
slow:   $local.#Resource.local_file & {filename: "pipe", content: "slow"}
```

```sh run
pravo -C flight init > /dev/null
echo blocker > flight/blocker
mkfifo flight/pipe
(sleep 1; cat flight/pipe > /dev/null) &
pravo -C flight apply --auto-approve > flight.txt 2>&1
echo "exit $?"
grep -E 'slow: Creation complete|^Error' flight.txt
pravo -C flight state list
```

```text out
exit 1
slow: Creation complete after 0s [id=57e8a7776d6892a83f2a49678f8141f4fb883e62]
Error: bad: Create local file error: An unexpected error occurred while writing the file
first
second
slow
```

`bad` failed while `slow` was at work, and `slow` has a record. A signal stops the apply in the same way, in three stages.

| Signal | What pravo does |
|---|---|
| The first `SIGINT` or `SIGTERM` | No node is started. pravo prints `Interrupted. Waiting for N operations; interrupt again to ask the providers to stop.` and waits for the providers that are at work. Their results are recorded. The apply fails with `interrupted: M of T tasks were not started` when it had nodes left to start. |
| The second | pravo asks every provider to stop what it is doing, and waits up to 30 seconds. A provider that stops returns the state it had reached, and the apply records it. |
| The third, or the end of the 30 seconds | pravo exits without waiting for the providers, with exit code 1. |

The records of the changes that finished, the transaction and the release of the locks are written to the state after a signal as before it, also when the state is in a bucket. An apply of a stored plan or a plan file that a signal stops records the changes that finished in the plan, as [a failed apply](#apply-a-stored-plan) does, except after the third signal. When the third signal comes and the state does not answer in 20 seconds, pravo exits anyway and says so.

A change whose result is not known is named in the error: the call to its provider failed, or the provider answered an update or a destruction with an error and no state, or the provider was asked to stop and returned no state, or pravo exited without waiting. The message reads `creation of NODE was in flight; its result is unknown; check the provider`. The verb is `creation`, `update` or `destruction`. If the node has a record, the apply writes it again without `refreshed_at`: the revision moves, so a saved plan made against the record is stale, and the next plan reads the node whatever its refresh age, also when the plan does not reach it. [`pravo state log`](state.md) lists it as `unsettled`. A creation that has no record has none to mark, and the message is all that is left of it. A creation that the provider refused with an error and no state is not named: the object does not exist.

A change that the provider finished and that the state could not record is named too: `creation of NODE finished, but the state does not record it; check the provider`. The record of the node, if it has one, is marked in the same way. The message follows the error of the write, which names the node.

pravo releases the locks after the records are written, and when it exits without waiting. A command that is killed releases nothing. Its locks go stale, `pravo state locks` marks them, and the next plan reads the nodes they held whatever their refresh age, as [Plans](../concepts/plans.md#records-a-plan-reads-whatever-their-age) describes.

### JSON

With `--json`, a plain apply prints the plan document, then an error object, unless `--auto-approve` is given. The progress and the result are newline-delimited events.

```sh run
pravo apply --json --auto-approve --replace solo 2>/dev/null | grep -E '"kind":"(replace|apply)"'
```

```text out
{"time":"<time>","kind":"replace","addr":"solo","phase":"done","message":"solo: Replacement complete after 0s [id=93]","id":"93","txn":"<txn>"}
{"added":1,"changed":0,"destroyed":1,"kind":"apply","outputs":{},"phase":"done","planfile":"","remaining":0,"txn":"<txn>"}
```

The last line has `"kind":"apply"`, the counts, the outputs and the name of the plan file when there is one. An apply of a stored plan names the id under `plan` and not `planfile`. The event fields are in [Global flags](global-flags.md#json).

## See also

- [`pravo plan`](plan.md) makes, saves and stores a plan.
- [`pravo show`](show.md) prints a saved plan or a stored plan and says whether it is stale.
- [`pravo destroy`](destroy.md) removes everything in the state.
- [Exit codes](exit-codes.md) lists the codes of every command.
