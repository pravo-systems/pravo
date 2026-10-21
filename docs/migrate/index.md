---
title: Migrate from OpenTofu or Terraform
description: A gated path from an OpenTofu or Terraform project to pravo, with the promise it keeps, what it applies to, and three ways to go.
order: 0
---

You run OpenTofu or Terraform today. pravo drives the same providers, so your infrastructure stays where it is. What changes is the specification, which you write in CUE, and the state, which pravo keeps in its own format.

pravo reads an OpenTofu or Terraform state file once and converts it. Then it plans against the real objects. When the translation is right, the plan has nothing to do. A plan that has something to do means the CUE does not yet describe the object, and nothing is applied until it does. That empty plan is the gate.

The OpenTofu file below ran once, and OpenTofu wrote the state file that the terminal converts and plans. The CUE file is the specification that the terminal plans.

:::demo migrate
:::

The runbook is a sequence of gates. Each gate ends with a check that prints something you can read. Start the next page when the check passes.

## The promise

Every step before the first `pravo apply` with changes leaves your infrastructure and your OpenTofu state as they are.

- pravo reads the state file you name, with `pravo state import-tfstate`. It never writes a `.tfstate` file or an OpenTofu backend.
- Before an apply, pravo calls only the read side of a provider: schema, validation, configuration, upgrade, read, import, plan, data sources, ephemeral resources and functions. It never calls the operation that creates, updates or deletes an object.
- pravo writes `pravo.state` (or the bucket that `$settings` names), `pravo.lock.cue` and `cue.mod` in each root. It writes the provider cache in `~/.pravo` (`PRAVO_HOME`). It writes a plan file when you name one with `--out`.

A plan writes to `pravo.state`: it refreshes records, carries out `$from` moves and `$import` declarations there, and adds an entry to the transaction log when it wrote anything. Those writes change pravo's records and nothing else.

Two limits belong to the promise.

- A read is the provider's own code. A data source that runs a program runs it under pravo, as it does under OpenTofu. pravo cannot know what a provider does when it reads.
- The promise holds while the pravo state path is not the OpenTofu state file. Leave the path at its default, the directory `pravo.state`.

`pravo.state` holds the attributes of every resource, including the values a provider marks sensitive, in plain files. pravo creates the files with mode 0600 and the directories with mode 0700. Keep the directory out of version control and out of reach of people who cannot read the OpenTofu state.

[M3](m3-worked-example.md) shows the promise as numbers: checksums of the state file and of the objects, taken before the first command and after the last.

## The runbook

:::runbook
:::

| Page | You | What it changes | To undo |
|---|---|---|---|
| [M1 Before you start](m1-before-you-start.md) | Empty the OpenTofu plan, back up the state, scan for decisions, write the recovery plan. | Nothing in the project. | Nothing to undo. |
| [M2 Concept map](m2-concept-map.md), [M3 Worked example](m3-worked-example.md) | Read what each construct becomes, and watch one stack move. | Nothing. | Nothing to undo. |
| [M4 Translate the specification](m4-translate-the-specification.md) | Write CUE next to the HCL, one construct at a time. | New `.cue` files, `cue.mod` and `pravo.lock.cue`. | Delete them. |
| [M5 Convert the state](m5-convert-the-state.md) | Convert a copy of the state, point each record at its address, plan until the plan is empty. | `pravo.state`. | Delete the directory. |
| [M6 Rewrite the checks on plans](m6-rewrite-plan-checks.md) | Turn the CI checks that read the plan JSON into plan policies. | Policy files. | Delete them. |
| [M7 Cut over](m7-cut-over.md) | Remove OpenTofu's claim on the objects, move the pipeline, archive the old state. | The OpenTofu state and the pipeline. | [M8](m8-rollback.md), after the cut over. |
| [M8 Roll back](m8-rollback.md) | Rehearse the way back before you need it. | A copy only. | Not applicable. |
| [M9 Pitfalls](m9-pitfalls.md), [M10 Not in pravo](m10-not-in-pravo.md) | Look up a message, or a feature that has no counterpart. | Nothing. | Not applicable. |
| [M11 After the migration](m11-after-the-migration.md) | Rewrite the faithful translation in the idiom of CUE. | The specification. | Revert the commit. |

The root is frozen from M1 to M7: nothing applies to it with OpenTofu, and nothing merges into it. A change that lands in between sends you back to M5.

One rule governs M4 to M6: apply nothing that creates, changes, replaces or destroys. A plan that shows one means the specification does not yet describe the object. Fix the specification and plan again.

Until the first `pravo apply` that changes an object, going back means deleting the pravo files. After it, the OpenTofu state is out of date, and going back means making it agree with the objects. [M8](m8-rollback.md) rehearses the first case and lays out the second.

## Applies to

| | Supported |
|---|---|
| pravo and CUE | pravo 0.1, CUE 0.17 |
| Tools that wrote the state | OpenTofu, and Terraform 0.12 or newer: any tool that writes state format 4 |
| State format read | 4. Any other format and legacy flat attributes are refused with an error. |
| Where the old state lives | Any backend. Write it to a file with `tofu state pull > FILE`. |
| Workspaces | One state for each workspace. Convert each one into its own pravo state: [M5](m5-convert-the-state.md#a-root-with-workspaces). |
| Provider plugin protocol | 5 and 6 |
| Provider source | The OpenTofu registry. A state address on `registry.terraform.io` is read as the OpenTofu registry. Other registries: [providers from a registry](../how-to/providers-from-a-registry.md). |
| pravo state | A local directory or an S3-compatible bucket |

## Choose a path

:::cards
### Translate and convert
Recommended. pravo manages every object of the root, and you remove OpenTofu's claim at cut-over.

**Use when** you want one tool per root and you are willing to translate the HCL by hand.

[Translate the specification](m4-translate-the-specification.md)

### Import selected resources
pravo manages the objects you import with `$import` or `pravo import`. OpenTofu keeps the rest.

**Use when** you move a few resources, or you add new resources in pravo beside an existing root.

[Import and move](../how-to/importing-and-moving.md)

### Side by side per root
Each root has one tool, and you move one root at a time.

**Use when** your roots are independent. Roots that read each other's outputs move together, the publisher first.

[Roots that share outputs](m7-cut-over.md#roots-that-share-outputs)
:::

pravo has no converter for HCL. You translate by hand, and the pages tell you how.

Start with [Before you start](m1-before-you-start.md). The [concept map](m2-concept-map.md) lists what each construct, command and function becomes. The [worked example](m3-worked-example.md) runs the whole path on one stack.
