---
title: "Policies: two moments, one idea"
description: "One idea at two moments: a constraint on a provider is checked at evaluation, a policy file against a plan. What each binds and what neither guarantees."
order: 8
minutes: 20
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: 'plan:1:[0-9]+'
    replace: 'plan:1:<col>'
---

A policy is a rule written in CUE. pravo checks one kind of rule when it evaluates the specification. It checks another kind when it judges a plan. Both are values that unify with data.

The two kinds do not do the same thing, and they bind different people. This page states what each does, when pravo checks it, and what neither can promise.

## Two moments

| | Specification policy | Plan policy |
|---|---|---|
| Written as | A constraint on the nested `#Resource` of a provider definition | A CUE file |
| Named by | The provider struct that a resource is built through | The person who runs pravo, with `--policy FILE` |
| Checked | When the specification evaluates: `pravo validate`, `plan`, `apply` and your editor | On the plan, after it is computed and before it is saved or applied |
| Judges | The values that a resource sets | The plan document: actions, values before and after, counts |
| Can require that an attribute is set | No | Yes, for the changes that the plan makes |
| Binds | Every resource built through a provider struct made from the definition | Every invocation that names the file |

:::figure Two moments in one run. A specification policy is part of evaluation. A plan policy is stage 5, after the plan.
<svg viewBox="0 0 780 112" role="img" aria-label="A row of six stages: evaluate, discover, graph, plan, judge, apply. A specification policy is checked inside evaluate and is named by the provider struct. A plan policy is checked at judge and is named by the invocation that runs pravo." style="min-width:700px">
<rect x="0" y="14" width="100" height="40" rx="2" style="fill:var(--key-wash);stroke:var(--key);stroke-width:1.6"/>
<text x="50.0" y="39" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">evaluate</text>
<line x1="104" y1="34" x2="132" y2="34" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="132,34 127,31.0 127,37.0" style="fill:var(--text-3)"/>
<rect x="136" y="14" width="100" height="40" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.1"/>
<text x="186.0" y="39" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:400">discover</text>
<line x1="240" y1="34" x2="268" y2="34" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="268,34 263,31.0 263,37.0" style="fill:var(--text-3)"/>
<rect x="272" y="14" width="100" height="40" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.1"/>
<text x="322.0" y="39" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:400">graph</text>
<line x1="376" y1="34" x2="404" y2="34" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="404,34 399,31.0 399,37.0" style="fill:var(--text-3)"/>
<rect x="408" y="14" width="100" height="40" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.1"/>
<text x="458.0" y="39" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:400">plan</text>
<line x1="512" y1="34" x2="540" y2="34" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="540,34 535,31.0 535,37.0" style="fill:var(--text-3)"/>
<rect x="544" y="14" width="100" height="40" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.6"/>
<text x="594.0" y="39" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">judge</text>
<line x1="648" y1="34" x2="676" y2="34" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="676,34 671,31.0 671,37.0" style="fill:var(--text-3)"/>
<rect x="680" y="14" width="100" height="40" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.1"/>
<text x="730.0" y="39" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:400">apply</text>
<line x1="50" y1="54" x2="50" y2="70" style="stroke:var(--key);stroke-width:1.4"/>
<line x1="594" y1="54" x2="594" y2="70" style="stroke:var(--accent);stroke-width:1.4"/>
<text x="0" y="86" style="fill:var(--text);font-size:12.5px;font-weight:600">specification policy</text>
<text x="0" y="104" style="fill:var(--text-3);font-size:11px">checked inside evaluate</text>
<text x="544" y="86" style="fill:var(--text);font-size:12.5px;font-weight:600">plan policy</text>
<text x="544" y="104" style="fill:var(--text-3);font-size:11px">checked at judge</text>
</svg>
:::

## Specification policy

A constraint written on the nested `#Resource` of a provider definition applies to every resource built through any struct made from it. Work in an empty directory.

```cue file=providers.cue
package estate

import rnd "pravo.dev/providers/hashicorp/random"

// The platform defines a provider with a rule on the resources built through it.
#Platform: rnd.#Provider & {
	#Resource: random_pet: keepers: owner: =~"^team-"
}

$random: #Platform
```

```cue file=resources.cue
package estate

bare: $random.#Resource.random_pet & {length: 2}

owned: $random.#Resource.random_pet & {length: 2, keepers: owner: "team-data"}
```

```sh run
pravo init | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

A constraint rejects a value that is set and wrong. It does not require that a value is set. `bare` sets no `keepers`, and `owned` sets an allowed owner. Both pass.

```sh run
pravo validate
```

```text out
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

A third resource sets an owner that the rule does not allow.

```cue file=stray.cue status=rejected
package estate

stray: $random.#Resource.random_pet & {length: 2, keepers: owner: "sales"}
```

The error names the path of the resource, the value and the bound.

```sh run
pravo validate > validate.out 2>&1
echo "exit $?"
head -3 validate.out
```

```text out
exit 6

Error: evaluating .:
stray.keepers.owner: invalid value "sales" (out of bound =~"^team-"):
```

The check runs in evaluation. No provider has started, and nothing is saved.

The rule is checked only for resources built through a struct made from the definition. A team that declares its own provider struct from the package does not carry the rule. Build `stray` through one.

```cue file=providers.cue
package estate

import rnd "pravo.dev/providers/hashicorp/random"

#Platform: rnd.#Provider & {
	#Resource: random_pet: keepers: owner: =~"^team-"
}

$random: #Platform
$free:   rnd.#Provider
```

```cue file=stray.cue
package estate

stray: $free.#Resource.random_pet & {length: 2, keepers: owner: "sales"}
```

```sh run
pravo validate
```

```text out
Success! 3 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

A specification policy binds those who build from the platform provider. What makes it hold is that the platform controls which provider structs a root is built from. A rule that must hold however a resource was declared, or that requires a value, is a plan policy.

## Plan policy

A plan policy is one CUE file. The person who runs pravo names it with `--policy FILE`. The flag is repeatable and is taken by `plan`, `apply`, `destroy` and `mcp`. The file is evaluated against the plan document, the data that `pravo plan --json` prints.

A plan satisfies a policy when the file unifies with the document and the result is concrete. Every policy is judged on its own, and all of them must hold.

```cue file=policy/no-delete.cue
// Deleting a managed resource needs a human. Plans that only create or
// update pass.
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && action == "delete" {
		error("\(addr): delete is not allowed")
	}
}]
```

The plan below creates three resources and deletes nothing.

```sh run
pravo plan --policy policy/no-delete.cue | tail -3
```

```text out
Plan: 3 to add, 0 to change, 0 to destroy.
Graph: planned 3
Policy policy/no-delete.cue (sha256:065bfbaff717) is satisfied.
```

pravo prints the plan first. Then it prints one line for each policy that is satisfied, with the first twelve digits of the SHA-256 of the file. The digest identifies the bytes of the file.

### The plan document

The document has these top-level fields: `format`, `created`, `actor`, `reason`, `git_revision`, `trace_id`, `destroy`, `summary`, `stats`, `changes`, `outputs`, `hints` and `providers`. [Plans](plans.md) describes the fields of a change. Four facts matter to a policy.

- `addr`, `mode`, `type`, `name` and `action` are present on every change. `mode` is `managed`, `data`, `ephemeral` or `call`.
- Every other field is left out when it is empty or false. `changes` is `[]` for an empty plan, never null.
- `before` is absent for a create, and `after` is absent for a delete. A no-op change has neither.
- A value that is not known until apply is `{"$unknown": true}`.

The document is the one that is printed. The same document is judged. It is not masked, so a sensitive attribute appears in it as it is.

### Strict by default

By default, CUE accepts an incomplete value: a field that the data lacks does not conflict with a constraint. A plan policy does not. A plan satisfies it only when the document shows compliance. A value that the policy requires and that is absent, null or unknown until apply is a violation.

The plan below creates three pets. A policy can require an owner, which `bare` does not set.

```cue file=policy/owner.cue
changes: [...{
	type: string
	if type == "random_pet" {
		after: keepers: owner: =~"^team-"
	}
}]
```

```sh run
pravo plan --policy policy/owner.cue > plan.out 2>&1
echo "exit $?"
sed -n '/^Error/,$p' plan.out
```

```text out
exit 5
Error: the plan violates the policy policy/owner.cue (sha256:88d9b5cb971a):
changes.0.after.keepers.owner: conflicting values null and =~"^team-" (mismatched types null and string):
    plan:1:<col>
    policy/owner.cue:3:2
    policy/owner.cue:4:26
changes.2.after.keepers.owner: invalid value "sales" (out of bound =~"^team-"):
    policy/owner.cue:4:26
    plan:1:<col>
```

The path `changes.0` indexes the `changes` array of the document. The value is `null`, which is not a string that matches. A resource that no plan touches is not judged, because a plan policy judges what a plan does.

An unknown value is a violation in the same way.

```cue file=policy/unknown.cue
changes: [...{
	type: string
	if type == "random_pet" {
		after: id: !="forbidden"
	}
}]
```

```sh run
pravo plan --policy policy/unknown.cue > plan.out 2>&1
echo "exit $?"
sed -n '/^Error/,$p' plan.out
```

```text out
exit 5
Error: the plan violates the policy policy/unknown.cue (sha256:e50bf9f3ca54):
changes.0.after.id: conflicting values {$unknown:true} and !="forbidden" (mismatched types struct and string):
    plan:1:<col>
    policy/unknown.cue:3:2
    policy/unknown.cue:4:14
changes.1.after.id: conflicting values {$unknown:true} and !="forbidden" (mismatched types struct and string):
    plan:1:<col>
    policy/unknown.cue:3:2
    policy/unknown.cue:4:14
changes.2.after.id: conflicting values {$unknown:true} and !="forbidden" (mismatched types struct and string):
    plan:1:<col>
    policy/unknown.cue:3:2
    policy/unknown.cue:4:14
```

If the value is not known, the plan does not show that it is allowed. Where an unset attribute is acceptable, say so with `null | CONSTRAINT` in the same place.

```cue file=policy/prefix.cue
changes: [...{
	type: string
	if type == "random_pet" {
		after: prefix: null | =~"^app-"
	}
}]
```

```sh run
pravo plan --policy policy/prefix.cue | tail -1
```

```text out
Policy policy/prefix.cue (sha256:c8eed3ea8432) is satisfied.
```

Two rules follow. A guard that tests a field must declare the field. CUE scoping is lexical, so a guard cannot read a field that its own struct does not mention. Do not prohibit a value with an `==` guard. An unknown value equals nothing, so the guard is false and the plan passes. Write the allowed set as a constraint.

### Where each command judges

| Command | Judgement |
|---|---|
| `pravo plan` | Prints the plan, judges it, and only then saves it with `-out`. A refused plan writes no file. |
| `pravo apply FILE` | Judges the changes that remain in the file, all of them, whatever `-only` selects, before it asks any provider for anything. |
| `pravo destroy` | Prints the deletes and judges them before it asks for confirmation. |
| `pravo apply` without a file | Refused when `--policy` is given. |
| `pravo mcp` | Reads and compiles the files when the server starts. |

A plain apply plans again while it executes. Under a policy, pravo applies only a saved plan.

```sh run exit=5
pravo apply --policy policy/no-delete.cue -auto-approve
```

```text out
Error: a policy is judged against a saved plan: run "pravo plan -out <file>", then "pravo apply <file>"
```

A saved plan is judged by the policies that the applier names, whatever policy, or none, was present when the plan was made. A policy file is not part of the fingerprint of the plan, so editing a policy does not make a saved plan stale. A stale plan is refused before any policy is read, with exit code 3.

The whole file is judged, so a limit on the size of a plan holds when the plan is applied in stages. A plan that is larger than the limit cannot be applied. Make it smaller in the specification.

Apply the three resources, then remove `stray` from the specification.

```sh run
pravo apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
```

```sh run
rm stray.cue
```

The plan is now a delete.

```sh run
pravo plan --policy policy/no-delete.cue -out gone.plan > plan.out 2>&1
echo "exit $?"
sed -n '/^Error/,$p' plan.out
test -e gone.plan || echo "no plan file was written"
```

```text out
exit 5
Error: the plan violates the policy policy/no-delete.cue (sha256:065bfbaff717):
changes.2: stray: delete is not allowed:
    policy/no-delete.cue:8:3

no plan file was written
```

The exit code is 5. The path `changes.2` is the position of `stray` in the `changes` array of the document, and the message begins with the address, so the entry is named without counting. The same policy refuses `pravo destroy`, which judges its deletes before it asks for confirmation, and `pravo apply FILE`.

```sh run
pravo destroy --policy policy/no-delete.cue -auto-approve > destroy.out 2>&1
echo "exit $?"
sed -n '/^Error/,$p' destroy.out
```

```text out
exit 5
Error: the plan violates the policy policy/no-delete.cue (sha256:065bfbaff717):
changes.0: bare: delete is not allowed:
    policy/no-delete.cue:8:3
changes.1: owned: delete is not allowed:
    policy/no-delete.cue:8:3
changes.2: stray: delete is not allowed:
    policy/no-delete.cue:8:3
```

### Refusals of the policy itself

Before pravo installs a provider, writes the lock file or opens the state, it reads every named file. It refuses with exit code 5 when a file cannot be used, and it names the policy as the fault. The cases are:

- an empty value;
- a file that does not exist or is unreadable;
- a file that does not compile;
- an import outside the CUE standard library;
- a file directly in the root directory.

Each of these stops the run.

```cue file=root-policy.cue
changes: [...{action: !="delete"}]
```

```sh run exit=5
pravo plan --policy root-policy.cue
```

```text out
Error: policy root-policy.cue sits in the root directory, whose .cue files are the specification; keep policies outside it
```

```sh run
rm root-policy.cue
```

A relative path is resolved in the directory that pravo runs in, which is the `-C` directory when that flag is given. A policy may define helper values and hidden fields. It may import only the CUE standard library. A policy kept in a subdirectory of the root is not loaded as part of the specification and is accepted.

Before the verdict, a plan is not read-only. To compute it, pravo installs providers and updates the lock. It refreshes records, and reads the objects that `$import` names. It writes no record for a `$from` move or an `$import`: the apply does. It runs data sources and ephemeral resources, and it takes the reservations that `--reserve` asks for. A refusal does not release a reservation. It expires.

The guarantee concerns changes to infrastructure. With `--policy`, `plan -out` writes no refused plan. `apply FILE` and `destroy` ask no provider to create, update or delete anything unless every named policy is satisfied.

### Typos in a policy

The failures of a misspelt policy are quiet. The plan below deletes `stray`. Five policies are judged against it.

```cue file=policy/typo-literal.cue
changes: [...{action: !="delet"}]
```

```cue file=policy/typo-list.cue
chnages: [...{action: !="delete"}]
```

```cue file=policy/typo-field.cue
summary: destory: 0
```

```cue file=policy/typo-bound.cue
summary: destory: <=0
```

```cue file=policy/exact.cue
summary: destroy: 0
```

```sh run
for f in typo-literal typo-list typo-field typo-bound exact; do
  if pravo plan --policy policy/$f.cue > /dev/null 2>&1; then echo "$f: satisfied"; else echo "$f: refused"; fi
done
```

```text out
typo-literal: satisfied
typo-list: satisfied
typo-field: satisfied
typo-bound: refused
exact: refused
```

Only the last policy forbids a delete. A misspelt literal forbids nothing. A misspelt list template adds a field and forbids nothing. A misspelt field with a concrete value is a new field that holds. A misspelt field with a constraint is refused, and so is every other plan, because the field is never set. Test a policy with a fixture that it must refuse and a fixture that it must pass.

## The trust anchor

A plan policy binds the invocations that name it. What it guarantees depends on four rights.

| Right | Whoever holds it can |
|---|---|
| 1. Write access to the policy source: the directory, the branch, the repository | Change what the policy says. |
| 2. Write access to the job definition and the environment that spell `--policy` | Name another file, name none, or name a file that says nothing. |
| 3. Write access to the state store | Change what pravo believes exists and what plans are computed from. |
| 4. Cloud credentials | Change infrastructure without running pravo. |

:::figure The four rights, on the path from a policy file to the infrastructure. Rights 1 and 2 change the policy. Right 3 changes the input of the plan. Right 4 does not pass through pravo.
<svg viewBox="0 0 796 296" role="img" aria-label="The path from a policy file to the infrastructure. A policy source feeds a job definition, which runs pravo with the policy flag, and pravo changes the cloud through providers. The state store feeds pravo. Cloud credentials reach the cloud directly. Four numbered marks show the rights: 1 on the policy source, 2 on the job definition, 3 on the state store, 4 on the credentials." style="min-width:700px">
<g transform="translate(14,0)">
<rect x="0" y="30" width="150" height="64" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="75.0" y="54" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">policy file</text>
<text x="75.0" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10px">directory, branch,</text>
<text x="75.0" y="86" text-anchor="middle" style="fill:var(--text-3);font-size:10px">repository</text>
<rect x="190" y="30" width="170" height="64" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="275.0" y="54" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">job definition</text>
<text x="275.0" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10px">pravo plan --policy FILE</text>
<text x="275.0" y="86" text-anchor="middle" style="fill:var(--text-3);font-size:10px">environment, wrapper</text>
<rect x="400" y="30" width="160" height="64" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="480.0" y="54" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">pravo</text>
<text x="480.0" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10px">judges the plan,</text>
<text x="480.0" y="86" text-anchor="middle" style="fill:var(--text-3);font-size:10px">then applies</text>
<rect x="600" y="30" width="180" height="64" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="690.0" y="54" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">providers and cloud</text>
<text x="690.0" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10px">the real infrastructure</text>
<line x1="152" y1="62" x2="188" y2="62" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="188,62 182,58.4 182,65.6" style="fill:var(--text-3)"/>
<line x1="362" y1="62" x2="398" y2="62" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="398,62 392,58.4 392,65.6" style="fill:var(--text-3)"/>
<line x1="562" y1="62" x2="598" y2="62" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="598,62 592,58.4 592,65.6" style="fill:var(--text-3)"/>
<circle cx="0" cy="24" r="10" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.5"/>
<text x="0" y="28" text-anchor="middle" style="fill:var(--conflict);font-size:11.5px;font-weight:700">1</text>
<circle cx="190" cy="24" r="10" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.5"/>
<text x="190" y="28" text-anchor="middle" style="fill:var(--conflict);font-size:11.5px;font-weight:700">2</text>
<rect x="400" y="170" width="160" height="64" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="480.0" y="194" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">state store</text>
<text x="480.0" y="212" text-anchor="middle" style="fill:var(--text-3);font-size:10px">what exists,</text>
<text x="480.0" y="226" text-anchor="middle" style="fill:var(--text-3);font-size:10px">what plans start from</text>
<line x1="480" y1="168" x2="480" y2="96" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="480,96 476.4,102 483.6,102" style="fill:var(--text-3)"/>
<circle cx="400" cy="164" r="10" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.5"/>
<text x="400" y="168" text-anchor="middle" style="fill:var(--conflict);font-size:11.5px;font-weight:700">3</text>
<rect x="600" y="170" width="180" height="64" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="690.0" y="194" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">cloud credentials</text>
<text x="690.0" y="212" text-anchor="middle" style="fill:var(--text-3);font-size:10px">held by people and jobs</text>
<line x1="690" y1="168" x2="690" y2="96" style="stroke:var(--conflict);stroke-width:1.5" stroke-dasharray="5 3"/>
<polygon points="690,96 686.4,102 693.6,102" style="fill:var(--conflict)"/>
<circle cx="600" cy="164" r="10" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.5"/>
<text x="600" y="168" text-anchor="middle" style="fill:var(--conflict);font-size:11.5px;font-weight:700">4</text>
<text x="0" y="144" style="fill:var(--text-2);font-size:11px">1 changes what the policy says</text>
<text x="0" y="162" style="fill:var(--text-2);font-size:11px">2 names another file, or none</text>
<text x="0" y="196" style="fill:var(--text-2);font-size:11px">3 changes what pravo believes exists</text>
<text x="0" y="214" style="fill:var(--text-2);font-size:11px">4 changes the cloud without pravo</text>
<text x="0" y="262" style="fill:var(--text-3);font-size:11.5px">A policy is an admission check on the invocations that name it. It does not stop the holder of a right.</text>
</g>
</svg>
:::

Whoever holds the first or the second right can replace or remove the policy. Whoever holds the third or the fourth is not stopped by anything in pravo. `pravo state rm` and `pravo state mv` edit what exists. `pravo import` adopts objects under addresses that the holder chooses. A call to the cloud API directly makes changes that no policy sees.

pravo does not know who holds which right. The repository, the pipeline and the state store do. A policy is an admission check on the invocations that name it. It is not an access control. It makes every invocation that names it refuse a plan that violates it, as far as the values that the plan shows. Against anyone who holds one of the four rights it does nothing.

One arrangement puts each right with a different party: the policy in a repository that the platform team owns and reviews; the job that names it in a pipeline that application authors cannot edit; the state store writable by that pipeline only; the credentials that can change infrastructure available only to the job that applies saved plans.

The specification cannot name, weaken or exempt a policy. The `$settings` block takes `state` and `refresh` and nothing else.

```cue file=settings.cue status=rejected
package estate

$settings: policy: "policy/no-delete.cue"
```

```sh run exit=6
pravo validate
```

```text out
Error: $settings.policy is not a setting (the block takes state and refresh)
```

```sh run
rm settings.cue
```

There is no environment variable that names a policy, and there is no default place where pravo looks for one. There is no flag that skips a policy. A job that must apply without a rule names a narrower policy, or none. The command line in the transaction log says which.

### What a policy does not guarantee

- **Omission.** Without `--policy`, nothing is checked. A laptop apply, an agent shell and a pipeline step that left the flag out are unchecked. The log records the command line of an apply of a saved plan, so it shows afterwards whether a policy was named. It does not prevent the apply.
- **Facts chosen by the specification.** The specification decides the state location, the providers and their sources, and the addresses that `$from` and `$import` set. It also decides the refresh ages and the data sources. A `$lifecycle` setting such as `ignore_changes` keeps differences out of the plan. A policy judges the plan that these choices give.
- **Provider code runs before any verdict.** Providers run with the credentials of the person who invokes pravo. Data sources and ephemeral resources run while the plan is computed.
- **Saved plans are unsigned.** A plan file holds no entry for an ephemeral resource or a function call. A rule about those entries is judged when the plan is made, and not when it is applied.
- **A limit binds one plan.** Twenty plans of five changes each pass `summary: blast_radius: <=5`.
- **Labels are the caller's words.** `actor`, `reason`, `git_revision` and `trace_id` are supplied by the caller, and a rule over them binds nothing.
- **The document is not masked.** A refusal quotes the offending value, so a rule over a sensitive attribute prints the secret. Do not write rules over secrets, and do not commit a plan document that was not scrubbed.
- **`blast_radius` and `dependents` are read from the state at that moment.** A later write to the state changes them.
- **The digest identifies bytes, not the evaluator.** The same file judged by another version of CUE may be judged differently. `pravo version` prints the version that is inside the binary.
- **A policy judges what a plan does.** A no-op change carries no `after`. A rule over attributes reports a resource only when a change touches it. An audit of what exists is `pravo state query`.

### The server for agents

`pravo mcp --policy ABSOLUTE_PATH` takes the flag once for each file. The server reads and compiles the files when it starts, and the path must be absolute. It adds `--policy` to its `plan` and `apply` tools and to no other. A tool argument cannot remove a policy that the server names. The server applies only from saved plan files. It refuses a tool argument that is empty or begins with `-`. The refusal comes before any pravo process runs, so no argument can be read as a flag.

The policy binds the tool. It does not bind the process around it. An agent with a shell next to the server can run pravo without the flag.

## Recap

- A specification policy is a constraint on a provider definition. It is checked when the specification evaluates and rejects a value that is set and wrong.
- A plan policy is a file that the invocation names. It is checked on the plan, can require a value, and is strict about absent, null and unknown values.
- A policy binds the invocations that name it. Whoever can change the policy source, the job definition, the state or the credentials is not bound by it.

Next: [Secrets and ephemeral values](secrets-and-ephemeral-values.md) describes what a plan, the state and the log hold.
