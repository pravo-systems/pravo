<!-- Generated from docs/how-to/providers-from-a-registry.md by tools/skillgen. Do not edit. -->

# Install providers from your own registry

Copy a provider into a container registry, install it from there and move a root that has resources to the copy without touching them.

A team can keep its providers in a container registry, next to its images and CUE modules, pinned by digest and inside its own network. This page copies a provider into a registry, installs it from there and moves a root that already has resources.

> [!NOTE]
> Every command below runs against `tools/fakeoci`, the in-memory registry in pravo's source, on port 5001, and downloads `hashicorp/random` from the OpenTofu registry.

Work in an empty directory.

## 1. Provide a registry

Any registry that stores OCI artifacts works. pravo reads the credentials in the docker configuration, `~/.docker/config.json`, and sends plain HTTP to `localhost` only. To follow along without an account, start the in-memory registry in a second terminal from a checkout of pravo's source. It forgets everything when it stops.

```sh
go run ./tools/fakeoci -addr 127.0.0.1:5001
```

## 2. Start from the OpenTofu registry

File `main.cue`:

```cue
package mirror

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "3.9.1"}

server: $random.#Resource.random_pet & {length: 2}
```

```sh
pravo init > /dev/null
pravo apply -auto-approve | grep '^Apply complete'
pravo state show server | head -n 1
```

Output:

```text
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
// server (provider registry.opentofu.org/hashicorp/random v3.9.1 as random, schema version 0, revision 1)
```

The record of `server` names the provider that wrote it: `registry.opentofu.org/hashicorp/random`.

## 3. Copy the provider

The command takes a source, a version and a destination.

```sh
pravo providers copy hashicorp/random 3.9.1 oci://localhost:5001/acme/providers/random
```

Output:

```text
Copying hashicorp/random v3.9.1 (linux_amd64)
Copying hashicorp/random v3.9.1 (linux_arm64)
Copying hashicorp/random v3.9.1 (darwin_arm64)
Copying hashicorp/random v3.9.1 (darwin_amd64)
Published oci://localhost:5001/acme/providers/random v3.9.1

Import it with: import "pravo.dev/providers/oci/localhost_5001/acme/providers/random"
```

The copy covers linux and darwin on amd64 and arm64; `--platforms linux_amd64,darwin_arm64` narrows it. The repository is `acme/providers/random`, the version is a tag, and the tag holds one manifest per platform. Each manifest has one layer, the zip archive that the OpenTofu registry serves.

pravo checks the archives against the checksums that the source gives. It does not verify the signature of the original release. After the copy, what you trust is the digest that pravo records and whoever can write to the repository.

A published version is never replaced.

```sh
pravo providers copy hashicorp/random 3.9.1 oci://localhost:5001/acme/providers/random
```

Output:

```text
Error: oci://localhost:5001/acme/providers/random already has version 3.9.1; a published version must not change
```

## 4. Switch the import

Change the import to the path that the command printed. The port of the host follows an underscore: `localhost_5001`.

File `main.cue`:

```cue
package mirror

import rnd "pravo.dev/providers/oci/localhost_5001/acme/providers/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {length: 2}
```

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  random = oci://localhost:5001/acme/providers/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/oci/localhost_5001/acme/providers/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

`pravo init` lists the tags, takes the newest that satisfies `~> 3.6` and has a build for your platform, downloads it and generates the package from its schema. The package is a separate directory, so both sources can be present while you move.

## 5. Say where the resources came from

```sh
pravo plan
```

Output:

```text
Error: server was written by provider registry.opentofu.org/hashicorp/random, but the provider is now oci://localhost:5001/acme/providers/random; add $from: "registry.opentofu.org/hashicorp/random" to the provider
```

pravo does not guess that two sources are the same provider. Declare it with `$from` on the provider, the way `$from` declares a renamed resource.

File `main.cue`:

```cue
package mirror

import rnd "pravo.dev/providers/oci/localhost_5001/acme/providers/random"

$random: rnd.#Provider & {
	$version: "~> 3.6"
	$from:    "registry.opentofu.org/hashicorp/random"
}

server: $random.#Resource.random_pet & {length: 2}
```

```sh
pravo plan
```

Output:

```text
pravo will perform the following actions:

  # server will have its provider changed from registry.opentofu.org/hashicorp/random to oci://localhost:5001/acme/providers/random (record relabelled; no change to the object)

Plan: 0 to add, 0 to change, 0 to destroy.
Graph: planned 0 · skipped 1 · not read 1
```

The plan lists the relabelling of the record and writes nothing. It changes nothing else: no change and no replacement. The apply writes the new label, and then a plan prints no relabelling.

```sh
pravo apply --auto-approve | grep '^Apply complete'
pravo plan
pravo state show server | head -n 1
```

Output:

```text
Apply complete! Resources: 0 added, 0 changed, 0 destroyed. [txn <txn>]

No changes. The specification matches the state. 1 record was not read; run pravo drift to compare it with the infrastructure.
Graph: planned 0 · skipped 1 · not read 1
// server (provider oci://localhost:5001/acme/providers/random v3.9.1 as random, schema version 0, revision 2)
```

`$from` is a claim that the two builds are the same provider. pravo cannot check it. Copy from the source that you are moving away from, as here. Remove `$from` when every record has moved. Leaving it does no harm.

## 6. Pin every platform

The lock file records the digest of the archive for the platform that was installed. It equals the hash that the OpenTofu registry states for the same version, which shows that the copy is the original.

```sh
pravo providers lock
sed -n '/^providers: "oci/,/^}/p' pravo.lock.cue
```

Output:

```text
Pinned 4 platforms in pravo.lock.cue
providers: "oci://localhost:5001/acme/providers/random": {
	version:     "3.9.1"
	constraints: "~> 3.6"
	hashes: {
		"darwin_amd64": "sha256:aaa51d905b0e80a28e02f9bee2cf6c91ffade7389d77ab9198aa12809ed04955"
		"darwin_arm64": "sha256:a2259540854d5f699c36b89244fb202ebb2c219b64669a51072687d04fb47152"
		"linux_amd64":  "sha256:624fb6ed552abc34a5aaac41e76a373da65ac08e524b09b672f29c60e6ac896a"
		"linux_arm64":  "sha256:09aaf19b0d22726d2378e0e89fbbefc183494d7bd585759d6c4e69ba50951a2f"
	}
}
```

A Linux runner now installs the same bytes. A later `pravo init` rejects an archive that does not match the pinned hash. The entry for `registry.opentofu.org` stays in the file until you remove it.

## What next

- Run `pravo providers copy` from a release job when you adopt a new provider version, and give the rest of the team read access to the repository.
- [Upgrade pravo and providers](upgrading.md) covers the lock file and `pravo init -upgrade`.
- [Providers](../concepts/providers.md) describes sources, protocols and the generated packages.
- [pravo providers](../reference/providers.md) and [Providers in the specification](../reference/spec-providers.md) are the reference for `copy`, `lock` and `$from`.
