<!-- Generated from docs/reference/policies-plan-document.md by tools/skillgen. Do not edit. -->

# The plan document in a policy

What a policy is unified with. How absent, null and unknown values behave, and what differs when a saved plan is applied.

A policy is unified with the plan document, the JSON that `pravo plan --json` prints. All fields are in [The plan document](plan-document.md). The printed document lists the nodes the plan ran; a policy is judged against the document that `pravo plan --json --include-skipped` prints, which has an entry for every node of the specification. This page says what matters when a rule reads them.

| Rule about | Read | Always present |
|---|---|---|
| What the plan does to a resource | `changes[].action` | yes |
| What kind of object it is | `changes[].mode`, `changes[].type` | yes |
| Which object | `changes[].addr`, `changes[].name` | yes |
| A value after the change | `changes[].after` | for `create`, `update`, `replace`, `read` |
| A value before the change | `changes[].before` | for `update`, `replace`, `delete` |
| What forces a replacement | `changes[].requires_replace` | for `replace` |
| Who depends on the object | `changes[].dependents` | for a pending change, when there are any |
| The size of the plan | `summary.add`, `.change`, `.destroy`, `.read`, `.blast_radius` | yes |
| How much of the graph was walked | `stats.*` | yes |
| The outputs | `outputs.<name>` | yes |

`mode` is `managed`, `data`, `ephemeral` or `call`. A value that pravo reads itself (`pravo.#Env`, `pravo.#SecretEnv`, `pravo.#RemoteOutputs`) produces no entry, so a rule over it never fires. `action` is `create`, `update`, `replace`, `delete`, `read`, `open`, `call`, `move` or `no-op`.

A `replace` is its own action. A policy that forbids `delete` alone still allows a resource to be destroyed and created again.

Work in an empty directory. The root is `infra`, and the policies are in `policy`.

File `infra/main.cue`:

```cue
package infra

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {
	length: 2
	keepers: owner: "platform"
}

db_password: $random.#Resource.random_password & {
	length:  8
	special: false
}
```

```sh
pravo -C infra init > /dev/null
```

## Absent, null and unknown

A value that a policy requires can fail to be a value in three ways. None of them is the same as a value that is present.

| State | In the document | Example |
|---|---|---|
| Absent | The field is not there. | `before` of a create. `after` of a delete. Both for a no-op. A boolean that is false. |
| Null | The field is there with the value `null`. | An attribute that is not set: the `prefix` of the pet. |
| Unknown | The struct `{"$unknown": true}`. | A value that the provider computes at the apply: the `id` of the pet. |

An unknown value is a struct, not a string or a number. It conflicts with every constraint on a string or a number.

```sh
pravo -C infra plan --json 2>/dev/null | jq -c '.changes[] | select(.addr == "pet") | {before, after}'
```

Output:

```text
{"before":null,"after":{"id":{"$unknown":true},"keepers":{"owner":"platform"},"length":2,"prefix":null,"separator":"-"}}
```

`before` is absent, so `jq` prints `null` for it. In `after`, `prefix` is `null` and `id` is `{"$unknown": true}`.

A rule that reads a field which may be absent declares it optional and tests it first. See [Read an absent field only after a presence test](policies-writing.md#read-an-absent-field-only-after-a-presence-test).

## The document is not masked

The text of a plan hides sensitive attributes. The document does not. Sensitive attributes, and the outputs marked sensitive, appear in it as they are. Apply the root, then change the length of the password from 8 to 9:

```sh
pravo -C infra apply --auto-approve | tail -n 1
sed -i.bak 's/length:  8/length:  9/' infra/main.cue
pravo -C infra plan 2>/dev/null | grep -n 'result\|bcrypt\|length'
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
7:  ~     bcrypt_hash:      (sensitive) -> (sensitive)
9:  ~     length:           8 -> 9 # forces replacement
17:  ~     result:           (sensitive) -> (sensitive)
```

The text shows `(sensitive)`. The document shows the value:

```sh
pravo -C infra plan --json 2>/dev/null | jq '.changes[] | select(.addr == "db_password") | {addr, action, before: {length: .before.length, result: .before.result}}'
```

Output:

```text
{
  "addr": "db_password",
  "action": "replace",
  "before": {
    "length": 8,
    "result": "<secret>"
  }
}
```

Do not write rules over secrets. When a rule fails, the refusal quotes the value that failed it. Do not commit a plan document as a test fixture without scrubbing it.

## Empty plans

A plan whose specification declares no resources has `changes: []`, an empty list and never `null`. A plan with resources and nothing to do has an entry for each of them, with the action `no-op`, in the document that a policy is judged against. An empty plan, and a plan of no-ops, satisfies a prohibition, because there is nothing to violate.

File `policy/no-destroy.cue`:

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

File `empty/main.cue`:

```cue
package empty

import "pravo.dev/pravo"

region: pravo.#Env & {name: "AWS_REGION", default: "eu-west-1"}

where: pravo.#Output & {value: region.value}
```

```sh
pravo -C empty init > /dev/null
pravo -C empty plan --policy ../policy/no-destroy.cue
pravo -C empty plan --json 2>/dev/null | jq -c '{changes, summary}'
```

Output:

```text
No changes. Your infrastructure matches the configuration.
Graph: planned 0 of 1.

Outputs:
  where: "eu-west-1"
Policy ../policy/no-destroy.cue (sha256:0c23a31e78bd) is satisfied.
{"changes":[],"summary":{"add":0,"change":0,"destroy":0,"read":0,"blast_radius":0}}
```

With the resources of `infra` applied and nothing changed, the plan ran none of them: it did not send them to the provider, and the printed document lists none. The document with `--include-skipped`, which is the one a policy sees, has an entry for each, with the action `no-op`:

```sh
sed -i.bak 's/length:  9/length:  8/' infra/main.cue
pravo -C infra plan --json 2>/dev/null | jq -c '[.changes[] | {addr, action, skipped}]'
pravo -C infra plan --json --include-skipped 2>/dev/null | jq -c '[.changes[] | {addr, action, skipped}], .summary'
pravo -C infra plan --policy ../policy/no-destroy.cue 2>&1 | tail -n 1
```

Output:

```text
[]
[{"addr":"pet","action":"no-op","skipped":true},{"addr":"db_password","action":"no-op","skipped":true}]
{"add":0,"change":0,"destroy":0,"read":0,"blast_radius":0}
Policy ../policy/no-destroy.cue (sha256:0c23a31e78bd) is satisfied.
```

The entry of a node that the plan left alone has `addr`, `mode`, `type`, `name`, `action` and `skipped`, and nothing else: no `reasons`, no `dependencies`, no `before` and no `after`. The entries are in the order of the specification, so a rule over the position of an entry reads the same document whatever the plan ran. A rule over the `type` or the `addr` of a node reaches it. A rule over attributes judges only the resources that a change touches. See [Operating policies](policies-operations.md#rolling-out-and-testing) for finding the resources that already break a rule.

## What apply judges

`pravo apply <planfile> --policy FILE` builds the document from the plan file when it is applied, and judges the changes that remain. The file has no entry for a node that the plan left alone. The document has one for it, built from the record in the state, as in `pravo plan --include-skipped`. These fields describe the apply, not the command that made the plan file: `created`, `actor`, `reason`, `git_revision` and `trace_id`. A plan made with `--actor agent` and applied with `--actor ci` is judged as `actor: "ci"`. The labels are the caller's words in any case, and a rule over them binds nothing.

The entries with the actions `open` and `call` are not written to a saved plan. A rule over `mode == "ephemeral"` or `mode == "call"` is judged when the plan is made and not again when the saved plan is applied. The document that `plan` and `apply` judge never carries `reservation`.

## See also

- [The plan document](plan-document.md) lists every field.
- [Writing policies](policies-writing.md) shows the rules that read these fields.
- [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) says what the document holds.
