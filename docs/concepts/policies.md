---
title: "Policies: two moments, one idea"
description: "One idea at two moments: a constraint on a provider is checked at evaluation, a policy node against a plan. What each binds and what neither guarantees."
order: 8
minutes: 20
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: 'plan:1:[0-9]+'
    replace: 'plan:1:<col>'
  - pattern: 'sha256:[0-9a-f]{64}'
    replace: 'sha256:<digest>'
  - pattern: '(owner@[0-9]+):[0-9a-f]{64}'
    replace: '$1:<digest>'
---

A policy is a rule written in CUE. pravo checks one kind of rule when it evaluates the specification. It checks another kind when it judges a plan. Both are values that unify with data.

The two kinds do not do the same thing, and they bind different people. This page states what each does, when pravo checks it, and what neither can promise.

## Two moments

| | Specification policy | Plan policy |
|---|---|---|
| Written as | A constraint on the nested `#Resource` of a provider definition | A `pravo.#Policy` node whose `rule` is CUE text |
| Named by | The provider struct that a resource is built through | Its path in the specification, such as `no_destroy` |
| Kept | In the specification | In the state, as signed numbered versions |
| Checked | When the specification evaluates: `pravo validate`, `plan`, `apply` and your editor | On every plan that changes a resource: as feedback at `plan`, and at `apply` under the locks |
| Judges | The values that a resource sets | The plan document: actions, values before and after, counts |
| Can require that an attribute is set | No | Yes, for the changes that the plan makes |
| Binds | Every resource built through a provider struct made from the definition | Every apply of the state |

:::figure Two moments in one run. A specification policy is part of evaluation. A plan policy is stage 5, after the plan.
<svg viewBox="0 0 780 112" role="img" aria-label="A row of six stages: evaluate, discover, graph, plan, judge, apply. A specification policy is checked inside evaluate and is named by the provider struct. A plan policy is checked at judge and is kept in the state." style="min-width:700px">
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

A plan policy is a node of the specification: `pravo.#Policy`, with a `rule` of CUE text. The first apply that carries it writes it to the state as a signed, numbered version. After that every plan that changes a resource is judged by the versions in force, at the apply, under the locks, and the signed start of the apply lists the versions that judged it. No flag names a policy and no invocation can leave one out.

The rule is evaluated against the plan document, the data that `pravo plan --json` prints, without the fields that only the state, the clock or the invocation can give. A plan satisfies a policy when the rule unifies with the document and the result is concrete. Every policy judges every plan, and all of them must hold.

A policy version is signed, so the state must be sealed. Seal the state of this directory to one reader, ada, and apply the two resources:

```sh run hidden
mkdir -p .pravo/access
pravo id new --name ada | grep '^  ada ssh' | sed 's/^  //' > .pravo/access/readers.txt
pravo protect init --encrypt --yes --allow-local
rm stray.cue
```

```sh run
pravo apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

Declare a policy that requires an owner on every pet that is created:

```cue file=policy.cue
package estate

import "pravo.dev/pravo"

owner: pravo.#Policy & {
	description: "Every pet that is created carries an owner."
	rule: #"""
		changes: [...{
			type:   string
			action: string
			if type == "random_pet" && action == "create" {
				after: keepers: owner: =~"^team-"
			}
		}]
		"""#
}
```

The plan prints the policy as a change of its own. The apply writes it to the state:

```sh run
pravo plan | sed -n '/^Policies/,/^Graph/p'
pravo apply -auto-approve | grep '^Apply complete'
ls pravo.state/policy
```

```text out
Policies: +1 new, ~0 changed, -0 removed.
  + owner (sha256:a3a16103498e)
Graph: planned 0 · skipped 2 · not read 2
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <id>]
owner@00000001.json
```

A policy is not judged by itself, and a plan with no resource change is not judged at all. Add a pet that sets no owner:

```cue file=stray.cue
package estate

stray: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo plan > plan.out 2>&1
echo "exit $?"
sed -n '/^Error/,$p' plan.out
```

```text out
exit 5
Error: the plan violates the policy owner@1 (sha256:a3a16103498e):
changes.0.after.keepers.owner: conflicting values null and =~"^team-" (mismatched types null and string):
    owner:4:2
    owner:5:26
    plan:1:<col>
```

The exit code is 5. The path `changes.0` indexes the `changes` array of the document, and the position points into the text of the rule. The same plan is refused by `pravo apply`, which records a rejected transaction, and a plan file is not written for it.

### Strict by default

By default, CUE accepts an incomplete value: a field that the data lacks does not conflict with a constraint. A plan policy does not. A plan satisfies it only when the document shows compliance. A value that the rule requires and that is absent, null or unknown until apply is a violation. The pet above sets no `keepers`, so the value is `null`, which is not a string that matches.

Two rules follow. A guard that tests a field must declare the field, because CUE scoping is lexical. Do not prohibit a value with an `==` guard: an unknown value equals nothing, so the guard is false and the plan passes. Write the allowed set as a constraint. [Writing policies](../reference/policies-writing.md) shows each with a run.

### Where each command judges

| Command | Judgement |
|---|---|
| `pravo plan` | Prints the plan and judges it for feedback. A refused plan is not saved with `--out` or `--store`. |
| `pravo apply` | Under the locks, after the staleness checks and before the transaction starts, judges the whole plan file, whatever earlier stages applied and whatever `--only` selects. |
| `pravo destroy` | As `apply`, for the plan of deletions. |
| `pravo mcp` | Its tools run the same commands. |

A change to a policy is never judged, so a policy that refuses every plan is retired by removing it, with an apply that has no resource change. A plan with no resource change is not judged. [Operating policies](../reference/policies-operations.md) shows each case.

Before the verdict, a plan is not read-only. To compute it, pravo installs providers and updates the lock. It refreshes records, applies `$from` moves and adopts `$import` objects. It runs data sources and ephemeral resources, and it takes the reservations that `--reserve` asks for. A refusal does not release a reservation. It expires.

The guarantee concerns changes to infrastructure. `plan --out` writes no refused plan, and `apply` and `destroy` ask no provider to create, update or delete anything unless every policy in force is satisfied.

### What the apply records

The signed start of an apply lists `NAME@SEQ:SHA-256` for each version that judged the plan, beside the digest of the plan. The list is the receipt: `pravo state verify` checks the signatures, finds the plan, and runs the listed versions on it.

Give the pet an owner. The plan is satisfied, and the apply that follows names the version that judged it:

```sh run
sed -i 's/length: 2}/length: 2, keepers: owner: "team-ops"}/' stray.cue
pravo apply -auto-approve | grep -E '^(Policy|Apply complete)'
pravo state log --json | jq -c '.[0] | {kind, status, policies}'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
{"kind":"apply","status":"applied","policies":["owner@1:<digest>"]}
```

### Typos in a rule

The failures of a misspelt rule are quiet. A misspelt literal forbids nothing. A misspelt list template adds a field and forbids nothing. A misspelt field with a concrete value is a new field that holds. A misspelt field with a constraint is refused when the rule is added, because the document of an empty plan never sets the field. Test a rule against a fixture that it must refuse and a fixture that it must pass. [Writing policies](../reference/policies-writing.md#mistakes-that-forbid-nothing) runs each of the four.

## The trust anchor

A plan policy binds the applies of the state it is kept in. What it guarantees depends on four rights.

| Right | Whoever holds it can |
|---|---|
| 1. Write access to the policy source: the files of the root, the branch, the repository | Change what a policy says. The apply writes the new text as a signed version, and the signature names who applied it. |
| 2. Write access to the job definition and the environment | Run something other than pravo, or a build of pravo that does not judge. |
| 3. Write access to the state store | Change what pravo believes exists and what plans are computed from, and delete the latest version of a policy or the directory `policy/`. |
| 4. Cloud credentials | Change infrastructure without running pravo. |

:::figure The four rights, on the path from a policy file to the infrastructure. Rights 1 and 2 change the policy. Right 3 changes the input of the plan. Right 4 does not pass through pravo.
<svg viewBox="0 0 796 296" role="img" aria-label="The path from a policy to the infrastructure. A policy source feeds a job definition, which runs pravo, and pravo changes the cloud through providers. The state store feeds pravo. Cloud credentials reach the cloud directly. Four numbered marks show the rights: 1 on the policy source, 2 on the job definition, 3 on the state store, 4 on the credentials." style="min-width:700px">
<g transform="translate(14,0)">
<rect x="0" y="30" width="150" height="64" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="75.0" y="54" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">policy source</text>
<text x="75.0" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10px">files of the root,</text>
<text x="75.0" y="86" text-anchor="middle" style="fill:var(--text-3);font-size:10px">branch, repository</text>
<rect x="190" y="30" width="170" height="64" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.4"/>
<text x="275.0" y="54" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">job definition</text>
<text x="275.0" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10px">pravo apply</text>
<text x="275.0" y="86" text-anchor="middle" style="fill:var(--text-3);font-size:10px">environment, wrapper</text>
<rect x="400" y="30" width="160" height="64" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="480.0" y="54" text-anchor="middle" style="fill:var(--text);font-size:12.5px;font-weight:600">pravo</text>
<text x="480.0" y="72" text-anchor="middle" style="fill:var(--text-3);font-size:10px">judges the plan</text>
<text x="480.0" y="86" text-anchor="middle" style="fill:var(--text-3);font-size:10px">with the policies of the state</text>
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
<text x="0" y="144" style="fill:var(--text-2);font-size:11px">1 changes what a policy says</text>
<text x="0" y="162" style="fill:var(--text-2);font-size:11px">2 runs something else than pravo</text>
<text x="0" y="196" style="fill:var(--text-2);font-size:11px">3 changes what pravo believes exists</text>
<text x="0" y="214" style="fill:var(--text-2);font-size:11px">4 changes the cloud without pravo</text>
<text x="0" y="262" style="fill:var(--text-3);font-size:11.5px">A policy is an admission check on the applies of the state. It does not stop the holder of a right.</text>
</g>
</svg>
:::

Whoever holds the first right can change or remove a policy, and the signed version names who did. Whoever holds the second can apply without judging. Whoever holds the third or the fourth is not stopped by anything in pravo. `pravo state rm` and `pravo state mv` edit what exists. `pravo import` adopts objects under addresses that the holder chooses. A call to the cloud API directly makes changes that no policy sees.

pravo does not know who holds which right. The repository, the pipeline and the state store do. A policy is an admission check on the applies of the state. It is not an access control. It makes every apply of the state refuse a plan that violates it, as far as the values that the plan shows. Against anyone who holds one of the four rights it does nothing.

One arrangement puts each right with a different party: the files that declare policies in a repository that the platform team owns and reviews, with code owners; the apply job in a pipeline that application authors cannot edit; the state store writable by that pipeline only; the credentials that can change infrastructure available only to the job that applies saved plans.

The specification declares a policy and cannot exempt a resource from it. The `$settings` block takes `state` and `refresh` and nothing else.

```cue file=settings.cue status=rejected
package estate

$settings: policy: "policy.cue"
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

There is no environment variable and no flag that names or skips a policy. A rule that has to be lifted is removed from the specification, and the apply that removes it writes a signed removal.

### What a policy does not guarantee

- **Bypass.** A laptop with another binary, an agent shell with cloud credentials and a job that calls the cloud API directly are not checked. Every apply by pravo that reads the state judges. The log records the versions that judged each apply.
- **Review.** A reader who can apply can write a new version of a policy. The signed version names who applied it, and review of the files that declare policies is the control.
- **Facts chosen by the specification.** The specification decides the state location, the providers and their sources, and the addresses that `$from` and `$import` set. It also decides the refresh ages and the data sources. A `$lifecycle` setting such as `ignore_changes` keeps differences out of the plan. A policy judges the plan that these choices give.
- **Provider code runs before any verdict.** Providers run with the credentials of the person who invokes pravo. Data sources and ephemeral resources run while the plan is computed.
- **Plan files are unsigned.** A plan file holds no entry for an ephemeral resource or a function call. A rule about those entries is judged when the plan is made, and not when it is applied.
- **A limit binds one plan.** Twenty plans of five changes each pass `summary: add: <=5`.
- **Labels are the caller's words.** `actor`, `reason` and `git_revision` are supplied by the caller, and a rule over them binds nothing.
- **The document is not masked.** A refusal quotes the offending value, so a rule over a sensitive attribute prints the secret. Do not write rules over secrets, and do not commit a plan document that was not scrubbed.
- **The planner's words.** `before`, the flags, `stats`, `outputs` and drift come from the plan. Apply checks the address, the action, the known values and the revisions.
- **The digest identifies bytes, not the evaluator.** The same text judged by another version of CUE may be judged differently. `pravo version` prints the version that is inside the binary.
- **A policy judges what a plan does.** A no-op change carries no `after`. A rule over attributes reports a resource only when a change touches it. An audit of what exists is `pravo state query`.
- **A deleted version.** A writer of the bucket who deletes the latest version of a policy rolls an edit back, or brings a removed policy back. The next plan shows the difference.

### The server for agents

`pravo mcp` takes no policy. Its `plan` and `apply` tools run the same commands, so the policies of the state judge them, and a tool argument cannot remove a policy. The server applies only from saved plan files. It refuses a tool argument that is empty or begins with `-`. The refusal comes before any pravo process runs, so no argument can be read as a flag.

The policies bind the tool. They do not bind the process around it. An agent with a shell next to the server can run another tool against the cloud.

## Recap

- A specification policy is a constraint on a provider definition. It is checked when the specification evaluates and rejects a value that is set and wrong.
- A plan policy is a node of the specification, kept in the state as signed versions. It judges every plan that changes a resource, can require a value, and is strict about absent, null and unknown values.
- The signed start of an apply lists the versions that judged it. Whoever can change the policy source, the job definition, the state or the credentials is not stopped by a policy.

Next: [Secrets and ephemeral values](secrets-and-ephemeral-values.md) describes what a plan, the state and the log hold.
