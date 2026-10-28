<!-- Generated from docs/learn/c05-write-a-plan-policy.md by tools/skillgen. Do not edit. -->

# Write a plan policy

A rule is a shape that the plan document has to fit, in the CUE you already know. Read the document, write a rule as a shape, as a template over every change and as a message, try the rules on plan files before anything is in force, and put one in force.

B10 wrote one policy: a list template with a guard and `error()`, and the plan that deleted a resource was refused. This lesson is about writing rules. In the OpenTofu world a rule on a plan is a program in another language, run by another tool over the JSON of the plan, and the team that writes infrastructure learns a second language to guard it. In pravo a rule is a CUE value, and the plan has to unify with it. The language of the rule is the language of the specification, and a rule is read the way A3 read two declarations of one field: it holds when the two agree.

```text
deny[msg] {
  some i
  input.resource_changes[i].change.actions[_] == "delete"
  msg := "no deletes"
}
```

```cue
summary: destroy: 0
```

This lesson reads the document a rule sees, writes three rules, each a little more specific, tries them on plan files with `pravo policy test`, which changes nothing, and then puts one in force.

## The document a rule reads

Work in an empty directory. The root is `team/`, with a name and a password, and its state is sealed to Ada as C3 did it, in a local directory, because a policy in force is a signed version in the state. The setup runs the steps of C3 for you. The rules on trial live in `rules/`, beside the root.

File `team/main.cue`:

```cue
package team

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet:    $random.#Resource.random_pet & {length: 2}
secret: $random.#Resource.random_password & {length: 16}
```

A rule sees the plan document: the JSON that `pravo plan --json` prints, with the fields that only the state or the clock could give left out. Make the password longer and save the plan, and read the parts of the document that rules read most:

```sh
sed -i 's/length: 16/length: 20/' team/main.cue
pravo -C team plan --out longer.plan --json 2> /dev/null | jq -c '{summary: (.summary | {add, change, destroy}), changes: [.changes[] | {addr, type, action, after: {length: .after.length}}]}'
```

Output:

```text
{"summary":{"add":1,"change":0,"destroy":1},"changes":[{"addr":"secret","type":"random_password","action":"replace","after":{"length":20}}]}
```

`summary` counts what the plan does. `changes` is a list with one entry per resource the plan touches: its address, its type, its action and its values after the change, and before it where there was a before. A password cannot be made longer in place, so the action is `replace`, and a replacement counts once as an addition and once as a destruction. [The plan document in a policy](../reference/policies-plan-document.md) lists every field and when each is present.

## A rule is a shape

The smallest rule is one field with one value. A file in `rules/` holds the rule alone, with a comment, and `pravo policy test` holds it against a plan file. Nothing is applied and nothing is stored.

File `rules/no_destroy.cue`:

```cue
// A plan may create and change. It may not destroy.
summary: destroy: 0
```

```sh
pravo -C team policy test ../rules/no_destroy.cue --plan longer.plan
```

Output:

```text
Policy no_destroy (../rules/no_destroy.cue)
  ✗ longer.plan  file  would be refused
      summary.destroy: conflicting values 1 and 0
      the plan has 1
  0 of 1 judged plan hold.

Error: 1 plan would be refused
```

The plan's `summary.destroy` is 1 and the rule's is 0. Two values for one field, and they do not agree: that is the error of A3, read the same way, with the path first. The paths are relative to the root, since `-C team` runs the command there. Exit code 5 says that a plan would be refused, so a pipeline can gate a draft rule on the plans the team has.

A plan that creates and destroys nothing fits the rule. Put the length back and add a name:

File `team/main.cue`:

```cue
package team

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet:    $random.#Resource.random_pet & {length: 2}
secret: $random.#Resource.random_password & {length: 16}
second: $random.#Resource.random_pet & {length: 2, prefix: "db"}
```

```sh
pravo -C team plan --out add.plan > /dev/null
pravo -C team policy test ../rules/no_destroy.cue --plan add.plan --plan longer.plan
```

Output:

```text
Policy no_destroy (../rules/no_destroy.cue)
  ✓ add.plan  file  holds
  ✗ longer.plan  file  would be refused
      summary.destroy: conflicting values 1 and 0
      the plan has 1
  1 of 2 judged plans hold.

Error: 1 plan would be refused
```

A rule says nothing about what it does not mention. `summary: destroy: 0` has no opinion on how many resources are added, on their types or on their values, and a plan that adds fifty passwords of one character fits it.

## A rule over every change

A rule about values is a rule about each entry of `changes`. A9 wrote one rule for every entry of a struct; `[...X]` is the same for a list: every element has to fit `X`. Inside it, an `if` from A12 picks the changes the rule is about, and a bound from A5 says how much is enough.

File `rules/long_passwords.cue`:

```cue
// A password has at least sixteen characters.
changes: [...{
	type: string
	if type == "random_password" {
		after?: length: >=16
	}
}]
```

`type: string` declares the field the guard reads. A template is its own scope, so a field has to be named there before a condition can read it. The condition adds the constraint on `after.length` to the changes of that type and to no other. The question mark in `after?` is A7's optional field: a change that has an `after`, which a creation, an update and a replacement do, has to fit the constraint, and a deletion, which has no `after`, is left alone. Without it the rule would ask a deletion for a length it cannot have, and refuse every destroy.

Make the password shorter and save that plan. The block carries `status=unintended`: the specification is valid, and the plan breaks the rule.

File `team/main.cue`:

```cue
package team

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet:    $random.#Resource.random_pet & {length: 2}
secret: $random.#Resource.random_password & {length: 12}
second: $random.#Resource.random_pet & {length: 2, prefix: "db"}
```

```sh
pravo -C team plan --out short.plan > /dev/null
pravo -C team policy test ../rules/long_passwords.cue --plan short.plan --plan add.plan
```

Output:

```text
Policy long_passwords (../rules/long_passwords.cue)
  ✗ short.plan  file  would be refused
      changes.0.after.length: invalid value 12 (out of bound >=16)
      change secret; the plan has 12
  ✓ add.plan  file  holds
  1 of 2 judged plans hold.

Error: 1 plan would be refused
```

`changes.0` is a position in the list, the first change of the plan, and the next line names it, `secret`, with the value the plan has. The message is the one A5 showed for a value outside a bound. The rule did not have to name `secret`: it holds for every password the team will ever plan.

## Say why

A conflict names a path and two values. For a rule that people will read in a refusal, `error()` from B10 writes the sentence, and the sentence can name the resource.

File `rules/no_replace.cue`:

```cue
// A replacement destroys the old object. It needs a maintenance window.
changes: [...{
	addr:   string
	action: string
	if action == "replace" {
		error("\(addr): a replacement needs a maintenance window")
	}
}]
```

```sh
pravo -C team policy test ../rules/no_replace.cue --plan longer.plan
```

Output:

```text
Policy no_replace (../rules/no_replace.cue)
  ✗ longer.plan  file  would be refused
      changes.0: secret: a replacement needs a maintenance window
      change secret
  0 of 1 judged plan hold.

Error: 1 plan would be refused
```

Three rules, three ways to write one: a shape for a count, a template with a bound for a value, and a template with a message for an action. Each reads one or two fields of the document and leaves the rest alone.

## Break it: a field that is not there

A rule that misspells a field would hold for every plan, because no plan has the field it names. pravo closes that door: the document has a schema, `pravo.#Plan`, and a rule is checked against it when it is loaded. The block carries `status=conflict`.

File `rules/typo.cue`:

```cue
summary: destory: 0
```

```sh
pravo -C team policy test ../rules/typo.cue --plan add.plan
```

Output:

```text
Policy typo (../rules/typo.cue)
  cannot be used: policy typo does not fit the plan document:
  summary.destory: field not allowed:
      typo:1:10

Error: 1 rule cannot be used
```

`field not allowed` is the error of A8: a definition rejects a field it does not declare. The rule is refused before it judges anything, and the same check runs when a policy is added to the root, with one change that holds no values, so that a template that reads a field no change has is caught then and not at the first plan that happens to have one.

## Put a rule in force

A rule on trial is a file. A policy in force is a node of the root, `pravo.#Policy`, with the rule inside it, and the apply that carries it writes it to the state as a signed version. Writing the template with `pravo.#Change` lets the schema check its fields in an editor as well. Restore the length first.

File `team/main.cue`:

```cue
package team

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

pet:    $random.#Resource.random_pet & {length: 2}
secret: $random.#Resource.random_password & {length: 16}
second: $random.#Resource.random_pet & {length: 2, prefix: "db"}
```

File `team/policy.cue`:

```cue
package team

import "pravo.dev/pravo"

long_passwords: pravo.#Policy & {
	description: "A password has at least sixteen characters."
	rule: changes: [...pravo.#Change & {
		type: string
		if type == "random_password" {
			after?: length: >=16
		}
	}]
}
```

```sh
pravo -C team apply --auto-approve | grep -E '^(Policies|  \+ long|Apply complete)'
```

Output:

```text
Policies: +1 new, ~0 changed, -0 removed.
  + long_passwords (sha256:<digest>)
Policies: +1 new, ~0 changed, -0 removed.
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

The plan that adds a policy is not judged by it. From this apply on, every plan against the state is, and a short password is refused where `policy test` said it would be:

```sh
sed -i 's/length: 16/length: 12/' team/main.cue
pravo -C team plan > plan.out 2>&1; echo "exit=$?"
sed -n '/^Error/,$p' plan.out
```

Output:

```text
exit=5
Error: the plan violates the policy long_passwords@1 (sha256:<digest>):
changes.0.after.length: invalid value 12 (out of bound >=16):
    long_passwords:5:12
    plan:1:137
```

The refusal names the version of the policy, the digest of its text, the path in the document and the line of the rule. A plan that holds is applied, and the transaction keeps a receipt: the versions and digests of the policies that judged it, so that the log says not only who applied but under which rules.

```sh
sed -i 's/length: 12/length: 16/' team/main.cue
sed -i 's/prefix: "db"/prefix: "web"/' team/main.cue
pravo -C team apply --auto-approve | grep '^Apply complete'
pravo -C team state log --json | jq -c '.[0].policies'
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
["long_passwords@1:<digest>"]
```

## Try a rule in the viewer

The policy studio of `pravo serve`, under `s`, is `policy test` with a text box: a rule typed on the left is held against every resource of the state, as if a plan updated it, and against the plans the team stored, with the verdict on the right as you type. Nothing is applied or stored there either.

*Figure: The policy studio with a blank policy: the rule on the left, and on the right the verdict against every resource of the estate.*

## Exercise

## Clean up

```sh
pravo -C team destroy --auto-approve | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 3 destroyed. [txn <txn>]
```

## Recap

- A rule is a CUE value that the plan document has to unify with. A shape for a count, a list template with a guard and a bound for a value, a template with `error()` for a message that names the resource.
- `pravo policy test RULE.cue --plan FILE` holds a rule file against plan files and changes nothing. Exit code 5 says a plan would be refused, and the paths are relative to the root.
- The document has a schema. A rule that names a field the document does not have is refused when it is loaded, with `field not allowed`.
- In the root, a rule is a `pravo.#Policy` node. The apply that carries it writes a signed version, every later plan is judged by it, and the transaction keeps the versions that judged it.

The how-to page [Write and roll out policies](../how-to/policies.md) tests rules on fixtures in a pipeline, rolls a rule out as advice and retires one. [Writing policies](../reference/policies-writing.md) lists the shapes that work and the ones that do not. The next lesson, [Ask for approvals](c06-ask-for-approvals.md), writes a rule that does not refuse a plan but makes it wait for a second person.
