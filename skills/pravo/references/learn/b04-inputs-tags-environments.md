<!-- Generated from docs/learn/b04-inputs-tags-environments.md by tools/skillgen. Do not edit. -->

# Inputs, tags, environments

Run one specification as dev, staging and prod. Declare the environments, select one with a tag and derive the state path from it.

## Warm-up

Two questions from earlier lessons. Decide, then open the answer.

## The problem

In OpenTofu the environment is a stored setting. `tofu workspace select prod` stores your choice in the directory, and every later `tofu apply` there reads it. The command line does not name the environment. If you open a terminal next week and run `tofu apply`, it goes to the workspace you selected last.

A variable with a `validation` block can list the allowed environment names, but nothing connects it to the workspace. `terraform.workspace` only reports the name in force.

## The idea

pravo has no workspaces. Declare the environments in the specification, and select one with `--env prod`, on every command. `--env prod` is the tag `env`: `-t env=prod` does the same. The names are the keys of one struct, each key has a row of what differs, and the state path is computed from the name.

```hcl
variable "env" {
  type    = string
  default = "dev"

  validation {
    condition     = contains(["dev", "prod"], var.env)
    error_message = "env must be dev or prod."
  }
}
```

```cue
$environments: {
	dev: {$default: true}
	prod: {}
}
```

`$environments` is a struct with one field for each environment. Its keys are the allowed names, as the alternatives of the disjunction in the warm-up are. `$default: true` marks the environment that a command without `--env` uses, as `*` marks a default. `--env prod` selects a row, and before pravo evaluates the rest of the specification it fills the field `$environment` with that row and with `$name`, the name of the row. A tag here is an input to the specification. It is not a label on a cloud resource.

## Write the specification

Work in an empty directory. Four short files. `main.cue` holds the environments, what differs between them, and the state path.

File `main.cue`:

```cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

// The environments, and what differs between them. dev unless --env says otherwise.
$environments: pravo.#Environments & {
	dev: {workers: 1, $default: true}
	prod: {workers: 3}
}

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}
```

File `port.cue`:

```cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: $environment.$name
}
```

File `web.cue`:

```cue
package estate

web: $local.#Resource.local_file & {
	filename: "\($environment.$name).conf"
	content:  "env = \($environment.$name)\nport = \(port.result)\nworkers = \($environment.workers)\n"
}
```

File `outputs.cue`:

```cue
package estate

import "pravo.dev/pravo"

web_file: pravo.#Output & {value: web.filename}
web_port: pravo.#Output & {value: port.result}
```

A row holds your own fields, here `workers`. A field that starts with `$` belongs to pravo. `pravo.#Environments` is a generated definition that checks the shape of the struct.

`$settings` is the block of pravo's own settings for the root. `pravo.#Settings` is a generated definition too, so a misspelt setting is `field not allowed`, as for a resource. Its `state.path` field says where the state lives. The default is `pravo.state`. Here it depends on the name of the environment.

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

## Select an environment

`pravo eval -e` prints one expression. Ask for the name of the environment and its workers with each tag:

```sh
pravo eval -e '$environment.$name'
pravo eval -e '$environment.$name' --env prod
pravo eval -e '$environment.workers'
pravo eval -e '$environment.workers' --env prod
```

Output:

```text
"dev"
"prod"
1
3
```

Without `--env`, the row marked `$default: true` is selected. With `--env prod`, `$environment` is the row of prod.

## Apply two environments

```sh
pravo apply --auto-approve | tail -n 3
pravo apply --auto-approve --env prod | tail -n 3
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]

2 outputs, 2 changed (web_file, web_port). See them with "pravo output" or "pravo serve".
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]

2 outputs, 2 changed (web_file, web_port). See them with "pravo output" or "pravo serve".
```

The two environments share one specification and differ in the values it computes:

```sh
cat dev.conf prod.conf
ls state
```

Output:

```text
env = dev
port = 29712
workers = 1
env = prod
port = 28064
workers = 3
dev
prod
```

The addresses are the same in both, `port` and `web`. They are paths in the specification. What differs is the state they are recorded in: `state/dev` and `state/prod`. This is the whole of what a workspace does for state.

## Every command names its environment

pravo keeps no current environment. Each command evaluates the specification again with the tags it receives. A command without `-t` gets the default.

```sh
pravo plan | grep -E '^(No changes|Graph)'
pravo plan --env prod | grep -E '^(No changes|Graph)'
```

Output:

```text
No changes. The specification matches the state. 2 records were not read, so a change made outside pravo would not show here; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
No changes. The specification matches the state. 2 records were not read, so a change made outside pravo would not show here; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
```

Each environment finds its own state and reports no changes. Pass the same tags to `plan`, `apply` and `destroy`. Otherwise they evaluate different specifications.

The `pravo state` commands take `-t` as well, and read the state of the environment they name:

```sh
pravo state query 'resources.port.attributes.result'
pravo state query 'resources.port.attributes.result' --env prod
```

Output:

```text
29712
28064
```

## Break it: an environment that does not exist

The struct has two keys. Ask for a third. pravo rejects it before it evaluates the specification, so before any provider starts.

Status: rejected.

```sh
pravo plan --env qa
```

Output:

```text
Error: "qa" is not an environment of this specification; the environments are dev and prod (pass --env NAME)
```

The message names the value you gave and the ones that are declared. The error is the same in `validate`, `eval`, `apply` and every other command that evaluates the specification. Check that no provider ran:

```sh
pravo plan --env qa -v 2>&1 | grep 'provider started' || echo 'no provider started'
```

Output:

```text
no provider started
```

## Change one environment

Give prod four workers. Only the row of prod changes.

File `main.cue`:

```cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

// The environments, and what differs between them. dev unless --env says otherwise.
$environments: pravo.#Environments & {
	dev: {workers: 1, $default: true}
	prod: {workers: 4}
}

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}
```

```sh
pravo plan | grep -E '^(No changes|Graph)'
pravo plan --env prod --out prod.plan | grep -E '^(  #|Plan|Graph|Saved|  pravo)'
```

Output:

```text
No changes. The specification matches the state. 2 records were not read, so a change made outside pravo would not show here; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
  # web (local_file) must be replaced
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 · skipped 1 · refreshed 2
Saved the plan to prod.plan. To apply exactly these actions, run:
  pravo apply prod.plan
```

The dev plan is empty. The prod plan replaces `web`, because `content` holds the number of workers. The plan was saved with `--out`. A saved plan carries the tags it was made with:

Status: rejected.

```sh
pravo apply prod.plan --env dev
```

Output:

```text
Error: -t, --env and -T cannot be combined with a saved plan; the plan carries its own tags
```

pravo refuses the tag. The plan was made for prod and can only be applied to prod. [B7](b07-changing-live-infrastructure.md) covers saved plans. Apply it without a tag:

```sh
pravo apply prod.plan | tail -n 3
cat prod.conf
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]

2 outputs, none changed. See them with "pravo output" or "pravo serve".
env = prod
port = 28064
workers = 4
```

## Exercise

## Clean up

Destroy each environment with its own `--env`. If you applied `staging` in the exercise, destroy it the same way with `--env staging`.

```sh
pravo destroy --auto-approve | tail -n 1
pravo destroy --auto-approve --env prod | tail -n 1
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

## Recap

- The environments are the keys of `$environments`, and each row holds what differs. `--env NAME` selects a row, which the rest of the specification reads as `$environment`.
- The state path is computed from the name, so each environment has its own state and the same addresses.
- A name that is not declared is refused before evaluation, and a saved plan carries the tags it was made with.

Reference: [Environments](../reference/spec-environments.md), [Tags and variables](../reference/spec-tags-and-variables.md), [Settings](../reference/spec-settings.md) and the how-to [Run one specification in several environments](../how-to/environments.md). An environment can also take only the releases that another one runs: [Promote a release from one environment to the next](../how-to/promote-a-release.md).

[B5](b05-many-resources-from-one-rule.md) generates many resources from one rule.
