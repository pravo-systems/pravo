<!-- Generated from docs/migrate/m10-not-in-pravo.md by tools/skillgen. Do not edit. -->

# Not in pravo: what to use instead

The OpenTofu features that pravo does not have, what to use in each case, and the parts of a state that the conversion does not carry over.

pravo has no workspaces, no `-target` and no provisioners. The same holds for the other features in the table below. Each row gives the replacement. The reference has a lookup page, [Not in pravo](../reference/not-in-pravo.md).

## Absent features

| In OpenTofu | Use in pravo |
|---|---|
| `tofu workspace`, `terraform.workspace` | A tag selects the environment: `env: *"dev" \| "prod" @tag(env)` and `-t env=prod`. The state path takes the tag: `$settings: state: path: "state/\(env)"`. |
| `-target=addr` | Scoped plans reach only what a change reaches. `pravo apply <planfile> --only addr` applies part of a saved plan. |
| `provisioner "local-exec"` | A resource of a shell provider, such as `scottwinkler/shell`. |
| `provisioner "remote-exec"`, `file`, `connection` | Cloud-init through `user_data`, a baked image, or a shell or SSH provider resource. |
| `terraform_data` | `null_resource` of `hashicorp/null`. |
| `terraform_remote_state` | `pravo.#RemoteOutputs`, which reads the other root's `outputs.json`. |
| `precondition`, `postcondition`, `check` blocks | A constraint on the field. A rule about a plan is a plan policy. |
| `lifecycle { replace_triggered_by }` | `pravo plan --replace addr`, or an attribute that depends on the trigger. |
| `tofu taint`, `tofu untaint` | `pravo plan --replace addr` and `pravo apply --replace addr`. |
| `tofu refresh`, `-refresh-only` | `pravo drift` reads every resource and reports changes. `--refresh=all` on `plan` refreshes every record. |
| `tofu console` | `pravo eval -e EXPR`. |
| `tofu graph` | `pravo state graph --dot`. |
| `tofu test`, `.tftest.hcl` | `pravo validate`, `pravo eval -c`, and a plan policy on a saved plan in CI. |
| `-var`, `-var-file`, `*.tfvars` | `-t name=value` for a string, a number or a boolean. A list or a map is a field in a `.cue` file. |
| `-parallelism`, `-lock`, `-lock-timeout`, `-input` | Locks are per resource and always on. A saved plan asks nothing. |
| `removed` block | `pravo state rm addr`. |
| `-generate-config-out` | Write the resource, then `pravo import addr id` or `$import`. |
| `required_version` | Pin `pravo version` in the pipeline image. |
| State backends other than a directory and an S3-compatible bucket | `state: path` or `state: backend: "s3"`. |
| DynamoDB lock table, `use_lockfile` | Per-resource locks are objects in the bucket, created with a conditional write. |
| State encryption | Encrypt the bucket or the volume that holds `pravo.state`. |
| HCP Terraform, `cloud` block | A pipeline that runs `pravo plan` and `pravo apply <planfile>`. |
| Modules from a registry or a `git::` source | CUE modules: `pravo mod get`, then `import`. |

The replacements for the first three rows are run below. The lookup of every command is in [Command map](command-map.md), and every construct is in [Construct matrix](construct-matrix.md).

## Check the table

A command or a flag that does not exist is refused before the specification loads.

```sh
for c in workspace taint refresh console graph test; do pravo $c 2>&1 | grep -m1 '^Error'; done
```

Output:

```text
Error: unknown command "workspace" for "pravo"
Error: unknown command "taint" for "pravo"
Error: unknown command "refresh" for "pravo"
Error: unknown command "console" for "pravo"
Error: unknown command "graph" for "pravo"
Error: unknown command "test" for "pravo"
```

```sh
for f in -target=x -parallelism=5 -lock=false -var=x=1 -var-file=x -input=false -refresh-only; do pravo plan $f 2>&1 | grep -m1 '^Error'; done
```

Output:

```text
Error: unknown flag: --target
Error: unknown flag: --parallelism
Error: unknown flag: --lock
Error: unknown flag: --var
Error: unknown flag: --var-file
Error: unknown flag: --input
Error: unknown flag: --refresh-only
```

A field that does not exist is refused when the specification evaluates. This root declares one resource.

File `check/main.cue`:

```cue
package check

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

pet: $random.#Resource.random_pet & {
	length: 1
	$lifecycle: replace_triggered_by: ["x"]
}
```

```sh
pravo -C check init > /dev/null
pravo -C check validate
```

Output:

```text
Error: evaluating ./check:
pet.$lifecycle.replace_triggered_by: field not allowed:
    ./check/main.cue:9:14
```

## Environments without workspaces

The environment is a field. The tag `-t env=prod` sets it, a constraint limits it, and the state path follows it.

File `env/main.cue`:

```cue
package env

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

$settings: pravo.#Settings & {state: path: "state/\(env)"}

env: *"dev" | "staging" | "prod" @tag(env)

_sizes: {dev: 1, staging: 2, prod: 3}

pet: $random.#Resource.random_pet & {length: _sizes[env]}

note: $local.#Resource.local_file & {
	filename: "out/\(env).txt"
	content:  "\(pet.id)\n"
}
```

```sh
pravo -C env init > /dev/null
pravo -C env apply --auto-approve 2>&1 | grep '^Apply complete'
pravo -C env apply --auto-approve -t env=prod 2>&1 | grep '^Apply complete'
ls env/state
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
dev
prod
```

Each environment has its own state directory, and a name outside the three is refused:

```sh
pravo -C env validate -t env=qa 2>&1 | grep -E '^(Error|env: conflicting)'
```

Output:

```text
Error: evaluating ./env:
env: conflicting values "dev" and "qa":
env: conflicting values "prod" and "qa":
env: conflicting values "staging" and "qa":
```

The `state` subcommands take `-t`, and read the state of the environment that the tag selects:

```sh
pravo -C env state show pet -t env=prod | head -n 1
```

Output:

```text
// pet (provider registry.opentofu.org/hashicorp/random v<version> as random, schema version 0, revision 1)
```

## Targeting without -target

A plan reaches the resources that a change reaches and skips the rest. A saved plan applies in parts with `--only`.

File `target/main.cue`:

```cue
package target

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 1}
b: $random.#Resource.random_pet & {length: 1}
c: $random.#Resource.random_pet & {length: 1}
```

```sh
pravo -C target init > /dev/null
pravo -C target apply --auto-approve 2>&1 | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <txn>]
```

Change `a` and `b`, and leave `c`:

File `target/main.cue`:

```cue
package target

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 2}
b: $random.#Resource.random_pet & {length: 2}
c: $random.#Resource.random_pet & {length: 1}
```

```sh
pravo -C target plan --out part.plan 2>&1 | grep -E '^(  # |Plan:|Graph:)'
pravo -C target apply part.plan --only a 2>&1 | grep -E '^(Apply complete|1 change)'
pravo -C target plan 2>&1 | grep -E '^(  # |Plan:)'
```

Output:

```text
  # a (random_pet) must be replaced
  # b (random_pet) must be replaced
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 · skipped 1 · refreshed 2 · not read 1
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
1 change remains in part.plan; apply with "pravo apply part.plan".
  # b (random_pet) must be replaced
Plan: 1 to add, 0 to change, 1 to destroy.
```

The plan skips `c`. The apply of `a` leaves the change to `b` in the file `part.plan`. The policies in force judge the saved plan as a whole, and not the selection.

## Commands without provisioners

A command is a resource. A shell provider runs it, and its result is an attribute that other resources can read. This root records an id, then appends it to a file.

File `shell/main.cue`:

```cue
package shell

import (
	nul "pravo.dev/providers/hashicorp/null:null_"
	sh "pravo.dev/providers/scottwinkler/shell"
)

$null:  nul.#Provider
$shell: sh.#Provider

host: $null.#Resource.null_resource & {triggers: version: "1"}

hosts: $shell.#Resource.shell_script & {
	lifecycle_commands: [{
		create: "echo \(host.id) >> hosts.txt"
		delete: "true"
	}]
}
```

```sh
pravo -C shell init > /dev/null
pravo -C shell apply --auto-approve 2>&1 | grep '^Apply complete'
grep -c . shell/hosts.txt
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
1
```

The package `hashicorp/null` is imported with a suffix, `:null_`, because `null` is a CUE keyword. The command ran at apply, once. `hosts` depends on `host`, because the command refers to `host.id`.

## State that does not carry over

The conversion copies resources, data sources, their attributes, the provider's private data, the dependencies and the output values with their `sensitive` mark. It does not carry the rest.

| In the OpenTofu state | What happens |
|---|---|
| A tainted instance | It is recorded as healthy, with a warning. `tofu plan` replaces a tainted instance, so a root with an empty plan has none. Use `--replace` to replace one that remains. |
| A deposed object | It is skipped, with a warning. `tofu plan` destroys a deposed object, so a root with an empty plan has none. Run `tofu apply` before you convert. |
| `lineage`, `serial`, `terraform_version` | Not read. The pravo state has a lineage of its own. |
| `check_results` | Not read. |
| `sensitive_attributes` | Not read. Sensitivity comes from the provider's schema. |
| Instance keys and module paths | Joined into the name: `random_pet.region["eu"]` is `random_pet.region_eu`. The command prints every rename. |
| Records of the built-in provider (`terraform_data`, `terraform_remote_state`) | Skipped, with a warning that names the replacement: `pravo.#RemoteOutputs` for `terraform_remote_state`, a `null_resource` of `hashicorp/null` for `terraform_data`. |
| A state in another format than 4, or with flat attributes | Refused. OpenTofu 1.13.1 refuses both too. Write the state in format 4 with structured attributes before you convert it. |

The warnings and the refusals are shown with their messages in [Pitfalls](m9-pitfalls.md#converting-the-state).

## Recap

- Each absent feature has a replacement. A tag replaces a workspace, scoped plans and `--only` replace `-target`, a resource replaces a provisioner.
- A command or flag that does not exist is refused with `unknown command` or `unknown flag`. A field that does not exist is refused when the specification evaluates.
- The conversion warns about taint and deposed objects, and skips the records of the built-in provider. It drops the lineage and the check results. It keeps the sensitive mark of outputs.

Next: [M11 After the migration](m11-after-the-migration.md) rewrites the faithful translation in the idiom of CUE, in separate commits.
