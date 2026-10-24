<!-- Generated from docs/how-to/modules-in-a-repository.md by tools/skillgen. Do not edit. -->

# Share code between roots in one repository

Keep shared modules in one repository, run one root per environment and see how a change to shared code reaches every root.

A repository holds shared modules and one root per environment. Each root has its own plans and state. All roots import the same code by path.

Work in an empty directory. It stands for the repository. The commands are for a POSIX shell, and `-C dir` runs pravo as if it started in `dir`.

## 1. The layout

```text
acme-infra/
├── cue.mod/module.cue          the top of the code pravo can see
├── modules/
│   └── service/service.cue     shared code
└── envs/
    ├── dev/main.cue            a root: its own state and its own plans
    └── prod/main.cue
```

A root is a directory that you run pravo in. pravo looks for the nearest `cue.mod` in that directory and then in each parent. Everything below the directory that holds `cue.mod` can be imported. Put `cue.mod` at the top of the repository.

File `cue.mod/module.cue`:

```cue
module: "example.com/acme-infra@v0"
language: version: "v0.14.0"
```

The `module` line is the name that import paths start with. It has to look like a domain. Nothing needs to exist at that address.

## 2. Write the shared module

A module is a definition that contains resources. It takes its providers as inputs and builds its resources through them.

File `modules/service/service.cue`:

```cue
package service

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

#Service: {
	random!: rnd.#Provider
	local!:  loc.#Provider

	name:  string & =~"^[a-z][a-z0-9-]*$"
	port:  int & >1024 & <65536
	owner: string

	codename: random.#Resource.random_pet & {length: 2}

	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\ncodename \(codename.id)\nport \(port)\nowner \(owner)\n"
	}

	endpoint: "http://\(name).internal:\(port)"
}
```

The module declares no provider of its own. Each input is required (`!`): a root that leaves one out does not load. The root decides the version and the configuration.

## 3. Write one root per environment

Each root declares the providers once and passes them in. The import path starts with the module name and follows the directories.

File `envs/dev/main.cue`:

```cue
package dev

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	"example.com/acme-infra/modules/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api: service.#Service & {random: $random, local: $local, name: "api", port: 8080, owner: "platform"}

api_endpoint: pravo.#Output & {value: api.endpoint}
```

`envs/prod/main.cue` is the same with `package prod` and its own port.

File `envs/prod/main.cue`:

```cue
package prod

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	"example.com/acme-infra/modules/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api: service.#Service & {random: $random, local: $local, name: "api", port: 8443, owner: "platform"}

api_endpoint: pravo.#Output & {value: api.endpoint}
```

## 4. Initialise from a root

Run `pravo init` in a root, not at the top of the repository.

```sh
pravo -C envs/dev init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
Using the module at ../..
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

`Using the module at ../..` says that pravo found the `cue.mod` above the root and did not create a second one. The provider packages went to `cue.mod/gen` at the top, where every root shares them. The lock file and the state belong to the root.

```sh
pravo -C envs/prod init > /dev/null
ls envs/dev envs/prod
```

Output:

```text
envs/dev:
main.cue
pravo.lock.cue

envs/prod:
main.cue
pravo.lock.cue
```

## 5. Apply each root

```sh
pravo -C envs/dev apply -auto-approve | grep '^Apply complete'
pravo -C envs/prod apply -auto-approve | grep '^Apply complete'
pravo -C envs/dev output
pravo -C envs/prod output
pravo -C envs/dev state list
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
api_endpoint: "http://api.internal:8080"
api_endpoint: "http://api.internal:8443"
api.codename
api.config
```

The resources are at the path where the module instance sits: `api.codename` and `api.config`. Those are the addresses in plans and in the state. The two roots share code and nothing else.

## 6. Change the shared module

Add a line to the content in `modules/service/service.cue`.

File `modules/service/service.cue`:

```cue
package service

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

#Service: {
	random!: rnd.#Provider
	local!:  loc.#Provider

	name:  string & =~"^[a-z][a-z0-9-]*$"
	port:  int & >1024 & <65536
	owner: string

	codename: random.#Resource.random_pet & {length: 2}

	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\ncodename \(codename.id)\nport \(port)\nowner \(owner)\ntier standard\n"
	}

	endpoint: "http://\(name).internal:\(port)"
}
```

```sh
pravo -C envs/dev plan | grep -E '^(  #|Plan|Graph)'
pravo -C envs/prod plan | grep -E '^(  #|Plan|Graph)'
```

Output:

```text
  # api.config (local_file) must be replaced
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 · skipped 1 · refreshed 2
  # api.config (local_file) must be replaced
Plan: 1 to add, 0 to change, 1 to destroy.
Graph: planned 1 · skipped 1 · refreshed 2
```

One edit reached both roots, and each shows its own plan to review. The pet did not change, so pravo did not plan it.

## 7. Shared code is part of a saved plan

Save a plan in `dev`, then edit the module again before you apply it.

```sh
pravo -C envs/dev plan -out change.plan > /dev/null
sed 's/tier standard/tier premium/' modules/service/service.cue > service.tmp
mv service.tmp modules/service/service.cue
pravo -C envs/dev apply change.plan
```

Output:

```text
Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (modules/service/service.cue changed)
```

The fingerprint of a saved plan covers every file that was loaded for the root, including packages elsewhere in the repository. A package that the root does not import does not change it. The exit code is 3. [B7](../learn/b07-changing-live-infrastructure.md) covers saved plans.

## 8. What to commit

- `cue.mod/module.cue`, the shared packages and each root's `main.cue`.
- Each root's `pravo.lock.cue`.
- Either `cue.mod/gen`, so that `pravo validate` works in a clean checkout, or a `pravo init` step in [CI](ci.md).
- The state, in a place the team shares: [a bucket](remote-state.md), one prefix per root.

Keep one `cue.mod` per repository. A second `cue.mod` inside it starts a separate module, and code on the other side cannot import across the boundary by path. Code that is shared between repositories, or that has its own release schedule, is [published](publishing-a-module.md) and [used by version](using-a-module.md).

Addresses are paths. Renaming a field that holds a module instance renames every resource inside it. [Rename, move and adopt resources](importing-and-moving.md) shows how to keep the objects.

## What next

- [Publish a module](publishing-a-module.md) when a second repository needs the code.
- [Run one specification in several environments](environments.md) is the alternative when the environments differ in values only.
- [Modules](../reference/spec-modules.md) is the reference for definitions that contain resources.
