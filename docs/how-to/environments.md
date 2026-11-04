---
title: Run one specification in several environments
description: Declare dev, staging and prod in $environments, keep what differs in their rows, give each a state of its own and check every one before a merge.
order: 1
minutes: 10
needs: pravo on your PATH and an empty directory. B4 teaches the idea; this page is the recipe.
done_when: "`pravo validate` checks dev, staging and prod, `pravo apply --env prod` writes a state of its own, and a row that misses a field is caught before a plan."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\b[0-9a-f]{12}\b'
    replace: '<release-id>'
  - pattern: '\b[0-9]s ago'
    replace: '0s ago'
group: Specify
keywords: "workspace workspaces tfvars -var-file environments dev staging prod env"
---

One specification serves every environment. `$environments` names them and holds what differs between them, `--env NAME` selects one, and the state path follows the name.

Work in an empty directory. The commands are for a POSIX shell.

This page declares environments and gives each a state of its own. Making them follow an order, so that prod takes only what staging runs, needs each state sealed with a readers file, which [Promote a release](promote-a-release.md) sets up; without it the order is a convention that nothing enforces.

## 1. Declare the environments

`env.cue` declares the three environments and the state path.

```cue file=env.cue title="env.cue" hl="6-10,13"
package estate

import "pravo.dev/pravo"

// The environments, and what differs between them.
$environments: pravo.#Environments & {
	dev: {workers: 1, retention: 7, $default: true}
	staging: {workers: 2, retention: 30, $after: "dev"}
	prod: {workers: 3, retention: 90, $after: "staging"}
}

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}
```

- Each key of `$environments` is an environment, and its row holds your fields. The fields that start with `$` are pravo's: `$default: true` marks the environment that a command without `--env` uses, and `$after` names the environment that this one takes its releases from.
- Before the specification is evaluated, pravo fills `$environment` with the row that `--env NAME` selects, and adds `$name`, the name of the row.
- `state.path` is computed from `$environment.$name`, so each environment has its own state. Two environments with one state are refused.

## 2. Write the resources once

`main.cue` reads `$environment` and never names an environment.

```cue file=main.cue title="main.cue"
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

server: $random.#Resource.random_pet & {length: $environment.workers}

conf: $local.#Resource.local_file & {
	filename: "\($environment.$name).conf"
	content:  "env \($environment.$name)\nworkers \($environment.workers)\nretention \($environment.retention)\n"
}
```

`pravo validate` checks the specification once for each environment.

```sh run
pravo init > /dev/null
pravo validate
```

```text out
✓ dev: 2 resources, 0 data sources, 0 outputs.
✓ staging: 2 resources, 0 data sources, 0 outputs.
✓ prod: 2 resources, 0 data sources, 0 outputs.
Success! The specification is valid in dev, staging and prod.
```

## 3. Select an environment

`pravo eval -e` prints one expression. Ask for a field of the row with and without a tag.

```sh run
pravo eval -e '$environment.workers'
pravo eval -e '$environment.workers' --env prod
```

```text out
1
3
```

Without a tag, the row marked `$default: true` is used. pravo keeps no current environment. Every command evaluates the specification again with the tags it receives, so pass the same `-t` to `plan`, `apply` and `destroy`.

## 4. Apply each environment

```sh run
pravo apply -auto-approve | grep '^Apply complete'
pravo apply -auto-approve --env prod | grep '^Apply complete'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

The addresses are the same in both runs, `server` and `conf`. The state is not.

```sh run
ls state
grep -h retention dev.conf prod.conf
```

```text out
dev
prod
retention 7
retention 90
```

The `pravo state` commands take `-t`, and read the state of the environment they name. Without `-t` they read the default environment.

```sh run
pravo state list --env prod
```

```text out
conf
server
```

Prod follows staging, and was applied before staging. Its state is not sealed, so it takes any release, and the apply says so on a line that starts with `Environment:`. To hold prod to what staging runs, seal its state and promote each release into it: [Promote a release from one environment to the next](promote-a-release.md).

## 5. See every environment

`pravo environments` reads the state of each environment and says what it runs: the release of the specification by its id, when it was applied and by whom. It needs no key and starts no provider.

```sh run
pravo environments
```

```text out
dev → staging → prod

ENV      FOLLOWS  RUNS          GIT  SINCE   BY
dev      -        <release-id>       0s ago  -   runs this directory
staging  dev      -                              nothing applied yet; not sealed, takes any release
prod     staging  <release-id>       0s ago  -   runs this directory; not sealed, takes any release

This directory: <release-id>
Next: pravo apply --env staging
```

`pravo environments diff` compares what the specification declares in two environments, resource by resource, without a state or a provider.

```sh run
pravo environments diff dev prod
```

```text out
Comparing dev with prod:

  ~ server
      length: 1 → 3
  ~ conf
      content: "env dev\nworkers 1\nretention 7\n" → "env prod\nworkers 3\nretention 90\n"
      filename: "dev.conf" → "prod.conf"

2 resources differ; 0 are the same.
```

## 6. Add an environment

A new environment is a new row. Start with a row that misses a field.

```cue file=env.cue title="env.cue" hl="9" status=unintended
package estate

import "pravo.dev/pravo"

// The environments, and what differs between them.
$environments: pravo.#Environments & {
	dev: {workers: 1, retention: 7, $default: true}
	staging: {workers: 2, retention: 30, $after: "dev"}
	qa: {workers: 1, $after: "dev"}
	prod: {workers: 3, retention: 90, $after: "staging"}
}

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}
```

`pravo validate` compares the rows, and names the field that `qa` misses.

```sh run exit=6
pravo validate
```

```text out
✓ dev: 2 resources, 0 data sources, 0 outputs.
✓ staging: 2 resources, 0 data sources, 0 outputs.
✗ qa
✓ prod: 2 resources, 0 data sources, 0 outputs.

Error: the specification is not valid in qa: $environments.qa has no retention, which dev, staging and prod have
```

Add the field.

```cue file=env.cue title="env.cue" hl="9"
package estate

import "pravo.dev/pravo"

// The environments, and what differs between them.
$environments: pravo.#Environments & {
	dev: {workers: 1, retention: 7, $default: true}
	staging: {workers: 2, retention: 30, $after: "dev"}
	qa: {workers: 1, retention: 14, $after: "dev"}
	prod: {workers: 3, retention: 90, $after: "staging"}
}

// One state directory per environment.
$settings: pravo.#Settings & {state: path: "state/\($environment.$name)"}
```

```sh run
pravo validate | tail -n 1
pravo plan --env qa | grep '^Plan'
```

```text out
Success! The specification is valid in dev, staging, qa and prod.
Plan: 2 to add, 0 to change, 0 to destroy.
```

A field that every row has and that one row leaves without a value, such as `zone: string`, is caught the same way.

## 7. Refuse an unknown environment

A name that is not a key of `$environments` is refused before the specification is evaluated, and before any provider starts.

```sh run exit=6
pravo plan --env sandbox
```

```text out
Error: "sandbox" is not an environment of this specification; the environments are dev, staging, qa and prod (pass --env NAME)
```

To make `--env` mandatory, remove `$default: true` from the row of dev. A command without `--env`, including each `pravo state` command, then stops and lists the environments.

## Clean up

```sh run
pravo destroy -auto-approve | grep '^Destroy complete'
pravo destroy -auto-approve --env prod | grep '^Destroy complete'
```

```text out
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
Destroy complete! Resources: 0 added, 0 changed, 2 destroyed. [txn <id>]
```

## What next

- [Promote a release from one environment to the next](promote-a-release.md) holds prod to what staging runs. It also shows the sealed state and the readers file (`pravo id show` prints the line to append; add `role:admin` for the people who may promote) that the promotion gate needs.
- [B4, Inputs, tags, environments](../learn/b04-inputs-tags-environments.md) teaches each part of this recipe.
- [Keep the state in a shared store](keep-the-state-in-a-shared-store.md) puts one prefix per environment in a shared bucket.
- [CI](ci.md) runs one job per environment.
- [Environments](../reference/spec-environments.md) and [Settings](../reference/spec-settings.md) are the reference for `$environments` and `$settings`, and [pravo environments](../reference/environments.md) for the command.
