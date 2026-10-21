---
title: pravo init
description: Install the providers a root needs, write the lock file and generate the CUE packages that a specification imports.
order: 1
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'pravo\.local/ws'
    replace: 'pravo.local/demo'
  - pattern: '"(darwin|linux|windows|freebsd)_(amd64|arm64|386|arm)": "sha256:[0-9a-f]+"'
    replace: '"<os>_<arch>": "sha256:<hash>"'
---

## Quick reference

| | |
|---|---|
| Synopsis | `pravo init [--upgrade] [-t key=value] [-T]` |
| Run it | Once per root, after a checkout, and after a provider constraint changes. |
| Network | Needed to resolve versions. Not needed when `pravo.lock.cue` pins providers that are installed. |
| Changes infrastructure | No. |
| Taught in | [Quick start](../how-to/quick-start.md), step 3, and [B1, Providers and resources](../learn/b01-providers-and-resources.md). |

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--upgrade` | off | Ignore `pravo.lock.cue` and select the newest version that matches each constraint. |
| `-t`, `--tag key=value` | none | Inject a value for an `@tag()` field while the specification loads. Repeatable. |
| `-T`, `--inject-vars` | off | Inject the system variables of CUE, such as `os`, `arch` and `username`, into tags written `@tag(name,var=os)`. |

The [global flags](global-flags.md) apply. A single dash works as well: `-upgrade`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The root is initialised. |
| 1 | A provider could not be resolved or installed, the registry could not be reached, or an argument was given: `init` takes none. |
| 6 | The specification does not load or evaluate. |

## What init writes

| Path | Content |
|---|---|
| `cue.mod/module.cue` | The CUE module file. Written only when no module exists in the root or above it. The module name is `pravo.local/` and the name of the directory. |
| `cue.mod/gen/pravo.dev/providers/<namespace>/<type>/` | One package per provider. It holds every resource, data source, ephemeral resource and function of that provider. |
| `cue.mod/gen/pravo.dev/pravo/` | The package `pravo.dev/pravo`: `#Settings`, `#Output`, `#Env`, `#SecretEnv`, `#RemoteOutputs` and `#Movable`. |
| `pravo.lock.cue` | The provider versions and archive hashes in use, and the hash of each dependency module. Commit it. It is written in the layout of `pravo fmt`. |

## Where providers are kept

`init` keeps downloaded providers and the schemas it reads from them in one directory, `$PRAVO_HOME`. It defaults to `~/.pravo`. Every root on the machine shares it.

| Path below `$PRAVO_HOME` | Holds |
|---|---|
| `providers/<host>/<namespace>/<type>/<version>/<os>_<arch>/` | The provider binary. |
| `schemas/<host>/<namespace>/<type>/<version>.json` | The cached schema of that provider version. |
| `s3-probe/` | One empty file for each S3 store that passed the check for conditional requests. |

Set `PRAVO_HOME` to move the directory, for example to a cache directory on a build machine. Later commands in that root need the same `PRAVO_HOME`.

If you run OpenTofu, its plugin cache can serve the providers. Before it downloads, `init` looks for the selected version in `$TF_PLUGIN_CACHE_DIR` and in `~/.terraform.d/plugin-cache`. Both use the layout `<host>/<namespace>/<type>/<version>/<os>_<arch>/`. pravo uses such a binary where it lies and does not copy it. If you found it through `TF_PLUGIN_CACHE_DIR`, keep that variable set for every later pravo command.

`init` reads the `import` lines of the `.cue` files and the literal `$version`, `$path`, `$protocol` and `$address` fields of each provider struct. A `$version` that is computed instead of written as a string is not seen. The fields are covered in [Provider declarations](spec-providers.md). It reads `$settings` before the generated packages exist, with the tags given by `-t` and `-T`.

## Examples

Work in an empty directory named `demo`.

### A directory without a specification

A directory with no `.cue` file has nothing to load. `init` creates the module and the package `pravo.dev/pravo`, says so and exits 0.

```sh run
mkdir empty
pravo -C empty init
```

```text out
Created cue.mod/module.cue
There is no specification yet. Write a .cue file in this directory, then run "pravo eval" or "pravo validate".
```

Write a `.cue` file, then run `pravo eval` or `pravo validate`. A file that imports a provider needs `pravo init` again.

### A state path that depends on a tag

`init` reads the `$settings` block before the packages it imports exist. A tag that has no default needs a value there too, and `-t` gives it:

```cue file=tagged/main.cue
package tagged

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

env: string @tag(env)

$settings: pravo.#Settings & {state: path: "state/\(env)"}

$random: rnd.#Provider
```

```sh run
pravo -C tagged init -t env=dev
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

### A root without providers

A root that imports no generated package needs no provider. `init` creates the module file.

```cue file=main.cue
package demo

size: int
size: 3
```

```sh run
pravo init
```

```text out
Providers:
Created cue.mod/module.cue

pravo has been initialised. Run "pravo plan" to see what it would do.
```

`pravo validate` and `pravo eval` evaluate such a file with or without `init`. No provider is involved.

```sh run
pravo eval
cat cue.mod/module.cue
```

```text out
size: 3
module: "pravo.local/demo"
language: version: "v0.14.0"
```

### A root with providers

Replace `main.cue` with a specification that imports two generated packages. The packages do not exist yet.

```cue file=main.cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider
```

```sh run
pravo init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  local = hashicorp/local v2.9.1
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

The first line appears when the imports cannot resolve yet. `init` generates the packages for the declared providers, then loads the specification again. On a first run each provider also prints a `Downloading` line.

```sh run
find cue.mod pravo.lock.cue -type f | sort
```

```text out
cue.mod/gen/pravo.dev/pravo/settings_gen.cue
cue.mod/gen/pravo.dev/providers/hashicorp/local/local_gen.cue
cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue
cue.mod/module.cue
pravo.lock.cue
```

### The lock file

```sh run
head -n 9 pravo.lock.cue
```

```text out
// This file is maintained by "pravo init". Commit it to version control.

providers: "registry.opentofu.org/hashicorp/local": {
	version: "2.9.1"
	hashes: {
		"<os>_<arch>": "sha256:<hash>"
	}
}
providers: "registry.opentofu.org/hashicorp/random": {
```

`pravo fmt` prints the name of each file it rewrites. Right after `init` it prints nothing for the lock file:

```sh run
pravo fmt
```

The hashes name the platforms for which an archive was installed. [`pravo providers lock`](providers.md#pravo-providers-lock) adds the other platforms.

### Run it again, offline

With the lock file present and the providers installed, `init` makes no network request. The proxy below refuses every connection, and the command succeeds.

```sh run
HTTPS_PROXY=http://127.0.0.1:9 pravo init | tail -n 1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

Without the lock file, `init` asks the registry for versions and fails when it cannot reach it:

```text title="no lock file, no network"
Error: registry discovery for registry.opentofu.org: Get "https://registry.opentofu.org/.well-known/terraform.json": proxyconnect tcp: dial tcp 127.0.0.1:9: connect: connection refused
```

### Upgrade

`--upgrade` ignores the lock file, asks the registry for the newest version that matches each `$version` constraint, and rewrites the lock file.

```sh run
pravo init --upgrade | tail -n 1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

## Behaviour to know

- Every other command needs the generated packages. Run `pravo init` after a checkout, or commit `cue.mod/gen`.
- A generated package holds every type of its provider, so one package serves every root and module that imports it. Regenerate the packages with [`pravo gen`](validate-eval-fmt-gen.md#pravo-gen).
- In a repository with one CUE module at the top, `init` finds that module and does not create a nested one. See [Modules in a repository](../how-to/modules-in-a-repository.md).
- `init` accepts a dependency module whose content differs from `pravo.lock.cue` and records the new hash. Every other command refuses it with exit code 5.

## See also

- [`pravo providers`](providers.md) lists the resolved providers.
- [Generated packages](spec-generated-packages.md) describes the files under `cue.mod/gen`.
- [Providers from a registry](../how-to/providers-from-a-registry.md) shows a provider from a registry other than the default.
