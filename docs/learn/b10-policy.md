---
title: Policy on providers and on plans
description: A constraint on a provider binds every resource built through it, and a policy file judges what a plan does.
order: 40
group: Track B · pravo
minutes: 20
needs: B9
learn: How do you pin a value for every resource of a provider? How do you refuse a plan, and what does a policy leave unchecked?
done_when: "A file mode cannot be overridden. A plan that deletes is refused with exit code 5. You can say which runs the policy does not cover."
level: intermediate
normalize:
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

pravo has two checks of this kind. A **specification policy** is a constraint on a provider definition. It binds every resource built through that provider and is checked when the specification is evaluated. A **plan policy** is a file that a plan must satisfy. It judges what a plan does and is named by whoever runs pravo. They are two moments of one idea: a rule written as a CUE value that something must unify with.

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
<text x="398" y="130" text-anchor="middle">file named with --policy</text>
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
Graph: planned 1 of 1.
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
    ./root/cue.mod/gen/pravo.dev/providers/hashicorp/local/local_gen.cue:93:29
    ./root/main.cue:11:6
    ./root/main.cue:14:19
```

The path is `key.file_permission`. The first error names the two values, `"0600"` and `"0644"`, and three places: the pin, the resource and the value. The second error appears because the generated attribute is `string | null`, and CUE reports each alternative that failed. No provider started and nothing was planned.

A constraint rejects a value that is set and wrong. It does not require an attribute to be set. A rule such as "every file has an owner tag" is a rule for a plan policy.

## Refuse a plan

A plan policy is one CUE file. pravo unifies it with the plan document, the data that `pravo plan --json` prints. The plan satisfies the policy when the result is concrete and has no error. Write it outside the root.

```cue file=policy/no-delete.cue title="policy/no-delete.cue" hl="6-8"
// A plan may create and change. It may not delete.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && action == "delete" {
		error("\(addr): delete is not allowed")
	}
}]
```

`changes` is a list template: every change must have these fields. The `if` is a guard. It adds an error to a managed resource whose action is `delete`, and the message names the address.

Restore the root and add a second file. Apply it without a policy.

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

```sh run
pravo -C root apply --auto-approve
```

```text out
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2 of 2.
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

Add a third file and plan it under the policy. The plan creates and does not delete. The path of the policy is relative to the root, because `-C root` runs the command there.

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
pravo -C root plan --out add.plan --policy ../policy/no-delete.cue
```

```text out
...
  # log (local_file) will be created
  + log: {
...
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 of 3, 2 unchanged and skipped, 2 not read.
Policy ../policy/no-delete.cue (sha256:a1757b6a34e5) is satisfied.

Saved the plan to add.plan. To apply exactly these actions, run:
  pravo apply add.plan
```

The plan is printed first. The verdict gives the file name, twelve digits of the SHA-256 of its bytes, and `is satisfied`. The digest identifies the bytes that judged the plan.

Under a policy, an apply is of a saved plan.

```sh run
pravo -C root apply add.plan --policy ../policy/no-delete.cue
```

```text out
Policy ../policy/no-delete.cue (sha256:a1757b6a34e5) is satisfied.
log: Creating...
log: Creation complete after 0s [id=4508c1b19b8841345afeed30a6d835cca066d5e5]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
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

```sh run exit=5
pravo -C root plan --out delete.plan --policy ../policy/no-delete.cue
```

```text out
...
  # note (local_file) will be destroyed
  - note: {
...
    }

Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 of 2, 2 unchanged and skipped, 1 removed from the specification, 1 refreshed, 2 not read.

Error: the plan violates the policy ../policy/no-delete.cue (sha256:a1757b6a34e5):
changes.2: note: delete is not allowed:
    ../policy/no-delete.cue:7:3
```

The plan is printed, then judged, and the run exits with code 5. The error has three parts.

- The policy file and its digest.
- The path `changes.2`. It is a position in the plan document. That list includes unchanged resources, so it is not a count of the blocks above.
- The message from `error()`, and the line in the policy that wrote it.

A refused plan is not saved.

```sh run
ls root
```

```text out
add.plan
cue.mod
key.pem
log.txt
main.cue
note.txt
pravo.lock.cue
pravo.state
```

There is no `delete.plan`. A plain apply makes its own plan as it goes, so a policy refuses it.

```sh run exit=5
pravo -C root apply --auto-approve --policy ../policy/no-delete.cue
```

```text out
Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"
```

## Rehearse a policy on a fixture

A policy can be tested without a plan run. `pravo plan --json --include-skipped` prints the plan document with an entry for every node, which is data and is what a policy is judged against. Save it as CUE next to the policy, in one package, and unify them. `pravo eval -c` fails when the policy is violated.

```sh run exit=6
mkdir fixture
(echo "package fixture"; pravo -C root plan --json --include-skipped 2>/dev/null) > fixture/plan.cue
(echo "package fixture"; cat policy/no-delete.cue) > fixture/policy.cue
pravo -C fixture eval -c
```

```text out
Error: evaluating ./fixture:
changes.2: note: delete is not allowed:
    ./fixture/policy.cue:8:3
```

The verdict is the one `--policy` gives. Once saved, the fixture needs no state and no provider. A pipeline can test the policy on every change to the policy file. The line number is one more than in the policy, because the first line of the file is `package fixture`.

## What a policy does not bind

A policy binds the runs that name it. Save the same deletion without a policy and give it to `apply` in two ways.

```sh run
pravo -C root plan --out delete.plan
```

```text out
...
Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 of 2, 2 unchanged and skipped, 1 removed from the specification, 1 refreshed, 2 not read.

Saved the plan to delete.plan. To apply exactly these actions, run:
  pravo apply delete.plan
```

```sh run exit=5
pravo -C root apply delete.plan --policy ../policy/no-delete.cue
```

```text out
Error: the plan violates the policy ../policy/no-delete.cue (sha256:a1757b6a34e5):
changes.2: note: delete is not allowed:
    ../policy/no-delete.cue:7:3
```

Nothing was applied. Now leave the flag out.

```sh run
pravo -C root apply delete.plan
pravo -C root state list
```

```text out
note: Destroying... [id=4b61f9110fdc6c1d4ddb0e04f8e31621e755a4f4]
note: Destruction complete after 0s

Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
key
log
```

The deletion ran. A run that omits `--policy` is not checked. That includes a laptop, a shell that an agent holds, and a pipeline step with the flag missing.

A specification policy has the same limit. It binds the resources built through its provider. A root can declare another provider from `loc.#Provider` and build a resource through that one.

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

What a policy guarantees depends on four rights.

| Right | Whoever holds it can |
|---|---|
| Write the policy file | Change the policy or empty it. |
| Write the job that spells `--policy` | Drop the flag or name another file. |
| Write the state | Change what pravo believes exists, with `pravo state rm` or `pravo state mv`. No plan is made, so no policy sees it. |
| Hold the cloud credentials | Change infrastructure without running pravo. |

Give the first two rights to the people who review policies. Nothing in pravo covers the last two.

:::deep-dive The specification cannot name a policy
The `$settings` block takes `state` and `refresh`. Naming a policy there is an error. The specification cannot weaken the rule that judges it.

```sh run exit=6
printf 'package main\n\n$settings: policy: "../policy/no-delete.cue"\n' > root/setting.cue
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
A change of a file's content replaces the file. The policy as written does not refuse a replacement. Extend it so that it does. Change the content of `key` so that the plan replaces it. Prove the rule on a fixture before you use it.

::hint
The action of a replacement is `replace`. The guard already reads `action`. Combine the two names with `||`.

::hint
Make the fixture as in [Rehearse a policy on a fixture](#rehearse-a-policy-on-a-fixture), from the plan that replaces `key`. Use `mkdir -p`, because the directory exists.

::solution
```cue file=policy/no-delete.cue title="policy/no-delete.cue"
// A plan may create and change. It may not delete or replace.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && (action == "delete" || action == "replace") {
		error("\(addr): \(action) is not allowed")
	}
}]
```

Change the content of `key` so that the plan replaces it.

```cue file=root/main.cue title="root/main.cue"
package main

import loc "pravo.dev/providers/hashicorp/local"

#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}

$files: #Strict

key: $files.#Resource.local_file & {
	filename: "key.pem"
	content:  "new secret\n"
}

log: $files.#Resource.local_file & {
	filename: "log.txt"
	content:  "log\n"
}
```

Save the plan document and the policy as a fixture, and judge it.

```sh run exit=6
mkdir -p fixture
(echo "package fixture"; pravo -C root plan --json --include-skipped 2>/dev/null) > fixture/plan.cue
(echo "package fixture"; cat policy/no-delete.cue) > fixture/policy.cue
pravo -C fixture eval -c
```

```text out
Error: evaluating ./fixture:
changes.0: key: replace is not allowed:
    ./fixture/policy.cue:8:3
```

The same plan under `--policy` gives the same verdict.

```sh run exit=5
pravo -C root plan --policy ../policy/no-delete.cue
```

```text out
...
  # key (local_file) must be replaced
-/+ key: {
  ~     content:              "secret\n" -> "new secret\n" # forces replacement
...
    }

Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 of 2, 1 unchanged and skipped, 1 refreshed, 1 not read.

Error: the plan violates the policy ../policy/no-delete.cue (sha256:3aeafc5246d4):
changes.0: key: replace is not allowed:
    ../policy/no-delete.cue:7:3
```

```sh run check
pravo -C root plan --policy ../policy/no-delete.cue || test $? -eq 5
```
:::

## Clean up

```sh run
pravo -C root destroy --auto-approve | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- A constraint on a provider definition binds every resource built through it. A conflict is an error before any provider starts.
- A plan policy is a file named with `--policy`, and a saved plan document is a fixture that tests it. A violation exits with code 5, and a refused plan is not saved.
- A policy binds the runs that name it. Omitting the flag, editing the state and calling the cloud directly are outside it.

:::check
Which exit code means a plan violated a policy?
::answer
Exit code 5. The error names the policy file, its digest, the path in the plan document and the line in the policy.

Why does a plain pravo apply fail when a policy is named?
::answer
A plain apply makes its own plan as it goes. Under a policy, an apply is of a saved plan that was judged.
:::

The how-to pages cover writing and rolling out policies: [Policies](../how-to/policies.md). The reference describes the plan document: [Plan document](../reference/plan-document.md).

The [project](b11-project.md) combines every lesson of this track. [Lab 1](lab1-break-it-on-purpose.md) breaks a root ten ways to show the exit codes.
