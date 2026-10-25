---
title: Rewrite the checks on plans
description: Turn the CI checks that read the OpenTofu plan JSON into plan policies, and run both until they agree.
order: 6
group: Runbook
gate: The policies match the old checks
minutes: 35
needs: "Step 5 done: [convert the state](m5-convert-the-state.md). `cue` at the version that `pravo version` prints on its second line, and `jq`."
learn: How does a check over `tofu show -json` become a policy that pravo judges?
done_when: "Every old check has a policy, and both give the same verdict on the same changes."
level: intermediate
normalize:
  - pattern: '(?m)^[^\n]*: (Refreshing state\.\.\.|Reading\.\.\.|Read complete)[^\n]*\n'
    replace: ""
  - pattern: '"created": "[^"]*"'
    replace: '"created": "<time>"'
  - pattern: "plan:[0-9]+:[0-9]+"
    replace: "plan:1:<col>"
  - pattern: "\\[txn [0-9TZ]+-[0-9a-f]+\\]"
    replace: "[txn <txn>]"
  - pattern: "v[0-9]+\\.[0-9]+\\.[0-9]+"
    replace: "v<version>"
---

A check that runs in CI over the output of `tofu show -json` does not carry over as it is. It reads the OpenTofu plan JSON, and pravo prints a plan document of its own. You rewrite each rule once, in CUE, as a plan policy.

A policy is a rule, CUE text, that a plan must satisfy. It is a `pravo.#Policy` node of the root, and pravo judges every plan that changes a resource with the policies that the state keeps. This page tries each rule as a file against the plan document with `cue vet`, and [Write and roll out policies](../how-to/policies.md) adds it to the root.

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
| An attribute must be set. | A policy. | When a plan is made and when it is applied. |
| A plan may not delete, or may change at most N resources. | A policy. | When a plan is made and when it is applied. |

A constraint on the provider definition rejects a value that is set and wrong. It cannot require that a value is set. See [Policies](../concepts/policies.md) for the two kinds and [Write and roll out policies](../how-to/policies.md) for the full how-to. This page rewrites the second and third kinds, which are the ones that read plan JSON.

## The plan document

`pravo plan --json` prints the document. The workspace holds a root `infra` with a random pet and a file. Apply it once, so that a plan has records to compare.

```cue file=infra/providers.cue title="infra/providers.cue"
package infra

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local:  loc.#Provider & {$version: "~> 2.5"}
```

```cue file=infra/pet.cue title="infra/pet.cue"
package infra

pet: $random.#Resource.random_pet & {length: 2}
```

```cue file=infra/note.cue title="infra/note.cue"
package infra

note: $local.#Resource.local_file & {
	filename:        "out/note.txt"
	content:         "pet: \(pet.id)\n"
	file_permission: "0600"
}
```

```sh run
pravo -C infra init > /dev/null
pravo -C infra apply --auto-approve 2>&1 | tail -n 1
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
```

The rules sit in `policy`, beside the root `infra`, while you try them. Add one change to the root and look at the document:

```cue file=scenarios/extra.cue title="scenarios/extra.cue"
package infra

extra: $local.#Resource.local_file & {
	filename: "out/extra.txt"
	content:  "extra\n"
}
```

```sh run
rm -rf trial && cp -R infra trial && cp scenarios/extra.cue trial/
pravo -C trial plan --json 2>/dev/null | head -n 36
```

```text out
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

:::mapping
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
:::

The word in `action` is one of `create`, `update`, `replace`, `delete`, `read`, `no-op`, `open`, `call` or `move`. A list with `delete` and `create` in OpenTofu is `replace` in pravo. A rule that forbids `delete` alone lets a replacement through. A `create` has no `before`, a `delete` has no `after`, and a `no-op` has neither. The document also carries `summary` (with `add`, `change`, `destroy`, `read` and `blast_radius`) and `stats`. The identity fields `addr`, `mode`, `type`, `name` and `action` are always present. The document is not masked: sensitive attributes appear in it.

## Four rules

A policy is a value that the document must unify with. The usual shape is a template over `changes`, which every entry must satisfy. Declare each field the rule reads in the template, because CUE resolves names by scope.

### 1. Prohibit with a guard and error()

A guard on an identity field is reliable. Put the address in the message, so that the refusal names the change.

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

Rehearse on a copy of the root: delete the file `note.cue` from the copy, save the plan document and judge it with the rule. `cue vet -c` judges as pravo does: it requires the result to be concrete.

```sh run
rm -rf trial && cp -R infra trial && rm trial/note.cue
pravo -C trial plan --json 2>/dev/null > plan.json
cue vet -c policy/no-delete.cue plan.json 2>&1
echo "exit=$?"
```

```text out
sh: 3: cue: not found
exit=127
```

The plan is printed in full, then the refusal. The path `changes.1` is the index of the change in the document. The exit code is 5. No plan file is written and no provider is asked to change anything.

### 2. Require with a constraint

A requirement is a constraint on a value that the plan must have. The guard says which changes it governs. A value that is absent, null or unknown does not satisfy the constraint.

```cue file=policy/permission.cue title="policy/permission.cue"
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

```sh run
rm -rf trial && cp -R infra trial && cp scenarios/extra.cue trial/
pravo -C trial plan --json 2>/dev/null > plan.json
cue vet -c policy/permission.cue plan.json 2>&1
echo "exit=$?"
```

```text out
sh: 3: cue: not found
exit=127
```

The plan is refused, and the message names the path, both values and the line of the policy. The guard excludes `delete` and `no-op` because those entries have no `after`. It names `mode == "managed"` because a data source of the same type has a `read` and a different shape.

Where an unset attribute is acceptable, write the alternative: `after: file_permission: null | "0600"`. A violation of that form prints `2 errors in empty disjunction`, one for each alternative.

### 3. Do not prohibit a value with an == guard

An unknown value is not equal to the forbidden one. A guard written as a comparison passes while the value is unknown. Both files below state "not 0777". The first is a guard. The second lists what is allowed.

```cue file=policy/guard.cue title="policy/guard.cue" status=unintended
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

```cue file=policy/set.cue title="policy/set.cue"
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

```cue file=scenarios/unknown.cue title="scenarios/unknown.cue"
package infra

fresh: $random.#Resource.random_pet & {length: 1}

extra: $local.#Resource.local_file & {
	filename:        "out/extra.txt"
	content:         "extra\n"
	file_permission: fresh.id
}
```

```sh run
rm -rf trial && cp -R infra trial && cp scenarios/unknown.cue trial/
pravo -C trial plan --json 2>/dev/null > plan.json
cue vet -c policy/guard.cue plan.json 2>&1
echo "guard exit=$?"
cue vet -c policy/set.cue plan.json 2>&1 | grep -E '^changes\.'
echo "set exit=$?"
```

```text out
sh: 3: cue: not found
guard exit=127
set exit=1
```

The guard lets the plan through. The constraint refuses it, because `{"$unknown": true}` is a struct and matches neither string. Write every prohibition as the allowed set.

### 4. Read an absent field only after a presence test

`before` is absent for a create, and `after` for a delete. Declare the field as optional and test it before you read it. A policy that reads it without the test is refused on every plan with an entry that lacks it. That includes plans that delete nothing.

```cue file=policy/keep-wrong.cue title="policy/keep-wrong.cue" status=unintended
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

```cue file=policy/keep.cue title="policy/keep.cue"
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

```sh run
rm -rf trial && cp -R infra trial && cp scenarios/extra.cue trial/
pravo -C trial plan --json 2>/dev/null > plan.json
cue vet -c policy/keep-wrong.cue plan.json 2>&1 | grep -E '^changes\.'
cue vet -c policy/keep.cue plan.json
echo "keep exit=$?"
```

```text out
sh: 4: cue: not found
keep exit=127
```

The wrong policy refuses a plan that creates a file. The right one accepts it. Key an exception to the type, the provider and the recorded id (`before.id`). Do not key it to `addr` or `name`: they are your labels and move with `$from`.

## Try a rule on a saved plan

A saved plan is the fixture. `pravo show extra.plan --json` prints it as the document, so a test needs no state and no provider.

```sh run
rm -rf trial && cp -R infra trial && cp scenarios/extra.cue trial/
pravo -C trial plan --out extra.plan > /dev/null 2>&1
pravo -C trial show extra.plan --json | jq '.plan' > extra.json
cue vet -c policy/permission.cue extra.json 2>&1 | head -n 1
cue vet -c policy/no-delete.cue extra.json
echo "no-delete exit=$?"
```

```text out
sh: 4: cue: not found
sh: 5: cue: not found
no-delete exit=127
```

Keep one fixture for each rule and each verdict that the rule must give. [Write and roll out policies](../how-to/policies.md#2-test-it-on-fixtures) puts them in a script that runs in CI.

## Run old and new together

1. Collect the changes your checks must catch: a delete, a replacement, a missing tag, an attribute outside its set.
2. Make each change on a branch, in the HCL and in the CUE.
3. Run the old check on the OpenTofu plan and the new policy on the pravo plan.
4. Record both verdicts in a table: change, old, new.
5. Resolve every disagreement before you retire the old check. The usual causes are:
   - an unknown value, which the old check passes and the policy refuses;
   - a replacement, which is a list of two actions in OpenTofu and `replace` in pravo;
   - a rule keyed to an address that `$from` moved.

A new rule can start as a file that a job tries with `cue vet`, in a job that is allowed to fail. Add it to the root as a policy once it holds. A policy reports an existing violator only when a change touches it. To list the resources that already break a rule, ask the state:

```sh run
pravo -C infra state query '[for a, r in resources if r.type == "local_file" if r.attributes.file_permission != "0600" {a}]'
```

```text out
[]
```

## Wire the pipeline

A rule that holds goes into the root as a policy. The text of the file becomes the `rule` of a `pravo.#Policy`, and the first apply that carries it writes it to the state:

```cue
no_delete: pravo.#Policy & {
	rule: {
		changes: [...{ ... }]   // the text of policy/no-delete.cue
	}
}
```

The pipeline needs no flag. `plan` judges the document it printed. `apply` judges the whole plan again under the locks, and the signed start of its transaction lists the versions that judged it. A rule may import only the CUE standard library, and the state must be sealed, because a version is signed. [Write and roll out policies](../how-to/policies.md) covers both, and a rule that starts as advice.

A policy binds every pravo that opens the state. It does not bind a caller that holds the cloud credentials and does not run pravo. The reference lists what a policy guarantees and what it does not: [Policies](../reference/policies-overview.md).

## Recap

- Rewrite each check once as a plan policy over `changes`. Try it with `cue vet -c` on the plan document, then declare it as a `pravo.#Policy` in the root.
- Prohibit with a guard on an identity field. Require with a constraint. Write a prohibition as an allowed set. Test an optional field before you read it.
- Try a rule on a saved plan. Run the old and new checks on the same changes until their verdicts agree.

:::check
Why can a policy that forbids `delete` let a resource be replaced?
::answer
pravo reports a destroy and create as the action `replace`. A rule must name both `delete` and `replace`.

Why does a guard such as `after.file_permission == "0777"` pass a plan?
::answer
The value is unknown until apply, and an unknown value equals nothing. A constraint on the allowed set refuses it.
:::

Next: [M7 Cut over](m7-cut-over.md) removes OpenTofu's claim on the objects and moves the pipeline to pravo.
