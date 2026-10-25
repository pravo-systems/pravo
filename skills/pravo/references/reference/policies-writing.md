<!-- Generated from docs/reference/policies-writing.md by tools/skillgen. Do not edit. -->

# Writing policies

The four rules for writing a policy rule, each with a captured run, plan-wide rules, and the typos that forbid nothing.

A rule is a CUE value that the plan document must unify with. The usual shape is a list template over `changes`: a struct that every element of the list must unify with.

```cue
changes: [...{
	addr:   string
	mode:   string
	action: string
	// guards and constraints that read these fields
}]
```

CUE resolves a name by lexical scope. A guard reads only what the template declares: `mode: string` is what makes `mode` visible to the `if` below it. Without the declaration the rule does not compile, and the plan that adds it is refused.

| Rule | Use | Failure it avoids |
|---|---|---|
| [Prohibit with a guard](#prohibit-with-a-guard) | An `if` on identity fields and `error()`. | A rule that cannot say what is wrong. |
| [Require with a constraint](#require-with-a-constraint) | A constraint on `after`. | A value that is absent, null or unknown passes. |
| [Do not prohibit a value with an equality guard](#do-not-prohibit-a-value-with-an-equality-guard) | Write the allowed set as a constraint. | An unknown value passes. |
| [Read an absent field only after a presence test](#read-an-absent-field-only-after-a-presence-test) | Declare the field optional and test it. | A refusal for the wrong reason. |

Work in an empty directory. The root is `infra`, and the rules are in `policy`. The state is local and sealed to ada. The root has one policy, `check`, and `set-rule.sh` makes a file the rule of `check`: it restores the base specification, writes the rule into a policy node and applies. Each example below changes `check`, then changes the specification and reads the verdict of `plan`.

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

File `set-rule.sh`:

```sh
#!/bin/sh
# set-rule.sh FILE: restore base.cue, make FILE the rule of the policy "check" and apply.
cp base.cue infra/main.cue
{
	printf 'package infra\n\nimport "pravo.dev/pravo"\n\ncheck: pravo.#Policy & {rule: #"""\n'
	sed 's/^\(.\)/\t\1/' "$1"
	printf '\t"""#}\n'
} > infra/policy.cue
pravo -C infra apply --auto-approve 2>&1 | grep -A4 '^Error'
```

File `with-extra.sh`:

```sh
#!/bin/sh
# with-extra.sh LINE...: base.cue plus one more random_pet for each LINE, as its extra attributes.
cp base.cue infra/main.cue
n=0
for line in "$@"; do
	n=$((n + 1))
	printf '\nextra%d: $random.#Resource.random_pet & {length: 2, %s}\n' "$n" "$line" >> infra/main.cue
done
```

```sh
cp infra/main.cue base.cue
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

The first apply with the rule writes it to the state. The plan that adds it is not judged by it:

```sh
./set-rule.sh policy/no-destroy.cue
ls infra/pravo.state/policy
```

Output:

```text
check@N.json
```

Change `length` of the pet from 2 to 3 and `byte_length` of the seed from 4 to 5. The plan holds a replacement of all three resources, because `ticket` reads the pet:

```sh
sed -e 's/length: 2/length: 3/' -e 's/byte_length: 4/byte_length: 5/' base.cue > infra/main.cue
pravo -C infra plan 2>&1 | sed -n '/^Plan:/,$p'
```

Output:

```text
Plan: 3 to add, 0 to change, 3 to destroy.
Graph: planned 3 · refreshed 3

Outputs:
  name: (known after apply)

Error: the plan violates the policy check@N (sha256:20bbfeca6bff):
changes.0: seed: replace is not allowed:
    check:7:3
changes.1: pet: replace is not allowed:
    check:7:3
changes.2: ticket: replace is not allowed:
    check:7:3
```

Each message is the one the rule gave to `error()`, with the address of the change. The path before it, `changes.1`, is the index of the change in the document's list. The plan is printed above these lines in full, so what is judged is what the reader saw.

Under `--json` the document is printed on standard output, and then the error object, so that a program can read both:

```sh
pravo -C infra plan --json 2>/dev/null | tail -n 1
```

Output:

```text
{"code":5,"kind":"error","message":"the plan violates the policy check@N (sha256:20bbfeca6bff):\nchanges.0: seed: replace is not allowed:\n    check:7:3\nchanges.1: pet: replace is not allowed:\n    check:7:3\nchanges.2: ticket: replace is not allowed:\n    check:7:3\n"}
```

With `--out`, a refused plan writes no plan file:

```sh
pravo -C infra plan --out refused.plan > /dev/null 2>&1
test -e refused.plan || echo "refused.plan does not exist"
```

Output:

```text
refused.plan does not exist
```

## Require with a constraint

A requirement is a constraint on a value the plan must have. A value that is absent, null or unknown until apply does not satisfy it. This rule requires an owner key on every pet that is created:

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

The guard (`if type == "random_pet" && action == "create"`) says which changes the requirement governs. The constraint (`string & !=""`) says what they must have. Five plans follow, each adding a second pet with a different `keepers` line. The pet `pet` is in the state and unchanged, so only the new pet is judged. The unknown case adds two pets: the second reads the `id` of the first, which the provider computes at the apply.

| Case | Added line | Verdict |
|---|---|---|
| present | `keepers: owner: "platform"` | satisfied |
| absent | `keepers: team: "storage"` | refused: the key `owner` is not there |
| null | none | refused: `keepers` is `null` |
| unknown | `keepers: owner: extra1.id` | refused: the value is `{"$unknown": true}` |
| empty string | `keepers: owner: ""` | refused: `!=""` excludes it |

Present:

```sh
./set-rule.sh policy/owner.cue
./with-extra.sh 'keepers: owner: "platform"'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Policy check@N:4578c4749e3b453c66a554d4583f947134be34d4b8eae59c17ae179badf17671 is satisfied.
```

Absent:

```sh
./with-extra.sh 'keepers: team: "storage"'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:4578c4749e3b):
changes.0.after.keepers.owner: incomplete value !="":
    check:6:35
```

Null:

```sh
./with-extra.sh ''
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:4578c4749e3b):
changes.0.after.keepers: conflicting values null and {owner:(string & !="")} (mismatched types null and struct):
    check:5:2
    check:6:19
    plan:1:N
```

Unknown:

```sh
./with-extra.sh 'keepers: owner: "platform"' 'keepers: owner: extra1.id'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:4578c4749e3b):
changes.1.after.keepers.owner: conflicting values {$unknown:true} and string (mismatched types struct and string):
    check:5:2
    check:6:26
    plan:1:N
```

Empty string:

```sh
./with-extra.sh 'keepers: owner: ""'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:4578c4749e3b):
changes.0.after.keepers.owner: invalid value "" (out of bound !=""):
    check:6:35
    check:6:26
    plan:1:N
```

A rule that requires a value is not satisfied by leaving it out, by a provider default that is null, or by a value that is not known until apply. `string` alone is satisfied by the empty string. Require `string & !=""` for a value that must say something.

Where an unset attribute is acceptable, write the alternative: `null | <constraint>`. This rule lets a created pet either set no `prefix` or set a lowercase one:

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

| `prefix` of the added pet | Verdict |
|---|---|
| no line (null) | satisfied |
| `"app"` | satisfied |
| `"App"` | refused |
| `extra1.id` (unknown) | refused |

```sh
./set-rule.sh policy/prefix.cue
./with-extra.sh 'prefix: "App"'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:f0b587b826cd):
changes.0.after.prefix: 2 errors in empty disjunction:
changes.0.after.prefix: conflicting values "App" and null (mismatched types string and null):
    check:5:2
    check:6:18
    plan:1:N
changes.0.after.prefix: invalid value "App" (out of bound =~"^[a-z]+$"):
    check:6:25
    check:6:18
    plan:1:N
```

A violation of this form prints `2 errors in empty disjunction`, one error for each alternative. An unknown value fails both, as it must: whether it is null or lowercase cannot be said before the apply.

## Do not prohibit a value with an equality guard

An unknown value is not equal to anything, so a guard that compares it with the forbidden value finds it unequal, and the plan passes. These two rules state the same requirement. The first prohibits a dot as the separator with a guard. The second allows `-` and `_` and nothing else.

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
| `extra1.id` | **satisfied** | refused |

With the separator `"."`, both refuse:

```sh
./set-rule.sh policy/separator-guard.cue
./with-extra.sh 'separator: "."'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
./set-rule.sh policy/separator-set.cue
./with-extra.sh 'separator: "."'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:c11567fc54d1):
changes.0: extra1: the separator must not be a dot:
    check:8:4

Error: the plan violates the policy check@N (sha256:4b4a82eeb28b):
changes.0.after.separator: 2 errors in empty disjunction:
changes.0.after.separator: conflicting values "-" and ".":
    check:4:2
    check:5:22
    plan:1:N
changes.0.after.separator: conflicting values "_" and ".":
    check:4:2
    check:5:28
    plan:1:N
```

With `extra1.id`, which nobody can read before the apply, the guard lets the plan through and the constraint refuses it:

```sh
./set-rule.sh policy/separator-guard.cue
./with-extra.sh 'separator: "-"' 'separator: extra1.id'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
./set-rule.sh policy/separator-set.cue
./with-extra.sh 'separator: "-"' 'separator: extra1.id'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Policy check@N:c11567fc54d1fb0bb71afd3a3bcae9d68bfdf031ece4249ff74ef7bddbdd1308 is satisfied.
Error: the plan violates the policy check@N (sha256:4b4a82eeb28b):
changes.1.after.separator: 2 errors in empty disjunction:
changes.1.after.separator: conflicting values {$unknown:true} and "-" (mismatched types struct and string):
    check:4:2
    check:5:22
    plan:1:N
changes.1.after.separator: conflicting values {$unknown:true} and "_" (mismatched types struct and string):
    check:4:2
    check:5:28
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

Against a plan whose change is a create, which has no `before`:

```sh
./set-rule.sh policy/before-bare.cue
./with-extra.sh ''
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
./set-rule.sh policy/before-tested.cue
./with-extra.sh ''
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:bd8ad62462e6):
changes.0: cannot reference optional field: before:
    check:5:5

Policy check@N:23eb1edd2014a7d35a330128bb80702a2c8e5fea3c5215f9d6cfa3889db618b9 is satisfied.
```

The first rule refuses every plan that has a change without a `before`, which is nearly every plan. The second reads `before.id` only when `before` exists.

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
./set-rule.sh policy/drift-bare.cue
./with-extra.sh ''
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
./set-rule.sh policy/drift-default.cue
./with-extra.sh ''
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:cd2366535577):
changes.0: cannot reference optional field: drifted:
    check:5:5

Policy check@N:d3801bbdee3118476dd314afdb2076afae1e3cf962eaead97b64e72fa6b65288 is satisfied.
```

The default `*false | bool` supplies `false` where the field is omitted and takes the document's `true` where it is there. The same holds for `deferred`, `skipped`, `refreshed`, `drifted` and `applied`.

## Plan-wide rules

`summary` and `stats` hold counts, and a bound on them limits a plan as a whole. A replacement counts as an addition and as a destruction:

File `policy/limit.cue`:

```cue
// A plan adds at most one resource.
summary: add: <=1
```

```sh
./set-rule.sh policy/limit.cue
./with-extra.sh 'keepers: owner: "a"' 'keepers: owner: "b"'
pravo -C infra plan 2>&1 | grep -A20 -E '^(Policy|Error)'
```

Output:

```text
Error: the plan violates the policy check@N (sha256:3707bf6d655d):
summary.add: invalid value 2 (out of bound <=1):
    check:2:15
    plan:1:N
```

A limit binds one plan: twenty plans of five changes each pass `summary: add: <=5`. It is a brake on the size of one change set and not a budget. The document has no `summary.blast_radius`, because the number comes from the state and the document is built from the plan file alone. A rule that names it is refused when it is added.

Rules over `action`, `mode`, `type` and the counts are what a policy is for, and so is a rule that an attribute is set, such as the owner above. A rule about the value that an attribute may take can also be written on the provider definition: see [Providers](spec-providers.md#provider-scoped-constraints). A specification constraint cannot require that an attribute is set.

## Mistakes that forbid nothing

CUE does not know what a rule was meant to say. Four typos, each in a rule that is added and then run against a plan that destroys everything:

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
for p in typo-literal typo-template typo-concrete; do
  echo "$p"
  ./set-rule.sh policy/$p.cue > /dev/null
  pravo -C infra plan --destroy 2>&1 | grep -A20 -E '^(Policy|Error)'
done
echo typo-constraint
./set-rule.sh policy/typo-constraint.cue
```

Output:

```text
typo-literal
Policy check@N:13103e9ee57fc5805e81059b386c6d14704b4eacf6fb04bfa1652eea5053a080 is satisfied.
typo-template
Policy check@N:32522cf5f893452a1d942707eb11adae0731cacc614539039d0ecd9a31f2d1a6 is satisfied.
typo-concrete
Policy check@N:d3792fb9010b8e435c3456adbe37ffbf47e9785dba736ffb4a8806fd76a727b5 is satisfied.
typo-constraint
Error: policy check does not accept the document of an empty plan:
sumary.blast_radius: incomplete value <=5:
    check:2:23
```

- A misspelt literal (`"delet"`) forbids nothing, because no change has that action.
- A misspelt list template (`chnges`) forbids nothing, because the document has no such field and the list is empty.
- A misspelt field with a concrete value (`destory: 0`) forbids nothing. It adds a field that holds.
- A misspelt bound, type or pattern (`sumary: blast_radius: <=5`) is refused when the rule is added, because the document of an empty plan has no such field and the bound never becomes a value.

Only the last of the four is noticed. Test a rule against a plan it should refuse before relying on it. See [Rolling out and testing](policies-operations.md#rolling-out-and-testing).

## See also

- [The plan document in a policy](policies-plan-document.md) says what absent, null and unknown mean.
- [Operating policies](policies-operations.md) covers where each command judges a policy.
- [Write and roll out policies](../how-to/policies.md) tests a rule on a scratch root.
