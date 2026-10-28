---
title: Use a published CUE module
description: Add a published CUE module to a root, lock it, read an upgrade before you take it and know what happens in CI and offline.
order: 4
minutes: 12
needs: "The module from [Publish a CUE module](publishing-a-module.md), published as v0.1.0 and v0.2.0 in the in-memory registry from pravo's source. Any module published the same way works."
done_when: "`pravo mod verify` prints `Verified. 1 modules match pravo.lock.cue.` and a changed version is refused with exit code 5."
level: intermediate
services:
  - fakeoci:5078
env:
  CUE_REGISTRY: example.com=localhost:5078+insecure
  GIT_CONFIG_GLOBAL: /dev/null
  GIT_CONFIG_NOSYSTEM: "1"
  GIT_AUTHOR_NAME: Docs
  GIT_AUTHOR_EMAIL: docs@example.com
  GIT_COMMITTER_NAME: Docs
  GIT_COMMITTER_EMAIL: docs@example.com
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: 'sha256:[0-9a-f]{64}'
    replace: 'sha256:<hash>'
  - pattern: 'dial tcp (\[::1\]|127\.0\.0\.1):5078'
    replace: 'dial tcp [::1]:5078'
group: Specify
keywords: "oci registry module"
---

A root adds a CUE module with one command, and `pravo.lock.cue` pins the version and a hash of its content. What the module brings is packages: the root imports one and writes an instance of its definition, as it does with code of its own. An upgrade is a diff that you read and a plan that you review.

> [!NOTE]
> Every command below runs against `tools/fakeoci`, the in-memory registry in pravo's source, on port 5078, with the module from the previous guide. Modules under `github.com/<owner>` on ghcr.io need no `CUE_REGISTRY`.

Work in an empty directory. Point the shell at the registry and at a cache directory that you can find again:

```sh
export CUE_REGISTRY='example.com=localhost:5078+insecure'
export CUE_CACHE_DIR="$HOME/.cache/cue"
```

```sh run hidden
# The module of "Publish a CUE module", published as v0.1.0 and v0.2.0. The files
# below are copies of that page: keep service.cue equal to its two versions.
git init -q -b main publisher
pravo -C publisher mod init example.com/acme/pravo-service@v0
pravo -C publisher mod edit --source git
```

```cue file=publisher/service/service.cue hidden
// Package service describes a small service: validated inputs, the
// resources that realise it and an endpoint.
package service

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

#Service: {
	// The providers the service is built through. The caller passes them in.
	random!: rnd.#Provider
	local!:  loc.#Provider

	// name is used in every resource and file name.
	name: string & =~"^[a-z][a-z0-9-]*$"
	// port the service listens on.
	port: int & >1024 & <65536
	// owner is the team that answers for the service.
	owner: string

	codename: random.#Resource.random_pet & {length: 2}

	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\ncodename \(codename.id)\nport \(port)\nowner \(owner)\n"
	}

	endpoint: "http://\(name).internal:\(port)"
}
```

```cue file=publisher/examples/basic/main.cue hidden
package basic

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	"example.com/acme/pravo-service/service"
)

$random: rnd.#Provider
$local:  loc.#Provider

api: service.#Service & {random: $random, local: $local, name: "api", port: 8080, owner: "platform"}

endpoint: pravo.#Output & {value: api.endpoint}
```

```text file=publisher/.gitignore hidden
cue.mod/gen/
pravo.state/
out/
```

```sh run hidden
pravo -C publisher/examples/basic init
git -C publisher add -A
git -C publisher commit -q -m "service module"
pravo -C publisher mod publish v0.1.0
```

```cue file=publisher/service/service.cue hidden
// Package service describes a small service: validated inputs, the
// resources that realise it and an endpoint.
package service

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

#Service: {
	// The providers the service is built through. The caller passes them in.
	random!: rnd.#Provider
	local!:  loc.#Provider

	// name is used in every resource and file name.
	name: string & =~"^[a-z][a-z0-9-]*$"
	// port the service listens on.
	port: int & >1024 & <65536
	// owner is the team that answers for the service.
	owner: string
	// tier selects the service class.
	tier: *"standard" | "premium"

	codename: random.#Resource.random_pet & {length: 2}

	config: local.#Resource.local_file & {
		filename: "out/\(name).conf"
		content:  "service \(name)\ncodename \(codename.id)\nport \(port)\nowner \(owner)\ntier \(tier)\n"
	}

	endpoint: "http://\(name).internal:\(port)"
}
```

```sh run hidden
git -C publisher commit -q -am "tier input"
pravo -C publisher mod publish v0.2.0
```

## 1. Add the dependency

```sh run
pravo mod init example.com/shop@v0
pravo mod get example.com/acme/pravo-service@v0.1.0
cat cue.mod/module.cue
```

```text out
module: "example.com/shop@v0"
language: {
	version: "v0.17.1"
}
deps: {
	"example.com/acme/pravo-service@v0": {
		v: "v0.1.0"
	}
}
```

`pravo mod get` pins the version in `cue.mod/module.cue`. When `CUE_REGISTRY` is not set, pravo maps every `github.com/<owner>` module to `ghcr.io/<owner>/cue`. When you set it, pravo leaves it alone.

## 2. Use it

The module ships the package `service` with the definition `#Service`. Read its documentation for what it needs. This one builds resources through two providers that it takes as inputs, `random` and `local`. The root declares them, pins their versions and passes them in.

```cue file=main.cue title="main.cue" hl="7,10-11,13-14"
package shop

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	"example.com/acme/pravo-service/service"
)

$random: rnd.#Provider & {$version: "~> 3.6"}
$local: loc.#Provider & {$version: "~> 2.5"}

cart: service.#Service & {random: $random, local: $local, name: "cart", port: 8081, owner: "checkout"}
payments: service.#Service & {random: $random, local: $local, name: "payments", port: 8082, owner: "payments"}

cart_endpoint: pravo.#Output & {value: cart.endpoint}
payments_endpoint: pravo.#Output & {value: payments.endpoint}
```

`service.#Service & {...}` fills the inputs of the definition, and the constraints in the definition check them. Its `random` and `local` are the root's `$random` and `$local`, not copies. The resources are at the path of each instance: `cart.codename`, `cart.config`, `payments.codename` and `payments.config`.

```sh run
pravo init > /dev/null
pravo plan | grep -E '^(  #|Plan|Graph)'
pravo apply -auto-approve | grep '^Apply complete'
```

```text out
  # cart.codename (random_pet) will be created
  # cart.config (local_file) will be created
  # payments.codename (random_pet) will be created
  # payments.config (local_file) will be created
Plan: 4 to add, 0 to change, 0 to destroy.
Graph: planned 4
Apply complete! Resources: 4 added, 0 changed, 0 destroyed. [txn <id>]
```

## 3. Read what pravo recorded

`pravo.lock.cue` holds the module next to the providers.

```sh run
grep -A3 '^modules' pravo.lock.cue
pravo mod list
pravo mod verify
```

```text out
modules: "example.com/acme/pravo-service@v0": {
	version: "v0.1.0"
	hash:    "sha256:<hash>"
}
example.com/acme/pravo-service@v0  v0.1.0  sha256:<hash>
Verified. 1 modules match pravo.lock.cue.
```

The hash covers the files that pravo loaded from the module. Commit the lock file.

## 4. Upgrade after reading the change

The author published v0.2.0, which adds a `tier` input with a default. Take it.

```sh run
pravo mod get example.com/acme/pravo-service@v0.2.0
pravo validate
```

```text out
Success! 4 resources, 0 data sources, 0 ephemeral, 0 function calls, 2 outputs across 2 providers.
```

CUE keeps each extracted version of a module in its cache, one directory per version. Compare the two versions of the package.

```sh run exit=1
diff "$CUE_CACHE_DIR/mod/extract/example.com/acme/pravo-service@v0.1.0/service/service.cue" \
     "$CUE_CACHE_DIR/mod/extract/example.com/acme/pravo-service@v0.2.0/service/service.cue"
```

```text out
20a21,22
> 	// tier selects the service class.
> 	tier: *"standard" | "premium"
26c28
< 		content:  "service \(name)\ncodename \(codename.id)\nport \(port)\nowner \(owner)\n"
---
> 		content:  "service \(name)\ncodename \(codename.id)\nport \(port)\nowner \(owner)\ntier \(tier)\n"
```

A CUE module is CUE, which cannot run code or reach the network, so the diff shows everything that a version can do. What it does to your infrastructure is the plan.

```sh run
pravo plan | grep -E '^(  #|Plan|Graph)'
```

```text out
  # cart.config (local_file) must be replaced
  # payments.config (local_file) must be replaced
Plan: 2 to add, 0 to change, 2 to destroy.
Graph: planned 2 · skipped 2 · refreshed 4
```

The new input has a default, so the existing uses evaluate. The two files change and nothing else does. Apply when you are satisfied. The lock file now says v0.2.0 with the hash of that version.

## 5. A changed version is refused

A published version must not change. Change the hash in the lock file to simulate a republished tag or a tampered cache, and run any command.

```sh run exit=5
sed '/^modules/,$ s/sha256:[0-9a-f]*/sha256:0000/' pravo.lock.cue > lock.tmp
mv lock.tmp pravo.lock.cue
pravo plan
```

```text out
Error: module example.com/acme/pravo-service@v0 v0.2.0 has different content than when it was locked (sha256:0000, now sha256:<hash>); a published version must not change. If this is expected, review the change and run "pravo init" to relock
```

Exit code 5 is the code for a refusal: a CUE module that does not match its lock, or a plan that violates a [policy](policies.md). `pravo mod verify` lists the differences without a plan.

```sh run exit=5
pravo mod verify
```

```text out
  - example.com/acme/pravo-service@v0 v0.2.0: content differs from the lock (sha256:0000, now sha256:<hash>)

Error: 1 module(s) do not match the lock
```

With `--json` it prints the problems as data and still exits 5.

```sh run exit=5
pravo mod verify --json
```

```text out
{
  "ok": false,
  "problems": [
    "example.com/acme/pravo-service@v0 v0.2.0: content differs from the lock (sha256:0000, now sha256:<hash>)"
  ]
}
```

When nothing differs it prints `"ok": true` and exits 0. A pipeline can test the exit code in either mode.

Find out why the content changed before you relock. `pravo init` relocks.

```sh run
pravo init > /dev/null
pravo mod verify
```

```text out
Verified. 1 modules match pravo.lock.cue.
```

```sh run
pravo mod verify --json
```

```text out
{
  "ok": true,
  "problems": []
}
```

## 6. CI and offline

pravo fetches a module when it needs it and keeps it in the CUE cache. With the registry unreachable, a warm cache loads the module. A cold cache cannot. Stop the registry with Ctrl-C in its terminal.

```sh run hidden
kill "$FAKEOCI_PID"
```

```sh run
pravo validate
```

```text out
Success! 4 resources, 0 data sources, 0 ephemeral, 0 function calls, 2 outputs across 2 providers.
```

A new cache directory stands for an empty cache, as on a fresh CI runner.

```sh run exit=6
CUE_CACHE_DIR="$PWD/empty-cache" pravo validate
```

```text out
Error: loading .:
example.com/shop@v0: import failed: cannot find package "example.com/acme/pravo-service/service": cannot fetch example.com/acme/pravo-service@v0.2.0: module example.com/acme/pravo-service@v0.2.0: cannot do HTTP request: Get "http://localhost:5078/v2/example.com/acme/pravo-service/manifests/v0.2.0": dial tcp [::1]:5078: connect: connection refused:
    ./main.cue:7:2
```

The exit code is 6. In CI, point `CUE_CACHE_DIR` at a directory that the CI system keeps between runs, or accept one fetch on the first run. Extracted modules are read-only. To clear a cache, make it writable first with `chmod -R u+w`.

## 7. When it fails

| Message | Cause and fix |
|---|---|
| `cannot find package "pravo.dev/providers/..."` and the hint `a provider schema package is missing` | The generated packages are missing, or the package needs a provider that it did not before. Run `pravo init`. |
| `cannot find module providing package example.com/acme/pravo-service/service` | The dependency is not in `cue.mod/module.cue`. Run `pravo mod get <path>@<version>`. |
| `has different content than when it was locked` | A published version changed. Find out why before you run `pravo init`. |
| `cannot fetch` and `connection refused` | The registry is unreachable and the cache is cold. |

A private module needs credentials for pulls as well as for pushes. CUE reads the container credentials that `docker login` writes.

## What next

- [Publish a CUE module](publishing-a-module.md) if you want to write one.
- [Share code between roots in one repository](share-code-between-roots.md) when the code does not leave the repository: a package in the same CUE module.
- [Upgrade pravo and providers](upgrading.md) covers the lock file for providers.
- [pravo mod](../reference/mod.md) lists every subcommand.
