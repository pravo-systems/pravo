<!-- Generated from docs/migrate/m7-cut-over.md by tools/skillgen. Do not edit. -->

# Cut over

Remove OpenTofu's claim on the objects, move the pipeline to pravo, order roots that share outputs, and archive the old state.

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

```text
Would remove random_password.admin
Would remove module.service["api"].local_file.config
Would remove module.service["api"].random_pet.host
Would remove module.service["web"].local_file.config
Would remove module.service["web"].random_pet.host
Would remove module.service["worker"].local_file.config
Would remove module.service["worker"].random_pet.host
```

The removal prints two lines for each address:

```text
Removed random_password.admin
Successfully removed 1 resource instance(s).
Removed module.service["api"].local_file.config
Successfully removed 1 resource instance(s).
...
```

The last `tofu state list` prints nothing, and `pravo plan --detailed-exitcode` exits 0. The OpenTofu state still holds the outputs: `tofu output` prints `admin_password` and `endpoints` as before. A root that reads them with `terraform_remote_state` keeps reading them.

The HCL is still in the directory, and the state does not list what the HCL declares. A plan shows the consequence:

```text
...
Plan: 7 to add, 0 to change, 0 to destroy.
...
```

> [!PITFALL]
> Before the removal, `tofu destroy` and `tofu apply` act on the objects that pravo manages. After it, `tofu apply` creates every resource that the HCL declares, as the plan above shows. Either way, delete the HCL or move it out of the pipeline's path.

## Move the pipeline

A pipeline starts from a clean checkout, and `pravo.state` is not in the checkout. The state of a root that a team or a pipeline runs belongs in a bucket that only those people and that pipeline can read. Name the bucket in `$settings`, copy the converted directory into it, and check that the plan is still empty. Do this before the first run of the pipeline.

```cue
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

The settings, the bucket and the commands are in [Keep the state in a shared store](../how-to/keep-the-state-in-a-shared-store.md#6-move-a-local-state-into-the-bucket). Keep versioning on for the bucket, as you did for the OpenTofu state.

Each OpenTofu step has a pravo step. A policy is a node of the root, so the commands name none.

| Step | OpenTofu | pravo |
|---|---|---|
| Install providers | `tofu init` | `pravo init` |
| Check the specification | `tofu validate` | `pravo validate` |
| Plan and save | `tofu plan -out=change.plan` | `pravo plan --out change.plan` |
| Plan, exit 2 on a change | `tofu plan -detailed-exitcode` | `pravo plan --detailed-exitcode` |
| Machine-readable plan | `tofu show -json change.plan` | `pravo show change.plan --json` |
| Apply the saved plan | `tofu apply change.plan` | `pravo apply change.plan` |
| Read outputs | `tofu output -json` | `pravo output --json` |
| Debug log | `TF_LOG=debug` | `PRAVO_LOG=debug`, or `-v` |
| Provider cache | `TF_PLUGIN_CACHE_DIR` | `PRAVO_HOME` |
| Input variable | `TF_VAR_name` | a field with `@tag(name)` and `-t name=value`, or `pravo.#Env` |

`tofu plan` reads every resource, so its exit code 2 also reports a change made outside OpenTofu. `pravo plan --detailed-exitcode` reports a change among the resources the plan looked at. A pipeline that used the plan to find drift reads the other records on a schedule with `pravo drift --due --limit N`, or reads all of them in one plan with `pravo plan -refresh=all --detailed-exitcode`. [CI](../how-to/ci.md#5-find-drift-on-a-schedule) shows the job.

The full list is in [Command map](command-map.md). Here is the pipeline as a script. It runs against a small root in this workspace, so every command below is real.

File `infra/providers.cue`:

```cue
package infra

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider & {$version: "~> 2.5"}
```

File `infra/estate.cue`:

```cue
package infra

pet: $random.#Resource.random_pet & {length: 2}

note: $local.#Resource.local_file & {
	filename: "out/note.txt"
	content:  "pet: \(pet.id)\n"
}
```

File `infra/policy.cue`:

```cue
package infra

import "pravo.dev/pravo"

no_delete: pravo.#Policy & {
	rule: {
		// A managed resource is neither deleted nor replaced.
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

A policy version is signed by whoever applies it, so the state is sealed to one reader first.

```sh
mkdir -p infra/.pravo/access
pravo -C infra id new --name ada | grep '^  ada ssh' | sed 's/^  //' > infra/.pravo/access/readers.txt
pravo -C infra init > /dev/null
pravo -C infra protect init --encrypt --yes --allow-local > /dev/null
```

File `ci.sh`:

```sh
set -eu
pravo -C infra init
pravo -C infra validate
pravo -C infra plan --out change.plan
pravo -C infra apply change.plan
```

The script stops at the first failing command. `pravo plan --out` writes the plan only when every policy in force holds.

```sh
sh ci.sh 2>&1 | grep -E '^(Success|Plan:|Policy|Apply complete)'
```

Output:

```text
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
Plan: 2 to add, 0 to change, 0 to destroy.
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

File `infra/extra.cue`:

```cue
package infra

extra: $local.#Resource.local_file & {
	filename: "out/extra.txt"
	content:  "extra\n"
}
```

```sh
pravo -C infra plan --detailed-exitcode > /dev/null 2>&1
echo "exit=$?"
```

Output:

```text
exit=2
```

A saved plan goes stale when another apply changes the records it was made from:

```sh
pravo -C infra plan --out first.plan > /dev/null 2>&1
pravo -C infra apply --auto-approve > /dev/null 2>&1
pravo -C infra apply first.plan > stale.txt 2>&1
echo "exit=$?"
grep '^Error' stale.txt
```

Output:

```text
exit=3
Error: saved plan is stale; run "pravo plan" again:
```

A policy refusal is 5, and an invalid specification is 6:

```sh
rm infra/extra.cue
pravo -C infra plan > /dev/null 2>&1
echo "exit=$?"
printf 'package infra\n\nbroken: nope\n' > infra/broken.cue
pravo -C infra validate > /dev/null 2>&1
echo "exit=$?"
rm infra/broken.cue
```

Output:

```text
exit=5
exit=6
```

`extra` was applied above, so removing it plans a delete, and the policy refuses it.

## Roots that share outputs

A root can read the outputs of another root with `pravo.#RemoteOutputs`. It reads one object, `outputs.json`, in the other root's pravo state. OpenTofu's `terraform_remote_state` reads the OpenTofu state. The two do not read each other.

*Figure: Migrate the root that is read before the roots that read it.*

The reason is the file that is read. A consumer in pravo reads the pravo state of the producer. That state exists once the producer is converted. It holds the producer's outputs once the producer has had its first apply. A consumer that still runs in OpenTofu reads the producer's OpenTofu state, and pravo does not update that state. Keep the producer's outputs unchanged until the last consumer has moved.

The producer below has one output. The consumer reads it.

File `net/providers.cue`:

```cue
package net

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}
```

File `net/net.cue`:

```cue
package net

import "pravo.dev/pravo"

vpc: $random.#Resource.random_pet & {length: 2, prefix: "vpc"}

vpc_name: pravo.#Output & {value: vpc.id}
```

File `app/app.cue`:

```cue
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

Status: rejected.

```sh
pravo -C net init > /dev/null
pravo -C app init > /dev/null
pravo -C app plan
```

Output:

```text
Error: net: ./net/pravo.state has no recorded outputs; apply that root first
```

Apply the producer, then plan the consumer again:

```sh
pravo -C net apply --auto-approve 2>&1 | grep '^Apply complete'
pravo -C app plan 2>&1 | grep -E '^(Plan:|  # )'
```

Output:

```text
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

Next: [M8 Roll back](m8-rollback.md) states what you can undo and until when.
