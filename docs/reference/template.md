---
title: pravo template
description: List the templates of a specification and make the CUE of a new instance from the command line, checked against the specification and the policies in force, as the viewer's wizard does. Flags, the JSON documents and exit codes.
order: 27
group: Commands
keywords: "template catalog self service form wizard instance json list try blueprint platform"
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo template list`, `pravo template try TEMPLATE --name LABEL [--at PATH] [--set INPUT=VALUE]... [--secret INPUT=NAME]... [--secret-env INPUT=NAME]...` |
| Changes infrastructure | No. |
| Starts providers | No. |
| Writes | Nothing: no file, no lock, no state, no generated package. |
| Requires | A specification that loads, and `pravo init`. A sealed state is read with your reader identity, as [`pravo serve`](serve.md) reads it. |
| Taught in | [Offer a template to your team](../how-to/offer-a-template.md) and [Make a new instance from a template](../how-to/add-an-instance-from-a-template.md). |

A template is a definition that embeds `pravo.#Template` ([Templates and self-service](../concepts/templates.md)). The viewer lists the templates and its wizard turns a form into the CUE of a new instance. `pravo template` gives the same two answers on the command line and in JSON, so that a script, a pipeline or an agent can read the catalog and check a request without a browser.

Both commands run the code of the viewer in process. They say what the wizard says, with the same checks and the same redaction, and no server listens.

## list

```sh
pravo template list
pravo template list --json
```

Lists the templates of the specification: the name, the title, the definition, the number of inputs, how many resources one instance holds and how many instances exist. A specification without a template says so.

With `--json` the document is the one that the Templates section of the viewer reads. It has the fields `version`, `revision`, `spec_ok` and `templates`. Each template has `name`, `title`, `description`, `definition`, `source`, `inputs` in the order that `$template.inputs` gives, `problems`, `resources`, `instances`, `targets` and `file`. Each input has `name`, `kind`, `type`, `label`, `doc`, `required`, `default` and, where they apply, `options`, `choices`, `entity`, `long` and `secret`. When `spec_ok` is false, the specification does not load and the command exits with 6.

## try

```sh
pravo template try service --name checkout --at services --set name=checkout --set port=8081
```

Makes the CUE of a new instance of `service` named `checkout` in the struct `services`, loads the specification with that CUE laid over it in memory, and says what the specification and the policies in force say of it. The CUE is for a person to paste, in the file that the answer names. The plan of the pasted instance is made and applied as any other.

| Flag | Default | Meaning |
|---|---|---|
| `--name LABEL` | required | The name of the new instance: letters, digits, dashes and dots, up to 63. |
| `--at PATH` | top level | The struct that the instance goes into, such as `services`. |
| `--set INPUT=VALUE` | none | A value for an input that is not a secret. For an input of kind `string` the value is the text. For an `enum`, the text of an option. For any other kind it is CUE, such as `8081`, `true` or `["a", "b"]`. Repeatable. |
| `--secret INPUT=NAME` | none | A secret input that is read from the secret `NAME` of the state. The state must be sealed. Repeatable. |
| `--secret-env INPUT=NAME` | none | A secret input that is read from the environment variable `NAME` when pravo runs. Repeatable. |
| `-t`, `--tag key=value` | none | The tags of the specification. Repeatable. |
| `--access-dir DIR` | `.pravo/access` | The directory of the readers file. |

A secret input never takes a value. The CUE that `try` makes declares where the secret is read from, and a value never enters it, the answer or the log.

The answer has a state:

| State | Meaning |
|---|---|
| `valid` | The specification loads with the instance, and nothing is wrong with it. |
| `incomplete` | An input without a default has no value. |
| `invalid` | The specification does not load with the instance. `problems` says why, with the input when it can be told. |
| `exists` | The address is taken. |

For a valid instance the answer also lists the resources that the instance creates, and what the policies in force say of an approximate plan that creates them: `none`, `holds`, `refused` or `unsure`. The preview needs a sealed state, which keeps the policies.

With `--json` the document is the one that the wizard reads: `version`, `template`, `snippet`, `file`, `address`, `state`, `problems`, `creates`, `secrets`, `policy` and `took_ms`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | The instance is valid, and the policies hold or none judges. |
| 5 | The instance is valid and a policy refuses it. |
| 6 | Any other state of the answer, an unknown template or input, or a specification that does not load. |

[Exit codes](exit-codes.md) lists the rest. With `--json`, the document is written before the command exits with 5 or 6.
