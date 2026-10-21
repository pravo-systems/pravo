---
title: Reference
description: Lookup pages for every command, every field of a specification, plan policies, file formats and error messages. Each page opens with a table.
---

Reference is for lookup. Each page opens with a table and keeps its prose short. To learn by doing, use [Learn](../learn/index.md). To do one task, use [How-to](../how-to/index.md). To understand how pravo behaves, use [Concepts](../concepts/index.md).

## Which page

| You want | Page |
|---|---|
| The flags of a command, its exit codes and an example | [Commands](#commands) |
| The flags every command accepts, the environment variables, the exit codes | [Global flags, environment and exit codes](global-flags.md) |
| What a field of the specification means | [Language](#language) |
| To write a plan policy | [Policies](#policies) |
| The JSON of a plan, or the files of the state | [Data and formats](#data-and-formats) |
| An error message you do not recognise | [Errors](errors.md) |
| The meaning of a word | [Glossary](glossary.md) |
| To know whether pravo has a feature of OpenTofu | [Not in pravo](not-in-pravo.md) |

## Commands

Every page opens with the synopsis, the flags with their defaults, and the exit codes. Then it shows examples with output captured from the binary.

| Command | Does | Page |
|---|---|---|
| `pravo init` | Installs providers, writes the lock file, generates the CUE packages. | [init](init.md) |
| `pravo plan` | Shows the actions that apply takes. Saves, refreshes, reserves, judges. | [plan](plan.md) |
| `pravo apply` | Applies the specification or a saved plan, whole or in closed parts. | [apply](apply.md) |
| `pravo destroy` | Destroys everything in the state. | [destroy](destroy.md) |
| `pravo show` | Prints a saved plan. | [show](show.md) |
| `pravo output` | Prints the outputs the last apply recorded. | [output](output.md) |
| `pravo state` | Lists, shows, moves and forgets nodes. Reads the log and the locks. Queries, copies and converts the state. | [state](state.md) |
| `pravo import` | Brings an existing object under management. | [import](import.md) |
| `pravo providers` | Lists providers, pins hashes, copies a provider to a registry. | [providers](providers.md) |
| `pravo validate`, `eval`, `fmt`, `gen` | Checks the specification, prints its value, formats the files, regenerates the packages. | [validate, eval, fmt and gen](validate-eval-fmt-gen.md) |
| `pravo schema` | Prints the definition of one resource type. | [schema](schema.md) |
| `pravo why`, `drift` | Explains a change. Reports what changed outside pravo. | [why and drift](why-and-drift.md) |
| `pravo mcp` | Serves pravo to an agent over the Model Context Protocol. | [mcp](mcp.md) |
| `pravo mod` | Creates, fetches, publishes and verifies CUE modules. | [mod](mod.md) |
| `pravo version` | Prints the version of pravo and of CUE. | [version](version.md) |
| Flags, environment, exit codes | The parts every command shares. | [global flags](global-flags.md) |

## Language

The fields and definitions that a specification uses.

| Page | Covers |
|---|---|
| [Overview](spec-overview.md) | What a root is and how pravo finds the resources in it. |
| [Settings](spec-settings.md) | The `$settings` block. |
| [Resources](spec-resources.md) | Resources, data sources, ephemeral resources and function calls. |
| [Providers](spec-providers.md) | Provider declarations and their `$` fields. |
| [Moves and imports](spec-moves-and-imports.md) | `$from` and `$import`. |
| [Outputs](spec-outputs.md) | `pravo.#Output`. |
| [Environment and remote outputs](spec-env.md) | Environment values, secret environment values and the outputs of another root. |
| [Meta fields](spec-meta-fields.md) | `$lifecycle`, `$depends_on` and the other `$` fields of a resource. |
| [Tags and variables](spec-tags-and-variables.md) | `@tag()` fields and how a command fills them. |
| [Modules](spec-modules.md) | Modules that hold resources. |
| [Generated packages](spec-generated-packages.md) | The packages under `cue.mod/gen`. |

## Policies

A plan policy is a CUE file that a plan must satisfy.

| Page | Covers |
|---|---|
| [Overview](policies-overview.md) | What a policy is, where it is judged, what it binds. |
| [The plan document](policies-plan-document.md) | The fields a policy can see. |
| [Writing a policy](policies-writing.md) | The shapes that work. |
| [Operating policies](policies-operations.md) | Naming, testing and rolling out policies. |

## Data and formats

| Page | Covers |
|---|---|
| [State format](state-format.md) | The files of the state directory. |
| [The plan document](plan-document.md) | The JSON that `pravo plan --json` prints. |
| [Exit codes](exit-codes.md) | The exit code of each command, for pipelines. |
| [Telemetry](telemetry.md) | Traces and metrics. |
| [Compatibility](compatibility.md) | Which versions work together. |
| [S3-compatible stores](s3-stores.md) | The conditional requests that locks need, and how to test a store. |

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
- Output that differs between runs is shown with a placeholder: `<txn>` for a transaction, `<time>` for a time, `<age>` for an age.
- Every example ran on the version named in the footer of the page. The providers are `random`, `local`, `null` and `tls`, and the functions of `kubernetes`, so an example needs no account.
- A long output is cut. Three dots on a line of their own stand for the lines that were left out.
