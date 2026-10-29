<!-- Generated from docs/reference/policies-plan-document.md by tools/skillgen. Do not edit. -->

# The plan document in a policy

What a rule is unified with. How absent, null and unknown values behave, and what the judged document leaves out of the one that plan --json prints.

A rule is unified with the plan document. It is the JSON that `pravo plan --json` prints, built from the plan file alone and without the fields that only the state, the clock or the invocation can give. All fields are in [The plan document](plan-document.md). This page says what matters when a rule reads them.

| Rule about | Read | Always present |
|---|---|---|
| What the plan does to a resource | `changes[].action` | yes |
| What kind of object it is | `changes[].mode`, `changes[].type` | yes |
| Which object | `changes[].addr`, `changes[].name` | yes |
| A value after the change | `changes[].after` | for `create`, `update`, `replace`, `read` |
| A value before the change | `changes[].before` | for `update`, `replace`, `delete` |
| What forces a replacement | `changes[].requires_replace` | for `replace` |
| The size of the plan | `summary.add`, `.change`, `.destroy`, `.read` | yes |
| How much of the graph was walked | `stats.*` | yes |
| The outputs | `outputs.<name>` | yes |
| The moment of the judgement | `now` | yes |

`mode` is `managed`, `data`, `ephemeral` or `call`. A value that pravo reads itself (`pravo.#Env`, `pravo.#SecretEnv`, `pravo.#RemoteOutputs`) produces no entry, so a rule over it never fires. `action` is `create`, `update`, `replace`, `delete`, `read`, `open`, `call`, `move` or `no-op`.

A `replace` is its own action. A rule that forbids `delete` alone still allows a resource to be destroyed and created again. In `summary`, a replacement counts as an addition and as a destruction.

## The schema of a rule

`rule` is checked against `pravo.#Plan`, the shape of the judged document, in the specification itself. An editor with CUE support completes `changes`, `summary` and the fields of a change, and a field that is not in the document is refused when the specification loads. `#Plan` has no `blast_radius`, `dependents`, `hints` or `trace_id` for the reason in the table below.

Names are resolved where they are written, so a list template redeclares the fields it reads. Writing them with `pravo.#Change` lets the schema check them:

```cue
no_replace: pravo.#Policy & {
	rule: {
		changes: [...pravo.#Change & {action: string, addr: string
			if action == "replace" {error("\(addr): replace is not allowed")}
		}]
	}
}
```

When a policy is added, pravo also unifies the rule with one change that holds no values, only the schema. A field that a template misspells, an action outside `#Action`, and a read of an optional field such as `before` without a test are refused then, and not at the first plan that has such a change. A condition on a value the sample does not hold is not decided, so a guard is not run.

## What the judged document leaves out

The document that a rule sees is the one `plan --json` prints with these differences. A rule that names an absent field is refused when the specification loads.

| Field | In the judged document |
|---|---|
| `summary.blast_radius` | absent: only the state can give the number |
| `changes[].dependents` | absent: the state gives the dependents |
| `hints` | absent |
| `trace_id` | absent |
| `changes[].applied` | cleared: a plan file that was applied in part is judged as a whole |
| `changes` | an empty list, never `null` |
| `created`, `actor`, `reason`, `git_revision`, `reservation`, `providers` | the values the plan file holds |
| `now` | added: the moment the plan is judged, see [The clock](#the-clock) |

The document has the rows of the plan: the nodes the plan ran. It has no entry for a node that the plan left alone, for an ephemeral resource that was opened, or for a function that was called, because a plan file holds none. A rule over attributes judges only the resources that a change touches.

Work in an empty directory. The root is `infra`.

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

## The clock

`now` is the one value in the judged document that does not come from the plan file. It is the moment the plan is judged, in UTC to the second, such as `2026-10-29T14:30:00Z`. A text of this form has a fixed width, so two of them compare as strings (`now >= "2026-12-24T00:00:00Z"`), and the standard library's `time` package reads it: `time.FormatString("Monday", now)` is the day of the week and `time.Split(now).hour` the hour.

| Judged by | `now` is |
|---|---|
| `pravo apply` | The start of the transaction, which its signature covers. |
| `pravo state verify` | The start of the transaction that is verified, so a verdict replays. |
| `plan`, `show`, the viewer | The time of the command. |
| `pravo policy test` | The time of the command, or `--at`. |

A rule reads `now` inside a list template over `changes`, as the rules of [the policy pack](policies-pack.md) do. A plan with no change in it has no element for the rule to judge, so a window does not refuse an empty plan, and a plan with no resource change is not judged at all. A rule that tests `now` at the top level is not decided when the specification loads, and is refused for that.

## Absent, null and unknown

A value that a rule requires can fail to be a value in three ways. None of them is the same as a value that is present.

| State | In the document | Example |
|---|---|---|
| Absent | The field is not there. | `before` of a create. `after` of a delete. A boolean that is false. |
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

A plan whose specification declares no resources has `changes: []`, an empty list and never `null`. A plan with no resource change, such as this one, is not judged:

```sh
sed -i.bak 's/length:  9/length:  8/' infra/main.cue
pravo -C infra plan --json 2>/dev/null | jq -c '{changes}'
```

Output:

```text
{"changes":[]}
```

An empty plan satisfies a prohibition, because there is nothing to violate, and a requirement over `changes` has nothing to require.

## What apply judges

`pravo apply` builds the document from the plan file when it judges, and judges the whole file: the changes that earlier stages applied, and the changes that `--only` leaves out, are judged again. These fields describe the file and not the command that applies it: `created`, `actor`, `reason`, `git_revision`. A plan made with `--actor agent` and applied with `--actor ci` is judged as `actor: "agent"`. The labels are the caller's words in any case, and a rule over them binds nothing.

The entries with the actions `open` and `call` are not written to a saved plan. A rule over `mode == "ephemeral"` or `mode == "call"` is judged when the plan is made and not again when the saved plan is applied.

## See also

- [The plan document](plan-document.md) lists every field.
- [Writing policies](policies-writing.md) shows the rules that read these fields.
- [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md) says what the document holds.
