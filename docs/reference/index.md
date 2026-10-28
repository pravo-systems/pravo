---
title: Reference
description: Lookup pages for every command, every part of the viewer, every field of a specification, plan policies, file formats, the machine interfaces and error messages. Each page opens with a table.
---

Reference is for lookup. Each page opens with a table and keeps its prose short. To learn by doing, use [Learn](../learn/index.md). To do one task, use [How-to](../how-to/index.md). To understand how pravo behaves, use [Concepts](../concepts/index.md).

## Which page

| You want | Page |
|---|---|
| The flags of a command, its exit codes and an example | [Commands](#commands) |
| The flags every command accepts, the environment variables, the exit codes | [Global flags, environment and exit codes](global-flags.md) |
| What a section, a key or an address of the viewer does | [Viewer](#viewer) |
| What a field of the specification means, or the CUE behind it | [Language](#language) |
| To write a plan policy, or ask for approvals | [Policies](#policies) |
| The JSON of a plan, the files of the state, the Go library | [Formats and interfaces](#formats-and-interfaces) |
| An error message you do not recognise | [Errors](errors.md) |
| The meaning of a word | [Glossary](glossary.md) |
| To know whether pravo has a feature of OpenTofu | [Not in pravo](not-in-pravo.md) |

## Commands

Every page opens with the synopsis, the flags with their defaults, and the exit codes. Then it shows examples with output captured from the binary.

| Command | Does | Page |
|---|---|---|
| `pravo init` | Installs providers, writes the lock file, generates the CUE packages. | [init](init.md) |
| `pravo plan` | Shows the actions that apply takes. Saves, stores, refreshes, reserves, judges. Lists, shows, pulls, approves and collects stored plans. | [plan](plan.md) |
| `pravo apply` | Applies the specification, a saved plan or a stored plan, whole or in closed parts. | [apply](apply.md) |
| `pravo destroy` | Destroys everything in the state. | [destroy](destroy.md) |
| `pravo show` | Prints a saved plan or a stored plan. | [show](show.md) |
| `pravo output` | Prints the outputs the last apply recorded. | [output](output.md) |
| `pravo state` | Lists, shows, moves and forgets nodes. Reads the log and the locks. Queries, copies, verifies and converts the state. | [state](state.md) |
| `pravo import` | Brings an existing object under management. | [import](import.md) |
| `pravo discover` | Lists the objects that exist in a scope, shows the ones that nothing manages, shows the ones that are meant to stay apart, and writes the resource that adopts one. | [discover](discover.md) |
| `pravo why`, `drift` | Explains a change. Reports what changed outside pravo. | [why and drift](why-and-drift.md) |
| `pravo providers` | Lists providers, pins hashes, copies a provider to a registry. | [providers](providers.md) |
| `pravo validate`, `eval`, `fmt`, `gen` | Checks the specification, prints its value, formats the files, regenerates the packages. | [validate, eval, fmt and gen](validate-eval-fmt-gen.md) |
| `pravo schema` | Prints the definition of one resource type. | [schema](schema.md) |
| `pravo serve` | Starts the viewer: the estate, the stored plans, the discoveries, the policies and the recorded runs in a browser. Reads and writes nothing. | [serve](serve.md) |
| `pravo mcp` | Serves pravo to an agent over the Model Context Protocol. | [mcp](mcp.md) |
| `pravo skill` | Installs or prints the skill that teaches an agent to use pravo. | [skill](skill.md) |
| `pravo mod` | Creates, fetches, publishes and verifies CUE modules. | [mod](mod.md) |
| `pravo id` | Makes the identity that opens a sealed state and signs what you write. | [id](id.md) |
| `pravo protect` | Seals the state to a list of readers, checks it, changes the readers, sets a recovery, takes the seal off and collects old records. | [protect](protect.md) |
| `pravo protect roles` | Gives each identity a role and pins the roles in the state. | [roles](roles.md) |
| `pravo version` | Prints the version of pravo and of CUE. | [version](version.md) |
| Flags, environment, exit codes | The parts every command shares. | [global flags](global-flags.md) |

## Viewer

What `pravo serve` shows, in three pages.

| Page | Covers |
|---|---|
| [The sections of the viewer](viewer-sections.md) | Inventory, Plans, Discovery, Policies, Traces and State: every part of each, the filters, the inspector, the policy studio, the timeline. |
| [Keys and links of the viewer](viewer-keys-and-links.md) | Every parameter of the address that reproduces a view, the command palette and the keyboard. |
| [The API of the viewer](viewer-api.md) | The endpoints a script reads, with the token. |

## Language

The fields and definitions that a specification uses, and the CUE behind them.

| Page | Covers |
|---|---|
| [CUE for Terraform people](cue-for-terraform-people.md) | The CUE that the pages below assume, for a reader who writes HCL. |
| [The specification](spec-overview.md) | What a root is and how pravo finds the resources in it. |
| [Settings](spec-settings.md) | The `$settings` block. |
| [Resources](spec-resources.md) | Resources, data sources, ephemeral resources and function calls. |
| [Providers](spec-providers.md) | Provider declarations and their `$` fields. |
| [Moves and imports](spec-moves-and-imports.md) | `$from` and `$import`. |
| [Outputs](spec-outputs.md) | `pravo.#Output`. |
| [Environment and remote outputs](spec-env.md) | Environment values, secret environment values and the outputs of another root. |
| [Meta fields](spec-meta-fields.md) | `$lifecycle`, `$depends_on` and the other `$` fields of a resource. |
| [Tags and variables](spec-tags-and-variables.md) | `@tag()` fields and how a command fills them. |
| [Reuse](spec-reuse.md) | Definitions that hold resources, the packages that share them, and what a `module` block becomes. |
| [Generated packages](spec-generated-packages.md) | The packages under `cue.mod/gen`. |

## Policies

A plan policy is a CUE rule that every plan of the state must satisfy, kept in the state as signed versions.

| Page | Covers |
|---|---|
| [Policies](policies-overview.md) | What a policy is, how it is stored, how a plan is judged. |
| [The plan document in a policy](policies-plan-document.md) | The fields a rule can see, and what absent, null and unknown mean. |
| [Writing policies](policies-writing.md) | The shapes that work, and the typos that forbid nothing. |
| [Operating policies](policies-operations.md) | Where each command judges, retiring a policy, exemptions, receipts, and asking for approvals. |
| [`pravo policy`](policy.md) | Try a rule against stored plans and plan files before it is stored. |

## Formats and interfaces

| Page | Covers |
|---|---|
| [State format](state-format.md) | The objects of the state, in each of its formats. |
| [The plan document](plan-document.md) | The JSON that `pravo plan --json` prints. |
| [Exit codes](exit-codes.md) | The exit code of each command, for pipelines. |
| [Telemetry](telemetry.md) | The spans and metrics of a run, the recorded runs, and the network calls of a provider. |
| [Compatibility](compatibility.md) | Which versions work together. |
| [S3-compatible stores](s3-stores.md) | The conditional requests that locks need, and how to test a store. |
| [HTTP state servers](http-stores.md) | The object protocol of the http backend, and how pravo checks a server. |
| [The Go library](library.md) | Package `pravo`: what the command line does, in a program. |

## Lookup

| Page | Covers |
|---|---|
| [Errors](errors.md) | Symptom, cause and fix for the messages pravo prints. |
| [Glossary](glossary.md) | The terms of CUE and of pravo. |
| [FAQ](faq.md) | Short answers. |
| [Not in pravo](not-in-pravo.md) | Features that pravo does not have, and what to use. |

## Reading the examples

- In a synopsis, words in capitals are values you supply. Square brackets mark an optional part. Three dots mark a part that repeats.
- An example opens with the directory it runs in: an empty directory, named `demo` when the name shows. Commands never use `cd`.
- Output that differs between runs is shown with a placeholder: `<txn>` for a transaction, `<time>` for a time, `<age>` for an age, `plan-<id>` for the id of a stored plan.
- Every example ran on the version named in the footer of the page. The providers are `random`, `local`, `null` and `tls`, and the functions of `kubernetes`, so an example needs no account. A page that needs a store runs one on the loopback interface.
- A long output is cut. Three dots on a line of their own stand for the lines that were left out.
