---
title: State format
description: "The files of the state graph: nodes, outputs, transactions, locks and the metadata that identifies a store."
order: 50
group: Data and formats
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}'
    replace: '<txn>'
  - pattern: '"(created|refreshed_at|applied_at|updated_at|since|renewed_at|expires|started|finished)": "[^"]+"'
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
---

pravo records what it manages in a state graph, not a state file. Every resource and data source is a node with its own record and its own revision history. Dependencies are edges between nodes. A node is written on its own, so two commands that write different nodes never conflict.

| Path | Contents |
|---|---|
| `meta.json` | The format number and the lineage of the store. |
| `resources/<addr>.json` | One node for each resource or data source, named by its address. |
| `outputs.json` | The recorded outputs. A sensitive one is `{"$sensitive": value}`. |
| `txn/<id>.json` | One recorded transaction. |
| `locks/<addr>.lock` | A lock or a reservation, present while a command or a reservation holds the node. |

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

## Remote layout

On S3 or a compatible store the keys are the same, under the `prefix` of the settings: `meta.json`, `resources/<addr>.json`, `outputs.json`, `txn/<id>.json` and `locks/<addr>.lock`. `pravo state pull DIR` copies the configured graph into a local directory, and `pravo state push DIR` copies a local directory into the configured backend. Both copy `meta.json`, `outputs.json`, the nodes and the transactions. They do not copy locks.

## See also

- [State](../concepts/state.md) explains revisions, transactions and locks.
- [`pravo state`](state.md) reads and edits the state.
- [Keep the state in a shared bucket](../how-to/remote-state.md) moves a root to S3.
