<!-- Generated from docs/reference/mod.md by tools/skillgen. Do not edit. -->

# pravo mod

Create, fetch and publish CUE modules for sharing, list the modules a root loads and verify them against the lock file.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo mod SUBCOMMAND [ARGS]` |
| Taught in | [Publishing a module](../how-to/publishing-a-module.md), [Using a module](../how-to/using-a-module.md) and [Modules in a repository](../how-to/modules-in-a-repository.md). |

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

```sh
pravo mod init github.com/acme/net@v0
cat cue.mod/module.cue
```

Output:

```text
module: "github.com/acme/net@v0"
language: {
	version: "v0.17.1"
}
```

The module path needs a domain in its first element and a major version. Write a package and resolve its dependencies.

File `net.cue`:

```cue
package net

#Net: {cidr: string}
```

```sh
pravo mod tidy
pravo mod list
pravo mod list --json
```

Output:

```text
No dependency modules are loaded.
[]
```

`list` shows `PATH VERSION HASH` for each dependency, one per line. With `--json`, it prints an array with one object per module, with the fields `module`, `version` and `hash`.

### verify

`pravo.lock.cue` records each dependency module with its version and a hash of its content. `verify` compares them with what the root loads.

```sh
pravo mod verify
```

Output:

```text
Verified. 0 modules match pravo.lock.cue.
```

Add a module to the lock file that the root does not load:

File `pravo.lock.cue`:

```cue
modules: "example.com/gone@v0": {
	version: "v0.1.0"
	hash:    "sha256:0000"
}
```

```sh
pravo mod verify
```

Output:

```text
  - example.com/gone@v0 is locked but no longer used

Error: 1 module(s) do not match the lock
```

```sh
pravo mod verify --json
```

Output:

```text
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

```sh
pravo mod publish 1.0
```

Output:

```text
Error: usage: pravo mod publish <version>, for example v1.2.0
```

```sh
pravo mod publish v0.1.0
```

Output:

```text
Error: cue.mod/module.cue needs a source: run "pravo mod edit --source git"
```

`mod edit` is a CUE command. It sets the source:

```sh
pravo mod edit --source git
tail -n 3 cue.mod/module.cue
```

Output:

```text
source: {
	kind: "git"
}
```

```sh
pravo mod publish v0.1.0
```

Output:

```text
Error: source is git but . is not a git repository with a commit
```

Commit the module, and `publish` pushes it to the registry. It needs credentials for that registry, so this page does not run it. A first push to `ghcr.io` is private. When `CUE_REGISTRY` names `ghcr.io/OWNER`, the command prints the address of the package settings, where the visibility is changed. See [Publishing a module](../how-to/publishing-a-module.md).

## See also

- [`pravo init`](init.md) relocks a module whose content changed.
- [Modules](spec-modules.md) describes how a module holds resources.
- [CI](../how-to/ci.md) shows `pravo mod verify` in a pipeline, where exit code 5 fails the job.
