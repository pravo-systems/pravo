---
title: Inputs, tags, environments
description: Run one specification as dev, staging and prod. Use a constrained field, a tag and a state path derived from it.
order: 34
group: Track B · pravo
minutes: 12
needs: B3, A6
learn: How does one specification serve several environments, and what refuses an environment that does not exist?
done_when: "`pravo apply -t env=staging` creates `staging.conf`, and `-t env=qa` is refused."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
keywords: "tfvars variable workspace -var -t environments"
---

## Warm-up

Two questions from earlier lessons. Decide, then open the answer.

:::predict What does `pravo eval -e env` print?
The only line about `env` is:

```cue
env: *"dev" | "prod"
```

::reveal
`"dev"`. The `*` marks the default, and nothing else sets the field ([A6](a06-choices-and-defaults.md)).
:::

:::predict What exit status does `pravo plan --detailed-exitcode` return when nothing changes?
::reveal
0. It returns 2 when the plan has changes ([B3](b03-apply-outputs-state.md)).
:::

## The problem

In OpenTofu the environment is a stored setting. `tofu workspace select prod` stores your choice in the directory, and every later `tofu apply` there reads it. The command line does not name the environment. If you open a terminal next week and run `tofu apply`, it goes to the workspace you selected last.

A variable with a `validation` block can list the allowed environment names, but nothing connects it to the workspace. `terraform.workspace` only reports the name in force.

## The idea

pravo has no workspaces. Select the environment with `-t env=prod`, on every command. The name is one field, the restriction is part of the field, and the state path is computed from it.

:::compare
```hcl title="main.tf"
variable "env" {
  type    = string
  default = "dev"

  validation {
    condition     = contains(["dev", "prod"], var.env)
    error_message = "env must be dev or prod."
  }
}
```

```cue title="CUE"
env: *"dev" | "prod" @tag(env)
```
:::

The field has three parts. The disjunction `"dev" | "prod"` lists the allowed values, as in [A6](a06-choices-and-defaults.md). The `*` marks `"dev"` as the default. The attribute `@tag(env)` lets `-t env=...` fill the field. A tag here is an input to the specification. It is not a label on a cloud resource.

## Write the specification

Work in an empty directory. Five short files. `main.cue` holds the environment and the state path. `sizes.cue` holds what differs between environments, as data.

```cue file=main.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

// The environment: dev unless -t env=... says otherwise.
env: *"dev" | "prod" @tag(env)

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\(env)"}
```

```cue file=sizes.cue
package estate

// What differs between environments.
sizes: {dev: 1, prod: 3}

// The row of the environment that is selected.
size: sizes[env]
```

```cue file=port.cue
package estate

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: env
}
```

```cue file=web.cue
package estate

web: $local.#Resource.local_file & {
	filename: "\(env).conf"
	content:  "env = \(env)\nport = \(port.result)\nworkers = \(size)\n"
}
```

```cue file=outputs.cue
package estate

import "pravo.dev/pravo"

web_file: pravo.#Output & {value: web.filename}
web_port: pravo.#Output & {value: port.result}
```

`$settings` is the block of pravo's own settings for the root. `pravo.#Settings` is a generated definition, so a misspelt setting is `field not allowed`, as for a resource. Its `state.path` field says where the state lives. The default is `pravo.state`. Here it depends on `env`.

```sh run
pravo init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

## Select an environment

`pravo eval -e` prints one expression. Ask for the environment and its size with each tag:

```sh run
pravo eval -e env
pravo eval -e env -t env=prod
pravo eval -e size
pravo eval -e size -t env=prod
```

```text out
"dev"
"prod"
1
3
```

Without a tag, `env` takes its default. With `-t env=prod`, the field is `"prod"`, and `size` reads the row of that name.

## Apply two environments

```sh run
pravo apply --auto-approve | tail -n 3
pravo apply --auto-approve -t env=prod | tail -n 3
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]

2 outputs, 2 changed (web_file, web_port). See them with "pravo output" or "pravo serve".
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]

2 outputs, 2 changed (web_file, web_port). See them with "pravo output" or "pravo serve".
```

The two environments share one specification and differ in the values it computes:

```sh run
cat dev.conf prod.conf
ls state
```

```text out
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

pravo keeps no current environment. Each command evaluates the specification again with the tags it receives. A command without `-t` gets the defaults.

```sh run
pravo plan | grep -E '^(No changes|Graph)'
pravo plan -t env=prod | grep -E '^(No changes|Graph)'
```

```text out
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
```

Each environment finds its own state and reports no changes. Pass the same tags to `plan`, `apply` and `destroy`. Otherwise they evaluate different specifications.

The `pravo state` commands take `-t` as well, and read the state of the environment they name:

```sh run
pravo state query 'resources.port.attributes.result'
pravo state query 'resources.port.attributes.result' -t env=prod
```

```text out
29712
28064
```

## Break it: an environment that does not exist

The field allows two values. Ask for a third. pravo rejects it when it evaluates the specification, before any provider starts.

```sh run exit=6 status=rejected
pravo plan -t env=qa
```

```text out
Error: evaluating .:
env: 2 errors in empty disjunction:
env: conflicting values "dev" and "qa":
    ./main.cue:13:7
env: conflicting values "prod" and "qa":
    ./main.cue:13:15
```

Read it as path, values, location. The path is `env`. The message says `2 errors in empty disjunction`: pravo tried each alternative, and none accepts `"qa"`. Each `conflicting values` line compares one alternative with `"qa"`. The position under it is that alternative in `main.cue`: `"dev"` first, then `"prod"`.

The error is the same in `validate`, `eval`, `apply` and every other command that evaluates the specification. Check that no provider ran:

```sh run
pravo plan -t env=qa -v 2>&1 | grep 'provider started' || echo 'no provider started'
```

```text out
no provider started
```

## Change one environment

Give prod four workers. Only the row of prod changes.

```cue file=sizes.cue hl="4"
package estate

// What differs between environments.
sizes: {dev: 1, prod: 4}

// The row of the environment that is selected.
size: sizes[env]
```

```sh run
pravo plan | grep -E '^(No changes|Graph)'
pravo plan -t env=prod --out prod.plan | grep -E '^(  #|Plan|Graph|Saved|  pravo)'
```

```text out
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
  # web (local_file) must be replaced
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 · skipped 1 · refreshed 2
Saved the plan to prod.plan. To apply exactly these actions, run:
  pravo apply prod.plan
```

The dev plan is empty. The prod plan replaces `web`, because `content` holds the number of workers. The plan was saved with `--out`. A saved plan carries the tags it was made with:

```sh run exit=1 status=rejected
pravo apply prod.plan -t env=dev
```

```text out
Error: -t and -T cannot be combined with a saved plan; the plan carries its own tags
```

pravo refuses the tag. The plan was made for prod and can only be applied to prod. [B7](b07-changing-live-infrastructure.md) covers saved plans. Apply it without a tag:

```sh run
pravo apply prod.plan | tail -n 3
cat prod.conf
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]

2 outputs, none changed. See them with "pravo output" or "pravo serve".
env = prod
port = 28064
workers = 4
```

## Exercise

:::exercise Add staging
Add a third environment, `staging`, with two workers. It needs one edit in `main.cue` and one in `sizes.cue`. Then apply it.

When you are done, this must print `ok`:

```sh title="check"
pravo eval -e size -t env=staging | grep -qx 2 && pravo plan -t env=qa 2>&1 | grep -q 'empty disjunction' && test -f staging.conf && echo ok
```

::hint
Add `"staging"` to the disjunction of `env` and a row to `sizes`. Keep `"dev"` as the default.

::hint
`pravo validate` accepts a missing row. `pravo eval -e size -t env=staging` reports it as `undefined field: staging`. [A6](a06-choices-and-defaults.md) covers the disjunction.

::solution
```cue file=main.cue hl="13"
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

// The environment: dev unless -t env=... says otherwise.
env: *"dev" | "staging" | "prod" @tag(env)

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\(env)"}
```

```cue file=sizes.cue hl="4"
package estate

// What differs between environments.
sizes: {dev: 1, staging: 2, prod: 4}

// The row of the environment that is selected.
size: sizes[env]
```

```sh run
pravo apply --auto-approve -t env=staging | tail -n 3
pravo output -t env=staging
ls state
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]

2 outputs, 2 changed (web_file, web_port). See them with "pravo output" or "pravo serve".
web_file: "staging.conf"
web_port: 27815
dev
prod
staging
```

```sh run check
pravo eval -e size -t env=staging | grep -qx 2
pravo plan -t env=qa 2>&1 | grep -q 'empty disjunction'
test -f staging.conf
```
:::

## Clean up

Destroy each environment with its own tag. If you applied `staging` in the exercise, destroy it the same way with `-t env=staging`.

```sh run
pravo destroy --auto-approve | tail -n 1
pravo destroy --auto-approve -t env=prod | tail -n 1
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

## Recap

- An input is a field with a default, a constraint and `@tag(name)`. `-t name=value` fills it.
- The state path is computed from the environment, so each environment has its own state and the same addresses.
- A value outside the constraint is refused at evaluation, and a saved plan carries the tags it was made with.

:::check
What does pravo remember between commands about the current environment?
::answer
Nothing. Every command evaluates the specification again with the tags it is given.

Why can a plan made with `-t env=prod` not be applied with `-t env=dev`?
::answer
The saved plan carries its own tags. It can only be applied to the environment it was made for.
:::

Reference: [Tags and variables](../reference/spec-tags-and-variables.md), [Settings](../reference/spec-settings.md) and the how-to [Environments](../how-to/environments.md).

[B5](b05-many-resources-from-one-rule.md) generates many resources from one rule.
