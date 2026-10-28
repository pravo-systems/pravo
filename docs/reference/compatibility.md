---
title: Compatibility
description: The versions and formats that pravo reads and writes, the provider protocol it speaks, and what it does with a version it does not know.
order: 54
group: Formats and interfaces
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
---

| Surface | Marker | Behaviour |
|---|---|---|
| Provider plugin protocol | 5 and 6 | pravo starts and talks to providers that speak either. |
| Provider registry | `registry.opentofu.org` by default | pravo downloads provider archives from any registry that implements the provider registry protocol, and from OCI registries. |
| State store | `format: 1`, `2` or `3` in `meta.json` | A new store is `1`. A store with a higher number than the running pravo reads is refused. See [State format](state-format.md#formats). |
| Saved plan file | `format: 1` | A file with a higher number is refused. |
| Plan document | `format: 1` | The layout of [the plan document](plan-document.md). |
| JSON output | `--json` | One document on standard output, or one event per line for `apply` and `destroy`. |
| Exit codes | 0 to 7 | The table in [Exit codes](exit-codes.md). |
| OpenTofu state file | format version 4 | `pravo state import-tfstate` converts it. |
| CUE | the version `pravo version` prints | pravo evaluates specifications and policies with that version. |

Work in an empty directory.

```sh run
pravo version
```

```text out
pravo 0.1.0-dev
cue v0.17.1
```

The first line is pravo. The second is the version of CUE built into it. A policy is evaluated by that CUE, so a `cue` binary used to test a policy must have the same version. See [Operating policies](policies-operations.md#rolling-out-and-testing).

## Provider protocol

pravo speaks the OpenTofu provider plugin protocol over gRPC, in versions 5 and 6. A provider binary that serves either is started, configured and stopped by pravo. The version is negotiated when the provider starts, and the span of every provider call records it as `pravo.protocol`.

A provider that is already running is reached with `$protocol: "tfplugin+reattach"`, and `TF_REATTACH_PROVIDERS` is honoured. The protocol version of such a provider is detected by trying 6, then 5. See [Providers](spec-providers.md#protocol).

pravo calls these operations of the protocol:

| Operation | Used for |
|---|---|
| `GetProviderSchema` | The generated packages and the validation of a specification. `pravo init` calls it, and so does any command that finds no cached schema. |
| The validation of the provider configuration, of resources, of data sources and of ephemeral resources | The start of every plan. |
| `ConfigureProvider`, `StopProvider` | The life of a provider process. |
| `UpgradeResourceState` | A record written under an older schema version of a type. |
| `ReadResource`, `ReadDataSource` | Refresh and data sources. |
| `PlanResourceChange`, `ApplyResourceChange` | Plan and apply. |
| `ImportResourceState` | `$import` and `pravo import`. |
| `OpenEphemeralResource`, `RenewEphemeralResource`, `CloseEphemeralResource` | Ephemeral resources. |
| `CallFunction` | Provider-defined functions. |

Ephemeral resources, write-only attributes, provider-defined functions and the nested attributes of protocol 6 are supported. pravo does not call the operations for provider actions, list resources, state storage or resource identity.

The plugin protocol is the only family pravo speaks.

## Providers and registries

A provider source without a host, such as `hashicorp/random`, resolves at `registry.opentofu.org`. A source with a host, such as `registry.example.com/acme/thing`, resolves there. The registry must serve `/.well-known/terraform.json` and the `/v1/providers/...` endpoints. A provider can also be published to an OCI registry with `pravo providers copy`. See [Providers from a registry](../how-to/providers-from-a-registry.md).

The versions of the providers a root uses are in `pravo.lock.cue`, with the hash of each archive for each platform. `pravo init` writes it. Commit it.

## State and plan files

A store with a format number above the highest the running pravo reads, which is 3, and a plan file with a number above the one it writes, are refused with a message. Nothing is read from it or changed in it.

```cue file=main.cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo init > /dev/null
pravo plan --out change.plan > /dev/null
jq .format change.plan
pravo plan --json 2> /dev/null | jq .format
cat pravo.state/meta.json | jq .format
```

```text out
1
1
1
```

The plan file and the plan document have numbers of their own. A plan file and a store that carry a number above the one the running pravo writes:

```sh run exit=1
sed -i.bak 's/"format": 1/"format": 2/' change.plan
pravo show change.plan
```

```text out
Error: change.plan was written by a newer pravo (plan format 2)
```

```sh run exit=1
echo '{"format":5,"lineage":"abc","created":"2026-01-01T00:00:00Z"}' > pravo.state/meta.json
pravo plan
```

```text out
Error: state at ./pravo.state was written by a newer pravo (format 5)
```

Restore the store, and try a file that is not a plan file:

```sh run
sed -i.bak 's/"format":5/"format":1/' pravo.state/meta.json
```


```sh run exit=1
echo '{}' > not-a-plan.plan
pravo show not-a-plan.plan
```

```text out
Error: not-a-plan.plan is not a pravo plan file
```

`pravo state import-tfstate FILE` accepts format version 4 of the OpenTofu and legacy Terraform state file, which Terraform 0.12 and later write. It does not modify the file, and it starts no provider.

## JSON output

Every command accepts `--json`. Human progress moves to standard error, and standard output carries exactly one JSON document, except for `apply` and `destroy`, which stream newline-delimited events and end with a summary line. Commands that print JSON:

| Command | Document |
|---|---|
| `plan`, `show` | [The plan document](plan-document.md). |
| `apply`, `destroy` | Events, then a summary. |
| `validate` | Counts of what was found. |
| `output` | The recorded outputs. |
| `providers`, `schema`, `why`, `drift` | The providers, the definitions by type, the explanation of each address, the drift report. |
| `state log`, `state locks`, `state show`, `state query` | The transactions, the locks, a node with its dependents, the value of the expression. |
| `mod list`, `mod verify` | The modules, the verification. |

The other commands print text with `--json`: `version`, `eval`, `fmt`, `init`, `gen`, `state list`, `state graph` and `state dependents`.

```sh run
pravo apply --auto-approve > /dev/null
pravo version --json
pravo state list --json
pravo state show pet --json | jq -c 'keys'
```

```text out
pravo 0.1.0-dev
cue v0.17.1
pet
["dependents","node"]
```

An error under `--json` is also an object on standard output, `{"code":N,"kind":"error","message":"..."}`, where `N` is the exit code. A field marked "when set" in a document is left out when empty. A value that is unknown until apply is `{"$unknown": true}`.

## Exit codes

The exit codes 0 to 7 are listed with their causes in [Exit codes](exit-codes.md). Code 2 is a report, code 3 is a stale plan and code 4 is a rejection by a lock or a reservation. Code 5 is a refusal by a policy or a lock file, code 6 is an invalid specification and code 7 is a state that failed verification.

## Platforms

pravo is a single Go binary. Its `go.mod` asks for Go 1.26 or later to build. A provider archive is chosen for the platform pravo runs on. `pravo providers lock` adds the hashes of more platforms to `pravo.lock.cue`.

## Versions in the documentation

The footer of every page names the version of pravo and of CUE the page was written against. The captured output on a page comes from that version.

## See also

- [Upgrade pravo and providers](../how-to/upgrading.md) replaces the binary and moves a provider.
- [Providers](../concepts/providers.md) describes sources, protocols and the generated packages.
- [`pravo version`](version.md) prints the versions.
