<!-- Generated from docs/how-to/provider-mirror.md by tools/skillgen. Do not edit. -->

# Install providers from a mirror

Fill a container registry with the providers a lock file pins, and read their archives from there while nothing else about the providers changes.

A mirror is a container registry that holds copies of the provider archives that a lock file pins. A run that has the mirror set downloads the archives from it. A platform with no route to `registry.opentofu.org` installs its providers from the registry of its own network.

The mirror changes where an archive is downloaded from, and nothing else. The import path in the specification, the key in `pravo.lock.cue` and the provider that the state records name are those of the source, so one specification runs with a mirror in one account and without one in another. [Install providers from your own registry](providers-from-a-registry.md) is the other way: it changes the source, and it edits the specification and relabels the state.

> [!NOTE]
> Every command below runs against `tools/fakeoci`, the in-memory registry in pravo's source, on port 5079, and downloads `hashicorp/random` from the OpenTofu registry once, to fill the mirror.

Work in an empty directory. In a second terminal, from a checkout of pravo's source, start the registry. It forgets everything when it stops.

```sh
go run ./tools/fakeoci -addr 127.0.0.1:5079
```

## 1. Lock the providers

Start with a root that uses the OpenTofu registry, and pin the hash of every platform that will install the providers.

File `main.cue`:

```cue
package mirrored

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "3.9.1"}

server: $random.#Resource.random_pet & {length: 2}
```

```sh
pravo init > /dev/null
pravo providers lock
pravo apply -auto-approve | grep '^Apply complete'
```

Output:

```text
Pinned 4 platforms in pravo.lock.cue
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <txn>]
```

The lock file is the root of trust of the mirror. A mirror is not asked what a provider is: a run installs an archive from it only when the lock file pins the hash of that archive for the platform, and refuses any other.

## 2. Fill the mirror

```sh
pravo providers mirror oci://localhost:5079/mirror
```

Output:

```text
Copying hashicorp/random v3.9.1 (darwin_amd64)
Copying hashicorp/random v3.9.1 (darwin_arm64)
Copying hashicorp/random v3.9.1 (linux_amd64)
Copying hashicorp/random v3.9.1 (linux_arm64)
Published hashicorp/random v3.9.1 to oci://localhost:5079/mirror/hashicorp/random

Published 1, already there 0. Read from it with PRAVO_PROVIDER_MIRROR=oci://localhost:5079/mirror
```

`mirror` copies each provider of the lock file, for each platform that the lock file pins, into the repository `<prefix>/<namespace>/<type>`, tagged with the locked version. It reads the archives from their sources and checks them against the checksums that the source gives and against the hashes in the lock file. It publishes nothing when one differs. A version that is published already is compared with the lock file and left as it is, so the command can run again after every `pravo providers lock`.

```sh
pravo providers mirror oci://localhost:5079/mirror
```

Output:

```text
hashicorp/random v3.9.1 is in oci://localhost:5079/mirror/hashicorp/random already

Published 0, already there 1. Read from it with PRAVO_PROVIDER_MIRROR=oci://localhost:5079/mirror
```

`pravo providers copy hashicorp/random 3.9.1 oci://localhost:5079/mirror/hashicorp/random` publishes one version into the same layout.

## 3. Install from the mirror

Install into a new `PRAVO_HOME`, as a new machine does, with the mirror set and with the OpenTofu registry out of reach. The proxy variable points at a port where nothing listens, so a request to `registry.opentofu.org` fails. The registry on `localhost` is not sent through a proxy.

```sh
export HTTPS_PROXY=http://127.0.0.1:9
export PRAVO_HOME="$PWD/fresh-home"
export PRAVO_PROVIDER_MIRROR=oci://localhost:5079/mirror
pravo init
```

Output:

```text
Providers:
  random = hashicorp/random v3.9.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/random (10 resource types, 0 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
```

The provider is `hashicorp/random`, not the mirror, and the specification still imports `pravo.dev/providers/hashicorp/random`. The state records and the lock file are unchanged, so a plan has nothing to report.

```sh
export HTTPS_PROXY=http://127.0.0.1:9
export PRAVO_HOME="$PWD/fresh-home"
export PRAVO_PROVIDER_MIRROR=oci://localhost:5079/mirror
pravo plan
pravo state show server | head -n 1
grep -c oci:// pravo.lock.cue || true
```

Output:

```text
No changes. The specification matches the state. 1 record was not read, so a change made outside pravo would not show here; run pravo drift to compare it with the infrastructure.
Graph: planned 0 · skipped 1 · not read 1
// server (provider registry.opentofu.org/hashicorp/random v3.9.1 as random, schema version 0, revision 1)
0
```

## What the mirror refuses

- A provider whose hash for this platform is not in the lock file. The error says to run `pravo providers lock --platforms os_arch` where the source can be reached. `pravo providers lock` always reads the checksums from the sources, whatever mirror is set.
- An archive in the mirror that is not the pinned one. Nothing is installed.
- A version that the mirror does not hold. There is no fall back to the OpenTofu registry.
- With `PRAVO_REQUIRE_SIGNED` set, a provider that the lock file pins without a signing key. A container registry has no signed checksum list, so the key in the lock file, which was recorded when the lock was made from a source that signs, is what stands for it.

## Settings

| Setting | Meaning |
|---|---|
| `PRAVO_PROVIDER_MIRROR=oci://HOST/PATH` | Reads `namespace/type` from the repository `PATH/namespace/type` of `HOST`. |
| `--provider-mirror oci://HOST/PATH` | The same for one run, for any command. It takes the place of the variable. Repeat it for exceptions. |
| `SOURCE=oci://HOST/PATH/TYPE` | An entry beside the prefix, or alone: reads that one source from that repository. `PRAVO_PROVIDER_MIRROR` takes entries separated by commas, as in `oci://HOST/mirror,hashicorp/aws=oci://HOST/aws-fork/aws`. |

The setting is not in the specification, because the same specification runs where the mirror is another registry. A provider that is in a container registry already, with a `$source` of `oci://...`, is not moved by the prefix.

## Authentication

pravo reads the docker configuration, as [Install providers from your own registry](providers-from-a-registry.md) says, and a `credHelpers` entry or a `credsStore` runs the `docker-credential-NAME` program that it names. For Amazon ECR, put `docker-credential-ecr-login` in the image and write `{"credHelpers": {"123456789012.dkr.ecr.us-east-2.amazonaws.com": "ecr-login"}}` in `~/.docker/config.json`. The helper reads the AWS credentials of the process. ECR does not create a repository on a push, so create `PATH/namespace/type` for each provider before `pravo providers mirror`.
