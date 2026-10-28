<img src="site/assets/logo.svg" alt="" width="56" height="56" align="left">

# pravo

pravo specifies infrastructure in [CUE](https://cuelang.org) and runs the OpenTofu providers you already use, unchanged. Two values for one field must agree, so a conflict between values you write is an error before any provider starts.

## Example

A specification is the set of `.cue` files in one directory. This one declares a provider, an environment with a default, and one resource:

```cue
package infra

import loc "pravo.dev/providers/hashicorp/local"

// The default is "dev". -t env=prod selects the other value; nothing else is accepted.
env: *"dev" | "prod" @tag(env)

$local: loc.#Provider

hello: $local.#Resource.local_file & {filename: "hello-\(env).txt", content: "hello from \(env)\n"}
```

`pravo init` installs the provider and generates its CUE package. `pravo plan` prints what an apply would do. `pravo apply` does it. The output is abridged where it shows `...`.

```text
$ pravo init
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  local = hashicorp/local v2.9.1
...
pravo has been initialised. Run "pravo plan" to see what it would do.

$ pravo plan -t env=prod
pravo will perform the following actions:

  # hello (local_file) will be created
  + hello: {
  +     content:              "hello from prod\n"
...
  +     filename:             "hello-prod.txt"
  +     id:                   (known after apply)
    }

Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1

$ pravo apply -t env=prod -auto-approve
...
hello: Creation complete after 0s [id=421ed0d3d33f878272c9215e5388263188a47bd9]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn 20261020T023802Z-fd74bb]

$ pravo plan -t env=qa
Error: evaluating .:
env: 2 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./main.cue:6:7
env: conflicting values "prod" and "qa":
    ./main.cue:6:15
```

The last command exits with status 6 and starts no provider. `env` accepts `"dev"` or `"prod"`. The value `qa` is neither, so each alternative fails, and each error gives the position in `main.cue` where that alternative is written.

## Scope

pravo replaces the OpenTofu language (HCL), the OpenTofu evaluator and the OpenTofu state format. It does not replace providers. It speaks the provider plugin protocol, versions 5 and 6, so the providers on the OpenTofu registry run as they are. pravo has no workspaces, no `-target` and no provisioners; `docs/reference/not-in-pravo.md` names what to use in their place.

## Install, build and run

Install a release with `curl -fsSL https://pravo.systems/install.sh | sh` on macOS and Linux, or `irm https://pravo.systems/install.ps1 | iex` in PowerShell on Windows. The archives, the checksums and the provenance attestations are on the [releases page](https://github.com/pravo-systems/pravo/releases).

Go 1.26 or newer builds pravo from a checkout. The binary needs neither Go nor OpenTofu to run.

```sh
go build -o bin/pravo ./cmd/pravo
export PATH="$PWD/bin:$PATH"
pravo version
```

`pravo init` downloads providers from `registry.opentofu.org` into `~/.pravo` (`$PRAVO_HOME`) and generates the CUE packages that a specification imports.

The roots in `examples/` need no cloud account. They use `hashicorp/random`, `hashicorp/local`, `hashicorp/null` and `hashicorp/tls`, and the functions of `hashicorp/kubernetes`, which need no cluster.

```sh
cd examples/getting-started
pravo init
pravo plan
pravo apply
```

## The viewer

`pravo serve` shows the specification, the state and a saved plan of a root as an animated graph of groups and resources, in a browser. It listens on the loopback interface, needs the token that it prints, withholds sensitive values on the server and writes nothing.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/img/serve/overview-dark.png">
  <img src="docs/img/serve/overview-light.png" alt="The pravo viewer as it opens. A filter box in the toolbar narrows the nodes. The graph fills the window and draws the groups that fit as lanes and the others as cards, joined by arrows. The inspector comes in when something is selected." width="1440" height="900">
</picture>

```sh
pravo serve                          # in a root that has been applied
pravo serve --plan change.plan       # overlay a saved plan
```

`docs/how-to/explore-your-estate.md` walks through it, and `docs/reference/serve.md` is the reference. The pictures are made by `go run ./tools/serveshots -bin bin/pravo -out docs/img/serve`.

## Sealed state

`pravo protect init --encrypt` encrypts the attributes, outputs and stored plans of a state to a list of readers that the repository keeps in `.pravo/access/readers.txt`. `pravo id new` makes an identity and prints the line for that file. Addresses, types, names and dependencies stay readable, so the graph and the plans keep working. Protection is off by default, and a state that has it off is unchanged. `docs/how-to/seal-the-state.md` walks through it, and `docs/concepts/signatures-and-roles.md` says what it protects and what it leaves open.

## The skill for agents

`pravo skill install` writes a skill for AI agents to `~/.claude/skills/pravo`: a short guide and the documentation as Markdown, built from `docs/` by `go run ./tools/skillgen` and embedded in the binary, so that it matches the version that installs it. `docs/reference/skill.md` describes it. Each release carries it as `pravo-skill.zip`.

## Using pravo as a library

`github.com/pravo-systems/pravo/pravo` is a Go package with what the commands do: open a workspace, plan, apply, store plans, check drift. It is v0 and unstable. `examples/embed` is a small program that uses it; the package documentation (`go doc ./pravo`) has the rest.

## Documentation

- `docs/` holds the Markdown sources: Learn (CUE from zero, then pravo), Migrate (from OpenTofu or Terraform), How-to, Concepts and Reference. Begin with `docs/how-to/quick-start.md`.
- `go run ./tools/sitegen` renders `docs/` and the landing page in `site/` into `site/public/`. Open `site/public/index.html` from the file system, or serve the directory.
- `ARCHITECTURE.md` describes how pravo is built, for contributors.

## Layout

| Path | Contents |
|---|---|
| `cmd/pravo/` | The command line, the JSON output and the MCP server. |
| `internal/config/` | Loads a root; finds resources, providers and outputs; fingerprints a root. |
| `internal/convert/` | CUE values to provider values and back; known, unknown and null; reference analysis. |
| `internal/engine/` | Dependency graph, scheduler, plan, apply, saved and stored plans, plan policies. |
| `internal/state/` | The state store: records, transactions, locks; local and S3 backends. |
| `internal/plugin/` | Provider processes; plugin protocol 5 and 6 adapters; recording and replay. |
| `internal/tfplugin/` | Protocol definitions (`.proto`, MPL-2.0), generated messages, client stubs. |
| `internal/registry/` | Provider registry and container registry clients, download and cache. |
| `internal/schema/` | Provider schema model and the CUE generator. |
| `internal/tfstate/` | Converts an OpenTofu state file to state records. |
| `internal/ui/`, `internal/plog/`, `internal/telemetry/` | Plan rendering, the debug log, OpenTelemetry. |
| `internal/serve/` | The read-only web viewer behind `pravo serve`: the server, the JSON contract in `api.go` and the interface in `web/`. |
| `internal/fakes3/` | In-memory S3-compatible server for tests. |
| `tools/sitegen/` | Documentation site generator, its HTML contract and the contrast check. |
| `tools/capture/` | Runs the examples in the documentation and compares their output. |
| `tools/hclcheck/` | Runs the OpenTofu listings of the documentation through `tofu fmt`, `init` and `validate`. |
| `tools/_weblint/` | ESLint and Stylelint for the viewer and the site. The underscore keeps the Go tools out of its `node_modules`. |
| `tools/serveshots/` | Applies the estates in `scenarios/`, starts `pravo serve` and makes the screenshots of the documentation with headless Chrome. |
| `tools/protogen/`, `tools/fakes3/`, `tools/fakeoci/` | Protocol message generator; an in-memory S3 bucket and an in-memory container registry for trying state and modules. |
| `docs/` | Documentation sources. `docs/AUTHORING.md` fixes the format. |
| `site/` | Site configuration, landing page, style sheet, scripts, recorded landing data. |
| `examples/` | Four runnable roots. |
| `templates/module/` | Template repository for a shared module. |

## Contributing

Tests:

```sh
go test ./...
go test -short ./...    # skips the tests that build the binary or run sitegen
```

Linters. `.golangci.yml` configures golangci-lint for the Go code, and `tools/_weblint` has ESLint and Stylelint for the JavaScript and CSS of the viewer and the site. Each exclusion in them has the reason beside it.

```sh
golangci-lint run ./...                  # a build made with the Go version of go.mod
npm --prefix tools/_weblint ci            # once
npm --prefix tools/_weblint run lint
```

Documentation. `docs/AUTHORING.md` fixes the structure, the voice and the Markdown extensions. `tools/capture` runs every command on a page against the real binary and compares the output beside it:

```sh
go run ./tools/capture -check docs/how-to/quick-start.md     # one page
go run ./tools/capture -check ./docs                  # every page
go run ./tools/capture -update docs/how-to/quick-start.md    # rewrite the output blocks from a run
go run ./tools/hclcheck                              # validate every OpenTofu listing with tofu
go run ./tools/sitegen -pravo-bin bin/pravo           # build the site, lint it, check every link
```

Continuous integration. `.github/workflows/ci.yml` runs on every push to `main` and on every pull request. Its jobs start together and run side by side: formatting, vet and a build for every platform; golangci-lint for each of Linux, Windows and macOS, and ESLint and Stylelint for the viewer and the site; the Go tests split by package, with the race detector on the engine and the state; the tools; the documentation, one job for each section; the demos; the OpenTofu listings; and the site. The job `ci` passes when all of them do, and is the one check to require.

## License

Apache-2.0. The protocol definitions under `internal/tfplugin/proto` are copied from the terraform-plugin-go repository and are licensed MPL-2.0. pravo is an independent project, affiliated with neither the OpenTofu project nor HashiCorp.
