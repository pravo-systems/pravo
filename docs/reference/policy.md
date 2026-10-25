---
title: pravo policy
description: Hold a policy rule, or a draft of one, against stored plans and plan files before it is stored, and see which plans it would refuse and why.
order: 44
group: Policies
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo policy test [POLICY \| RULE.cue]... [--plan ID_OR_FILE]... [--all] [--in-force]` |
| Reads | The specification, the state and the plans named or stored. |
| Writes | Nothing. No state, no lock, no plan. |
| Taught in | [Operating policies](policies-operations.md#rolling-out-and-testing) |

`pravo policy test` judges plans by rules without applying anything. It is the question "what would this rule do to the plans I have?", asked before a rule is stored. The viewer asks the same question in its [policy workbench](serve.md#the-workbench).

### Arguments

Each argument names one rule:

- The name of a policy that the specification declares. Its text is the rule of the specification, that is, the one the next apply would store.
- A CUE file of policies, written as a specification declares them: the import of `pravo.dev/pravo` and any number of fields that are a `pravo.#Policy` with a `rule`. A draft that is not in the specification yet is tried this way. The file is checked as CUE against the `pravo` package, so a field that the plan document lacks is an error at its line, and a refusal names its policy.
- A CUE file that holds one rule, the struct that `rule:` would hold, without the declaration.

With no argument every policy of the specification is tried.

### Flags

| Flag | Meaning |
|---|---|
| `--plan ID_OR_FILE` | A stored plan, by id, or a plan file made by `plan --out`. Repeatable. With it, the plans that can be applied are not added. |
| `--all` | Every stored plan, including applied, stale and expired ones. |
| `--in-force` | Try the versions that the state holds instead of the text of the specification. Use it to ask what the policies of the state say about a plan. |

The [tag flags](spec-tags-and-variables.md) and the [global flags](global-flags.md) apply. `--json` prints the report as one document.

### What is judged

A rule is tried on the document that a policy sees: the JSON of `plan --json` without what only the state can give, such as the blast radius. [The plan document](policies-plan-document.md) lists the fields. As at apply, a plan that changes no resource is not judged by any policy, and the report says so. A sealed plan that this identity cannot open is reported as unreadable.

A rule must make the document concrete. A path that is absent, `null` or unknown in a plan is a violation, in the test as at apply. The test does not judge anything under the locks of an apply, so a plan that holds here can still be refused when the state has moved.

### Output

For each rule, one line for each plan: `✓` when the rule holds, `✗` when the plan would be refused, and `–` for a plan that no policy judges or that cannot be read. A refusal lists what failed, with the path in the document (`changes.2.after.tags`), the change it belongs to and the value that the plan has. A rule that does not load, or that does not unify with the plan document, says so instead of a verdict.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Every rule holds on every plan that was judged. |
| 5 | A rule would refuse a plan. |
| 6 | A rule or a file cannot be used: its text does not parse or check, or it does not name a policy. |
| 1 | Any other error. |

A pipeline can gate a rule on its own plans: `pravo policy test draft.cue --all` fails with 5 while the rule would refuse one of them. See [exit codes](exit-codes.md).
