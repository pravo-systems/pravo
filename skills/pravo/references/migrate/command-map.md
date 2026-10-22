<!-- Generated from docs/migrate/command-map.md by tools/skillgen. Do not edit. -->

# Command map

Each OpenTofu command, flag, environment variable and file beside the pravo command, flag, variable or file that replaces it.

A pravo command takes the same place in a pipeline as the tofu command it replaces. The table below lists the commands first, then the flags, the environment variables and the files. A row that says `None` gives the replacement in the last column.

`pravo -C dir` runs a command as if it started in `dir`, as `tofu -chdir=dir` does. A flag with a long name takes one dash or two: `-out` and `--out` are the same flag.

## Commands

| OpenTofu | pravo | Note |
|---|---|---|
| `tofu init` | `pravo init` | Installs the providers, writes `pravo.lock.cue` and generates the CUE packages under `cue.mod/gen`. `--upgrade` ignores the lock file. |
| `tofu validate` | `pravo validate` | Evaluates the specification and checks it against the provider schemas. No provider starts. |
| `tofu plan` | `pravo plan` | A plan reaches only what a change reaches. |
| `tofu plan -out=f` | `pravo plan -out f` | Writes the plan only when every named policy holds. |
| `tofu apply` | `pravo apply` | Plans, asks, applies. `--auto-approve` skips the question. |
| `tofu apply f` | `pravo apply f` | Asks nothing. `-only addr` applies part of the plan. |
| `tofu destroy` | `pravo destroy` | `--auto-approve` skips the question. |
| `tofu show f` | `pravo show f` | Prints a saved plan. `--json` prints the plan document. |
| `tofu show` (state) | `pravo state list`, `pravo state show addr` | One record at a time. |
| `tofu output` | `pravo output [name]` | `--json` for JSON. A string prints quoted. |
| `tofu console` | `pravo eval -e EXPR` | `pravo eval` prints the whole evaluated specification. `-c` fails unless every value is concrete. |
| `tofu fmt` | `pravo fmt` | Formats the `.cue` files of the current directory. It has no flags. |
| `tofu graph` | `pravo state graph` | `--dot` prints Graphviz. |
| `tofu import addr id` | `pravo import addr id` | The declaration `$import: "id"` does the same in the specification. |
| `tofu refresh` | None | `pravo drift` reads every resource and reports drift. `pravo drift --due --limit N` reads the oldest records past their refresh age. `pravo plan -refresh=all` refreshes every record. |
| `tofu taint`, `tofu untaint` | None | `pravo plan -replace addr` and `pravo apply -replace addr`. |
| `tofu force-unlock` | `pravo state unlock addr` | `pravo state locks` lists the locks and marks the stale ones; `pravo state unlock --stale` releases them. Locks are per resource. |
| `tofu workspace ...` | None | A tag selects the environment: `-t env=prod`. |
| `tofu test` | None | `pravo validate`, `pravo eval -c` and a policy on a saved plan. |
| `tofu get` | `pravo mod get path@version` | `pravo init` installs providers. |
| `tofu login`, `tofu logout` | None | A registry reads its credentials from the environment. |
| `tofu providers` | `pravo providers` | Lists the providers the specification requires. |
| `tofu providers lock` | `pravo providers lock` | Pins the archive hashes for more platforms. |
| `tofu providers schema -json` | `pravo providers schema name`, `pravo schema type` | Prints the schema as CUE. |
| `tofu state list` | `pravo state list` | |
| `tofu state show addr` | `pravo state show addr` | Attributes as CUE, with the bookkeeping as comments. |
| `tofu state mv a b` | `pravo state mv a b` | Within one state. |
| `tofu state rm addr` | `pravo state rm addr` | Forgets the record and leaves the object. |
| `tofu state pull` | `pravo state pull dir` | Copies the whole graph to a local directory. |
| `tofu state push` | `pravo state push dir` | Copies a local directory into the configured backend. |
| `tofu state replace-provider` | None | `$from` on the provider struct names the old source. |
| `tofu version` | `pravo version` | Prints the versions of pravo and of CUE. |
| `tofu metadata functions` | None | [Function map](function-map.md). |
| `tofu -chdir=dir` | `pravo -C dir` | |

These commands have no counterpart in OpenTofu:

| pravo | What it does |
|---|---|
| `pravo why addr` | Explains why a plan changes, skips or replaces a resource. |
| `pravo drift` | Reads every resource and exits 2 when one changed outside pravo. |
| `pravo state query EXPR` | Evaluates a CUE expression over the state graph. |
| `pravo state dependents addr` | Lists everything a change to the resource can reach. |
| `pravo state log` | Lists recorded transactions, newest first. |
| `pravo state import-tfstate f` | Converts an OpenTofu or Terraform state in format 4. |
| `pravo gen` | Regenerates the CUE packages under `cue.mod/gen`. |
| `pravo mcp` | Serves the commands to an agent over the Model Context Protocol. |

## Flags

| OpenTofu | pravo | Note |
|---|---|---|
| `-var name=value` | `-t name=value` | For a field with `@tag(name)`. A string, a number or a boolean. |
| `-var-file=f`, `*.tfvars` | None | A `.cue` file that sets the field. |
| `-out=f` | `-out f` | On `plan`. |
| `-destroy` | `--destroy` | On `plan`. `pravo destroy` also exists. |
| `-detailed-exitcode` | `--detailed-exitcode` | On `plan`. Exit 2 when the plan has changes among the resources it looked at. `pravo drift` reads the rest. |
| `-refresh=false` | `-refresh=false` | `-refresh=age` reads the records that the plan looks at when they are older than their refresh age. `-refresh=expired` reads every record that is older than its refresh age. `-refresh=all` reads every record. |
| `-refresh-only` | None | `pravo drift`. |
| `-replace=addr` | `-replace addr` | On `plan` and `apply`. |
| `-target=addr` | None | Scoped plans, and `apply f -only addr`. |
| `-auto-approve` | `--auto-approve` | On `apply` and `destroy`. |
| `-json` | `--json` | Prints the plan document on standard output and progress on standard error. |
| `-no-color` | `--no-color` | |
| `-input=false` | None | `pravo apply f` asks nothing. |
| `-lock=false`, `-lock-timeout` | None | Locks are per resource and always on. |
| `-parallelism=n` | None | |
| `-compact-warnings` | None | |
| `-backend-config`, `-reconfigure`, `-migrate-state` | None | `$settings: state:` names the backend. `pravo state push` moves a graph. |
| `-upgrade` | `--upgrade` | On `init`. |
| `-state=path` | None | `$settings: state: path`. |
| — | `--policy file` | On `plan`, `apply`, `destroy` and `mcp`. Repeatable. |
| — | `--actor`, `--reason` | Labels recorded on transactions and plans. |
| — | `-v`, `-vv` | Logs what pravo decides to standard error. |

## Environment variables

| OpenTofu | pravo | Note |
|---|---|---|
| `TF_LOG=debug` | `PRAVO_LOG=debug` | `trace` adds the providers' own log. `-v` and `-vv` do the same. |
| `TF_VAR_name` | `pravo.#Env` | `x: pravo.#Env & {name: "X"}` reads the variable and records it where it is used. |
| `TF_VAR_name` holding a secret | `pravo.#SecretEnv` | An ephemeral value. It never reaches the state. |
| `TF_PLUGIN_CACHE_DIR` | `PRAVO_HOME` | The directory of the provider and schema cache. The default is `~/.pravo`. |
| `TF_WORKSPACE` | None | `-t env=prod`. |
| `TF_IN_AUTOMATION`, `TF_INPUT` | None | |
| — | `PRAVO_ACTOR`, `PRAVO_REASON` | The same as `--actor` and `--reason`. |
| — | `PRAVO_RECORD`, `PRAVO_REPLAY` | Record provider calls to a file and replay them. |

## Files

| OpenTofu | pravo | Note |
|---|---|---|
| `*.tf` | `*.cue` in one package | Every file of the package is unified. |
| `.terraform.lock.hcl` | `pravo.lock.cue` | Commit it. |
| `.terraform/providers` | `$PRAVO_HOME/providers` | Shared by every project. |
| `.terraform/modules` | `cue.mod/module.cue` | CUE modules that `pravo mod get` adds are listed there and pinned in `pravo.lock.cue`. |
| — | `cue.mod/gen` | The generated provider packages. Commit it, or run `pravo init`. |
| `terraform.tfstate` | `pravo.state/` | A directory with one file per resource, `outputs.json` and a transaction log. |
| `terraform.tfvars` | None | A `.cue` file. |
| `override.tf` | None | Unification: a second file that adds to a declaration. |

## Check the table

Every pravo command in the tables exists. This prints the ones that answer `--help`:

```sh
for c in init validate plan apply destroy show output import eval fmt providers schema why drift mcp mod gen version; do pravo $c --help > /dev/null 2>&1 && echo "pravo $c"; done
```

Output:

```text
pravo init
pravo validate
pravo plan
pravo apply
pravo destroy
pravo show
pravo output
pravo import
pravo eval
pravo fmt
pravo providers
pravo schema
pravo why
pravo drift
pravo mcp
pravo mod
pravo gen
pravo version
```

```sh
for c in list show mv rm pull push log locks unlock graph dependents query import-tfstate; do pravo state $c --help > /dev/null 2>&1 && echo "pravo state $c"; done
for c in lock schema copy; do pravo providers $c --help > /dev/null 2>&1 && echo "pravo providers $c"; done
```

Output:

```text
pravo state list
pravo state show
pravo state mv
pravo state rm
pravo state pull
pravo state push
pravo state log
pravo state locks
pravo state unlock
pravo state graph
pravo state dependents
pravo state query
pravo state import-tfstate
pravo providers lock
pravo providers schema
pravo providers copy
```

The flags are listed by `pravo plan --help` and `pravo apply --help`. A flag that does not exist is refused before anything runs:

```sh
for f in -target=x -parallelism=5 -lock=false -var-file=x; do pravo apply $f 2>&1 | grep -m1 '^Error'; done
```

Output:

```text
Error: unknown flag: --target
Error: unknown flag: --parallelism
Error: unknown flag: --lock
Error: unknown flag: --var-file
```

## Recap

- Each tofu command has a pravo command, or a row that says what replaces it.
- Flags that select resources, environments or variables become scoped plans, `-t` and fields.
- The state is a directory, the lock file is `pravo.lock.cue`, and the cache is `$PRAVO_HOME`.

Next: [Construct matrix](construct-matrix.md) lists what pravo supports, supports in part and does not support.
