---
title: Keep the state in a shared store
description: Keep the state in an S3 bucket, or on an HTTP server of your own, so that a team shares it. Move an existing local state into it, read another root's outputs and decide who may write it.
order: 1
minutes: 10
needs: A specification that you can plan, and an S3 bucket. This page uses the in-memory bucket from pravo's source.
done_when: "`pravo state list` prints the same addresses from two checkouts, and `pravo plan` in the second one reports no changes."
level: intermediate
services:
  - fakes3:9000
env:
  AWS_ACCESS_KEY_ID: test
  AWS_SECRET_ACCESS_KEY: test
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}(Z|[+-][0-9:]{5})'
    replace: '<time>'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8} +[0-9.]+(µs|ms|s)'
    replace: '<date> <time> <took>'
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: 'http://[a-z-]+\.internal:[0-9]+'
    replace: 'http://<pet>.internal:<port>'
group: Share and protect
keywords: "remote state backend s3 bucket http server lock terraform_remote_state"
---

A shared store holds the state for every checkout, and the locks that keep two people from changing one resource. It is an S3 bucket, a store that speaks the S3 API, or an HTTP server of your own. This page puts a state in a bucket, reads it from a second checkout and moves a local state into it. It ends with the server, and with who may write.

> [!NOTE]
> Every command below runs against `tools/fakes3`, the in-memory S3 server in pravo's source, on port 9000. For a real bucket, change step 1 and the `endpoint` and `path_style` lines.

## 1. Provide a bucket

For AWS, create the bucket first and turn on versioning, so that an overwrite can be undone. pravo does not create the bucket. It needs permission to get, put, delete and list objects in it. A store other than Amazon S3 must honour conditional requests: [S3-compatible stores](../reference/s3-stores.md) says how to test it. Credentials come from the standard AWS chain: environment variables, a shared profile or an instance role. Never write keys in the specification.

To follow along without an account, start the in-memory server in a second terminal from a checkout of pravo's source. It forgets everything when it stops.

```sh
go run ./tools/fakes3 -addr 127.0.0.1:9000
```

Give the shell test credentials. The server accepts any value.

```sh
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
```

## 2. Point the root at the bucket

Work in an empty directory. The `state` block of `$settings` selects the backend.

```cue file=main.cue title="main.cue" hl="9-17"
package remote

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "pravo-state"
		prefix:     "demo/app"
		region:     "us-east-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {length: 2}
port: $random.#Resource.random_integer & {min: 8000, max: 8999}
```

- `bucket` is required. `prefix` lets several roots share one bucket.
- `endpoint` and `path_style` are for S3-compatible servers. For AWS, remove both and keep `region`.
- `prefix` may be computed. `prefix: "demo/\(env)"` gives each [environment](environments.md) its own state.

## 3. Apply and look at the state

A store that ignores conditional requests lets two commands hold one lock, so pravo probes an S3 store before it first writes to it. `pravo state probe` runs the same checks on demand:

```sh run
pravo init > /dev/null
pravo state probe
```

```text out
create only (If-None-Match)      ok
replace (If-Match on a put)      ok
delete (If-Match on a delete)    ok
concurrent creates               ok
s3://pravo-state/demo/app honours the conditional requests that the locks need.
```

```sh run
pravo init > /dev/null
pravo apply -auto-approve | grep '^Apply complete'
ls
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
cue.mod
main.cue
pravo.lock.cue
```

No `pravo.state` directory appears. The state is in the bucket, and every command reads it from there.

```sh run
pravo state list
pravo state log
```

```text out
port
server
<txn>  apply    applied   <date> <time> <took>
    pravo apply --auto-approve
    2 added, 0 changed, 0 destroyed
    create   port  r0 -> r1
    create   server  r0 -> r1
```

The addresses are the paths of the resources in the specification, whichever backend holds the state.

## 4. Use it from a second checkout

A colleague clones the repository. Here the clone is a new directory with the same `main.cue`.

```sh run
mkdir second
cp main.cue second
pravo -C second init > /dev/null
pravo -C second state list
pravo -C second plan | grep -E '^(No changes|Graph)'
```

```text out
port
server
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
```

The second checkout sees the same state. The resources are random values, which exist only in the state, so nothing differs. A resource that lives on one machine's disk, such as `local_file`, is a different case. Another machine finds the file missing and plans to create it again. Remote state suits resources that live somewhere shared, as cloud resources do.

## 5. Coordinate through the bucket

Locks and reservations are objects in the bucket. A reservation made in one checkout stops another actor in a second checkout. Change `length` to 3 in `main.cue` and reserve the node:

```cue file=main.cue title="main.cue" hl="21"
package remote

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "pravo-state"
		prefix:     "demo/app"
		region:     "us-east-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {length: 3}
port: $random.#Resource.random_integer & {min: 8000, max: 8999}
```

```sh run
pravo plan -out change.plan -reserve=10m --actor alice | grep '^Reserved'
pravo state locks
```

```text out
Reserved 1 node until <time>.
server  reserved by alice until <time>: pravo plan --out change.plan --reserve=10m --actor alice
```

The second checkout takes the same change and plans as another actor. `-refresh=false` skips reading the resources, so the output is the refusal alone.

```sh run exit=4
cp main.cue second
pravo -C second plan -refresh=false --actor bob
```

```text out
Error: plan rejected: server is reserved by alice (pravo plan --out change.plan --reserve=10m --actor alice until <time>, since <time>)
```

Exit 4 means that a node is locked or reserved. A command that changes a resource holds a lock on it. A running command renews the lock every 2 minutes, and a lock that is not renewed for 10 minutes is reclaimed. `pravo state locks` marks the locks that may be reclaimed with `[stale]`. `pravo state unlock <address>` releases a lock that a crashed run left behind, and `pravo state unlock --stale` releases all of them. A lock or a reservation covers one resource, and `pravo state locks` lists them by address. Applying the plan ends the reservation:

```sh run
pravo apply change.plan --actor alice | grep '^Apply complete'
pravo state locks
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <txn>]
No locks or reservations held.
```

A reservation keeps others out. To let a colleague see, read and apply a plan, store it in the bucket: [Store a plan and apply it later](store-a-plan-and-apply-it-later.md) shows how.

An apply locks the resources it writes and no others. After it holds the locks, it reads the revision of each resource it writes, and of each record it reads, from the bucket, and stops with exit code 3 if another command wrote one of them since the plan was made. The bucket must return the object that was last written when it is read back. S3 does. Opening the state reads one object for each record before a command starts, 64 at a time, so the time to open grows with the size of the estate and with the latency of the bucket. A plan then reads from the providers only the records it looks at.

## 6. Move a local state into the bucket

A root that has used the local `pravo.state` directory is moved by copying the directory up. Start from such a root. Its `settings.cue` is not written yet.

```cue file=moved/main.cue title="moved/main.cue"
package moved

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider & {$version: "~> 3.6"}

server: $random.#Resource.random_pet & {length: 2}
port: $random.#Resource.random_integer & {min: 8000, max: 8999}
```

```sh run
pravo -C moved init > /dev/null
pravo -C moved apply -auto-approve | grep '^Apply complete'
ls moved
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <txn>]
main.cue
pravo.lock.cue
pravo.state
```

Add the `state` block in a file of its own, with a new prefix.

```cue file=moved/settings.cue title="moved/settings.cue"
package moved

import "pravo.dev/pravo"

$settings: pravo.#Settings & {
	state: pravo.#S3State & {
		backend:    "s3"
		bucket:     "pravo-state"
		prefix:     "demo/moved"
		region:     "us-east-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}
```

The bucket is empty at this prefix, so a plan creates everything. `pravo state push` copies the local directory into the configured backend, and the plan is then empty.

```sh run
pravo -C moved plan | grep -E '^(Plan|No changes)'
pravo -C moved state push pravo.state
pravo -C moved plan | grep -E '^(Plan|No changes)'
```

```text out
Plan: 2 to add, 0 to change, 0 to destroy.
Copied 5 objects from pravo.state to s3://pravo-state/demo/moved
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
```

Archive the local directory when you are satisfied. `pravo state pull <dir>` copies the other way, from the bucket into a local directory.

## 7. Read another root's outputs

A second root reads the values that the first one published, and nothing else from its state. The first root publishes an output:

```cue file=url.cue title="url.cue"
package remote

import "pravo.dev/pravo"

url: pravo.#Output & {value: "http://\(server.id).internal:\(port.result)"}
```

```sh run
pravo apply -auto-approve | grep -A1 '^Outputs'
```

```text out
Outputs:
  url: "http://<pet>.internal:<port>"
```

In the second root, `pravo.#RemoteOutputs` points at the first root's state. The settings are those of the state itself.

```cue file=app/main.cue title="app/main.cue"
package app

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
)

$random: rnd.#Provider & {$version: "~> 3.6"}

net: pravo.#RemoteOutputs & {
	state: {
		backend:    "s3"
		bucket:     "pravo-state"
		prefix:     "demo/app"
		region:     "us-east-1"
		endpoint:   "http://127.0.0.1:9000"
		path_style: true
	}
}

worker: $random.#Resource.random_pet & {length: 2, prefix: net.outputs.url}
```

```sh run
pravo -C app init > /dev/null
pravo -C app plan | grep -E 'prefix|^Plan'
```

```text out
  +     prefix:    "http://<pet>.internal:<port>"
Plan: 1 to add, 0 to change, 0 to destroy.
```

pravo reads only the object `outputs.json` under the prefix. A bucket policy that allows reading that object gives the second root the outputs and none of the resources. Outputs marked `sensitive: true` are not given out. pravo does not order the two roots: apply the first before the second.

## 8. Decide who may write the bucket

The bucket holds the record of what exists. Whoever can write to it can change what the next plan believes: `pravo state rm`, `pravo state mv` and `pravo import` are not plans, so no [policy](policies.md) sees them. The locks and the transaction log are objects in the same bucket. Give read, write, delete and list permission to the identities that run pravo against this root, and treat the right to write the bucket as the right to change what pravo knows. The bucket also holds the plans that `pravo plan --store` writes. A stored plan holds the same values as a plan file, sensitive values included, so whoever can read the bucket can read its plans.

Two further steps narrow that. [Seal the state](seal-the-state.md) encrypts the attributes, the outputs and the stored plans to a list of readers, so that the bucket, a backup or an old version of an object shows addresses and no value. [Give people roles](give-people-roles.md) then says which of those readers may write, and the credentials of the bucket follow the roles: a reader gets `get` and `list`, and `put` under `plans/`; a writer gets `put` and `delete` as well, except on `meta.json`, `policy/` and `roles/`; an admin gets everything. [`pravo protect roles`](../reference/roles.md#what-roles-do-not-do) has the table.

## 9. Use an HTTP server instead of a bucket

A team that has no bucket, or that keeps its state behind a service of its own, points the root at an HTTP server. The backend keeps the same objects, one for each record, each lock, each transaction and each stored plan, under a URL, and the server has to honour the three conditional requests that the locks rest on, as a bucket does.

```cue
$settings: pravo.#Settings & {
	state: pravo.#HTTPState & {
		backend: "http"
		url:     "https://state.example.com/team/app"
	}
}
```

Every command of this page runs the same against it, and `pravo state probe` runs the same checks against a server as against a bucket. [HTTP state servers](../reference/http-stores.md) lists the requests a server has to answer, how pravo retries, and `tools/fakehttp`, an in-memory server in pravo's source to try the protocol against. [Settings](../reference/spec-settings.md#http-keys) lists the keys, among them the token or the user name that every request carries.

## What next

- [Environments](environments.md) computes the prefix from the environment.
- [Seal the state](seal-the-state.md) encrypts the values in the bucket to a list of readers, and [Give people roles](give-people-roles.md) says which of them may write.
- [Store a plan and apply it later](store-a-plan-and-apply-it-later.md) stores a plan in the bucket, where a colleague can list, approve and apply it.
- [CI](ci.md) runs plan and apply as separate jobs against the shared state.
- [State](../concepts/state.md) describes what the bucket holds.
- [state](../reference/state.md), [Settings](../reference/spec-settings.md) and [HTTP state servers](../reference/http-stores.md) are the reference for `pravo state push`, `pull`, the `state` block and the server. [Environment variables and remote outputs](../reference/spec-env.md) covers `pravo.#RemoteOutputs`.
