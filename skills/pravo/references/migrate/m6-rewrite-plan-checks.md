<!-- Generated from docs/migrate/m6-rewrite-plan-checks.md by tools/skillgen. Do not edit. -->

# Rewrite the checks on plans

Turn the CI checks that read the OpenTofu plan JSON into plan policies, and run both until they agree.

A check that runs in CI over the output of `tofu show -json` does not carry over as it is. It reads the OpenTofu plan JSON, and pravo prints a plan document of its own. You rewrite each rule once, in CUE, as a plan policy.

A policy is one CUE file that a plan must satisfy. Whoever runs pravo names it with `--policy`. pravo judges the plan before it acts.

## The check you have

A common arrangement is a step between `plan` and `apply`:

```sh
tofu plan -out=change.plan
tofu show -json change.plan > plan.json
./check-plan plan.json
tofu apply change.plan
```

The script is a program or a few lines of `jq`. Two typical rules:

```sh
# No resource is deleted or replaced.
jq -e '[.resource_changes[] | select(.change.actions | index("delete"))] | length == 0' plan.json

# A local file that is created or updated has the permission 0600.
jq -e 'all(.resource_changes[] | select(.mode == "managed" and .type == "local_file" and (.change.actions | index("create") or index("update"))); .change.after.file_permission == "0600")' plan.json
```

Both exit non-zero when the rule fails, and the job stops. The build of this page does not run OpenTofu. The `tofu` and `jq` lines above are shown for comparison; every `pravo` command below is run.

## Where each rule goes

| The rule says | It goes in | Checked |
|---|---|---|
| An attribute may only take these values. | A constraint on the provider definition. | By `pravo validate`, with no provider started. |
| An attribute must be set. | A plan policy. | When a plan is made and when it is applied. |
| A plan may not delete, or may change at most N resources. | A plan policy. | When a plan is made and when it is applied. |

A constraint on the provider definition rejects a value that is set and wrong. It cannot require that a value is set. See [Policies](../concepts/policies.md) for the two kinds and [Write and roll out plan policies](../how-to/policies.md) for the full how-to. This page rewrites the second and third kinds, which are the ones that read plan JSON.

## The plan document

`pravo plan --json` prints the document. The workspace holds a root `infra` with a random pet and a file. Apply it once, so that a plan has records to compare.

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

File `infra/pet.cue`:

```cue
package infra

pet: $random.#Resource.random_pet & {length: 2}
```

File `infra/note.cue`:

```cue
package infra

note: $local.#Resource.local_file & {
	filename:        "out/note.txt"
	content:         "pet: \(pet.id)\n"
	file_permission: "0600"
}
```

```sh
pravo -C infra init > /dev/null
pravo -C infra apply --auto-approve 2>&1 | tail -n 1
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

A policy lives outside the root directory. The root is `infra`, and the policies sit in `policy` beside it. Add one change to the root and look at the document:

File `scenarios/extra.cue`:

```cue
package infra

extra: $local.#Resource.local_file & {
	filename: "out/extra.txt"
	content:  "extra\n"
}
```

```sh
rm -rf trial && cp -R infra trial && cp scenarios/extra.cue trial/
pravo -C trial plan --json 2>/dev/null | head -n 36
```

Output:

```text
{
  "format": 1,
  "created": "<time>",
  "summary": {
    "add": 1,
    "change": 0,
    "destroy": 0,
    "read": 0,
    "blast_radius": 1
  },
  "stats": {
    "total": 3,
    "reached": 1,
    "skipped": 2,
    "refreshed": 0,
    "fresh": 0,
    "not_read": 2,
    "drifted": 0,
    "opened": 0,
    "called": 0,
    "imported": 0,
    "moved": 0
  },
  "changes": [
    {
      "addr": "extra",
      "mode": "managed",
      "type": "local_file",
      "name": "extra",
      "action": "create",
      "provider": "local",
      "reasons": [
        "not in the state: a new resource"
      ],
      "after": {
        "content": "extra\n",
```

The fields that checks read correspond like this.

| OpenTofu JSON plan | pravo plan document |
|---|---|
| `resource_changes[]` | `changes[]` |
| `address` | `addr`, the path of the declaration: `note`, `ports[0]`, `notes.files.alpha` |
| `module_address`, `index` | none; the module and the key are part of `addr` |
| `mode`: `managed` or `data` | `mode`: `managed`, `data`, `ephemeral` or `call` |
| `type`, `name` | `type`, `name`; `name` is the last element of the path |
| `provider_name` | `provider`, the provider instance such as `local.$local` |
| `change.actions`, a list | `action`, one word |
| `change.before`, `change.after` | `before`, `after` |
| `change.after_unknown` | `{"$unknown": true}` in place of the value in `after` |
| `change.before_sensitive`, `change.after_sensitive` | none; the document is not masked |
| `change.replace_paths` | `requires_replace`, a list of attribute names |
| `action_reason` | `reasons`, in words for people |
| `output_changes` | `outputs` |
| `resource_drift` | `drifted: true` on the change that the refresh found changed |
| `planned_values`, `prior_state`, `configuration`, `variables` | none |

The word in `action` is one of `create`, `update`, `replace`, `delete`, `read`, `no-op`, `open`, `call` or `move`. A list with `delete` and `create` in OpenTofu is `replace` in pravo. A rule that forbids `delete` alone lets a replacement through. A `create` has no `before`, a `delete` has no `after`, and a `no-op` has neither. The document also carries `summary` (with `add`, `change`, `destroy`, `read` and `blast_radius`) and `stats`. The identity fields `addr`, `mode`, `type`, `name` and `action` are always present. The document is not masked: sensitive attributes appear in it.

## Four rules

A policy is a value that the document must unify with. The usual shape is a template over `changes`, which every entry must satisfy. Declare each field the rule reads in the template, because CUE resolves names by scope.

### 1. Prohibit with a guard and error()

A guard on an identity field is reliable. Put the address in the message, so that the refusal names the change.

File `policy/no-delete.cue`:

```cue
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

Rehearse on a copy of the root: delete the file `note.cue` from the copy and plan with the policy.

```sh
rm -rf trial && cp -R infra trial && rm trial/note.cue
pravo -C trial plan --policy ../policy/no-delete.cue > plan.txt 2>&1
echo "exit=$?"
sed -n '/^Plan:/,$p' plan.txt
```

Output:

```text
exit=5
Plan: 0 to add, 0 to change, 1 to destroy.
Graph: planned 0 of 1, 1 unchanged and skipped, 1 removed from the specification, 1 refreshed, 1 not read.

Error: the plan violates the policy ../policy/no-delete.cue (sha256:0c23a31e78bd):
changes.1: note: delete is not allowed:
    ../policy/no-delete.cue:7:3
```

The plan is printed in full, then the refusal. The path `changes.1` is the index of the change in the document. The exit code is 5. No plan file is written and no provider is asked to change anything.

### 2. Require with a constraint

A requirement is a constraint on a value that the plan must have. The guard says which changes it governs. A value that is absent, null or unknown does not satisfy the constraint.

File `policy/permission.cue`:

```cue
// A local file that is created or changed has the permission 0600.
changes: [...{
	mode:   string
	type:   string
	action: string
	if mode == "managed" && type == "local_file" && action != "delete" && action != "no-op" {
		after: file_permission: "0600"
	}
}]
```

The new file `extra` sets no permission, so the provider's default applies:

```sh
rm -rf trial && cp -R infra trial && cp scenarios/extra.cue trial/
pravo -C trial plan --policy ../policy/permission.cue > plan.txt 2>&1
echo "exit=$?"
sed -n '/^Plan:/,$p' plan.txt
```

Output:

```text
exit=5
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 of 3, 2 unchanged and skipped, 2 not read.

Error: the plan violates the policy ../policy/permission.cue (sha256:7d3651432df2):
changes.0.after.file_permission: conflicting values "0600" and "0777":
    ../policy/permission.cue:6:2
    ../policy/permission.cue:7:27
    plan:1:<col>
```

The plan is refused, and the message names the path, both values and the line of the policy. The guard excludes `delete` and `no-op` because those entries have no `after`. It names `mode == "managed"` because a data source of the same type has a `read` and a different shape.

Where an unset attribute is acceptable, write the alternative: `after: file_permission: null | "0600"`. A violation of that form prints `2 errors in empty disjunction`, one for each alternative.

### 3. Do not prohibit a value with an == guard

An unknown value is not equal to the forbidden one. A guard written as a comparison passes while the value is unknown. Both files below state "not 0777". The first is a guard. The second lists what is allowed.

File `policy/guard.cue`:

```cue
// Wrong: a prohibition written as an == guard.
changes: [...{
	addr:   string
	mode:   string
	type:   string
	after?: {...}
	if mode == "managed" && type == "local_file" && after != _|_ {
		if after.file_permission == "0777" {
			error("\(addr): file_permission 0777 is not allowed")
		}
	}
}]
```

File `policy/set.cue`:

```cue
// Right: the allowed set is a constraint.
changes: [...{
	mode: string
	type: string
	if mode == "managed" && type == "local_file" {
		after?: file_permission: "0600" | "0640"
	}
}]
```

The permission of `extra` comes from a new resource, so it is not known until apply:

File `scenarios/unknown.cue`:

```cue
package infra

fresh: $random.#Resource.random_pet & {length: 1}

extra: $local.#Resource.local_file & {
	filename:        "out/extra.txt"
	content:         "extra\n"
	file_permission: fresh.id
}
```

```sh
rm -rf trial && cp -R infra trial && cp scenarios/unknown.cue trial/
pravo -C trial plan --policy ../policy/guard.cue 2>&1 | tail -n 1
pravo -C trial plan --policy ../policy/set.cue > plan.txt 2>&1
echo "exit=$?"
grep -E '^(Error:|changes\.)' plan.txt
```

Output:

```text
Policy ../policy/guard.cue (sha256:2cf0a242a1ed) is satisfied.
exit=5
Error: the plan violates the policy ../policy/set.cue (sha256:2aeebc9057c0):
changes.1.after.file_permission: 2 errors in empty disjunction:
changes.1.after.file_permission: conflicting values {$unknown:true} and "0600" (mismatched types struct and string):
changes.1.after.file_permission: conflicting values {$unknown:true} and "0640" (mismatched types struct and string):
```

The guard lets the plan through. The constraint refuses it, because `{"$unknown": true}` is a struct and matches neither string. Write every prohibition as the allowed set.

### 4. Read an absent field only after a presence test

`before` is absent for a create, and `after` for a delete. Declare the field as optional and test it before you read it. A policy that reads it without the test is refused on every plan with an entry that lacks it. That includes plans that delete nothing.

File `policy/keep-wrong.cue`:

```cue
// Wrong: before is optional, and the guard reads it without a test.
changes: [...{
	addr:    string
	action:  string
	before?: {filename: string, ...}
	if action == "delete" && before.filename == "out/note.txt" {
		error("\(addr): out/note.txt must stay")
	}
}]
```

File `policy/keep.cue`:

```cue
// The file out/note.txt must stay.
changes: [...{
	addr:    string
	action:  string
	before?: {filename: string, ...}
	if before != _|_ {
		if action == "delete" && before.filename == "out/note.txt" {
			error("\(addr): out/note.txt must stay")
		}
	}
}]
```

```sh
rm -rf trial && cp -R infra trial && cp scenarios/extra.cue trial/
pravo -C trial plan --policy ../policy/keep-wrong.cue > plan.txt 2>&1
echo "exit=$?"
grep -E '^(Error:|changes\.)' plan.txt
pravo -C trial plan --policy ../policy/keep.cue 2>&1 | tail -n 1
```

Output:

```text
exit=5
Error: the plan violates the policy ../policy/keep-wrong.cue (sha256:59f47414b2ee):
changes.0: cannot reference optional field: before:
changes.1: cannot reference optional field: before:
changes.2: cannot reference optional field: before:
Policy ../policy/keep.cue (sha256:03288f85999c) is satisfied.
```

The wrong policy refuses a plan that creates a file. The right one accepts it. Key an exception to the type, the provider and the recorded id (`before.id`). Do not key it to `addr` or `name`: they are your labels and move with `$from`.

## Try a rule on a saved plan

A saved plan is the fixture. `pravo apply <planfile> --policy` judges the plan before it acts, so a refusal changes nothing.

```sh
rm -rf trial && cp -R infra trial && cp scenarios/extra.cue trial/
pravo -C trial plan --out extra.plan > /dev/null 2>&1
pravo -C trial apply extra.plan --policy ../policy/permission.cue > apply.txt 2>&1
echo "exit=$?"
grep -E '^Error:' apply.txt
pravo -C trial state list
```

Output:

```text
exit=5
Error: the plan violates the policy ../policy/permission.cue (sha256:7d3651432df2):
note
pet
```

The state still holds two records. A plain `pravo apply --policy` is refused. A plain apply plans again while it runs, and that second pass is not the one that was judged:

```sh
pravo -C trial apply --policy ../policy/permission.cue --auto-approve
```

Output:

```text
Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"
```

`pravo show extra.plan --json` prints the saved plan as a document, for a test that needs a file.

## Run old and new together

1. Collect the changes your checks must catch: a delete, a replacement, a missing tag, an attribute outside its set.
2. Make each change on a branch, in the HCL and in the CUE.
3. Run the old check on the OpenTofu plan and the new policy on the pravo plan.
4. Record both verdicts in a table: change, old, new.
5. Resolve every disagreement before you retire the old check. The usual causes are:
   - an unknown value, which the old check passes and the policy refuses;
   - a replacement, which is a list of two actions in OpenTofu and `replace` in pravo;
   - a rule keyed to an address that `$from` moved.

A new rule can start in an advisory file. Run it in a job that is allowed to fail. Move it to the file that `--policy` names once it holds. A policy reports an existing violator only when a change touches it. To list the resources that already break a rule, ask the state:

```sh
pravo -C infra state query '[for a, r in resources if r.type == "local_file" if r.attributes.file_permission != "0600" {a}]'
```

Output:

```text
[]
```

## Wire the pipeline

The pipeline names the policy on both commands:

```sh
pravo plan --out change.plan --policy ../policy/no-delete.cue
pravo apply change.plan --policy ../policy/no-delete.cue
```

The command that acts gives the verdict. `plan` judges the document it printed. `apply <planfile>` judges the saved plan again, with the policies named at that apply. A policy file may import only the CUE standard library. It must not sit directly in the root directory.

A policy binds the runs that name it. A run that omits `--policy` is not checked. The reference lists what a policy guarantees and what it does not: [Policies](../reference/policies-overview.md).

## Recap

- Rewrite each check once as a plan policy over `changes`. Name the file with `--policy` on `plan` and `apply <planfile>`.
- Prohibit with a guard on an identity field. Require with a constraint. Write a prohibition as an allowed set. Test an optional field before you read it.
- Try a rule on a saved plan. Run the old and new checks on the same changes until their verdicts agree.

Next: [M7 Cut over](m7-cut-over.md) removes OpenTofu's claim on the objects and moves the pipeline to pravo.
