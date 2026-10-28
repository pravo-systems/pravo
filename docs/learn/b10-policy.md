---
title: Policy on providers and on plans
description: A constraint on a provider binds every resource built through it, and a policy node judges what a plan does.
order: 40
group: Track B · pravo
minutes: 20
needs: B9
learn: How do you pin a value for every resource of a provider? How do you refuse a plan, and what does a policy leave unchecked?
done_when: "A file mode cannot be overridden. A plan that deletes is refused with exit code 5. You can say what a policy does not cover."
level: intermediate
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: 'records sealed in [0-9.]+ s'
    replace: 'records sealed in 0.0 s'
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: "txn [0-9TZ]+-[0-9a-f]+"
    replace: "txn <txn>"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
---

A platform team wants every file that every team creates to have mode `0600`. In OpenTofu a module sets a default, and the value the caller passes replaces it.

:::compare
```hcl title="modules/keys/variables.tf"
variable "file_permission" {
  default = "0600"
}
```

```hcl title="main.tf"
module "keys" {
  source          = "./modules/keys"
  file_permission = "0644"
}
```

```cue title="main.cue"
#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}
```
:::

pravo has two checks of this kind. A **specification policy** is a constraint on a provider definition. It binds every resource built through that provider and is checked when the specification is evaluated. A **plan policy** is a node of the root that a plan must satisfy. It judges what a plan does, and pravo keeps it in the state. They are two moments of one idea: a rule written as a CUE value that something must unify with.

:::figure A specification policy is checked when the specification is evaluated. A plan policy is checked when the plan is judged.
<svg font-size="13" viewBox="0 0 564 170" role="img" aria-label="A pipeline of five stages: CUE files, evaluate, plan, judge, apply. A specification policy acts at evaluate and fails with exit 6. A plan policy acts at judge and fails with exit 5.">
<g fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="20" width="96" height="44"/><rect x="118" y="20" width="96" height="44"/><rect x="234" y="20" width="96" height="44"/><rect x="350" y="20" width="96" height="44"/><rect x="466" y="20" width="96" height="44"/><path d="M98 42H118M214 42H234M330 42H350M446 42H466"/></g>
<g fill="currentColor"><path d="M111 38l7 4-7 4zM227 38l7 4-7 4zM343 38l7 4-7 4zM459 38l7 4-7 4z"/></g>
<g fill="none" stroke-width="1.5"><path style="stroke: var(--key)" d="M166 64V92"/><path style="stroke: var(--conflict)" d="M398 64V92"/></g>
<text x="16" y="47" style="fill: var(--text)">CUE files</text>
<text x="144" y="47" style="fill: var(--text)">evaluate</text>
<text x="272" y="47" style="fill: var(--text)">plan</text>
<text x="380" y="47" style="fill: var(--text)">judge</text>
<text x="502" y="47" style="fill: var(--text)">apply</text>
<text x="166" y="112" text-anchor="middle" style="fill: var(--key)">specification policy</text>
<text x="166" y="130" text-anchor="middle">constraint on the provider</text>
<text x="166" y="148" text-anchor="middle">validate: exit 6</text>
<text x="398" y="112" text-anchor="middle" style="fill: var(--conflict)">plan policy</text>
<text x="398" y="130" text-anchor="middle">node kept in the state</text>
<text x="398" y="148" text-anchor="middle">plan, apply: exit 5</text>
</svg>
:::

## Pin a value for every resource

Work in an empty directory. The root lives in `root`, and the policies live beside it. Every command names the root with `-C root`, which runs it as if started there. Wrap the provider in a definition and constrain the resource inside.

```cue file=root/main.cue title="root/main.cue" hl="5-7,9"
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

key: $files.#Resource.local_file & {
	filename: "key.pem"
	content:  "secret\n"
}
```

`#Strict` is a provider definition with one more constraint. `$files` is a provider built from it. Every `local_file` built through `$files` carries the mode `0600`.

```sh run
pravo -C root init
pravo -C root plan
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
...
pravo has been initialised. Run "pravo plan" to see what it would do.
...
  # key (local_file) will be created
  + key: {
  +     content:              "secret\n"
...
  +     file_permission:      "0600"
  +     filename:             "key.pem"
  +     id:                   (known after apply)
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1
```

`key` never set a mode. The plan shows `file_permission: "0600"` all the same.

## Break it: override the pin

Set a different mode on the resource. The block carries `status=conflict`.

```cue file=root/main.cue title="root/main.cue" status=conflict hl="14"
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

key: $files.#Resource.local_file & {
	filename:        "key.pem"
	content:         "secret\n"
	file_permission: "0644"
}
```

```sh run exit=6
pravo -C root validate
```

```text out
Error: evaluating ./root:
key.file_permission: 2 errors in empty disjunction:
key.file_permission: conflicting values "0600" and "0644":
    ./root/main.cue:6:42
    ./root/main.cue:11:6
    ./root/main.cue:14:19
key.file_permission: conflicting values "0644" and null (mismatched types string and null):
    ./root/cue.mod/gen/pravo.dev/providers/hashicorp/local/local_gen.cue:97:29
    ./root/main.cue:11:6
    ./root/main.cue:14:19
```

The path is `key.file_permission`. The first error names the two values, `"0600"` and `"0644"`, and three places: the pin, the resource and the value. The second error appears because the generated attribute is `string | null`, and CUE reports each alternative that failed. No provider started and nothing was planned.

A constraint rejects a value that is set and wrong. It does not require an attribute to be set. A rule such as "every file has an owner tag" is a rule for a plan policy.

## Refuse a plan

A plan policy is a node of the root. Its `rule` is a CUE struct, and pravo unifies it with the plan document, the data that `pravo plan --json` prints. The plan satisfies the policy when the result is concrete and has no error. Write the policy beside the resources:

```cue file=root/policy.cue title="root/policy.cue" hl="7-14"
package main

import "pravo.dev/pravo"

no_delete: pravo.#Policy & {
	description: "A plan may create and change. It may not delete."
	rule: {
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

`changes` is a list template: every change must have these fields. The `if` is a guard. It adds an error to a managed resource whose action is `delete`, and the message names the address.

Restore the root and add a second file.

```cue file=root/main.cue title="root/main.cue"
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

key: $files.#Resource.local_file & {
	filename: "key.pem"
	content:  "secret\n"
}

note: $files.#Resource.local_file & {
	filename: "note.txt"
	content:  "note\n"
}
```

A policy version is signed by whoever applies it, so the state must be sealed. Make an identity, list it as a reader, and seal the state:

```sh run
mkdir -p root/.pravo/access
pravo -C root id new --name ada | grep '^  ada ssh' | sed 's/^  //' > root/.pravo/access/readers.txt
pravo -C root protect init --encrypt --yes --allow-local
```

```text out
Readers  .pravo/access/readers.txt  (ada)
This will:
  1  write 0 records as immutable objects  rec/<sha256>
  2  encrypt their attributes and private data to 1 reader (ada)
  3  seal outputs.json, stored-plan values and the values in transaction command lines the same way
  4  set the state to format 3. Earlier versions of pravo will refuse it
store check passed
0 records sealed in 0.0 s
outputs sealed · recipients written to meta.json

The state is sealed to ada.

Commit the readers file. Writers read the list from git:
  .pravo/access/readers.txt
No recovery recipient. If every reader loses their key, the state cannot be read.
Add one with: pravo protect rewrap --recovery 3/5
```

The first apply creates both files and writes the policy to the state. The plan that adds a policy is not judged by it.

```sh run
pravo -C root apply --auto-approve | sed -n '/^Policies/,/^Graph/p;/^Apply complete/p'
```

```text out
Policies: +1 new, ~0 changed, -0 removed.
  + no_delete (sha256:d0fce23004ba)
Graph: planned 2
Policies: +1 new, ~0 changed, -0 removed.
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

Add a third file and plan it. The plan creates and does not delete, so the policy is satisfied.

```cue file=root/main.cue title="root/main.cue" hl="21-24"
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

key: $files.#Resource.local_file & {
	filename: "key.pem"
	content:  "secret\n"
}

note: $files.#Resource.local_file & {
	filename: "note.txt"
	content:  "note\n"
}

log: $files.#Resource.local_file & {
	filename: "log.txt"
	content:  "log\n"
}
```

```sh run
pravo -C root plan --out add.plan | sed -n '/^Plan:/,$p'
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 2 · not read 2
Policies: 1 satisfied.

Saved the plan to add.plan. To apply exactly these actions, run:
  pravo apply add.plan
```

The plan is printed first, and the last line of the plan says how many policies judged it and held. The apply judges the plan again, under the locks, and signs the versions that judged it into the transaction: the name, the version and the SHA-256 of the text, so that the log says under which rules a change was made.

```sh run
pravo -C root apply add.plan
pravo -C root state log --json | jq -c '.[0].policies'
```

```text out
log: Creating...
log: Creation complete after 0s [id=4508c1b19b8841345afeed30a6d835cca066d5e5]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
["no_delete@1:d0fce23004ba3284e425b8753266ed26f2203fac9f8c06ffe60a7ca1db8c2bc9"]
```

## Break it: a plan that deletes

Remove `note` from the root. The plan deletes it. The block carries `status=unintended`: the specification is valid, and the plan breaks the policy.

```cue file=root/main.cue title="root/main.cue" status=unintended
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

key: $files.#Resource.local_file & {
	filename: "key.pem"
	content:  "secret\n"
}

log: $files.#Resource.local_file & {
	filename: "log.txt"
	content:  "log\n"
}
```

:::predict Does the plan pass the policy? Which exit code does the run give?
The specification is valid. Read what the plan does to `note`.

::reveal
The plan deletes `note`, so the policy refuses it. The exit code is 5, and the plan is not saved.
:::

```sh run
pravo -C root plan --out delete.plan 2>&1 | sed -n '/^Plan:/,$p'
```

```text out
Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 · skipped 2 · removed 1 · refreshed 1 · not read 2

Error: the plan violates the policy no_delete@1 (sha256:d0fce23004ba):
changes.0: note: delete is not allowed:
    no_delete:6:3
```

The plan is printed, then judged, and the run exits with code 5. The error has three parts.

- The version of the policy and the digest of its text.
- The path `changes.0`. It is a position in the list of changes of the plan document, in the order the document holds them, which is not the order of the blocks above.
- The message from `error()`, and the line in the rule that wrote it.

A refused plan is not saved.

```sh run
ls root/*.plan
```

```text out
root/add.plan
```

There is no `delete.plan`. An apply judges its plan too, under the locks, and records the refusal:

```sh run
pravo -C root apply --auto-approve 2>&1 | sed -n '/^Error/,$p'
pravo -C root state log --json | jq -c '.[0] | {kind, status}'
```

```text out
Error: the plan violates the policy no_delete@1 (sha256:d0fce23004ba):
changes.0: note: delete is not allowed:
    no_delete:6:3

{"kind":"apply","status":"rejected"}
```

## What a policy does not bind

A policy binds the applies of this state. The way to lift a rule is to remove it from the root. A change to a policy is not judged by the rule, so the apply that removes it passes, and the state keeps a signed removal. With roles in the readers file, removing a policy is an admin's apply, and a rule that asks for approvals cannot be changed without an admin's approval; [D6](d06-ask-for-approvals.md) shows both. Here the state has one member, who is an admin. Restore `note`, remove `policy.cue`, and apply:

```cue file=root/main.cue title="root/main.cue"
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

key: $files.#Resource.local_file & {
	filename: "key.pem"
	content:  "secret\n"
}

note: $files.#Resource.local_file & {
	filename: "note.txt"
	content:  "note\n"
}

log: $files.#Resource.local_file & {
	filename: "log.txt"
	content:  "log\n"
}
```

```sh run
rm root/policy.cue
pravo -C root apply --auto-approve | sed -n '/^Policies/,/^Graph/p;/^Apply/p'
ls root/pravo.state/policy
```

```text out
Policies: +0 new, ~0 changed, -1 removed.
  - no_delete (version 1)
Graph: planned 0 · skipped 3 · not read 3
Policies: +0 new, ~0 changed, -1 removed.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
no_delete@00000001.json
no_delete@00000002.json
```

With the rule gone, a plan that deletes `note` is applied:

```sh run
rm -f delete.plan
sed -i '/^note:/,/^}/d' root/main.cue
pravo -C root apply --auto-approve | grep '^Apply complete'
pravo -C root state list
```

```text out
Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
key
log
```

The log of the removal names who applied it, and every apply names the versions that judged it. A policy does not stop a person who holds the state, who can write a new version or remove the policy, so the files that declare policies are reviewed like the rest of the root.

A specification policy has its own limit. It binds the resources built through its provider. A root can declare another provider from `loc.#Provider` and build a resource through that one.

```cue file=root/main.cue title="root/main.cue" status=unintended hl="10,22"
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict
$other: loc.#Provider

key: $files.#Resource.local_file & {
	filename: "key.pem"
	content:  "secret\n"
}

log: $files.#Resource.local_file & {
	filename: "log.txt"
	content:  "log\n"
}

probe: $other.#Resource.local_file & {filename: "probe.txt", content: "x\n", file_permission: "0644"}
```

```sh run
pravo -C root validate
```

```text out
Success! 3 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

The mode `0644` passes, because `$other` carries no pin. The platform decides which provider structs a root is built from, for example by reviewing the files that declare them.

Take `probe` out again:

```cue file=root/main.cue title="root/main.cue"
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

key: $files.#Resource.local_file & {
	filename: "key.pem"
	content:  "secret\n"
}

log: $files.#Resource.local_file & {
	filename: "log.txt"
	content:  "log\n"
}
```

What a policy guarantees depends on four rights.

| Right | Whoever holds it can |
|---|---|
| Write the files that declare policies | Change a policy or remove it. The signed version names who applied it. |
| Write the job that runs pravo | Run something other than pravo. |
| Write the state | Change what pravo believes exists, with `pravo state rm` or `pravo state mv`. No plan is made, so no policy sees it. |
| Hold the cloud credentials | Change infrastructure without running pravo. |

Give the first two rights to the people who review policies. Nothing in pravo covers the last two.

:::deep-dive The specification cannot exempt a resource
The `$settings` block takes `state` and `refresh`. Naming a policy there is an error. A policy is a node, and the block holds no rule.

```sh run exit=6
printf 'package main\n\n$settings: policy: "x"\n' > root/setting.cue
pravo -C root plan
```

```text out
Error: $settings.policy is not a setting (the block takes state and refresh)
```

```sh run
rm root/setting.cue
```
:::

## Exercise

:::exercise Refuse replacements too
A change of a file's content replaces the file. The policy as written does not refuse a replacement. Extend it so that it does. Apply the new text, then change the content of `key` so that the plan replaces it.

::hint
The action of a replacement is `replace`. The guard already reads `action`. Combine the two names with `||`.

::hint
A change to a policy is applied like any other change: the apply writes version 2. The plan that replaces `key` comes after it.

::solution
```cue file=root/policy.cue title="root/policy.cue"
package main

import "pravo.dev/pravo"

no_delete: pravo.#Policy & {
	description: "A plan may create and change. It may not delete or replace."
	rule: {
		changes: [...{
			addr:   string
			mode:   string
			action: string
			if mode == "managed" && (action == "delete" || action == "replace") {
				error("\(addr): \(action) is not allowed")
			}
		}]
	}
}
```

Apply the new text of the policy, then change the content of `key` so that the plan replaces it.

```sh run
pravo -C root apply --auto-approve | sed -n '/^Policies/,/^Graph/p'
sed -i 's/secret/new secret/' root/main.cue
```

```text out
Policies: +0 new, ~1 changed, -0 removed.
  ~ no_delete (version 2, sha256:5530c1a63699)
Graph: planned 0 · skipped 2 · not read 2
Policies: +0 new, ~1 changed, -0 removed.
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]
```

```sh run
pravo -C root plan 2>&1 | sed -n '/^Error/,$p'
```

```text out
Error: the plan violates the policy no_delete@3 (sha256:5530c1a63699):
changes.0: key: replace is not allowed:
    no_delete:6:3
```

```sh run check
pravo -C root plan > /dev/null 2>&1 || test $? -eq 5
```
:::

## Clean up

Destroy needs the policy out of the way, because a destroy deletes. Remove the policy, then destroy:

```sh run
rm -f root/policy.cue
sed -i 's/new secret/secret/' root/main.cue
pravo -C root apply --auto-approve > /dev/null
pravo -C root destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- A constraint on a provider definition binds every resource built through it. A conflict is an error before any provider starts.
- A plan policy is a `pravo.#Policy` node. The apply writes it to the state as a signed version, and later applies are judged by it. A violation exits with code 5, and a refused plan is not saved.
- A policy binds the applies of the state. Editing the state, running something other than pravo and calling the cloud directly are outside it.

:::check
Which exit code means a plan violated a policy?
::answer
Exit code 5. The error names the policy version, the digest of its text, the path in the plan document and the line in the rule.

How do you lift a rule that refuses every plan?
::answer
Remove the policy from the root and apply. A change to a policy is not judged, and the apply writes a signed removal.
:::

[D5, Write a plan policy](d05-write-a-plan-policy.md) teaches the shapes of a rule and tries them on saved plans with `pravo policy test`, and [D6](d06-ask-for-approvals.md) writes a rule that makes a plan wait for a second person. The how-to page [Write and roll out policies](../how-to/policies.md) rolls a rule out, and the reference describes [the plan document](../reference/plan-document.md).

The [project](b11-project.md) combines every lesson of this track. [Lab 1](lab1-break-it-on-purpose.md) breaks a root ten ways to show the exit codes.
