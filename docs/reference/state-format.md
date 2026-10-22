---
title: State format
description: "The files of the state graph: nodes, outputs, transactions, locks, stored plans and the metadata that identifies a store."
order: 50
group: Data and formats
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: '"(created|refreshed_at|applied_at|updated_at|since|renewed_at|expires|started|finished|stale_since|done_at|updated|at)": "[^"]+"'
    replace: '"$1": "2026-01-01T00:00:00Z"'
  - pattern: '"lineage": "[0-9a-f]+"'
    replace: '"lineage": "<lineage>"'
  - pattern: '"(nonce)": "[0-9a-f]+"'
    replace: '"$1": "<nonce>"'
  - pattern: '"(pid)": [0-9]+'
    replace: '"$1": 1234'
  - pattern: '"(host|user)": "[^"]+"'
    replace: '"$1": "<$1>"'
  - pattern: '"id": "[a-z]+-[a-z]+"'
    replace: '"id": "<pet>"'
  - pattern: '(Hi|Hello), [a-z]+-[a-z]+!'
    replace: '$1, <pet>!'
  - pattern: '"name": "[a-z]+-[a-z]+"'
    replace: '"name": "<pet>"'
  - pattern: '"id":"[0-9a-f]{40}"'
    replace: '"id":"<sha1>"'
  - pattern: '"config_hash": "[0-9a-f]{16}"'
    replace: '"config_hash": "<hash>"'
  - pattern: '"config_fingerprint": "[0-9a-f]{32}"'
    replace: '"config_fingerprint": "<fingerprint>"'
  - pattern: '"size": [0-9]+'
    replace: '"size": 0'
  - pattern: '"by": "[^@"]+@[^#"]+#[0-9]+\.[0-9a-f]+"'
    replace: '"by": "<who>@<host>#<pid>.<token>"'
---

pravo records what it manages in a state graph, not a state file. Every resource and data source is a node with its own record and its own revision history. Dependencies are edges between nodes. A node is written on its own, so two commands that write different nodes never conflict.

| Path | Contents |
|---|---|
| `meta.json` | The format number and the lineage of the store. |
| `resources/<addr>.json` | One node for each resource or data source, named by its address. |
| `outputs.json` | The recorded outputs. A sensitive one is `{"$sensitive": value}`. |
| `txn/<id>.json` | One recorded transaction. |
| `locks/<addr>.lock` | A lock or a reservation, present while a command or a reservation holds the node. |
| `plans/<id>.plan.json`, `plans/<id>.meta.json`, `plans/<id>.progress.json` | A stored plan: the plan, its summary and what was applied from it. See [Plans](#plans). |

The graph lives in a directory, `pravo.state` next to the specification by default. [Settings](spec-settings.md#state) sets another directory or an S3 bucket. A state path that names a file is an error, and pravo changes nothing. Keep the directory on storage that every user of the root can reach. Treat nodes as sensitive: provider attributes can include secrets.

Work in an empty directory.

```cue file=main.cue
package demo

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 2}

servers: [
	$random.#Resource.random_integer & {seed: "fixed", min: 1, max: 99},
]

hello: $local.#Resource.local_file & {
	filename: "hello.txt"
	content:  "Hello, \(pet.id)!"
}

readme: $local.#Data.local_file & {filename: "hello.txt", $depends_on: [hello]}

name: pravo.#Output & {value: pet.id}
```

```sh run
pravo init > /dev/null
pravo apply --auto-approve --actor ci --reason example | grep '^Apply complete'
find pravo.state -type f | sort
```

```text out
Apply complete! Resources: 3 added, 0 changed, 0 destroyed. [txn <id>]
pravo.state/meta.json
pravo.state/outputs.json
pravo.state/resources/hello.json
pravo.state/resources/pet.json
pravo.state/resources/readme.json
pravo.state/resources/servers%005B0%005D.json
pravo.state/txn/<txn>.json
```

Ephemeral resources and function calls are never recorded.

## Addresses in file names

The address of a node is the CUE path of its resource. In a file name, the characters `.`, `_`, `-`, letters and digits stay as they are. Every other character is percent-encoded as `%` and four hexadecimal digits. The address `servers[0]` is stored as `resources/servers%005B0%005D.json`. A data source has no `data.` prefix: the `mode` field of the node says what it is.

Each node is written atomically: pravo writes a temporary file and renames it. A crash never leaves a half-written node.

## meta.json

```sh run
jq . pravo.state/meta.json
```

```text out
{
  "format": 1,
  "lineage": "<lineage>",
  "created": "2026-01-01T00:00:00Z"
}
```

| Field | Meaning |
|---|---|
| `format` | The version of the store layout. It is `1`. |
| `lineage` | An identifier chosen when the store was created. A saved plan records it. |
| `created` | When the store was created. |

pravo refuses a store with a higher format number:

```sh run exit=1
cp pravo.state/meta.json meta.json.bak
echo '{"format":4,"lineage":"abc","created":"2026-01-01T00:00:00Z"}' > pravo.state/meta.json
pravo plan
```

```text out
Error: state at ./pravo.state was written by a newer pravo (format 4)
```

```sh run
cp meta.json.bak pravo.state/meta.json
```

A command that writes the state, such as `plan` or `apply`, creates the directory and `meta.json` when they do not exist. A command that only reads it, such as `eval`, `output` or `pravo state list`, reads an empty state and creates nothing.

## A node

```sh run
jq 'del(.attributes)' pravo.state/resources/hello.json
```

```text out
{
  "addr": "hello",
  "mode": "managed",
  "type": "local_file",
  "name": "hello",
  "provider": "registry.opentofu.org/hashicorp/local",
  "provider_key": "local",
  "provider_version": "2.9.1",
  "schema_version": 0,
  "dependencies": [
    "pet"
  ],
  "config_hash": "<hash>",
  "revision": 1,
  "generation": "<txn>",
  "refreshed_at": "2026-01-01T00:00:00Z",
  "applied_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z",
  "txn": "<txn>"
}
```

| Field | Meaning |
|---|---|
| `addr` | The address. A node written by `pravo state import-tfstate` has no `addr`; its address is `<type>.<name>`, or `data.<type>.<name>` for a data source. |
| `mode`, `type`, `name` | `managed` or `data`, the provider's type, and the last element of the path. |
| `provider` | The fully qualified provider address, including the registry host. |
| `provider_protocol` | How the provider was reached when the node was written. Absent for the default, `tfplugin`. |
| `provider_key` | The provider instance used: the local name when the struct is the only one of its source, and `<local>.<path>` when there are several. |
| `provider_version` | The provider version that last wrote the node. A different installed version re-plans the node. |
| `schema_version` | The provider's schema version for the type, used when the provider upgrades a record. |
| `attributes` | The attributes as the provider returned them. |
| `private` | Opaque provider data, base64-encoded. Absent when empty. |
| `dependencies` | The addresses this node depended on when it was applied: its outgoing edges. |
| `config_hash` | A digest of the configuration value the provider last saw. A plan skips a node when the hash is unchanged. |
| `provider_config` | A digest of the configuration of the provider instance that last wrote the node, left out of the node when the instance has no attribute that says where it points. A plan reads a node whose digest differs from the one of its provider now. See [Plans](../concepts/plans.md#records-a-plan-reads-whatever-their-age). |
| `revision` | Incremented on every write of the node that changes it. |
| `generation` | The transaction that created the record. A node that is removed and created again starts at revision 1 in another generation. A plan is made against a revision and a generation. Absent in a node written before generations were recorded; the next write adds it. |
| `drifted_at` | When a read found that the object differs from the record, or that it is gone. Absent when no read has. A node with it is planned by every plan, saved plan and apply until an apply writes the node. Setting it moves the revision. |
| `gone` | `true` when the read that left the mark did not find the object. The record keeps the attributes of the last state that was read. A later read that finds the object clears it. Absent otherwise. |
| `refreshed_at` | When the provider last reported the real state of the node, by reading or applying it. The refresh age is compared against it. A node without it is read by the next plan whatever its age. |
| `applied_at` | When pravo last created or updated the node. |
| `updated_at` | When the record was last written. |
| `txn` | The transaction that last wrote the node. |

A refresh that finds nothing changed keeps the revision, so looking at the real world does not make a saved plan stale.

```sh run
jq -c '.attributes | {id, filename, content}' pravo.state/resources/hello.json
```

```text out
{"id":"<sha1>","filename":"hello.txt","content":"Hello, <pet>!"}
```

## outputs.json

```sh run
jq . pravo.state/outputs.json
```

```text out
{
  "name": "<pet>"
}
```

A sensitive output is recorded as `{"$sensitive": value}`. `pravo state import-tfstate` records an output that the OpenTofu state marks sensitive in the same form. `pravo.#RemoteOutputs` reads only this object, and leaves the sensitive ones out.

## Transactions

Every `plan` that reads providers or moves records, and every `apply`, `destroy`, `import` and state edit, is recorded as a transaction in `txn/`.

```sh run
jq . pravo.state/txn/*.json
```

```text out
{
  "id": "<txn>",
  "kind": "apply",
  "command": "pravo apply --auto-approve --actor ci --reason example",
  "user": "<user>",
  "host": "<host>",
  "started": "2026-01-01T00:00:00Z",
  "finished": "2026-01-01T00:00:00Z",
  "status": "applied",
  "summary": "3 added, 0 changed, 0 destroyed",
  "changes": [
    {
      "addr": "hello",
      "action": "create",
      "after_revision": 1
    },
    {
      "addr": "pet",
      "action": "create",
      "after_revision": 1
    },
    {
      "addr": "readme",
      "action": "read",
      "after_revision": 1
    },
    {
      "addr": "servers[0]",
      "action": "create",
      "after_revision": 1
    }
  ],
  "actor": "ci",
  "reason": "example"
}
```

| Field | Meaning |
|---|---|
| `id` | A UTC timestamp, a hyphen and six hexadecimal digits. |
| `kind` | `plan`, `apply`, `destroy`, `import` or `edit`. |
| `command` | The command line as it was run, from the word `pravo`, flags included. |
| `user`, `host` | The operating-system user and the host. |
| `started`, `finished` | When it began and ended. |
| `status` | `planned`, `applied`, `rejected`, `failed`. `running` while an apply holds its locks; the file of a command that was killed keeps it, and `pravo state log` shows it as `interrupted`. |
| `summary` | The counts, in words. |
| `changes` | Each node the transaction touched: `addr`, `action`, and the revisions before and after. The action `unsettled` marks a node that an apply stopped on while a provider was changing it. |
| `actor`, `reason`, `git_revision`, `trace_id` | The labels of the run, when set. |

The command line is evidence of what was run. It is written by the process that ran, in a log that is as writable as the state itself, and it prevents nothing. An apply that left out `--policy` is an entry without it, not a refusal. A plan that touches no record is not recorded. An apply, a rejection and a failure are. The `txn/` directory can be pruned at any time.

## Locks and reservations

An apply locks the nodes its plan writes, all of them before it changes anything, and releases them when it finishes. A lock is a file created with an exclusive create. On S3 it is an object created with a conditional write (`If-None-Match: *`), so two commands on different machines cannot both take it.

```sh run
sed -i.bak 's/Hello, /Hi, /' main.cue
pravo plan --reserve 15m --actor ci > /dev/null 2>&1
ls pravo.state/locks
jq . pravo.state/locks/hello.lock
```

```text out
hello.lock
readme.lock
{
  "addr": "hello",
  "pid": 1234,
  "host": "<host>",
  "user": "<user>",
  "command": "pravo plan --reserve 15m --actor ci",
  "nonce": "<nonce>",
  "actor": "ci",
  "since": "2026-01-01T00:00:00Z",
  "kind": "reservation",
  "expires": "2026-01-01T00:00:00Z",
  "renewed_at": "2026-01-01T00:00:00Z"
}
```

| Field | Meaning |
|---|---|
| `addr`, `pid`, `host`, `user`, `command`, `nonce` | Who holds it. The `nonce` identifies the process across hosts. |
| `actor` | The `--actor` label, when set. |
| `since`, `renewed_at` | When it was taken, and when its holder last renewed it. |
| `kind` | `lock` for a running command. `reservation` for a node reserved by `plan --reserve`. |
| `expires` | The end of a reservation. |

A running command renews its locks every 2 minutes, so a command that ends sooner renews nothing. A lock that is not renewed for 10 minutes is treated as left behind by a crashed command and reclaimed. On the same host, a lock whose process is gone is reclaimed at once. A reservation needs no process: only its expiry ends it. A lock file is created whole: it is never empty or cut short while its holder writes it. Of commands that reclaim the same lock, one does, and a command whose lock was reclaimed neither renews nor deletes the lock that replaced it. `pravo state locks` lists the files and marks the ones that may be reclaimed with `[stale]`, `pravo state unlock` removes one, and `pravo state unlock --stale` removes them all.

There is no global lock and no queue. An apply that needs a node another process holds is rejected before it starts, with exit code 4. Before it is rejected it gives back the locks it took, and looks for a short time at a lock whose command has not begun to apply, because that command may be taking locks of its own and give them up. A plan takes a node's lock only for the instant it writes a refreshed record. It leaves the record alone if another process holds the lock, or if the record is not the one the plan read.

On a store that is not on this machine, the lock files rely on conditional requests: `If-None-Match: *` to create a lock, and `If-Match` on the entity tag to renew, release or reclaim one. A store that ignores them lets two commands hold or reclaim one lock.

## Plans

A stored plan is three objects. `pravo plan --store` writes the first two, and the third appears when an apply or a collection first touches the plan. The id is `plan-` and the first eight hexadecimal digits of the SHA-256 digest of the plan object. The id has more digits when two different plans share the first eight.

| Object | Written | Contents |
|---|---|---|
| `plans/<id>.plan.json` | Once, with a conditional create. Never changed. | The plan, in the form of a [saved plan file](plan-document.md#the-saved-plan-file). |
| `plans/<id>.meta.json` | Once, after the plan. Never changed. | The summary that a listing reads. |
| `plans/<id>.progress.json` | At the first change to it, then replaced by a conditional replace. | What was applied from the plan, and who applies or removes it. |

A plan without a summary is a write that did not finish. A listing does not show it, and a collection removes its objects after an hour. A summary that exists and cannot be read, because a newer pravo wrote it or the object is damaged, is not that: the listing leaves the plan out, and a collection leaves it alone. A plan that nothing was done with has no progress object.

Change the specification and store a plan as `ci`. The reservation from the section above belongs to `ci`, so the plan is allowed.

```sh run
pravo plan --store --actor ci --reason example > /dev/null
ls pravo.state/plans
```

```text out
plan-<id>.meta.json
plan-<id>.plan.json
```

### The summary

```sh run
jq . pravo.state/plans/plan-*.meta.json
```

```text out
{
  "format": 1,
  "id": "plan-<id>",
  "created": "2026-01-01T00:00:00Z",
  "expires": "2026-01-01T00:00:00Z",
  "author": "ci",
  "actor": "ci",
  "reason": "example",
  "command": "pravo plan --store --actor ci --reason example",
  "lineage": "<lineage>",
  "config_fingerprint": "<fingerprint>",
  "summary": {
    "add": 1,
    "change": 0,
    "destroy": 1,
    "read": 1
  },
  "changes": [
    {
      "addr": "hello",
      "mode": "managed",
      "type": "local_file",
      "action": "replace"
    },
    {
      "addr": "readme",
      "mode": "data",
      "type": "local_file",
      "action": "read"
    }
  ],
  "bases": {
    "hello": {
      "revision": 1,
      "generation": "<txn>"
    },
    "pet": {
      "revision": 1,
      "generation": "<txn>",
      "reads": true
    },
    "readme": {
      "revision": 1,
      "generation": "<txn>"
    }
  },
  "needs": {
    "readme": [
      "hello"
    ]
  },
  "size": 0
}
```

| Field | Meaning |
|---|---|
| `format` | The layout of the summary and the progress object. It is `1`. |
| `id` | The id of the plan. |
| `created`, `expires` | When the plan was made, and when an unapplied plan is collected: `created` plus the `--ttl`, seven days by default. |
| `author` | The `--actor` label, or the operating system user when there is none. It is a label that nobody verifies. |
| `actor`, `reason`, `git_revision` | The labels of the run, when set. |
| `command` | The command line that made the plan. |
| `tags`, `sys_tags` | The `-t` and `-T` of that command. The commands that show or apply the plan need the same. |
| `lineage` | The lineage of the state the plan was made against. |
| `config_fingerprint` | A digest of the files the plan was made from. |
| `destroy` | `true` for a destroy plan. |
| `summary` | The counts `add`, `change` and `destroy`, and `read`. A replacement counts under `add` and `destroy`. The reads are counted as `plan --json` counts them. |
| `changes` | What the plan does, one entry for each resource that it creates, changes, replaces, destroys, imports or moves, and for each read that is left to the apply: `addr`, `mode`, `type` and `action`. Sorted by address. |
| `bases` | The records the plan expects, by address: the `revision` and the `generation` of the record when the plan was made, and `reads` for a record that the plan reads and does not write. A listing compares them with the state, and nothing else, to tell where the plan stands. A moved record of a change that is not applied yet makes that change conflict, and a moved record with `reads` makes the whole plan stale. |
| `needs` | For a change, the other changes of the plan that an apply of it has to apply with it, by address, sorted. These are its dependencies that are not applied yet, a data source whose read returned something other than its record among them. For a delete they are the deletes that depend on it. It is the one-step relation by which `--only` closes a selection: a change that is applied needs nothing, and so cuts a chain. Only a change that an apply can select has an entry, and a change that needs nothing has none. A new summary has the field at least as `{}`. A listing uses it to tell which changes conflict because one they need conflicts, and which are live. A summary without the field says nothing about the dependencies, and the plan is judged as a whole. |
| `reservation` | The nodes the plan reserved and until when, when it was made with `--reserve`. |
| `size` | The size of the plan object in bytes. |

### The plan

The plan object is what `plan --out` writes. `pravo plan pull` writes it to a file, and `pravo show` reads it.

```sh run
jq -r 'keys | join(" ")' pravo.state/plans/plan-*.plan.json
```

```text out
actor changes command config_files config_fingerprint created format input_generations inputs lineage outputs providers reason stats
```

### Progress

Store a second plan, apply the first one and collect. The apply writes the progress object of the first plan. The second plan is stale, and the collection marks it.

```sh run
pravo plan --store --actor ci --reason second > /dev/null
first=$(pravo plan list --json | jq -r '.plans[-1].id')
second=$(pravo plan list --json | jq -r '.plans[0].id')
pravo apply $first --actor ci | grep '^Apply complete'
pravo plan gc > /dev/null
jq . pravo.state/plans/$first.progress.json pravo.state/plans/$second.progress.json
```

```text out
Apply complete! Resources: 1 added, 0 changed, 1 destroyed. [txn <id>]
{
  "format": 1,
  "id": "plan-<id>",
  "applied": {
    "hello": "<txn>",
    "readme": "<txn>"
  },
  "apply_txn": "<txn>",
  "done_at": "2026-01-01T00:00:00Z",
  "updated": "2026-01-01T00:00:00Z"
}
{
  "format": 1,
  "id": "plan-<id>",
  "stale_since": "2026-01-01T00:00:00Z",
  "updated": "2026-01-01T00:00:00Z"
}
```

| Field | Meaning |
|---|---|
| `format`, `id` | As in the summary. |
| `applied` | The address of each change applied from the plan, with the transaction that applied it. An apply that failed or was interrupted adds the changes it finished, and not the one that failed. |
| `apply_txn` | The transaction of the last apply that ended without an error. A failed apply leaves it as it was. |
| `applying` | A list of claims, one for each apply from the plan that runs, so that no collection removes the plan. Each has `by`, who runs the apply, and `at`, when it began. `by` is `WHO@HOST#PID.TOKEN`: the actor or the user of the machine, the host, the process and a token that tells it from another process with the same number. Applies of separate parts of one plan, with `--only`, run at the same time. Each holds its own claim and ends only its own. A running apply renews its `at` every 30 minutes, so an apply that runs for days keeps its claim. A claim that a killed apply left does not count after 24 hours. |
| `collecting` | `by` and `at` of the collection that is about to remove the plan, in the form of `applying`. An apply that starts meanwhile is refused with exit code 3. A claim older than 10 minutes does not count, unless `committed` is `true`. |
| `collecting.committed` | Set by a collection in a conditional write, just before it deletes: it succeeds only while the claim is that collection's and no apply runs. An apply is refused from then on, whatever its clock says, and the claim does not lapse for applies. Another collection finishes the removal when the claim has not been renewed for 10 minutes. |
| `stale_since` | When a collection first saw the plan stale. A collection that runs more than an hour later removes the plan. A collection clears the field when it finds that the plan is not stale any more. |
| `done_at` | When every change that was pending has been applied, by whichever apply finished last. A collection removes the plan 24 hours later. A failed apply does not set it, so the outputs and the records that the plan only writes are left to the apply that finishes. |
| `updated` | When the object was last written. |

An apply writes the changes it finished into `applied` when it ends. When it ends with an error or is interrupted, it writes the changes that completed before the error: the plan is `partial`, and the next apply of the plan does what is left. A change that failed, one whose result is not known and one that was not started are not written. Only an apply that ends without an error sets `apply_txn` and, when nothing is left, `done_at`. Recording progress leaves the claims as they are.

Every write replaces the object only if it is still what the writer read. A command that lost the race reads the object again and retries. A progress object that cannot be parsed keeps the plan from being listed and applied. A collection makes it empty once the plan is older than an hour, and the plan can be listed and applied again.

An apply holds a claim while it runs. The root `slow` has one file that is a named pipe. Writing to a pipe waits until something reads it, so the apply waits and its claim can be read.

```cue file=slow/main.cue
package slow

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

pipe: $local.#Resource.local_file & {filename: "pipe", content: "slow"}
```

```sh run
pravo -C slow init > /dev/null
mkfifo slow/pipe
pravo -C slow plan --store --actor ci > /dev/null
id=$(pravo -C slow plan list --json | jq -r '.plans[0].id')
pravo -C slow apply $id > /dev/null &
until jq -e '.applying' slow/pravo.state/plans/$id.progress.json > /dev/null 2>&1; do sleep 0.1; done
jq '.applying' slow/pravo.state/plans/$id.progress.json
cat slow/pipe > /dev/null
wait
jq '.applying' slow/pravo.state/plans/$id.progress.json
```

```text out
[
  {
    "by": "<who>@<host>#<pid>.<token>",
    "at": "2026-01-01T00:00:00Z"
  }
]
null
```

## Remote layout

On S3 or a compatible store the keys are the same, under the `prefix` of the settings: `meta.json`, `resources/<addr>.json`, `outputs.json`, `txn/<id>.json`, `locks/<addr>.lock` and, for a stored plan, `plans/<id>.plan.json`, `plans/<id>.meta.json` and `plans/<id>.progress.json`. `pravo state pull DIR` copies the configured graph into a local directory, and `pravo state push DIR` copies a local directory into the configured backend. Both copy `meta.json`, `outputs.json`, the nodes, the transactions and the stored plans, with the claims on them. They do not copy locks.

## See also

- [State](../concepts/state.md) explains revisions, transactions and locks.
- [Plans](../concepts/plans.md#stored-plans) explains the life of a stored plan.
- [`pravo state`](state.md) reads and edits the state.
- [Keep the state in a shared bucket](../how-to/remote-state.md) moves a root to S3.
