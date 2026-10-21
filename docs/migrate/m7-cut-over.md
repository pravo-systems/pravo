---
title: Cut over
description: Remove OpenTofu's claim on the objects, move the pipeline to pravo, order roots that share outputs, and archive the old state.
order: 7
group: Runbook
gate: OpenTofu lists no resource
minutes: 30
needs: "Step 6 done: [rewrite the checks on plans](m6-rewrite-plan-checks.md)."
learn: In what order do you stop OpenTofu, start the pravo pipeline and retire the old state?
done_when: "OpenTofu lists no resource for the root, the pipeline runs pravo, and `pravo plan` is still empty."
level: intermediate
normalize:
  - pattern: '(?m)^[^\n]*: (Refreshing state\.\.\.|Reading\.\.\.|Read complete)[^\n]*\n'
    replace: ""
  - pattern: "\\[txn [0-9TZ]+-[0-9a-f]+\\]"
    replace: "[txn <txn>]"
  - pattern: "v[0-9]+\\.[0-9]+\\.[0-9]+"
    replace: "v<version>"
  - pattern: "\\[id=[a-z-]+\\]"
    replace: "[id=<id>]"
  - pattern: "\\(revision [0-9]+, plan expected [0-9]+\\)"
    replace: "(revision <n>, plan expected <n>)"
  - pattern: "after ([0-9]+m)?[0-9.]+s"
    replace: "after 0s"
---

After M5 and M6 both tools can plan the same objects. An apply from either tool changes objects that the other tool's state does not know about. Cutting over ends that. You remove OpenTofu's claim, move the pipeline, and archive the old state.

Do this for one root at a time. A root is a directory with its own state. A root with workspaces has one state for each workspace, and the steps repeat for each one.

## Before you cut

Each line below is observed, not assumed:

| Gate | How you see it |
|---|---|
| The plan is empty and has read every object. | `pravo plan -refresh=all --detailed-exitcode` exits 0 ([M5](m5-convert-the-state.md)). |
| The first apply has run. | `pravo state show <addr>` prints a `config hash` for every record. |
| A small change has been planned and applied. | `pravo state list` shows the new record. |
| Every check has a policy that agrees with it. | The verdict table of [M6](m6-rewrite-plan-checks.md) has no disagreement. |
| The rollback is written down. | [M8](m8-rollback.md) has your names in it. |

## Remove OpenTofu's claim

The objects are in the pravo state now. OpenTofu still lists them in its own state. If OpenTofu applies, it plans against stale records and may replace or destroy objects that pravo manages.

`tofu state rm` forgets a resource in the OpenTofu state. It does not touch the object. Run it only after pravo manages the objects. Pause every pipeline that runs `tofu apply` for the root first.

1. Pause the OpenTofu jobs for the root. Disable the schedule and protect the branch.
2. Run the last gate: `pravo plan -refresh=all --detailed-exitcode` exits 0. The flag reads every object. After the first apply a plain plan trusts the records.
3. Write down what OpenTofu holds, and preview the removal with `-dry-run`.
4. Remove the addresses.
5. Check both sides.

```sh
tofu state pull > archive/platform-final.tfstate
tofu state list > archive/platform-claimed.txt
tofu state list | while read -r addr; do tofu state rm -dry-run "$addr"; done
tofu state list | while read -r addr; do tofu state rm "$addr"; done
tofu state list
pravo plan --detailed-exitcode
```

The loops quote each address, because an address with a key contains quotes, such as `module.service["api"].random_pet.host`. The page does not run OpenTofu, so the `tofu` lines are not captured. The output below comes from OpenTofu 1.13.1 on the `platform` root of [M3](m3-worked-example.md). The dry run lists every address and changes nothing:

```text title="the dry run, OpenTofu 1.13.1"
Would remove random_password.admin
Would remove module.service["api"].local_file.config
Would remove module.service["api"].random_pet.host
Would remove module.service["web"].local_file.config
Would remove module.service["web"].random_pet.host
Would remove module.service["worker"].local_file.config
Would remove module.service["worker"].random_pet.host
```

The removal prints two lines for each address:

```text title="the removal, OpenTofu 1.13.1 (excerpt)"
Removed random_password.admin
Successfully removed 1 resource instance(s).
Removed module.service["api"].local_file.config
Successfully removed 1 resource instance(s).
...
```

The last `tofu state list` prints nothing, and `pravo plan --detailed-exitcode` exits 0. The OpenTofu state still holds the outputs: `tofu output` prints `admin_password` and `endpoints` as before. A root that reads them with `terraform_remote_state` keeps reading them.

The HCL is still in the directory, and the state does not list what the HCL declares. A plan shows the consequence:

```text title="tofu plan -detailed-exitcode, OpenTofu 1.13.1, exit code 2 (excerpt)"
...
Plan: 7 to add, 0 to change, 0 to destroy.
...
```

> [!PITFALL]
> Before the removal, `tofu destroy` and `tofu apply` act on the objects that pravo manages. After it, `tofu apply` creates every resource that the HCL declares, as the plan above shows. Either way, delete the HCL or move it out of the pipeline's path.

## Move the pipeline

A pipeline starts from a clean checkout, and `pravo.state` is not in the checkout. The state of a root that a team or a pipeline runs belongs in a bucket that only those people and that pipeline can read. Name the bucket in `$settings`, copy the converted directory into it, and check that the plan is still empty. Do this before the first run of the pipeline.

```cue title="settings.cue"
package platform

import "pravo.dev/pravo"

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend: "s3"
		bucket:  "acme-pravo-state"
		prefix:  "platform"
		region:  "eu-north-1"
	}
}
```

```sh
pravo state push pravo.state
pravo plan --detailed-exitcode
```

The settings, the bucket and the commands are in [Keep the state in a shared bucket](../how-to/remote-state.md#6-move-a-local-state-into-the-bucket). Keep versioning on for the bucket, as you did for the OpenTofu state.

Each OpenTofu step has a pravo step. The policy is named on `plan` and on `apply <planfile>`.

| Step | OpenTofu | pravo |
|---|---|---|
| Install providers | `tofu init` | `pravo init` |
| Check the specification | `tofu validate` | `pravo validate` |
| Plan and save | `tofu plan -out=change.plan` | `pravo plan --out change.plan --policy policy/rules.cue` |
| Plan, exit 2 on a change | `tofu plan -detailed-exitcode` | `pravo plan --detailed-exitcode` |
| Machine-readable plan | `tofu show -json change.plan` | `pravo show change.plan --json` |
| Apply the saved plan | `tofu apply change.plan` | `pravo apply change.plan --policy policy/rules.cue` |
| Read outputs | `tofu output -json` | `pravo output --json` |
| Debug log | `TF_LOG=debug` | `PRAVO_LOG=debug`, or `-v` |
| Provider cache | `TF_PLUGIN_CACHE_DIR` | `PRAVO_HOME` |
| Input variable | `TF_VAR_name` | a field with `@tag(name)` and `-t name=value`, or `pravo.#Env` |

`tofu plan` reads every resource, so its exit code 2 also reports a change made outside OpenTofu. `pravo plan --detailed-exitcode` reports a change among the resources the plan looked at. A pipeline that used the plan to find drift reads the other records on a schedule with `pravo drift --due --limit N`, or reads all of them in one plan with `pravo plan -refresh=all --detailed-exitcode`. [CI](../how-to/ci.md#5-find-drift-on-a-schedule) shows the job.

The full list is in [Command map](command-map.md). Here is the pipeline as a script. It runs against a small root in this workspace, so every command below is real.

```cue file=infra/providers.cue title="infra/providers.cue"
package infra

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider & {$version: "~> 2.5"}
```

```cue file=infra/estate.cue title="infra/estate.cue"
package infra

pet: $random.#Resource.random_pet & {length: 2}

note: $local.#Resource.local_file & {
	filename: "out/note.txt"
	content:  "pet: \(pet.id)\n"
}
```

```cue file=policy/no-delete.cue title="policy/no-delete.cue"
// A managed resource is neither deleted nor replaced.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && (action == "delete" || action == "replace") {
		error("\(addr): \(action) is not allowed")
	}
}]
```

```sh file=ci.sh title="ci.sh"
set -eu
pravo -C infra init
pravo -C infra validate
pravo -C infra plan --out change.plan --policy ../policy/no-delete.cue
pravo -C infra apply change.plan --policy ../policy/no-delete.cue
```

The script stops at the first failing command. `pravo plan --out` writes the plan only when every named policy holds.

```sh run
sh ci.sh 2>&1 | grep -E '^(Success|Plan:|Policy|Apply complete)'
```

```text out
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
Plan: 2 to add, 0 to change, 0 to destroy.
Policy ../policy/no-delete.cue (sha256:0c23a31e78bd) is satisfied.
Policy ../policy/no-delete.cue (sha256:0c23a31e78bd) is satisfied.
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

A job reads the exit code of each command. pravo uses the same code for the same cause on every command.

| Code | Meaning | Where you see it |
|---|---|---|
| 0 | Success. | Every command. |
| 1 | Any other error. | Every command. |
| 2 | The plan has changes, or `drift` found drift. | `plan --detailed-exitcode`, `drift` |
| 3 | A saved plan is stale. | `apply <planfile>` |
| 4 | A resource is locked or reserved. | `plan`, `apply` |
| 5 | A policy refused the plan. | `plan`, `apply`, `destroy` |
| 6 | The specification does not evaluate or validate. | Every command that loads the root. |

Four of them in this workspace. Add a resource, and the plan has changes:

```cue file=infra/extra.cue title="infra/extra.cue"
package infra

extra: $local.#Resource.local_file & {
	filename: "out/extra.txt"
	content:  "extra\n"
}
```

```sh run
pravo -C infra plan --detailed-exitcode > /dev/null 2>&1
echo "exit=$?"
```

```text out
exit=2
```

A saved plan goes stale when another apply changes the records it was made from:

```sh run
pravo -C infra plan --out first.plan > /dev/null 2>&1
pravo -C infra apply --auto-approve > /dev/null 2>&1
pravo -C infra apply first.plan > stale.txt 2>&1
echo "exit=$?"
grep '^Error' stale.txt
```

```text out
exit=3
Error: saved plan is stale; run "pravo plan" again:
```

A policy refusal is 5, and an invalid specification is 6:

```sh run
rm infra/extra.cue
pravo -C infra plan --policy ../policy/no-delete.cue > /dev/null 2>&1
echo "exit=$?"
printf 'package infra\n\nbroken: nope\n' > infra/broken.cue
pravo -C infra validate > /dev/null 2>&1
echo "exit=$?"
rm infra/broken.cue
```

```text out
exit=5
exit=6
```

`extra` was applied above, so removing it plans a delete, and the policy refuses it.

## Roots that share outputs

A root can read the outputs of another root with `pravo.#RemoteOutputs`. It reads one object, `outputs.json`, in the other root's pravo state. OpenTofu's `terraform_remote_state` reads the OpenTofu state. The two do not read each other.

:::figure Migrate the root that is read before the roots that read it.
<svg viewBox="0 0 600 130" role="img" aria-label="net is migrated first, then app, which reads the outputs of net" style="width:100%;height:auto;display:block;margin:0 auto">
  <g fill="none" stroke="currentColor" stroke-width="1.5">
    <rect x="20" y="30" width="200" height="70" rx="3"/>
    <rect x="380" y="30" width="200" height="70" rx="3"/>
    <path d="M220 65 H380"/>
    <path d="M366 57 L380 65 L366 73"/>
  </g>
  <g fill="currentColor" font-family="IBM Plex Mono, monospace" font-size="15" text-anchor="middle">
    <text x="120" y="60">net (producer)</text>
    <text x="120" y="82" opacity="0.7">migrate first</text>
    <text x="480" y="60">app (consumer)</text>
    <text x="480" y="82" opacity="0.7">migrate second</text>
    <text x="300" y="54" opacity="0.7">outputs.json</text>
  </g>
</svg>
:::

The reason is the file that is read. A consumer in pravo reads the pravo state of the producer. That state exists once the producer is converted. It holds the producer's outputs once the producer has had its first apply. A consumer that still runs in OpenTofu reads the producer's OpenTofu state, and pravo does not update that state. Keep the producer's outputs unchanged until the last consumer has moved.

The producer below has one output. The consumer reads it.

```cue file=net/providers.cue title="net/providers.cue"
package net

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}
```

```cue file=net/net.cue title="net/net.cue"
package net

import "pravo.dev/pravo"

vpc: $random.#Resource.random_pet & {length: 2, prefix: "vpc"}

vpc_name: pravo.#Output & {value: vpc.id}
```

```cue file=app/app.cue title="app/app.cue"
package app

import (
	"pravo.dev/pravo"
	loc "pravo.dev/providers/hashicorp/local"
)

$local: loc.#Provider

net: pravo.#RemoteOutputs & {state: path: "../net/pravo.state"}

note: $local.#Resource.local_file & {
	filename: "out/vpc.txt"
	content:  "vpc: \(net.outputs.vpc_name)\n"
}
```

Plan the consumer before the producer has a state:

```sh run exit=1 status=rejected
pravo -C net init > /dev/null
pravo -C app init > /dev/null
pravo -C app plan
```

```text out
Error: net: ./net/pravo.state has no recorded outputs; apply that root first
```

Apply the producer, then plan the consumer again:

```sh run
pravo -C net apply --auto-approve 2>&1 | grep '^Apply complete'
pravo -C app plan 2>&1 | grep -E '^(Plan:|  # )'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
  # note (local_file) will be created
Plan: 1 to add, 0 to change, 0 to destroy.
```

A reader does not receive an output that the producer marks `sensitive`. The conversion keeps the mark, so this holds for a producer converted with `pravo state import-tfstate`.

## Archive the old state

Keep what lets you answer "what did OpenTofu believe?" and nothing that can be applied by mistake.

| Keep | Where | Until |
|---|---|---|
| The last state, from `tofu state pull` | A restricted bucket or the artifact store | The retention date you wrote in M1 |
| The HCL at the final commit | A git tag such as `opentofu-final`, off the default branch | The same date |
| The `.terraform.lock.hcl` | In the tag | The same date |
| The versioned backend bucket | Versioning stays on | The same date |

Delete the working copies of `.terraform/` and any `terraform.tfstate` files from the checkout. After the retention date, delete the archive and the backend. [M8](m8-rollback.md) says what each copy is good for.

## Recap

- Remove OpenTofu's claim with `tofu state rm` only after pravo manages the objects and the OpenTofu jobs are paused.
- Each OpenTofu pipeline step has a pravo step. The exit codes tell a job what happened: 2 changes, 3 stale, 5 refused, 6 invalid.
- Migrate the root that is read before the roots that read it. Archive the old state with a retention date.

:::check
What does `tofu state rm` do to a real object?
::answer
Nothing. It removes the record from the OpenTofu state, so OpenTofu stops planning the object.

Which root do you migrate first when `app` reads the outputs of `net`?
::answer
`net`. The consumer reads the producer's pravo state, which exists after the producer is converted and applied.
:::

Next: [M8 Roll back](m8-rollback.md) states what you can undo and until when.
