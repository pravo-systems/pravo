---
title: Not in pravo
description: The commands, flags, blocks and files of OpenTofu and Terraform that pravo does not have, and what to use in each place.
order: 63
group: Lookup
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
keywords: "workspace provisioner taint lifecycle precondition postcondition check"
---

pravo has no workspaces. Select the environment with `-t env=prod`. Each row below states one absent feature and the replacement. For the full mapping of constructs, commands and functions, see [Migrate](../migrate/index.md).

## Commands and flags

| In OpenTofu or Terraform | In pravo | Use |
|---|---|---|
| `workspace`, `terraform.workspace` | Absent. | A tagged field selects the environment: `env: *"dev" \| "prod" @tag(env)` and `-t env=prod`. The state path takes the tag: `$settings: state: path: "state/\(env)"`. See [Settings](spec-settings.md). |
| `-target` | Absent. | A plan reaches only what a change reaches. `pravo plan --out FILE` and `pravo apply FILE --only ADDR` apply a closed part of a plan that was reviewed whole. |
| `taint`, `untaint` | Absent. | `pravo plan --replace ADDR` replaces a resource although the specification did not change. |
| `refresh`, `plan -refresh-only` | Absent. | `pravo drift` reads every resource and reports the ones that changed. `-refresh=all` reads every record in a plan. |
| `console` | Absent. | `pravo eval -e EXPRESSION` evaluates an expression in the scope of the specification. |
| `graph` | Absent. | `pravo state graph` prints the edges of the state. `-dot` prints Graphviz. |
| `force-unlock` | Absent. | `pravo state unlock ADDR` removes the lock or the reservation of one node. |
| `test` | Absent. | `pravo validate` checks the constraints before a provider starts. `pravo plan --policy FILE` judges a plan. |
| `login`, `logout` | Absent. | Credentials come from the environment and from the files that each provider documents. |
| `-var`, `-var-file`, `TF_VAR_*` | Absent. | `-t name=value` fills a field tagged `@tag(name)`. `pravo.#Env` and `pravo.#SecretEnv` read environment variables. |
| `-lock`, `-lock-timeout` | Absent. | Each node has its own lock. A command that needs a node held by another is rejected. |
| `-parallelism` | Absent. | Independent nodes run at the same time. |
| `-input=false` | Absent. | `apply` and `destroy` ask for approval, and `--auto-approve` skips the question. No other command prompts. |
| `-backend-config` | Absent. | The state settings are CUE. They can depend on a tag. |
| `output -raw` | Absent. | `pravo output NAME` prints JSON. `pravo output NAME \| jq -r .` prints the bare string. |

```sh run
for c in workspace taint untaint console test graph refresh force-unlock login logout; do pravo $c 2>&1 | grep '^Error'; done
for f in -target=pet -var=a=1 -var-file=x -lock=false -lock-timeout=5s -parallelism=2 -refresh-only -input=false -backend-config=x; do pravo plan $f 2>&1 | grep '^Error'; done
pravo output --raw 2>&1 | grep '^Error'
```

```text out
Error: unknown command "workspace" for "pravo"
Error: unknown command "taint" for "pravo"
Error: unknown command "untaint" for "pravo"
Error: unknown command "console" for "pravo"
Error: unknown command "test" for "pravo"
Error: unknown command "graph" for "pravo"
Error: unknown command "refresh" for "pravo"
Error: unknown command "force-unlock" for "pravo"
Error: unknown command "login" for "pravo"
Error: unknown command "logout" for "pravo"
Error: unknown flag: --target
Error: unknown flag: --var
Error: unknown flag: --var-file
Error: unknown flag: --lock
Error: unknown flag: --lock-timeout
Error: unknown flag: --parallelism
Error: unknown flag: --refresh-only
Error: unknown flag: --input
Error: unknown flag: --backend-config
Error: unknown flag: --raw
```

## Language

| In OpenTofu or Terraform | In pravo | Use |
|---|---|---|
| `variable` block with `default`, `type` and `validation` | Absent. | A field with a default, a type and a constraint: `retention: *30 \| int & >0 @tag(retention,type=int)`. See [Tags and variables](spec-tags-and-variables.md). |
| `locals` | Absent. | Any field. A field that starts with `_` is hidden, and `let` names a value inside a struct. |
| `count`, `for_each`, `dynamic` | Absent. | A comprehension: `for r in regions {"\(r)": ...}`. The key is the last element of the address. See [Resources](spec-resources.md#several-resources-from-one-rule). |
| Conditional expression `? :`, `for` expressions | Absent. | `if` and `for` clauses, and CUE disjunctions with defaults. |
| `depends_on` | `$depends_on` | A list of references, for ordering with no data flow. See [Meta fields](spec-meta-fields.md). |
| `lifecycle` | `$lifecycle` | `create_before_destroy`, `prevent_destroy`, `ignore_changes` and `refresh`. There is no `replace_triggered_by`: use `--replace`. |
| `precondition`, `postcondition`, `check` | Absent. | A constraint on a field you write is evaluated before any provider starts. A constraint on a value the provider computes is checked during apply, after the provider has returned it. A rule about a plan is a policy. See [Policies](policies-overview.md). |
| `moved`, `import`, `removed` blocks | Absent. | `$from` and `$import` on the resource. `pravo state rm ADDR` forgets a record without destroying the object. See [Moves and imports](spec-moves-and-imports.md). |
| `module` block, `source`, `version` | Absent. | A definition or a package, instantiated with `&`. A module takes its providers as inputs. See [Modules](spec-modules.md). |
| `provider` aliases and the `provider` meta-argument | Absent. | A provider struct for each configuration. A resource is built through the one it uses. See [Providers](spec-providers.md). |
| `data "terraform_remote_state"` | Absent. | `pravo.#RemoteOutputs`. See [Environment and remote outputs](spec-env.md#remote-outputs). |
| `terraform_data` | Absent. | `null_resource` of `hashicorp/null`. |
| Built-in functions | Absent. | CUE expressions and the standard library. See the [function map](../migrate/function-map.md). |
| Provisioners and `connection` | Absent. | Cloud-init through `user_data`, or a resource of a provider that runs commands, such as `scottwinkler/shell`. A provider function or data source reads a value from a script. |

## Configuration and files

| In OpenTofu or Terraform | In pravo | Use |
|---|---|---|
| `terraform` block, `required_version`, `required_providers` | Absent. | An import of the generated package of each provider, and `$version` on the provider struct. `pravo version` prints the version. |
| `backend` block, `cloud` block | Absent. | `$settings: state` with a local directory or an S3 bucket. See [Settings](spec-settings.md#state). |
| Encryption of the state | Absent. | Encrypt the bucket or the disk that holds the state directory. |
| `.terraform.lock.hcl` | Absent. | `pravo.lock.cue`. |
| `.terraform` directory | Absent. | Providers and schemas are cached in `PRAVO_HOME`, `~/.pravo` by default. The root holds `cue.mod`, `pravo.lock.cue` and the state directory. |
| `.terraformrc`, `provider_installation`, `plugin_cache_dir` | Absent. | `PRAVO_HOME` selects the cache. A provider can be copied to a container registry with `pravo providers copy`. |
| `.tfvars` files | Absent. | A `.cue` file of the package that sets the fields. |
| `terraform.tfstate` | Absent. | The state graph: one file for each node. `pravo state import-tfstate FILE` converts a state file of format version 4 once. See [State format](state-format.md). |
| `TF_LOG` | Absent. | `-v` and `-vv`, or `PRAVO_LOG=debug`. |

## Not carried over by a conversion

`pravo state import-tfstate` reads the resources, data sources, attributes, private provider data, dependencies and outputs, with the `sensitive` mark of each output. It prints a note for what it cannot carry over.

| Item | Result |
|---|---|
| Taint marks | The object is recorded as healthy, and the conversion prints a note. Use `--replace ADDR` to replace it. |
| Deposed objects | Skipped, with a note. Destroy them with OpenTofu first. |
| Records of the built-in provider | Skipped, with a note that names the replacement: `pravo.#RemoteOutputs` for `terraform_remote_state`, a `null_resource` of `hashicorp/null` for `terraform_data`. |
| Instance keys of `count` and `for_each` | The instance is renamed `<name>_<key>`. The conversion lists every rename. |
| Resources inside modules | The module path becomes a prefix of the name. |
| Check results | Not carried over. |
| Serial and lineage | The store has its own lineage. |

See [Convert the state](../migrate/m5-convert-the-state.md).

## See also

- [Migrate](../migrate/index.md) moves a project from OpenTofu or Terraform.
- [Function map](../migrate/function-map.md) maps each built-in function to CUE.
- [FAQ](faq.md) answers the questions asked first.
