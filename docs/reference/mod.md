---
title: pravo mod
description: Create, fetch and publish CUE modules for sharing, list the modules a root loads and verify them against the lock file.
order: 17
group: Commands
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
---

A CUE module is the tree of packages under `cue.mod/module.cue`. Import paths start with its name, a registry stores it by version, and `pravo.lock.cue` holds a hash of each one a root loads. It is not a `module` block: a definition that contains resources is written as [Reuse](spec-reuse.md) describes, and a CUE module carries it between repositories.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo mod SUBCOMMAND [ARGS]` |
| Taught in | [Publish a CUE module](../how-to/publishing-a-module.md), [Use a published CUE module](../how-to/using-a-module.md) and [Share code between roots in one repository](../how-to/share-code-between-roots.md). |

| Subcommand | Does | Network |
|---|---|---|
| `init MODULE@vN` | Creates `cue.mod/module.cue`. | no |
| `get PATH@VERSION...` | Adds or upgrades a dependency and pins its version in `cue.mod/module.cue`. | yes |
| `tidy` | Resolves and pins the dependencies. | to fetch dependencies |
| `publish VERSION` | Checks the module, then publishes it. | yes |
| `list` | Lists the modules the root loads, with versions and content hashes. | no |
| `verify` | Compares the loaded modules with `pravo.lock.cue`. | no |
| any other CUE module command | `edit`, `mirror` and the rest run as `cue mod` does, embedded in pravo. | depends |

Modules under `github.com/OWNER` are fetched from, and published to, `ghcr.io/OWNER/cue`. Everything else comes from the central registry of CUE. Set `CUE_REGISTRY` to use another registry, and pravo leaves it alone.

### Flags

`mod` hands every argument to CUE, except the global flags `--json`, `--no-color`, `--actor`, `--reason` and `-C`, which pravo reads first. `--json` applies to `list` and `verify`. See [global flags](global-flags.md).

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Done, or `verify` found no difference. |
| 1 | An error: bad arguments, a failed check of `publish`, or a failure of the CUE command. |
| 5 | `verify` found a module that does not match `pravo.lock.cue`. |
| 6 | `verify` and `list` could not load the specification. |

Every other command that loads the root also exits with 5 when a dependency module has the content of another version than the lock file records. `pravo init` records the new hash.

## Examples

Work in an empty directory named `demo`.

### init, tidy, list

```sh run
pravo mod init github.com/acme/net@v0
cat cue.mod/module.cue
```

```text out
module: "github.com/acme/net@v0"
language: {
	version: "v0.17.1"
}
```

The module path needs a domain in its first element and a major version. Write a package and resolve its dependencies.

```cue file=net.cue
package net

#Net: {cidr: string}
```

```sh run
pravo mod tidy
pravo mod list
pravo mod list --json
```

```text out
No dependency modules are loaded.
[]
```

`list` shows `PATH VERSION HASH` for each dependency, one per line. With `--json`, it prints an array with one object per module, with the fields `module`, `version` and `hash`.

### verify

`pravo.lock.cue` records each dependency module with its version and a hash of its content. `verify` compares them with what the root loads.

```sh run
pravo mod verify
```

```text out
Verified. 0 modules match pravo.lock.cue.
```

Add a module to the lock file that the root does not load:

```cue file=pravo.lock.cue
modules: "example.com/gone@v0": {
	version: "v0.1.0"
	hash:    "sha256:0000"
}
```

```sh run exit=5
pravo mod verify
```

```text out
  - example.com/gone@v0 is locked but no longer used

Error: 1 module(s) do not match the lock
```

```sh run exit=5
pravo mod verify --json
```

```text out
{
  "ok": false,
  "problems": [
    "example.com/gone@v0 is locked but no longer used"
  ]
}
```

Each difference is listed, and then the command exits with 5. With `--json`, the document is the whole answer and no error object follows. The differences that `verify` reports:

| Difference | Meaning |
|---|---|
| `PATH VERSION is not in pravo.lock.cue` | A loaded module has no entry. |
| `PATH: locked V1, loaded V2` | The version in `module.cue` differs from the lock. |
| `PATH VERSION: content differs from the lock` | The same version has other content. |
| `PATH is locked ...` | The lock names a module that the root does not load. |

### publish

`publish VERSION` checks the module and publishes it. The version starts with `v`. The module needs a path with a domain and a major version, a `source` in `cue.mod/module.cue`, and, for the source `git`, a repository with a clean working tree. Each missing piece is an error that names it.

```sh run exit=1
pravo mod publish 1.0
```

```text out
Error: usage: pravo mod publish <version>, for example v1.2.0
```

```sh run exit=1
pravo mod publish v0.1.0
```

```text out
Error: cue.mod/module.cue needs a source: run "pravo mod edit --source git"
```

`mod edit` is a CUE command. It sets the source:

```sh run
pravo mod edit --source git
tail -n 3 cue.mod/module.cue
```

```text out
source: {
	kind: "git"
}
```

```sh run exit=1
pravo mod publish v0.1.0
```

```text out
Error: source is git but . is not a git repository with a commit
```

Commit the module, and `publish` pushes it to the registry. It needs credentials for that registry, so this page does not run it. A first push to `ghcr.io` is private. When `CUE_REGISTRY` names `ghcr.io/OWNER`, the command prints the address of the package settings, where the visibility is changed. See [Publish a CUE module](../how-to/publishing-a-module.md).

## See also

- [`pravo init`](init.md) relocks a CUE module whose content changed.
- [Reuse](spec-reuse.md) describes how a definition holds resources and a package shares it.
- [CI](../how-to/ci.md) shows `pravo mod verify` in a pipeline, where exit code 5 fails the job.
