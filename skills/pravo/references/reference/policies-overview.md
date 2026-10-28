<!-- Generated from docs/reference/policies-overview.md by tools/skillgen. Do not edit. -->

# Policies overview

A policy is a node of the specification whose rule is a CUE struct. pravo keeps every version in the state, judges every plan with the versions in force, and signs the versions into the apply.

A policy is a node of the specification: `pravo.#Policy`, with a `rule` that is a CUE struct written in the specification. The first apply that carries the policy writes it to the state as a signed, numbered version. From then on every plan that changes a resource is judged by the versions in force, at the apply, and the signed start of the apply lists the versions that judged it. A plan that a policy refuses is not applied, and the refusal is exit code 5. An apply that writes versions prints `Policies: +N new, ~N changed, -N removed.` above its result line, and its JSON line has `policies`.

| Item | Fact |
|---|---|
| Declared | A field of the specification with `$kind: "policy"`. Use `pravo.#Policy & {rule: …}`. |
| Name | The path of the field. `no_destroy` in the root is the policy `no_destroy`. |
| Rule | A CUE struct that is never empty, kept in the state as its formatted source. It may import the CUE standard library and nothing else. |
| Stored | `policy/NAME@SEQ.json` in the state. A version is written once and never replaced or deleted. |
| In force | For each name, its latest version, unless that version is a removal. |
| Judges | The changes of every plan that creates, updates, replaces or deletes a resource. |
| Judged | At `apply`, under the locks, once the staleness checks pass and before the transaction starts. `plan` and `show` judge for feedback. |
| Not judged | A change to a policy. A plan with no resource change. |
| Receipt | The signed start of the apply transaction. It lists `NAME@SEQ:SHA-256` for each version that judged. |
| Needs | A sealed state. A root that declares a policy on an unsealed state is refused with exit code 5. |
| Refusal | Exit code 5. The plan is printed. Nothing is applied. |

How a rule is written is on [Writing policies](policies-writing.md). Where each command judges is on [Operating policies](policies-operations.md), and what the receipt proves is on [What a policy guarantees](policies-operations.md#what-a-policy-guarantees). The idea is taught in [B10](../learn/b10-policy.md), and a rollout is in [Write and roll out policies](../how-to/policies.md).

## A policy and a plan

Work in an empty directory. The root is `infra`. The state is local and sealed to one reader, ada, because a policy version is signed and an unsealed state signs nothing.

File `infra/main.cue`:

```cue
package infra

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider & {$version: "~> 3.6"}

pet: $random.#Resource.random_pet & {
	length: 2
	keepers: owner: "platform"
}

name: pravo.#Output & {value: pet.id}

no_destroy: pravo.#Policy & {
	description: "A managed resource is neither deleted nor replaced."
	rule: {
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

The plan prints the policy as a change of its own, beside the resource:

```sh
pravo -C infra plan | sed -n '/^Plan:/,$p'
```

Output:

```text
Plan: 1 to add, 0 to change, 0 to destroy.
Policies: +1 new, ~0 changed, -0 removed.
  + no_destroy (sha256:5530c1a63699)
Graph: planned 1

Outputs:
  name: (known after apply)
```

The apply creates the pet and writes the policy. The policy does not judge the plan that adds it:

```sh
pravo -C infra apply --auto-approve | grep '^Apply complete'
ls infra/pravo.state/policy
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
no_destroy@00000001.json
```

The next plan that replaces the pet is judged by `no_destroy@1`:

```sh
sed -i 's/length: 2/length: 3/' infra/main.cue
pravo -C infra plan 2>&1
```

Output:

```text
pet: Refreshing state... [id=<pet>]

pravo will perform the following actions:

  # pet (random_pet) must be replaced
-/+ pet: {
  ~     id:        "<pet>" -> (known after apply)
        keepers:   {
        owner: "platform"
    }
  ~     length:    2 -> 3 # forces replacement
        separator: "-"
    }

Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 · refreshed 1

Outputs:
  name: "<pet>" → (known after apply)

Error: the plan violates the policy no_destroy@1 (sha256:5530c1a63699):
changes.0: pet: replace is not allowed:
    no_destroy:6:3
```

The rule is the text of the policy, so the position `no_destroy:6:3` is a line and a column of that text. The same plan is refused by `apply`, and `apply` records the refusal as a rejected transaction.

## Naming a policy

The name is the path of the field in the specification. A policy may sit in a struct, such as `rules: no_destroy: pravo.#Policy & {…}`, and its name is then `rules.no_destroy`. A policy may not sit below an optional or a required field, or deeper than 48 levels: the load fails and names the field.

`description` is free text for the reader of the specification. The state does not keep it.

## The rule

A rule is standalone CUE. It may import the CUE standard library, such as `strings`, and nothing else: it cannot import `pravo.dev/pravo` or a provider. A definition (`#Limit: 5`) or a hidden field (`_exempt: [...]`) can serve as a helper.

A policy that is new or changed is checked when the plan is made, before anything is applied. The check refuses a rule that cannot be used, with exit code 6:

| Cause | Message |
|---|---|
| The text does not parse | `policy NAME: <CUE's error>` |
| An import outside the standard library | `policy NAME imports "<path>"; a policy is standalone and may import only the CUE standard library` |
| The text parses and does not compile | `policy NAME does not compile: <CUE's error>` |
| The rule fails on the document of an empty plan | `policy NAME does not accept the document of an empty plan:` followed by CUE's error |
| The state is not sealed | `the root declares policies, and a policy version is signed: the state must be sealed (pravo protect init --encrypt)`, exit code 5 |

The last check catches a rule that is misspelt, such as a bound on `sumary` or on `summary.blast_radius`, which the document does not hold. It does not catch a rule that is too strict. [Writing policies](policies-writing.md#mistakes-that-forbid-nothing) shows the misspellings that pass it.

## Versions in the state

A version is the object `policy/NAME@SEQ.json`, where `SEQ` has eight digits. It holds the text, the time it was signed (`at`) and the signature of whoever applied it:

```sh
sed -n '/"signature"/,$p' infra/pravo.state/policy/no_destroy@00000001.json | sed 's/"sig": ".*"/"sig": "<signature>"/; s/"key": ".*"/"key": "<fingerprint>"/'
```

Output:

```text
  "signature": {
    "signer": "ada",
    "key": "<fingerprint>",
    "sig": "<signature>"
  }
}
```

- A version is written with a put that fails if the object exists. A policy that changes gets the next number. A policy that is removed from the specification gets a version with empty text, a tombstone.
- The first version raises `meta.json` to format 4. A pravo that does not know format 4 refuses the state.
- The signature covers the state, the name, the number, the digest of the text and `at`. A version cannot be moved to another state.
- A reader who can apply can write a version. The signature names who wrote it, and review of the specification is the control. With [roles](roles.md) a writer writes a version and only an admin writes a removal.
- `pravo state pull` and `pravo state push` copy the versions with the rest.

## How a plan is judged

For each version in force, in the order of the names, pravo unifies the rule with the plan document and validates the result as concrete. Every policy judges every plan, and the policies are independent: a satisfied policy does not excuse a violated one. All violations are reported together.

When every policy is satisfied, `plan` prints one line for each: `Policy no_destroy@1:<sha-256 of the text> is satisfied.` Under `plan --json` these lines go to standard error, and standard output holds only the document.

### Reading a refusal

- `NAME@SEQ` is the version that refused, and the digest is the first twelve hexadecimal digits of the SHA-256 of its text.
- The path at the start of a line indexes the plan document: `changes.0` is the first element of `changes`, and `changes.1.after.keepers.owner` is a value inside the second.
- A message from `error()` is the text the rule wrote, with the address of the change if the rule included it.
- The lines below a message are positions. They point into the text of the rule, and for a violation of a value also into the document, as `plan:1:782`. The document is held in memory when it is judged, so the position is not in a file. Use the path to find the value in `pravo plan --json`.
- A refusal quotes the value that failed. The document is not masked: see [The plan document in a policy](policies-plan-document.md#the-document-is-not-masked).

Under `--json` the error is also printed as an object on standard output, after the document: `{"code":5,"kind":"error","message":"..."}`.

## Two moments

A constraint on the `#Resource` of a `#Provider` definition is checked when the specification evaluates. It is a CUE constraint, not a policy. A policy is a rule over the plan. It is checked when a plan is judged, by the versions in the state, so a change of the specification that weakens a rule is itself a change that review sees, and the version it writes is signed.

A constraint rejects a value that is set and wrong. It does not require that an attribute is set, which is a rule for a policy. See [Providers](spec-providers.md#provider-scoped-constraints). [Constraints and policies](../concepts/policies.md#two-moments) compares the two.

## See also

- [Write and roll out policies](../how-to/policies.md) takes a rule from a first draft to an apply job.
- [Constraints and policies](../concepts/policies.md) explains the two moments and what a policy binds.
- [Writing policies](policies-writing.md) lists the shapes that work.
