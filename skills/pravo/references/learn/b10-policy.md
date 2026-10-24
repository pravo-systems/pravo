<!-- Generated from docs/learn/b10-policy.md by tools/skillgen. Do not edit. -->

# Policy on providers and on plans

A constraint on a provider binds every resource built through it, and a policy file judges what a plan does.

A platform team wants every file that every team creates to have mode `0600`. In OpenTofu a module sets a default, and the value the caller passes replaces it.

```hcl
variable "file_permission" {
  default = "0600"
}
```

```hcl
module "keys" {
  source          = "./modules/keys"
  file_permission = "0644"
}
```

```cue
#Strict: loc.#Provider & {
	#Resource: local_file: file_permission: "0600"
}
```

pravo has two checks of this kind. A **specification policy** is a constraint on a provider definition. It binds every resource built through that provider and is checked when the specification is evaluated. A **plan policy** is a file that a plan must satisfy. It judges what a plan does and is named by whoever runs pravo. They are two moments of one idea: a rule written as a CUE value that something must unify with.

*Figure: A specification policy is checked when the specification is evaluated. A plan policy is checked when the plan is judged.*

## Pin a value for every resource

Work in an empty directory. The root lives in `root`, and the policies live beside it. Every command names the root with `-C root`, which runs it as if started there. Wrap the provider in a definition and constrain the resource inside.

File `root/main.cue`:

```cue
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

```sh
pravo -C root init
pravo -C root plan
```

Output:

```text
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

File `root/main.cue`:

```cue
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

```sh
pravo -C root validate
```

Output:

```text
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

File `policy/no-delete.cue`:

```cue
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

File `root/main.cue`:

```cue
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

```sh
pravo -C root apply --auto-approve
```

Output:

```text
...
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2
...
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

Add a third file and plan it under the policy. The plan creates and does not delete. The path of the policy is relative to the root, because `-C root` runs the command there.

File `root/main.cue`:

```cue
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

```sh
pravo -C root plan --out add.plan --policy ../policy/no-delete.cue
```

Output:

```text
...
  # log (local_file) will be created
  + log: {
...
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 2 · not read 2
Policy ../policy/no-delete.cue (sha256:a1757b6a34e5) is satisfied.

Saved the plan to add.plan. To apply exactly these actions, run:
  pravo apply add.plan
```

The plan is printed first. The verdict gives the file name, twelve digits of the SHA-256 of its bytes, and `is satisfied`. The digest identifies the bytes that judged the plan.

Under a policy, an apply is of a saved plan.

```sh
pravo -C root apply add.plan --policy ../policy/no-delete.cue
```

Output:

```text
Policy ../policy/no-delete.cue (sha256:a1757b6a34e5) is satisfied.
log: Creating...
log: Creation complete after 0s [id=4508c1b19b8841345afeed30a6d835cca066d5e5]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

## Break it: a plan that deletes

Remove `note` from the root. The plan deletes it. The block carries `status=unintended`: the specification is valid, and the plan breaks the policy.

File `root/main.cue`:

```cue
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

```sh
pravo -C root plan --out delete.plan --policy ../policy/no-delete.cue
```

Output:

```text
...
  # note (local_file) will be destroyed
  - note: {
...
    }

Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 · skipped 2 · removed 1 · refreshed 1 · not read 2

Error: the plan violates the policy ../policy/no-delete.cue (sha256:a1757b6a34e5):
changes.2: note: delete is not allowed:
    ../policy/no-delete.cue:7:3
```

The plan is printed, then judged, and the run exits with code 5. The error has three parts.

- The policy file and its digest.
- The path `changes.2`. It is a position in the plan document. That list includes unchanged resources, so it is not a count of the blocks above.
- The message from `error()`, and the line in the policy that wrote it.

A refused plan is not saved.

```sh
ls root
```

Output:

```text
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

```sh
pravo -C root apply --auto-approve --policy ../policy/no-delete.cue
```

Output:

```text
Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"
```

## Rehearse a policy on a fixture

A policy can be tested without a plan run. `pravo plan --json --include-skipped` prints the plan document with an entry for every node, which is data and is what a policy is judged against. Save it as CUE next to the policy, in one package, and unify them. `pravo eval -c` fails when the policy is violated.

```sh
mkdir fixture
(echo "package fixture"; pravo -C root plan --json --include-skipped 2>/dev/null) > fixture/plan.cue
(echo "package fixture"; cat policy/no-delete.cue) > fixture/policy.cue
pravo -C fixture eval -c
```

Output:

```text
Error: evaluating ./fixture:
changes.2: note: delete is not allowed:
    ./fixture/policy.cue:8:3
```

The verdict is the one `--policy` gives. Once saved, the fixture needs no state and no provider. A pipeline can test the policy on every change to the policy file. The line number is one more than in the policy, because the first line of the file is `package fixture`.

## What a policy does not bind

A policy binds the runs that name it. Save the same deletion without a policy and give it to `apply` in two ways.

```sh
pravo -C root plan --out delete.plan
```

Output:

```text
...
Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 · skipped 2 · removed 1 · refreshed 1 · not read 2

Saved the plan to delete.plan. To apply exactly these actions, run:
  pravo apply delete.plan
```

```sh
pravo -C root apply delete.plan --policy ../policy/no-delete.cue
```

Output:

```text
Error: the plan violates the policy ../policy/no-delete.cue (sha256:a1757b6a34e5):
changes.2: note: delete is not allowed:
    ../policy/no-delete.cue:7:3
```

Nothing was applied. Now leave the flag out.

```sh
pravo -C root apply delete.plan
pravo -C root state list
```

Output:

```text
note: Destroying... [id=4b61f9110fdc6c1d4ddb0e04f8e31621e755a4f4]
note: Destruction complete after 0s

Apply complete! Resources: 0 added, 0 changed, 1 destroyed. [txn <txn>]
key
log
```

The deletion ran. A run that omits `--policy` is not checked. That includes a laptop, a shell that an agent holds, and a pipeline step with the flag missing.

A specification policy has the same limit. It binds the resources built through its provider. A root can declare another provider from `loc.#Provider` and build a resource through that one.

File `root/main.cue`:

```cue
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

```sh
pravo -C root validate
```

Output:

```text
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

**The specification cannot name a policy**

The `$settings` block takes `state` and `refresh`. Naming a policy there is an error. The specification cannot weaken the rule that judges it.

```sh
printf 'package main\n\n$settings: policy: "../policy/no-delete.cue"\n' > root/setting.cue
pravo -C root plan
```

Output:

```text
Error: $settings.policy is not a setting (the block takes state and refresh)
```

```sh
rm root/setting.cue
```

## Exercise

## Clean up

```sh
pravo -C root destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <txn>]
```

## Recap

- A constraint on a provider definition binds every resource built through it. A conflict is an error before any provider starts.
- A plan policy is a file named with `--policy`, and a saved plan document is a fixture that tests it. A violation exits with code 5, and a refused plan is not saved.
- A policy binds the runs that name it. Omitting the flag, editing the state and calling the cloud directly are outside it.

The how-to pages cover writing and rolling out policies: [Policies](../how-to/policies.md). The reference describes the plan document: [Plan document](../reference/plan-document.md).

The [project](https://pravo.systems/learn/b11-project.html) combines every lesson of this track. [Lab 1](https://pravo.systems/learn/lab1-break-it-on-purpose.html) breaks a root ten ways to show the exit codes.
