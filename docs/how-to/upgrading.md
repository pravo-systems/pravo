---
title: Upgrade pravo and providers
description: Replace the pravo binary, move a provider to a newer version through the lock file, pin the archives of every platform and review the plan that follows.
order: 1
minutes: 12
needs: "pravo on your PATH and network access to `registry.opentofu.org`. [Quick start](quick-start.md#1-install-pravo) for the binary."
done_when: "`pravo init -upgrade` moves a provider to a newer version, `pravo providers lock` pins four platforms, and `pravo plan` is empty."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: 'archive for [a-z]+_[a-z0-9]+ is sha256:[0-9a-f]{64}'
    replace: 'archive for <os>_<arch> is sha256:<hash>'
group: Maintain
---

Two things upgrade separately: the pravo binary and each provider. The lock file `pravo.lock.cue` decides which provider versions a root uses. An upgrade is an edit to that file, and you can read it in a diff.

## 1. Upgrade pravo

Build or install the new binary as in the [quick start](quick-start.md#1-install-pravo), then check what you have.

```sh run
pravo version
```

```text out
pravo 0.1.0-dev
cue v0.17.1
```

The first line is the version of pravo. The second is the version of CUE built into it. Policies are evaluated by that CUE, so a `cue` command that tests policies uses the same version; see [Write and roll out plan policies](policies.md).

After you replace the binary:

1. Run `pravo init` in each root. It regenerates the CUE packages under `cue.mod/gen` from the provider schemas.
2. Run `pravo validate`, and then `pravo plan`. A plan that is not empty after a change of binary is a difference to read before you apply anything.

A store and a saved plan file carry a format number, and a pravo refuses one with a number above the one it writes: `written by a newer pravo`. Every command that shares a state must be the same build or a newer one, so upgrade every machine that uses a shared state together.

A record that was written before the digest of its provider's configuration was recorded has none. A plan reads a record when the provider now points somewhere else only if the record carries the digest, so after you change where a provider points, read the records once with `pravo plan -refresh=all`. Every record that is written or read after that carries the digest.

## 2. See what the lock file pins

Work in an empty directory. The root pins `hashicorp/random` to one version.

```cue file=main.cue title="main.cue" hl="5"
package upgrade

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "3.6.3"}

server: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo init | grep -A1 '^Providers'
pravo apply -auto-approve | grep '^Apply complete'
pravo providers | head -n 1
```

```text out
Providers:
  random = hashicorp/random v3.6.3
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
random = hashicorp/random v3.6.3
```

`pravo init` selects the newest version that satisfies `$version`, installs it and writes the choice to `pravo.lock.cue`. Commit that file. A later `pravo init` installs exactly the locked version.

## 3. Widen the constraint

Allow every version from 3.6 up to 3.7.2.

```cue file=main.cue title="main.cue" hl="5"
package upgrade

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: ">= 3.6, <= 3.7.2"}

server: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo init | grep -A1 '^Providers'
```

```text out
Providers:
  random = hashicorp/random v3.6.3
```

The lock wins. The constraint changed and the locked version still satisfies it, so nothing moves. The `constraints` line of the lock file records the new constraint.

## 4. Take the newest matching version

`pravo init -upgrade` ignores the lock file and selects the newest matching version.

```sh run
pravo init -upgrade
```

```text out
Providers:
  random = hashicorp/random v3.7.2
Generated cue.mod/gen/pravo.dev/providers/hashicorp/random (8 resource types, 0 data sources, 1 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

`init` regenerates the package from the schema of the selected version. The `Generated` line shows that it wrote the file.

Read the effect before you take it. A new provider version can change a schema, a default or a computed attribute.

```sh run
pravo plan
```

```text out
server: Refreshing state... [id=<id>]

No changes. Your infrastructure matches the configuration.
Graph: planned 1 · refreshed 1
```

An empty plan shows that the upgrade changed nothing in the real world. A plan with changes is the provider telling you what the new version does differently. Read each one, and apply when you accept them.

## 5. Pin every platform

A provider archive differs per platform, so the lock file keeps one hash per `<os>_<arch>`. `pravo init` records the hash for the platform it installs for. A Linux runner then has no hash to check against. `pravo providers lock` adds the others from what the source states. It downloads nothing.

```sh run
pravo providers lock
sed -n '/^providers/,/^}/p' pravo.lock.cue
```

```text out
Pinned 4 platforms in pravo.lock.cue
providers: "registry.opentofu.org/hashicorp/random": {
	version:     "3.7.2"
	constraints: ">= 3.6, <= 3.7.2"
	hashes: {
		"darwin_amd64": "sha256:d5b5fc881ccc41b268f952dae303501d6ec9f9d24ee11fe2fa56eed7478e15d0"
		"darwin_arm64": "sha256:c71f18b0cb5d55a103ea81e346fb56db15b144459123f1be1b0209cffc1deb4e"
		"linux_amd64":  "sha256:9b0ac4c1d8e36a86b59ced94fa517ae9b015b1d044b3455465cc6f0eab70915d"
		"linux_arm64":  "sha256:519319ed8f4312ed76519652ad6cd9f98bc75cf4ec7990a5684c072cf5dd0a5d"
	}
}
```

Without `--platforms` the command covers linux and darwin on amd64 and arm64. `--platforms linux_amd64,darwin_arm64` narrows it. A hash that is already pinned and differs is an error.

## 6. Reject different bytes

A runner with an empty provider store downloads each provider and compares the archive with the lock file. Change the hashes to simulate an archive that is not the one you locked.

```sh run exit=1
cp pravo.lock.cue lock.good
sed 's/sha256:[0-9a-f]\{64\}/sha256:0000/' lock.good > pravo.lock.cue
PRAVO_HOME=$PWD/runner-home pravo init
```

```text out
Error: provider hashicorp/random v3.7.2: the downloaded archive for <os>_<arch> is sha256:<hash>, but pravo.lock.cue pins sha256:0000; a published version must not change. If this is expected, remove the hash from the lock file and run "pravo init"
```

The error names your platform and the hash of the archive that was downloaded. Nothing is installed. Put the lock file back and the same runner installs the provider.

```sh run
cp lock.good pravo.lock.cue
PRAVO_HOME=$PWD/runner-home pravo init | grep -A1 '^Providers'
```

```text out
Providers:
  random = hashicorp/random v3.7.2
```

An archive that is already unpacked in the provider store is neither downloaded nor compared. Remove the hash from the lock file, and run `pravo init`, only when you expect the bytes to differ.

## 7. Upgrade a module

A module is locked in the same file, with a hash of its content. `pravo mod get <path>@<version>` moves it, and the next plan shows what the new version does. [Use a published module](using-a-module.md) walks through it.

## What next

- [Compatibility](../reference/compatibility.md) lists the provider protocols and the state format that pravo reads.
- [Install providers from your own registry](providers-from-a-registry.md) moves a root to providers that you host.
- [Run pravo in a pipeline](ci.md) runs `pravo init` and `pravo validate` before every plan.
- [pravo init](../reference/init.md) and [pravo providers](../reference/providers.md) list the flags used here.
