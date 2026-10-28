---
title: State
description: What pravo records for each resource, how revisions, locks and transactions work, why the cost of a run follows the change, and where the state lives.
order: 5
minutes: 14
keywords: "terralith monolith split states large estate scale"
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: 'txn/[0-9]{8}T[0-9]{6}Z-[0-9a-f]+\.json'
    replace: 'txn/<id>.json'
  - pattern: '"(txn|generation)": "[0-9]{8}T[0-9]{6}Z-[0-9a-f]+"'
    replace: '"$1": "<id>"'
  - pattern: '"(refreshed_at|applied_at|updated_at)": "[^"]+"'
    replace: '"$1": "<time>"'
  - pattern: '"config_hash": "[0-9a-f]+"'
    replace: '"config_hash": "<hash>"'
  - pattern: '"provider_version": "[0-9.]+"'
    replace: '"provider_version": "<v>"'
  - pattern: 'hashicorp/local v[0-9]+\.[0-9]+\.[0-9]+'
    replace: 'hashicorp/local v<v>'
  - pattern: '[0-9]+s ago'
    replace: '<n>s ago'
  - pattern: 'config hash [0-9a-f]+'
    replace: 'config hash <hash>'
  - pattern: '(?m)^[0-9]{8}T[0-9]{6}Z-[0-9a-f]+ '
    replace: '<id> '
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8} [0-9.]+(ms|µs|s)'
    replace: '<time> <took>'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}(Z|[+-][0-9]{2}:[0-9]{2})'
    replace: '<time>'
---

pravo records what it manages in a **state graph**. Every resource and every data source is a node with its own record, its own revision and its own lock. A dependency is an edge between two nodes. There is no state file and no lock on the whole state. [Cost follows the change](#cost-follows-the-change) says what that gives a large estate.

## A directory of records

The default state is a directory next to the specification, `pravo.state`. Work in an empty directory. The specification below has two resources and two outputs. One output is sensitive.

```cue file=main.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {refresh: "1h"}

$random: rnd.#Provider
$local:  loc.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29999, seed: "web"}

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}

url: pravo.#Output & {value: "http://localhost:\(port.result)"}
digest: pravo.#Output & {value: web.content_sha256, sensitive: true}
```

```sh run
pravo init | tail -1
```

```text out
pravo has been initialised. Run "pravo plan" to see what it would do.
```

```sh run
pravo apply -auto-approve | tail -4
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]

2 outputs, 2 changed (digest, url). See them with "pravo output" or "pravo serve".
```

```sh run
find pravo.state -type f | sort
```

```text out
pravo.state/meta.json
pravo.state/outputs.json
pravo.state/resources/port.json
pravo.state/resources/web.json
pravo.state/txn/<id>.json
```

| Object | Content |
|---|---|
| `meta.json` | The format version of the store, its lineage and the time it was created. |
| `resources/ADDRESS.json` | One node for each resource or data source, named by its address. |
| `outputs.json` | The outputs recorded by the last apply. |
| `txn/ID.json` | One recorded transaction. |
| `locks/ADDRESS.lock` | A lock or a reservation, present while it is held. |
| `plans/ID.plan.json`, `plans/ID.meta.json`, `plans/ID.progress.json` | A stored plan: the plan, its summary, and what was applied from it. They exist after `pravo plan --store` and until a collection removes them. See [Stored plans and approvals](stored-plans-and-approvals.md). |

The lineage identifies the store for its whole life. A saved plan records it, and an apply refuses the plan when the lineage differs. A stored plan records it too, and a listing marks a plan stale when the lineage differs.

In a file name, every other character of an address is written as `%` and four hexadecimal digits. The characters that stay are letters, digits, `.`, `_` and `-`. The address `replicas[0]` is stored as `resources/replicas%005B0%005D.json`.

Each node is written on its own, to a temporary file that replaces the old one. A crash never leaves a half-written node. Two commands that write different nodes never conflict. The files are written with mode 0600 in directories with mode 0700, and the attributes of a node can include secrets. [Secrets and ephemeral values](secrets-and-ephemeral-values.md) says what the state holds.

## A record

A node holds what the provider reported and what pravo needs to plan.

```sh run
jq 'del(.attributes)' pravo.state/resources/web.json
```

```text out
{
  "addr": "web",
  "mode": "managed",
  "type": "local_file",
  "name": "web",
  "provider": "registry.opentofu.org/hashicorp/local",
  "provider_key": "local",
  "provider_version": "<v>",
  "schema_version": 0,
  "dependencies": [
    "port"
  ],
  "config_hash": "<hash>",
  "revision": 1,
  "generation": "<id>",
  "refreshed_at": "<time>",
  "applied_at": "<time>",
  "updated_at": "<time>",
  "txn": "<id>"
}
```

| Field | Meaning |
|---|---|
| `addr` | The address of the node: the path of the resource in the specification. |
| `mode`, `type`, `name` | `managed` or `data`, the provider type, and the last element of the path. |
| `provider`, `provider_key`, `provider_version` | The provider that wrote the node, the instance that was used, and its version. A different installed version, or a different instance, plans the node again. |
| `schema_version` | The provider's schema version of the type. |
| `attributes` | The attributes as the provider returned them. |
| `private` | Opaque provider data, base64 encoded. |
| `dependencies` | The addresses the node depended on when it was applied. These are the outgoing edges. |
| `config_hash` | A digest of the configuration that the provider last saw. A plan compares it to skip a resource. An apply that stopped halfway leaves it empty, so the next plan changes the resource again. |
| `provider_config` | A digest of the configuration of the provider instance that last wrote the node, where the instance has an attribute that says where it points. A plan reads a node whose digest differs from the one of its provider now. |
| `drifted_at`, `gone` | A mark. A read found that the object differs from the record, or that it is gone. A node with a mark is planned until an apply writes it. Both are absent from a record without a mark. |
| `revision` | The count of writes of the node. |
| `generation` | The transaction that created the record. A node that is removed and created again has another generation. |
| `refreshed_at` | When the provider last reported the real state, by a read or an apply. A plan compares it with the refresh age of the node, for the nodes it looks at. |
| `applied_at`, `updated_at` | When pravo last created or changed the object, and when it last wrote the record. |
| `txn` | The transaction that last wrote the node. |

An ephemeral resource and a function call have no node. `pravo state show` prints the bookkeeping as comments and the attributes as CUE.

```sh run
pravo state show web | head -4
```

```text out
// web (provider registry.opentofu.org/hashicorp/local v<v> as local, schema version 0, revision 1)
// refreshed <n>s ago, applied <n>s ago, config hash <hash>
// depends on port
{
```

The graph is queryable. `pravo state graph` prints the edges, or Graphviz with `-dot`. `pravo state dependents ADDRESS` prints everything that a change to the node can reach. `pravo state query EXPRESSION` evaluates a CUE expression with `resources`, `edges` and `outputs` in scope.

```sh run
pravo state graph
```

```text out
web -> port
```

```sh run
pravo state query '[for a, r in resources if r.type == "local_file" {a}]'
```

```text out
["web"]
```

## Revisions

A revision counts the writes of a node. A new node has revision 1.

- A write that changes the node raises the revision by one.
- A refresh that returns the attributes already recorded updates the timestamps and keeps the revision. Looking at the real world does not invalidate the saved plans that touch the node.
- A refresh that returns other attributes, or finds that the object is gone, writes the mark and raises the revision, so a saved plan made against the node is stale.
- A read of a data source that returns the attributes and edges already recorded keeps the revision. With the refresh age `always` or `never` it writes nothing. With a duration it writes the time of the read, as the refresh of a resource does.
- A replacement deletes the record and writes a new one that continues the count: the new record is one revision above the old one. A saved plan made against the old record is stale.
- A move writes the record at its new address, one revision above the old one, in the same generation.
- A node that is removed and created again, by `pravo state rm` or by a destroy and a later apply, starts at revision 1 again. Its **generation** is the transaction that created the new record, so it differs from the generation of the record it replaced. A record written before generations were recorded has none, and gets one when it is next written.

A saved plan records the revision and the generation of every node it touches. [Plans](plans.md) describes how an apply uses them. A plan made against a record that has since been removed and created again is stale, although the new record may be at the same revision.

## Transactions

A transaction is the record of one command that read providers, changed a record or changed the infrastructure. `pravo state log` lists them, newest first.

Change the specification so that it replaces `port`, then rename `web` to `site` with `$from`.

```cue file=main.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {refresh: "1h"}

$random: rnd.#Provider
$local:  loc.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29998, seed: "web"}

site: $local.#Resource.local_file & {
	$from:    "web"
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}

url: pravo.#Output & {value: "http://localhost:\(port.result)"}
digest: pravo.#Output & {value: site.content_sha256, sensitive: true}
```

```sh run
pravo plan -out change.plan | tail -2
```

```text out
Saved the plan to change.plan. To apply exactly these actions, run:
  pravo apply change.plan
```

```sh run
pravo apply change.plan | grep '^Apply complete'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 2 destroyed. [txn <id>]
```

The saved plan replaced `port` and `site`, which reads it. The plan also listed the move of `web` to `site`, and wrote nothing: a plan changes a record only to refresh it. The apply wrote the move, in its own transaction, and then replaced `site`.

```sh run
pravo state log
```

```text out
<id>  apply    applied   <time> <took>
    pravo apply change.plan
    2 added, 0 changed, 2 destroyed
    replace  port  r1 -> r2
    move-to  site  r0 -> r2
    replace  site  r2 -> r3
    move-from web
<id>  apply    applied   <time> <took>
    pravo apply --auto-approve
    2 added, 0 changed, 0 destroyed
    create   port  r0 -> r1
    create   web  r0 -> r1
```

A transaction records:

- the kind: `plan`, `apply`, `destroy`, `import` or `edit`;
- the command line as it was run, flags included;
- the user, the host, the actor and the reason, the time and the duration;
- the outcome: `planned`, `applied`, `rejected` or `failed`. An apply is `running` from the moment it holds its locks until it ends; a running apply whose command was killed is listed by `pravo state log` as `interrupted`;
- every node that it touched, with the revision before and after.

A plan that read nothing and changed no record is not recorded. A plan that refreshed records is. An apply that a lock rejected is recorded too, with no changes. An apply that has no record to write and no output to write or remove takes no lock and is not recorded.

The command line is the one that ran. An apply that a policy refused is recorded as rejected, with the versions that refused it. The signed start of every apply that judged a plan lists the policy versions that judged it. The log is evidence. It prevents nothing, and it is as writable as the rest of the state.

## Locks and reservations

An apply locks every node that its plan writes, and no other, all of them before it changes anything, and releases them when it finishes. A node that the apply only reads is not locked, and neither is a data source whose read returned its record. A lock held by another process rejects the apply before it starts, with exit code 4. An apply never holds some of its locks while it waits for the others. When it finds a lock held it releases the ones it has taken. If the command that holds the lock has not begun to apply, because it is taking locks of its own and may give them up, the apply looks at the lock for a short time, between 25 milliseconds and 3 seconds, and takes its locks again when the lock is free. A command that applies, or a reservation, rejects it at once. Of applies that start together and need some of the same nodes, one takes them and the others are rejected because of it. Once the apply holds its locks, it reads the revisions and generations of the nodes it writes and of the records it reads that the plan lists from the state, and a node that another command wrote, removed or created again since the plan was made stops it with exit code 3. Applies that need different nodes run at the same time. Outputs are one record that each apply updates when it ends. An apply writes the outputs whose value it changed and leaves the others as they are recorded at that moment, so two applies that run at the same time both keep their own changes. Two applies that change the same output leave the value of the one that ends last.

:::figure Locks belong to nodes. Apply A holds db for the length of a slow create. Apply B needs web and runs at once. Apply C needs db and is rejected. A plan holds a lock only while it writes a refreshed record.
<svg viewBox="0 0 780 250" role="img" aria-label="A timeline with four rows. Apply A holds the lock of node db from the start to the end of a slow create. Apply B needs node web, takes its lock while A runs, and finishes. Apply C starts while A runs and needs db, so it is rejected and exits with code 4. A plan runs while A runs, takes no lock, and writes a refreshed record only for a node that nobody holds." style="min-width:680px">
<text x="0" y="53" style="fill:var(--text-2);font-size:12px;font-weight:500">apply A</text>
<line x1="120" y1="60" x2="770" y2="60" style="stroke:var(--line);stroke-width:1"/>
<text x="0" y="103" style="fill:var(--text-2);font-size:12px;font-weight:500">apply B</text>
<line x1="120" y1="110" x2="770" y2="110" style="stroke:var(--line);stroke-width:1"/>
<text x="0" y="153" style="fill:var(--text-2);font-size:12px;font-weight:500">apply C</text>
<line x1="120" y1="160" x2="770" y2="160" style="stroke:var(--line);stroke-width:1"/>
<text x="0" y="203" style="fill:var(--text-2);font-size:12px;font-weight:500">plan</text>
<line x1="120" y1="210" x2="770" y2="210" style="stroke:var(--line);stroke-width:1"/>
<rect x="135.6" y="34" width="577.1999999999999" height="26" rx="2" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.4"/>
<text x="145.6" y="52" style="fill:var(--text);font-size:12px;font-weight:500">holds db: a slow create</text>
<rect x="322.8" y="84" width="93.59999999999997" height="26" rx="2" style="fill:var(--accent-wash);stroke:var(--accent);stroke-width:1.4"/>
<text x="426.4" y="102" style="fill:var(--text-2);font-size:12px">holds web, then releases it</text>
<rect x="229.2" y="134" width="26" height="26" rx="2" style="fill:var(--conflict-wash);stroke:var(--conflict);stroke-width:1.4"/>
<text x="242.2" y="152" text-anchor="middle" style="fill:var(--conflict);font-size:13px;font-weight:700">x</text>
<text x="267.2" y="152" style="fill:var(--text-2);font-size:12px">db is locked: apply rejected, exit 4, nothing changes</text>
<rect x="291.6" y="184" width="62.39999999999998" height="26" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2" stroke-dasharray="4 3"/>
<text x="364.0" y="202" style="fill:var(--text-2);font-size:12px">no lock; writes a refreshed record if the node is free</text>
<line x1="120" y1="232" x2="770" y2="232" style="stroke:var(--text-3);stroke-width:1.2"/>
<polygon points="770,232 764,228.4 764,235.6" style="fill:var(--text-3)"/>
<text x="770" y="247" text-anchor="end" style="fill:var(--text-3);font-size:10.5px">time</text>
<line x1="135.6" y1="228" x2="135.6" y2="236" style="stroke:var(--text-3);stroke-width:1"/>
<line x1="229.2" y1="228" x2="229.2" y2="236" style="stroke:var(--text-3);stroke-width:1"/>
<line x1="322.8" y1="228" x2="322.8" y2="236" style="stroke:var(--text-3);stroke-width:1"/>
<line x1="416.4" y1="228" x2="416.4" y2="236" style="stroke:var(--text-3);stroke-width:1"/>
<line x1="712.8" y1="228" x2="712.8" y2="236" style="stroke:var(--text-3);stroke-width:1"/>
<line x1="229.2" y1="62" x2="229.2" y2="134" style="stroke:var(--conflict);stroke-width:1" stroke-dasharray="2 4"/>
</svg>
:::

The rejection names the node, the command, the process, the host, the holder and the time. It reads `apply rejected: ADDRESS is locked by COMMAND (pid PID on HOST, HOLDER, since TIME)`, followed by `; N other nodes are held as well` when the apply needs more nodes that are held. The smallest address is named. There is no global lock and no queue.

A lock is an object that exists while the command runs. It is created whole, or not at all, and it is released only by the command that holds it: a lock that another command took over is neither renewed nor deleted by the former holder. The command renews it every 2 minutes, so a command that ends sooner renews nothing. A lock whose process has stopped on the same host is reclaimed at once. A lock that has not been renewed for 10 minutes is reclaimed. Of commands that find the same lock left behind, one reclaims it. `pravo state locks` lists the locks and marks the ones that may be reclaimed with `[stale]`. `pravo state unlock ADDRESS` releases one by hand, and `pravo state unlock --stale` releases every stale lock and reservation.

A plan holds the lock of a node for the instant that it writes a refreshed record. When another process holds the lock, the plan leaves the record alone. A plan that refreshes a node another process is applying does not wait for it. With the lock held, the plan reads the record from the state once more, and writes the refreshed record only if it is the one the plan read. When another command wrote it, removed it or created it again in the meantime, the plan leaves it as it is and prints a warning.

### Reservations

`pravo plan --reserve DURATION` takes a **reservation** on every node with a pending change. A reservation is a lock that outlives the command. Another actor cannot plan or apply a reserved node until the reservation expires. The actor who holds it applies the nodes, and the apply ends the reservation.

Change the number again. Two nodes have a pending change.

```cue file=main.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$settings: pravo.#Settings & {refresh: "1h"}

$random: rnd.#Provider
$local:  loc.#Provider

port: $random.#Resource.random_integer & {min: 20000, max: 29997, seed: "web"}

site: $local.#Resource.local_file & {
	$from:    "web"
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}

url: pravo.#Output & {value: "http://localhost:\(port.result)"}
digest: pravo.#Output & {value: site.content_sha256, sensitive: true}
```

```sh run
pravo plan --reserve 10m --actor alice | tail -1
```

```text out
Reserved 2 nodes until <time>.
```

```sh run
pravo state locks
```

```text out
port  reserved by alice until <time>: pravo plan --reserve 10m --actor alice
site  reserved by alice until <time>: pravo plan --reserve 10m --actor alice
```

```sh run exit=4
pravo plan --actor bob
```

```text out
Error: plan rejected: port is reserved by alice (pravo plan --reserve 10m --actor alice until <time>, since <time>)
```

```sh run
pravo apply -auto-approve --actor alice | grep '^Apply complete'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 2 destroyed. [txn <id>]
```

```sh run
pravo state locks
```

```text out
No locks or reservations held.
```

An actor is a label that pravo does not verify. A reservation coordinates the people and agents who tell the truth about who they are. [Constraints and policies](policies.md) says what that means for a rule.

## Outputs

An output is a struct built from `pravo.#Output`. An apply evaluates every output and records it in `outputs.json`. `pravo output` prints them. The first apply of a root creates `outputs.json`, empty when the root declares no output, so that a root that reads these outputs finds it. Later applies write it only when an output changed.

```sh run
pravo output
```

```text out
digest: (sensitive)
url: "http://localhost:29960"
```

An output with `sensitive: true` is hidden where outputs are listed. That is a plan, the summary of an apply, and `pravo output` without a name. The state holds it as `{"$sensitive": VALUE}`. `pravo output NAME` and `--json` print it.

```sh run
jq -c 'map_values(if type == "object" then keys else . end)' pravo.state/outputs.json
```

```text out
{"digest":["$sensitive"],"url":"http://localhost:29960"}
```

`pravo destroy` removes every output. A part of a saved destroy plan removes the outputs that read the resources it destroys and leaves the others; see [Outputs of a partial destroy](../reference/apply.md#outputs-of-a-partial-destroy).

An output is not a copy of the state. Another root can read `outputs.json` alone with `pravo.#RemoteOutputs`, so a reader needs access to that one object and not to the resources. An output that the other root marked sensitive is not given out.

## Backends

The state graph lives in a backend. A backend stores objects by key, reads them, deletes them, lists them, and creates one only if it does not exist. The local backend is a directory. The S3 backend keeps the same keys as objects in a bucket.

| Backend | `$settings.state` | Location |
|---|---|---|
| local | `path: "state/prod"` | A directory, relative to the root. The default is `pravo.state`. A path that names a file is an error. |
| s3 | `backend: "s3"`, `bucket`, and optionally `prefix`, `region`, `endpoint`, `path_style`, `profile`, `access_key`, `secret_key` | Objects in a bucket that must exist, below an optional prefix. |

```cue title="main.cue"
$settings: pravo.#Settings & {state: pravo.#S3State & {
	bucket:     "infra-state"
	prefix:     "teams/platform/app"
	region:     "eu-central-1"
	endpoint:   "https://minio.internal:9000" // an S3-compatible store
	path_style: true
}}
```

The block is typed by backend. Local fields with an S3 backend, S3 fields with the local backend, and `backend: "s3"` without a `bucket` are errors at evaluation. A `bucket` with no `backend` means `s3`. Credentials come from the usual AWS chain: the environment, a profile, or a role. Static `access_key` and `secret_key` fields end up in the specification.

Every property of the local store holds in a bucket. Each node is one object, written on its own. The bucket holds a log of transactions, a lock for each node, the stored plans and, when the state is sealed, its secrets. A lock is an object created with the `If-None-Match: *` conditional write. Two commands on different machines cannot both create it, so the store must honour that header, and `If-Match` on renew, release and reclaim. Stored plans use the same requests: a plan is created with `If-None-Match: *`, and its progress object is replaced and its objects are deleted with `If-Match`. [S3-compatible stores](../reference/s3-stores.md) says how to test a store. The prefix lets several roots share a bucket.

`pravo state pull DIR` copies the configured graph into a local directory. `pravo state push DIR` copies a local directory into the configured backend, objects, transaction log, stored plans and secrets included.

## Cost follows the change

The state keeps one record for each resource, and the record names the resources it depends on. A command does not have to read, plan or lock the estate to find what a change affects. Four parts of the design make the work of a command follow the change and not the number of records.

| Part | What it does | What it spares |
|---|---|---|
| A record and its edges for each resource | A node holds its attributes and the addresses it depends on, and is written on its own. | Reading the whole estate to find what a change affects, and rewriting one file that holds all of it. |
| Reach | A plan starts at the change and follows the edges for as long as a value changes. | Validating, planning, locking and writing the resources that the change does not touch. |
| Refresh age | A plan reads a record from its provider only when it looks at the record and the record is older than its refresh age. | A call to the provider for every other record, and a second call for a record that was read a moment ago. |
| A lock for each node | An apply locks the nodes it writes, and no others. | Waiting for, or being refused by, an apply that works on other resources. |

Reach and the refresh age work together. Reach decides which records a plan looks at: the resources it plans and the resources they read. The refresh age decides whether a record that the plan looks at is read again. A short default age does not make a plan read a resource it does not look at, and reach does not make a plan trust a record that is older than its age.

### In practice

A change to one resource that no other resource reads reads that resource and the resources it reads, takes one lock, writes one record and saves a plan with one change, whether the state holds twenty records or twenty thousand. A change that other resources read reaches them as well, and the work follows the number it reaches. The printed plan is as long as the change. A saved plan stays current while other commands write nodes that it neither changes nor reads.

People and jobs that change different parts of one state apply at the same time. A job that needs a node that another job holds is refused with exit code 4 and is not queued. [A change reads what it reaches](plans.md#a-change-reads-what-it-reaches) shows the reads, as the `Graph:` line counts them, and [Lab 2](../learn/lab2-two-applies-at-once.md) shows applies that do not meet.

An estate therefore does not need cutting into several states to keep the reads, the locks and the writes of a change small. A split also has a price: the edges stop at the boundary of a state. A change in one root does not reach a root that reads its outputs with `pravo.#RemoteOutputs`, so the roots are planned and applied one after the other, in an order that someone keeps.

Two costs follow the size of the estate. pravo evaluates the whole specification on every run, and it opens every record of the state before it plans. A change that reaches every resource, such as a change to a resource that all the others read, costs what the estate costs, and so does creating the estate. `-v` prints the time of each phase: see [Debugging](../how-to/debugging.md#2-ask-what-pravo-decided). A split makes each of these smaller, because each state holds fewer resources.

A few records are read whether or not the change reaches them. A data source is read once its record is older than its refresh age, so with the default age an estate with many data sources reads all of them on every plan, and a refresh age set with `$settings: refresh` keeps a plan from reading them again within that age. A resource that sets `$lifecycle: refresh` is read once its record is older than that age. [Refresh age](plans.md#refresh-age) lists every case.

A plan does not promise more than it read. A record that a plan did not read is trusted. For a record that the plan looked at, the refresh age says for how long. For one that it did not look at, the trust lasts until something reads it. The `Graph:` line counts the records that were not read. `pravo drift` compares them with the infrastructure, and a job that runs `pravo drift --due --limit N` reads the estate in turns. [Plans](plans.md#choosing-a-refresh-age) says how to choose the age.

### When to split

Split a state for ownership, permissions or blast radius.

- **Ownership and permissions.** A state is the unit of access. Whoever may write the bucket may write every record in it, and the records hold the attributes of the resources, secrets included. Teams that must not change each other's resources keep separate states.
- **Blast radius.** A command acts on one state. `pravo destroy` deletes every node in it, and a wrong edit to the specification, or to the credentials of a provider, can reach every node that uses it. A policy can bound the blast radius of a plan; separate states bound what any command can touch.

[Keep the state in a shared store](../how-to/keep-the-state-in-a-shared-store.md) describes the bucket that one state lives in.

## Recap

- The state is a directory or a bucket of independent records. Each has an address, a revision and a lock.
- A revision counts writes. A saved plan records the revision of every node it touches.
- An apply locks every node it writes. A plan holds a lock only while it writes a refreshed record. A reservation is a lock that outlives its plan.
- A transaction records who ran what, and which nodes changed. The log is evidence and prevents nothing.
- A stored plan is three objects below `plans/`. The plan and its summary are written once. The progress object is the only one that changes.
- The cost of a plan, an apply and a lock follows the change. Ownership, permissions and blast radius decide whether to split a state.

Next: [Providers](providers.md) describes the processes behind the records.
