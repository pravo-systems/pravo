<!-- Generated from docs/reference/policies-writing.md by tools/skillgen. Do not edit. -->

# Writing policies

The four rules for writing a plan policy, each with a captured run, plan-wide rules, and the typos that forbid nothing.

A policy is a CUE value that the plan document must unify with. The usual shape is a list template over `changes`: a struct that every element of the list must unify with.

```cue
changes: [...{
	addr:   string
	mode:   string
	action: string
	// guards and constraints that read these fields
}]
```

CUE resolves a name by lexical scope. A guard reads only what the template declares: `mode: string` is what makes `mode` visible to the `if` below it. Without the declaration the file does not compile, and the run is refused at load.

| Rule | Use | Failure it avoids |
|---|---|---|
| [Prohibit with a guard](#prohibit-with-a-guard) | An `if` on identity fields and `error()`. | A rule that cannot say what is wrong. |
| [Require with a constraint](#require-with-a-constraint) | A constraint on `after`. | A value that is absent, null or unknown passes. |
| [Do not prohibit a value with an equality guard](#do-not-prohibit-a-value-with-an-equality-guard) | Write the allowed set as a constraint. | An unknown value passes. |
| [Read an absent field only after a presence test](#read-an-absent-field-only-after-a-presence-test) | Declare the field optional and test it. | A refusal for the wrong reason. |

Work in an empty directory. The root is `infra`, and the policies are in `policy`. The commands print the plan before the verdict. This page shows the verdict.

File `infra/main.cue`:

```cue
package infra

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider & {$version: "~> 3.6"}

seed: $random.#Resource.random_id & {byte_length: 4}

pet: $random.#Resource.random_pet & {
	length: 2
	keepers: owner: "platform"
}

ticket: $random.#Resource.random_integer & {
	min: 1
	max: 99
	keepers: source: pet.id
}

name: pravo.#Output & {value: pet.id}
```

```sh
cp infra/main.cue base.cue
pravo -C infra init > /dev/null
pravo -C infra apply --auto-approve | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
```

## Prohibit with a guard

The identity fields (`addr`, `mode`, `type`, `name`, `action`) are always present, so a guard on them is reliable. Declare each field the guard reads, and name the change in the message.

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

Change `length` of the pet from 2 to 3 and `byte_length` of the seed from 4 to 5. The plan holds a replacement of all three resources, because `ticket` reads the pet:

```sh
sed -e 's/length: 2/length: 3/' -e 's/byte_length: 4/byte_length: 5/' base.cue > infra/main.cue
pravo -C infra plan --policy ../policy/no-destroy.cue 2>&1 | sed -n '/^Plan:/,$p'
```

Output:

```text
Plan: 3 to add, 0 to change, 3 to destroy.
Graph: planned 3 of 3, 3 refreshed.

Outputs:
  name: (known after apply)

Error: the plan violates the policy ../policy/no-destroy.cue (sha256:0c23a31e78bd):
changes.0: seed: replace is not allowed:
    ../policy/no-destroy.cue:7:3
changes.1: pet: replace is not allowed:
    ../policy/no-destroy.cue:7:3
changes.2: ticket: replace is not allowed:
    ../policy/no-destroy.cue:7:3
```

Each message is the one the policy gave to `error()`, with the address of the change. The path before it, `changes.1`, is the index of the change in the document's list. The plan is printed above these lines in full, so what is judged is what the reader saw.

Under `--json` the document is printed on standard output, and then the error object, so that a program can read both:

```sh
pravo -C infra plan --json --policy ../policy/no-destroy.cue 2>/dev/null | tail -n 1
```

Output:

```text
{"code":5,"kind":"error","message":"the plan violates the policy ../policy/no-destroy.cue (sha256:0c23a31e78bd):\nchanges.0: seed: replace is not allowed:\n    ../policy/no-destroy.cue:7:3\nchanges.1: pet: replace is not allowed:\n    ../policy/no-destroy.cue:7:3\nchanges.2: ticket: replace is not allowed:\n    ../policy/no-destroy.cue:7:3\n"}
```

With `--out`, a refused plan writes no plan file:

```sh
pravo -C infra plan --policy ../policy/no-destroy.cue --out refused.plan > /dev/null 2>&1
test -e refused.plan || echo "refused.plan does not exist"
```

Output:

```text
refused.plan does not exist
```

## Require with a constraint

A requirement is a constraint on a value the plan must have. A value that is absent, null or unknown until apply does not satisfy it. This policy requires an owner key on every pet that is created:

File `policy/owner.cue`:

```cue
// Every random_pet that is created carries an owner.
changes: [...{
	type:   string
	action: string
	if type == "random_pet" && action == "create" {
		after: keepers: owner: string & !=""
	}
}]
```

The guard (`if type == "random_pet" && action == "create"`) says which changes the requirement governs. The constraint (`string & !=""`) says what they must have. Five plans follow, each with a different `keepers` line in the `pet` block. The state is empty first, so the plans create the pet.

```sh
pravo -C infra destroy --auto-approve | grep '^Destroy complete'
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 3 destroyed. [txn <id>]
```

| Case | In `main.cue` | Verdict |
|---|---|---|
| present | `keepers: owner: "platform"` | satisfied |
| absent | `keepers: team: "storage"` | refused: the key `owner` is not there |
| null | no `keepers` line | refused: `keepers` is `null` |
| unknown | `keepers: owner: seed.hex` | refused: the value is `{"$unknown": true}` |
| empty string | `keepers: owner: ""` | refused: `!=""` excludes it |

Present:

```sh
pravo -C infra plan --policy ../policy/owner.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Policy ../policy/owner.cue (sha256:bf44cd1fce1f) is satisfied.
```

Absent:

```sh
sed 's/keepers: owner: "platform"/keepers: team: "storage"/' base.cue > infra/main.cue
pravo -C infra plan --policy ../policy/owner.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/owner.cue (sha256:bf44cd1fce1f):
changes.1.after.keepers.owner: incomplete value !="":
    ../policy/owner.cue:6:35
```

Null:

```sh
grep -v 'keepers: owner' base.cue > infra/main.cue
pravo -C infra plan --policy ../policy/owner.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/owner.cue (sha256:bf44cd1fce1f):
changes.1.after.keepers: conflicting values null and {owner:(string & !="")} (mismatched types null and struct):
    ../policy/owner.cue:5:2
    ../policy/owner.cue:6:19
    plan:1:N
```

Unknown:

```sh
sed 's/keepers: owner: "platform"/keepers: owner: seed.hex/' base.cue > infra/main.cue
pravo -C infra plan --policy ../policy/owner.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/owner.cue (sha256:bf44cd1fce1f):
changes.1.after.keepers.owner: conflicting values {$unknown:true} and string (mismatched types struct and string):
    ../policy/owner.cue:5:2
    ../policy/owner.cue:6:26
    plan:1:N
```

Empty string:

```sh
sed 's/keepers: owner: "platform"/keepers: owner: ""/' base.cue > infra/main.cue
pravo -C infra plan --policy ../policy/owner.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/owner.cue (sha256:bf44cd1fce1f):
changes.1.after.keepers.owner: invalid value "" (out of bound !=""):
    ../policy/owner.cue:6:35
    ../policy/owner.cue:6:26
    plan:1:N
```

A policy that requires a value is not satisfied by leaving it out, by a provider default that is null, or by a value that is not known until apply. `string` alone is satisfied by the empty string. Require `string & !=""` for a value that must say something.

Where an unset attribute is acceptable, write the alternative: `null | <constraint>`. This policy lets a created pet either set no `prefix` or set a lowercase one:

File `policy/prefix.cue`:

```cue
// A random_pet that is created either sets no prefix or sets a lowercase one.
changes: [...{
	type:   string
	action: string
	if type == "random_pet" && action == "create" {
		after: prefix: null | =~"^[a-z]+$"
	}
}]
```

| `prefix` in the root | Verdict |
|---|---|
| no line (null) | satisfied |
| `"app"` | satisfied |
| `"App"` | refused |
| `seed.hex` (unknown) | refused |

```sh
sed 's/length: 2/length: 2, prefix: "App"/' base.cue > infra/main.cue
pravo -C infra plan --policy ../policy/prefix.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/prefix.cue (sha256:f773a753cb48):
changes.1.after.prefix: 2 errors in empty disjunction:
changes.1.after.prefix: conflicting values "App" and null (mismatched types string and null):
    ../policy/prefix.cue:5:2
    ../policy/prefix.cue:6:18
    plan:1:N
changes.1.after.prefix: invalid value "App" (out of bound =~"^[a-z]+$"):
    ../policy/prefix.cue:6:25
    ../policy/prefix.cue:6:18
    plan:1:N
```

A violation of this form prints `2 errors in empty disjunction`, one error for each alternative. An unknown value fails both, as it must: whether it is null or lowercase cannot be said before the apply.

## Do not prohibit a value with an equality guard

An unknown value is not equal to anything, so a guard that compares it with the forbidden value finds it unequal, and the plan passes. These two policies state the same rule. The first prohibits a dot as the separator with a guard. The second allows `-` and `_` and nothing else.

File `policy/separator-guard.cue`:

```cue
// Wrong: a prohibition written as an == guard.
changes: [...{
	addr:   string
	type:   string
	after?: {...}
	if type == "random_pet" && after != _|_ {
		if after.separator == "." {
			error("\(addr): the separator must not be a dot")
		}
	}
}]
```

File `policy/separator-set.cue`:

```cue
// Right: the allowed set is written as a constraint.
changes: [...{
	type: string
	if type == "random_pet" {
		after?: separator: "-" | "_"
	}
}]
```

| `separator` | guard | constraint |
|---|---|---|
| `"."` | refused | refused |
| `"-"` | satisfied | satisfied |
| `seed.hex` | **satisfied** | refused |

With the separator `"."`, both refuse:

```sh
sed 's/length: 2/length: 2, separator: "."/' base.cue > infra/main.cue
pravo -C infra plan --policy ../policy/separator-guard.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
pravo -C infra plan --policy ../policy/separator-set.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/separator-guard.cue (sha256:4e31fa112b22):
changes.1: pet: the separator must not be a dot:
    ../policy/separator-guard.cue:8:4

Error: the plan violates the policy ../policy/separator-set.cue (sha256:769f5b89f838):
changes.1.after.separator: 2 errors in empty disjunction:
changes.1.after.separator: conflicting values "-" and ".":
    ../policy/separator-set.cue:4:2
    ../policy/separator-set.cue:5:22
    plan:1:N
changes.1.after.separator: conflicting values "_" and ".":
    ../policy/separator-set.cue:4:2
    ../policy/separator-set.cue:5:28
    plan:1:N
```

With `seed.hex`, which nobody can read yet, the guard lets the plan through and the constraint refuses it:

```sh
sed 's/length: 2/length: 2, separator: seed.hex/' base.cue > infra/main.cue
pravo -C infra plan --policy ../policy/separator-guard.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
pravo -C infra plan --policy ../policy/separator-set.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Policy ../policy/separator-guard.cue (sha256:4e31fa112b22) is satisfied.
Error: the plan violates the policy ../policy/separator-set.cue (sha256:769f5b89f838):
changes.1.after.separator: 2 errors in empty disjunction:
changes.1.after.separator: conflicting values {$unknown:true} and "-" (mismatched types struct and string):
    ../policy/separator-set.cue:4:2
    ../policy/separator-set.cue:5:22
    plan:1:N
changes.1.after.separator: conflicting values {$unknown:true} and "_" (mismatched types struct and string):
    ../policy/separator-set.cue:4:2
    ../policy/separator-set.cue:5:28
    plan:1:N
```

Write the allowed set as a constraint, and an unknown value is refused until it is known.

## Read an absent field only after a presence test

Reading a field that may be absent is an error that CUE reports as `cannot reference optional field`, and it refuses the plan for the wrong reason. Declare the field optional and test it first.

File `policy/before-bare.cue`:

```cue
// Wrong: before is read without a presence test.
changes: [...{
	addr:    string
	before?: {id: string}
	if before.id == "desired-mosquito" {
		error("\(addr): this object may not be touched")
	}
}]
```

File `policy/before-tested.cue`:

```cue
// Right: before is read only after a presence test.
changes: [...{
	addr:    string
	before?: {id: string}
	if before != _|_ {
		if before.id == "desired-mosquito" {
			error("\(addr): this object may not be touched")
		}
	}
}]
```

Against a plan whose changes are creates, which have no `before`:

```sh
cp base.cue infra/main.cue
pravo -C infra plan --policy ../policy/before-bare.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
pravo -C infra plan --policy ../policy/before-tested.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/before-bare.cue (sha256:73ae9a966458):
changes.0: cannot reference optional field: before:
    ../policy/before-bare.cue:5:5
changes.1: cannot reference optional field: before:
    ../policy/before-bare.cue:5:5
changes.2: cannot reference optional field: before:
    ../policy/before-bare.cue:5:5

Policy ../policy/before-tested.cue (sha256:c54ae37ee36f) is satisfied.
```

The first policy refuses every plan that has a change without a `before`, which is nearly every plan. The second reads `before.id` only when `before` exists.

An omitted boolean needs a default in the template, for the same reason: `drifted` is not in the document when it is false.

File `policy/drift-bare.cue`:

```cue
// Wrong: drifted is omitted from the document when it is false.
changes: [...{
	addr:     string
	drifted?: bool
	if drifted {
		error("\(addr): has drifted")
	}
}]
```

File `policy/drift-default.cue`:

```cue
// Right: an omitted boolean has a default in the template.
changes: [...{
	addr:    string
	drifted: *false | bool
	if drifted {
		error("\(addr): has drifted")
	}
}]
```

```sh
pravo -C infra plan --policy ../policy/drift-bare.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
pravo -C infra plan --policy ../policy/drift-default.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/drift-bare.cue (sha256:9b3387b6279a):
changes.0: cannot reference optional field: drifted:
    ../policy/drift-bare.cue:5:5
changes.1: cannot reference optional field: drifted:
    ../policy/drift-bare.cue:5:5
changes.2: cannot reference optional field: drifted:
    ../policy/drift-bare.cue:5:5

Policy ../policy/drift-default.cue (sha256:f9c436a2933d) is satisfied.
```

The default `*false | bool` supplies `false` where the field is omitted and takes the document's `true` where it is there. The same holds for `deferred`, `skipped`, `refreshed`, `drifted` and `applied`.

## Plan-wide rules

`summary` and `stats` hold counts, and a bound on them limits a plan as a whole:

File `policy/limit.cue`:

```cue
// A plan touches at most two nodes.
summary: blast_radius: <=2
```

```sh
pravo -C infra plan --policy ../policy/limit.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy ../policy/limit.cue (sha256:b8d1f7b6cd67):
summary.blast_radius: invalid value 3 (out of bound <=2):
    ../policy/limit.cue:2:24
    plan:1:N
```

`blast_radius` is read from the state at the moment of the plan. A limit binds one plan: twenty plans of five changes each pass `blast_radius: <=5`. It is a brake on the size of one change set and not a budget.

Rules over `action`, `mode`, `type` and the counts are what the plan policy is for, and so is a rule that an attribute is set, such as the owner above. A rule about the value that an attribute may take can also be written on the provider definition: see [Providers](spec-providers.md#provider-scoped-constraints). A specification constraint cannot require that an attribute is set.

## Mistakes that forbid nothing

CUE does not know what a policy was meant to say. Four typos, each run against a plan that destroys everything:

File `policy/typo-literal.cue`:

```cue
// The value is misspelt: no change has the action "delet".
changes: [...{action: !="delet"}]
```

File `policy/typo-template.cue`:

```cue
// The field is misspelt: the document has no "chnges".
chnges: [...{action: !="delete"}]
```

File `policy/typo-constraint.cue`:

```cue
// The field is misspelt: the document has no "sumary".
sumary: blast_radius: <=5
```

File `policy/typo-concrete.cue`:

```cue
// The field is misspelt: the document has no "destory", and the value is concrete.
summary: destory: 0
```

```sh
pravo -C infra apply --auto-approve | grep '^Apply complete'
for p in typo-literal typo-template typo-constraint typo-concrete; do
  echo "$p"
  pravo -C infra plan --destroy --policy ../policy/$p.cue 2>&1 | grep -A20 -E '^(Policy|Error)'
done
```

Output:

```text
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
typo-literal
Policy ../policy/typo-literal.cue (sha256:e7cd66e0dfc0) is satisfied.
typo-template
Policy ../policy/typo-template.cue (sha256:167504bf1469) is satisfied.
typo-constraint
Error: the plan violates the policy ../policy/typo-constraint.cue (sha256:af32e6414ce4):
sumary.blast_radius: incomplete value <=5:
    ../policy/typo-constraint.cue:2:23

typo-concrete
Policy ../policy/typo-concrete.cue (sha256:c1ca4d6eaec1) is satisfied.
```

- A misspelt literal (`"delet"`) forbids nothing, because no change has that action.
- A misspelt list template (`chnges`) forbids nothing, because the document has no such field and the list is empty.
- A misspelt field with a concrete value (`destory: 0`) forbids nothing. It adds a field that holds.
- A misspelt bound, type or pattern (`sumary: blast_radius: <=5`) refuses every plan, because the document has no such field and the bound never becomes a value.

Only the last of the four is noticed. Test a policy against a plan it should refuse before relying on it. See [Rolling out and testing](policies-operations.md#rolling-out-and-testing).

## See also

- [The plan document in a policy](policies-plan-document.md) says what absent, null and unknown mean.
- [Operating policies](policies-operations.md) covers where each command judges a policy.
- [Write and roll out plan policies](../how-to/policies.md) tests a policy on fixtures.
