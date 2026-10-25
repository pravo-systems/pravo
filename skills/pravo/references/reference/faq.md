<!-- Generated from docs/reference/faq.md by tools/skillgen. Do not edit. -->

# FAQ

Short, factual answers to the questions asked first.

| Question | Answer in one line |
|---|---|
| [Does pravo need OpenTofu?](#does-pravo-need-opentofu) | No. |
| [Which providers work?](#which-providers-work) | Those that speak plugin protocol 5 or 6. |
| [Which registry is used?](#which-registry-is-used) | `registry.opentofu.org`, or the host in the import path. |
| [Can pravo read my OpenTofu state?](#can-pravo-read-my-opentofu-state) | Once, by conversion. |
| [Is there remote state?](#is-there-remote-state) | Yes: S3 and compatible stores. |
| [Do I need to split a large estate into several states?](#do-i-need-to-split-a-large-estate-into-several-states) | Not to keep plans short. |
| [How do I pass a variable?](#how-do-i-pass-a-variable) | `-t name=value`. |
| [How do I pass a secret?](#how-do-i-pass-a-secret) | `pravo.#SecretEnv`. |
| [How do I enforce rules on plans?](#how-do-i-enforce-rules-on-plans) | `--policy FILE`. |

## Does pravo need OpenTofu?

No. pravo downloads provider binaries and starts them itself. The `tofu` binary is never run. pravo finds an already unpacked provider in its own store, then in the plugin cache that `TF_PLUGIN_CACHE_DIR` names or in `~/.terraform.d/plugin-cache`.

## Which providers work?

Providers that serve plugin protocol 5 or 6 are started and called for the operations that [Compatibility](compatibility.md#provider-protocol) lists. Ephemeral resources, write-only attributes and provider-defined functions are supported.

## Which registry is used?

`registry.opentofu.org`. A package imported from `pravo.dev/providers/hashicorp/random` resolves there. To use another registry, put its host in the import path: `pravo.dev/providers/registry.example.com/hashicorp/random`. Any registry that serves `/.well-known/terraform.json` and the `/v1/providers/...` endpoints works, and so does an OCI registry.

## Do I have to declare providers?

Yes, one line each: `$random: rnd.#Provider & {$version: "~> 3.6"}`. A provider that needs no configuration is `$local: loc.#Provider`. Every resource is built through a declared provider. See [Providers](spec-providers.md).

## Can pravo read my OpenTofu state?

`pravo plan` and `pravo apply` never read `terraform.tfstate`, and a `state.path` that names it is an error. `pravo state import-tfstate FILE` converts a state file of format version 4 into the state graph, once. It prints the renamed instances and the records it skips. See [Not in pravo](not-in-pravo.md#not-carried-over-by-a-conversion).

## Is there remote state?

Yes. The state graph can live in an S3 bucket or in an S3-compatible store that honours conditional writes (`If-None-Match: *` and `If-Match`). [S3-compatible stores](s3-stores.md) says how to test a store. The layout is the same as the local one. See [Settings](spec-settings.md#s3-keys) and [State format](state-format.md#remote-layout).

## Do I need to split a large estate into several states?

Not to keep plans and applies short. The state keeps one record for each resource, with its dependencies. A plan reads and plans what a change reaches, a refresh age keeps it from reading a record again within that age, and an apply locks only the nodes it writes. What a command does for a change follows the change, whatever the number of records. Evaluating the specification and opening the records grow with the estate, and a change that reaches every resource costs what the estate costs. Split a state for ownership, permissions or blast radius. See [Cost follows the change](../concepts/state.md#cost-follows-the-change).

## What do "skipped", "not read" and "within refresh age" mean?

A plan walks the part of the graph a change reaches. A resource whose configuration hash matches the state, and whose provider and schema versions match, is not validated or planned. Its record is not read from the provider either, unless the plan reaches a resource that reads it, the resource sets a refresh age of its own that has run out, or the record has a mark. The `Graph:` line counts what was planned, skipped, refreshed, within its refresh age and not read. `--full` plans every resource, `-refresh=expired` reads every record that is past its refresh age and `-refresh=all` reads every record. See [Plans](../concepts/plans.md#scoped-plans) and [The plan document](plan-document.md#summary-and-stats).

## Does a plan find drift in every resource?

No. A plan finds drift in the resources it looks at: those it reaches and those they read. `pravo drift` reads every record, and `pravo drift --due --limit N` reads the oldest N records that are past their refresh age. See [Refresh age](../concepts/plans.md#refresh-age) and [CI](../how-to/ci.md).

## Does a plan change anything?

A plan is not read-only. It installs providers, writes the lock file, refreshes the records it looks at whose refresh age ran out, marks a record whose object differs or is gone, renames records for `$from`, adopts the objects named by `$import`, reads data sources and opens ephemeral resources. It creates, updates and destroys no infrastructure. See [Before the verdict](policies-operations.md#before-the-verdict).

## Where are `moved` and `import` blocks?

They are fields of the resource they describe. `$from: "old.address"` is a move, and `$import: "id"` adopts an object. See [Moves and imports](spec-moves-and-imports.md).

## Where are provisioners, workspaces and `-target`?

They are absent. [Not in pravo](not-in-pravo.md) lists each with its replacement.

## How do I pass a variable?

With `-t name=value` on any command that evaluates the specification. It sets the field tagged `@tag(name)`. Defaults and constraints are CUE on the field. `-T` injects the system tags. There is no variables file: a `.cue` file of the package sets a set of values. See [Tags and variables](spec-tags-and-variables.md).

## How do I pass a secret?

Not with `-t`: a tag value is written into saved plans and stored plans. Read it from the environment with `pravo.#SecretEnv` and use it in provider configuration, in a write-only attribute or in another ephemeral resource. pravo does not let it reach anything that is recorded. See [Environment and remote outputs](spec-env.md#secret-env).

## How does one root use the outputs of another?

With `pravo.#RemoteOutputs`. Only the other root's `outputs.json` is read, so read access to that one object is enough. Apply the producer first. See [Environment and remote outputs](spec-env.md#remote-outputs).

## How do I enforce rules on plans?

With a plan policy: one CUE file that whoever runs pravo names with `--policy FILE` on `plan`, `apply`, `destroy` or `mcp`. A plan that violates it is printed and refused with exit code 5. A run that omits the flag is not checked. See [Policies overview](policies-overview.md) and [the trust anchor](../concepts/policies.md#the-trust-anchor).

## How do I see what pravo is doing?

Run the command with `-v`. It logs the load, the provider versions and where they came from, the state it opened and, for each resource, the action and the reasons. `-vv` adds the providers' own log. `pravo why ADDR` explains one plan. `OTEL_TRACES_EXPORTER=console` prints traces. See [Telemetry](telemetry.md).

## What is the address of a resource?

The CUE path at which its struct sits: `pet`, `api.config`, `servers[0]`, `pets.eu`. `pravo state list` prints the addresses, and `--replace`, `pravo import` and `pravo why` take them. See [Resources](spec-resources.md#addresses).

## Do I have to run `pravo init` before `pravo validate`?

When the specification imports a generated package, yes. `pravo init` installs the providers, writes the lock file, and writes the packages under `cue.mod/gen/`. A directory of plain CUE with no import validates without it.

File `main.cue`:

```cue
package demo

size: >=1 & <=20 | *5
name: "web\(size)"
```

```sh
pravo validate
```

Output:

```text
Success! 0 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 0 providers.
```

## Does `pravo eval` write anything?

No. It reads the state of the root to fill in the values that are recorded there. When the state does not exist, `eval` reads an empty one and creates nothing.

```sh
pravo eval
test -e pravo.state || echo "no state directory"
```

Output:

```text
size: 5
name: "web5"
no state directory
```

## How do I upgrade a provider?

`pravo init --upgrade` ignores the lock file and selects the newest version that matches `$version`. It rewrites `pravo.lock.cue` and the generated package. See [init](init.md).

## Does pravo send telemetry?

No. pravo exports OpenTelemetry traces and metrics only when the `OTEL_*` variables configure it. A command that did some work also keeps its own trace in `.pravo/traces` of the project, which stays on the machine and which `PRAVO_TRACES=off` turns off. See [Telemetry](telemetry.md).

## What is the license?

Apache-2.0. The protocol definitions under `internal/tfplugin/proto` are licensed MPL-2.0. Providers keep their own licenses, which the registry shows for each provider.

## How do I install it?

Install a release with the script at `https://pravo.systems/install.sh` (macOS and Linux) or `install.ps1` (Windows), or download an archive from the releases page. To build it instead, use a checkout of the repository with Go 1.26 or newer: `go build -o bin/pravo ./cmd/pravo`. See the [quick start](../how-to/quick-start.md#1-install-pravo).

## Which version is this?

`pravo version` prints the version of pravo and of the CUE it embeds. The footer of each page names the versions it was written against. See [Compatibility](compatibility.md).

## See also

- [Not in pravo](not-in-pravo.md) lists the features that pravo does not have.
- [Error catalogue](errors.md) lists the messages pravo prints.
