<!-- Generated from docs/how-to/environments.md by tools/skillgen. Do not edit. -->

# Run one specification in several environments

Select dev, staging or prod with a tag, keep the differences in one table and give each environment its own state.

One specification serves every environment. A tag selects the environment, one table holds what differs, and the state path follows the tag.

Work in an empty directory. The commands are for a POSIX shell.

## 1. Declare the environment

`env.cue` holds the environment, the table and the state path.

File `env.cue`:

```cue
package estate

import "pravo.dev/pravo"

// The environment: dev unless -t env=... says otherwise.
env: *"dev" | "staging" | "prod" @tag(env)

// What differs between environments.
_envs: {
	dev: {workers: 1, retention: 7}
	staging: {workers: 2, retention: 30}
	prod: {workers: 3, retention: 90}
}

// The row of the selected environment.
cfg: _envs[env]

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\(env)"}
```

- The disjunction lists the allowed values, `*` marks the default and `@tag(env)` lets `-t env=...` fill the field.
- `_envs` is hidden, so pravo does not send it to a provider. `cfg` is the row that `env` selects.
- `state.path` is computed from `env`, so each environment has its own state.

## 2. Write the resources once

`main.cue` reads `cfg` and never names an environment.

File `main.cue`:

```cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

server: $random.#Resource.random_pet & {length: cfg.workers}

conf: $local.#Resource.local_file & {
	filename: "\(env).conf"
	content:  "env \(env)\nserver \(server.id)\nretention \(cfg.retention)\n"
}
```

```sh
pravo init > /dev/null
pravo validate
```

Output:

```text
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
```

## 3. Select an environment

`pravo eval -e` prints one expression. Ask for the row with and without a tag.

```sh
pravo eval -e cfg
pravo eval -e cfg -t env=prod
```

Output:

```text
workers:   1
retention: 7
workers:   3
retention: 90
```

Without a tag, `env` takes its default. pravo keeps no current environment. Every command evaluates the specification again with the tags it receives, so pass the same `-t` to `plan`, `apply` and `destroy`.

## 4. Apply each environment

```sh
pravo apply -auto-approve | grep '^Apply complete'
pravo apply -auto-approve -t env=prod | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

The addresses are the same in both runs, `server` and `conf`. The state is not.

```sh
ls state
grep -h retention dev.conf prod.conf
```

Output:

```text
dev
prod
retention 7
retention 90
```

The `pravo state` commands take `-t`, and read the state of the environment they name. Without `-t` they read the default environment.

```sh
pravo state list -t env=prod
```

Output:

```text
conf
server
```

## 5. Add an environment

A new environment is a new value in the disjunction and a new row in the table. Start with the value only.

File `env.cue`:

```cue
package estate

import "pravo.dev/pravo"

// The environment: dev unless -t env=... says otherwise.
env: *"dev" | "staging" | "prod" | "qa" @tag(env)

// What differs between environments.
_envs: {
	dev: {workers: 1, retention: 7}
	staging: {workers: 2, retention: 30}
	prod: {workers: 3, retention: 90}
}

// The row of the selected environment.
cfg: _envs[env]

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\(env)"}
```

`qa` is allowed and has no row. `pravo validate` does not notice, because a value that is not yet concrete is not an error for it.

```sh
pravo validate -t env=qa
pravo plan -t env=qa
```

Output:

```text
Success! 2 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.

Error: server.length: cfg: undefined field: qa
```

`pravo eval -c` requires every value to be concrete. Run it on `cfg` for every environment, in a loop, before you merge.

```sh
for e in dev staging prod qa; do pravo eval -c -e cfg -t env=$e; done
```

Output:

```text
workers:   1
retention: 7
workers:   2
retention: 30
workers:   3
retention: 90

Error: -e "cfg": undefined field: qa:
    ./env.cue:16:12
```

The message names the row that is missing. Add it.

File `env.cue`:

```cue
package estate

import "pravo.dev/pravo"

// The environment: dev unless -t env=... says otherwise.
env: *"dev" | "staging" | "prod" | "qa" @tag(env)

// What differs between environments.
_envs: {
	dev: {workers: 1, retention: 7}
	staging: {workers: 2, retention: 30}
	qa: {workers: 1, retention: 14}
	prod: {workers: 3, retention: 90}
}

// The row of the selected environment.
cfg: _envs[env]

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\(env)"}
```

```sh
for e in dev staging prod qa; do pravo eval -c -e cfg -t env=$e > /dev/null; done
pravo plan -t env=qa | grep '^Plan'
```

Output:

```text
Plan: 2 to add, 0 to change, 0 to destroy.
```

## 6. Refuse an unknown environment

A value outside the disjunction is rejected when pravo evaluates the specification, before any provider starts.

```sh
pravo plan -t env=sandbox
```

Output:

```text
Error: evaluating .:
env: 4 errors in empty disjunction:
env: conflicting values "dev" and "sandbox":
    ./env.cue:6:7
env: conflicting values "prod" and "sandbox":
    ./env.cue:6:27
env: conflicting values "qa" and "sandbox":
    ./env.cue:6:36
env: conflicting values "staging" and "sandbox":
    ./env.cue:6:15
```

To make the tag mandatory, remove `*"dev" |` from the field. A command without `-t`, including each `pravo state` command, then stops with `$settings.state.path is not concrete`. `pravo providers lock`, `pravo providers schema` and `pravo mod verify` do not apply `-t`, so in such a root they stop with the same error.

## Clean up

```sh
pravo destroy -auto-approve | grep '^Destroy complete'
pravo destroy -auto-approve -t env=prod | grep '^Destroy complete'
```

Output:

```text
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

## What next

- [B4, Inputs, tags, environments](../learn/b04-inputs-tags-environments.md) teaches each part of this recipe.
- [Keep the state in a shared store](keep-the-state-in-a-shared-store.md) puts one prefix per environment in a shared bucket.
- [CI](ci.md) runs one job per environment.
- [Tags and variables](../reference/spec-tags-and-variables.md) and [Settings](../reference/spec-settings.md) are the reference for `@tag` and `$settings`.
