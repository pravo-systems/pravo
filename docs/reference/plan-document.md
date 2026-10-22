---
title: The plan document
description: The JSON document that pravo plan --json prints, the form of a saved plan file, and the events that apply and destroy stream.
order: 51
group: Data and formats
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '"(created|until)": ?"[^"]+"'
    replace: '"$1":"2026-01-01T00:00:00Z"'
  - pattern: '"id": "[a-z]+-[a-z]+"'
    replace: '"id": "<pet>"'
  - pattern: '"source": "[a-z]+-[a-z]+"'
    replace: '"source": "<pet>"'
  - pattern: '"id": "[0-9]+"'
    replace: '"id": "<n>"'
  - pattern: '"result": [0-9]+'
    replace: '"result": 0'
  - pattern: 'the record is [0-9]+[smhd] old'
    replace: 'the record is <age> old'
---

`pravo plan --json` prints one JSON document on standard output. A plan policy is judged against the same document. Progress goes to standard error.

| Field | Present | Meaning |
|---|---|---|
| `format` | always | The layout version of the document. It is `1`. |
| `created` | always | When the document was made: RFC 3339, UTC. |
| `actor`, `reason` | when set | The labels from `--actor` and `--reason`, or `PRAVO_ACTOR` and `PRAVO_REASON`. The caller chooses them. |
| `git_revision` | in a git repository | The short revision of `HEAD`, 12 digits, with `-dirty` appended when the working tree has changes. |
| `trace_id` | when tracing is on | The OpenTelemetry trace of the run. |
| `destroy` | for a destroy plan | `true`, for `plan --destroy` and `destroy`. |
| `summary` | always | Counts of actions and the blast radius. |
| `stats` | always | How much of the graph the plan walked. |
| `changes` | always | One entry for each node the plan ran: a resource or data source it changes, reads, deletes, moves or imports, an ephemeral resource it opens and a function it calls. `--include-skipped` adds an entry for each node it left alone. An empty list, never `null`. A consumer that needs the whole estate reads the counts in `stats`, passes `--include-skipped`, or queries the state with `pravo state query`. |
| `outputs` | always | The outputs by name. A value that is unknown until apply is `{"$unknown": true}`. |
| `hints` | when there are any | The text of each `Hint:` line the plan prints. |
| `providers` | when the root has providers | The resolved version of each provider, by local name. |
| `reservation` | in `pravo show --json` only | The nodes a `--reserve` plan reserved and until when. |
| `stored` | in `pravo plan --store --json`, for a plan that was stored | The `id`, the expiry, the `apply_command` and the plans that overlap. See [Store a plan](plan.md#store-a-plan). |

Work in an empty directory. This root has a managed resource that depends on another, a data source, an ephemeral resource, a function call and an output.

```text file=README.md
readme
```

```cue file=main.cue
package estate

import (
	"pravo.dev/pravo"
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
	k8s "pravo.dev/providers/hashicorp/kubernetes"
)

$random: rnd.#Provider
$local:  loc.#Provider
$k8s:    k8s.#Provider

pet: $random.#Resource.random_pet & {
	length: 2
	keepers: owner: "platform"
}

ticket: $random.#Resource.random_integer & {
	min: 1
	max: 99
	keepers: source: pet.id
}

readme: $local.#Data.local_file & {filename: "README.md"}

secret: $random.#Ephemeral.random_password & {length: 16}

kind: $k8s.#Function.manifest_decode & {args: ["apiVersion: v1\nkind: ConfigMap\nmetadata: {name: demo}"]}

name: pravo.#Output & {value: pet.id}
```

```sh run
pravo init > /dev/null
pravo plan --json 2>/dev/null | jq 'del(.changes)'
```

```text out
{
  "format": 1,
  "created":"2026-01-01T00:00:00Z",
  "summary": {
    "add": 2,
    "change": 0,
    "destroy": 0,
    "read": 1,
    "blast_radius": 2
  },
  "stats": {
    "total": 5,
    "reached": 3,
    "skipped": 0,
    "refreshed": 1,
    "fresh": 0,
    "not_read": 0,
    "drifted": 0,
    "opened": 1,
    "called": 1,
    "imported": 0,
    "moved": 0
  },
  "outputs": {
    "name": {
      "$unknown": true
    }
  },
  "providers": {
    "kubernetes": "3.4.0",
    "local": "2.9.1",
    "random": "3.9.1"
  }
}
```

## summary and stats

`summary` counts actions.

| Field | Counts |
|---|---|
| `add` | Creates and replacements. |
| `change` | Updates. |
| `destroy` | Deletes and replacements. |
| `read` | Data source reads. |
| `blast_radius` | The distinct nodes that have a pending change (a create, update, replacement, delete or deferred read), together with every node that depends on one of them, transitively, as the state records the dependencies at that moment. |

`stats` says how much of the graph the plan walked.

| Field | Counts |
|---|---|
| `total` | The declarations in the specification. |
| `reached` | Declarations planned with the provider. |
| `skipped` | Declarations reused from the state without planning. |
| `removed` | Records of resources that the specification does not declare and that the plan deletes. The field is absent when there are none. The `Graph:` line calls them `removed from the specification`. `refreshed`, `fresh` and `not_read` count them, and `total` does not. |
| `refreshed` | Records read from the provider during the plan. |
| `fresh` | Records that were not read because they were within their [refresh age](glossary.md#refresh-age). The `Graph:` line calls them `within refresh age`. |
| `not_read` | Records that were not read and were older than their refresh age, because the plan did not reach them. The `Graph:` line calls them `not read`. |
| `drifted` | Refreshed records that had changed. |
| `opened` | Ephemeral resources opened. |
| `called` | Function calls made. |
| `imported`, `moved` | Imports and moves carried out by the plan. |

Under `-refresh=false` no record is compared with its age, so none is counted in `fresh`. Under `-refresh=all` every record is read and counted in `refreshed`.

## changes

Every entry has `addr`, `mode`, `type`, `name` and `action`.

| Field | Present | Meaning |
|---|---|---|
| `addr` | always | The address: the CUE path of the declaration, such as `pet` or `servers[0]`. |
| `mode` | always | `managed`, `data`, `ephemeral` or `call`. |
| `type` | always | The provider's type of the resource, or the name of the function. |
| `name` | always | The last element of the path. |
| `action` | always | What the plan does. See [actions](#actions). |
| `provider` | when a provider runs it | The provider instance key, such as `random` or `local.$primary`. |
| `reasons` | when there are any | Why the plan chose the action, in words. The wording is for people. |
| `requires_replace` | for a replacement | The attributes that force it. |
| `before` | by action | The recorded state of the object. |
| `after` | by action | The planned state of the object. |
| `deferred` | when true | A data source whose read waits for the apply. |
| `skipped` | when true | Unchanged, and not planned with the provider. |
| `refreshed` | when true | The record was read from the provider during this plan. |
| `drifted` | when true | The refresh found the object changed. |
| `applied` | when true | Already applied by a staged apply of the plan file. |
| `dependencies` | when there are any | The addresses this change depends on. |
| `dependents` | for a pending change, when there are any | The addresses that depend on it in the state. |
| `warnings` | when there are any | The summaries of the provider's warnings. |

A boolean that is false is left out. A field marked "when there are any" is left out when empty.

A change that creates a resource, and that depends on another:

```sh run
pravo plan --json 2>/dev/null | jq '.changes[] | select(.addr == "ticket")'
```

```text out
{
  "addr": "ticket",
  "mode": "managed",
  "type": "random_integer",
  "name": "ticket",
  "action": "create",
  "provider": "random",
  "reasons": [
    "not in the state: a new resource"
  ],
  "after": {
    "id": {
      "$unknown": true
    },
    "keepers": {
      "source": {
        "$unknown": true
      }
    },
    "max": 99,
    "min": 1,
    "result": {
      "$unknown": true
    },
    "seed": null
  },
  "dependencies": [
    "pet"
  ]
}
```

The values of `after` that the provider computes are `{"$unknown": true}`: here `id` and `result`, and the key `source` of `keepers`, which reads `pet.id`. An attribute that is not set is `null`: here `seed`.

The entries that are not managed resources carry neither `before` nor `after`, except a data source that was read:

```sh run
pravo plan --json 2>/dev/null | jq -c '.changes[] | select(.mode != "managed") | {addr, mode, type, action}'
```

```text out
{"addr":"readme","mode":"data","type":"local_file","action":"read"}
{"addr":"secret","mode":"ephemeral","type":"random_password","action":"open"}
{"addr":"kind","mode":"call","type":"manifest_decode","action":"call"}
```

### Actions

| Action | `before` | `after` | Meaning |
|---|---|---|---|
| `create` | absent | present | The object does not exist yet. |
| `update` | present | present | The object is changed in place. |
| `replace` | present | present | The object is destroyed and created again. `after` may hold unknown values. |
| `delete` | present | absent | The object is destroyed. |
| `read` | absent | present | A data source is read. `after` is absent when the read is deferred to the apply. |
| `no-op` | absent | absent | Nothing changes. |
| `open` | absent | absent | An ephemeral resource is opened for the run. |
| `call` | absent | absent | A provider function is called. |
| `move` | absent | absent | A record is renamed by `$from`. `addr` is the new address, and the ordinary entry for that address follows. |

A `replace` is its own action: it is not a `delete` followed by a `create`.

`open` and `call` entries are not written to a saved plan, because their results are never recorded. Values that pravo reads itself, `pravo.#Env`, `pravo.#SecretEnv` and `pravo.#RemoteOutputs`, produce no entry.

### After an apply

```sh run
pravo apply --auto-approve | grep '^Apply complete'
pravo plan --json 2>/dev/null | jq -c '[.changes[].addr]'
pravo plan --json --include-skipped 2>/dev/null | jq -c '.changes[] | select(.mode == "managed")'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
["readme","secret","kind"]
{"addr":"pet","mode":"managed","type":"random_pet","name":"pet","action":"no-op","skipped":true}
{"addr":"ticket","mode":"managed","type":"random_integer","name":"ticket","action":"no-op","skipped":true}
```

The plan ran the data source, which it reads each time, the ephemeral resource, which it opens each time, and the function call. It left `pet` and `ticket` alone: nothing the provider saw has changed, so the plan did not ask the provider, and neither has an entry. `--include-skipped` lists them as entries with the action `no-op` and `skipped`, and with nothing else that the plan decided. `pravo why` prints the reasons of a node that the plan left alone. A policy is judged against a document that lists every node of the specification this way, so a rule over the `type` or the address of a node reaches it whether or not the plan ran it.

### A replacement

```sh run
sed -i.bak 's/length: 2/length: 3/' main.cue
pravo plan --json 2>/dev/null | jq '.changes[] | select(.addr == "pet")'
```

```text out
{
  "addr": "pet",
  "mode": "managed",
  "type": "random_pet",
  "name": "pet",
  "action": "replace",
  "provider": "random",
  "reasons": [
    "the specification changed since the last apply (hash 8b5b7a59ded5d995 -> cd8f659037768681)",
    "refreshed because the record is older than its refresh age; no drift",
    "the provider cannot change length in place; replacement required"
  ],
  "requires_replace": [
    "length"
  ],
  "before": {
    "id": "<pet>",
    "keepers": {
      "owner": "platform"
    },
    "length": 2,
    "prefix": null,
    "separator": "-"
  },
  "after": {
    "id": {
      "$unknown": true
    },
    "keepers": {
      "owner": "platform"
    },
    "length": 3,
    "prefix": null,
    "separator": "-"
  },
  "refreshed": true,
  "dependents": [
    "ticket"
  ]
}
```

`requires_replace` names the attribute that forces the replacement. `dependents` lists `ticket`, which reads `pet.id`. The entry for `ticket` is a replacement too, because its `keepers` change.

```sh run
pravo plan --json 2>/dev/null | jq -c '{summary, stats}'
```

```text out
{"summary":{"add":2,"change":0,"destroy":2,"read":1,"blast_radius":2},"stats":{"total":5,"reached":3,"skipped":0,"refreshed":3,"fresh":0,"not_read":0,"drifted":0,"opened":1,"called":1,"imported":0,"moved":0}}
```

### A destroy plan

`pravo plan --destroy` sets `destroy`. Every recorded node is a `delete`, a data source included:

```sh run
pravo plan --destroy --json 2>/dev/null | jq -c '{destroy, summary}'
pravo plan --destroy --json 2>/dev/null | jq -c '.changes[] | {addr, mode, action}'
```

```text out
{"destroy":true,"summary":{"add":0,"change":0,"destroy":3,"read":0,"blast_radius":3}}
{"addr":"readme","mode":"data","action":"delete"}
{"addr":"ticket","mode":"managed","action":"delete"}
{"addr":"pet","mode":"managed","action":"delete"}
```

### A hint and a move

Change the length back, and rename the pet. When a plan creates one resource and deletes another of the same type with identical configuration, it adds a hint and changes nothing:

```sh run
sed -i.bak 's/length: 3/length: 2/' main.cue
sed -i.bak 's/^pet:/owl:/;s/pet\.id/owl.id/' main.cue
pravo plan --json 2>/dev/null | jq -c '.hints, .summary'
```

```text out
["owl looks like pet under a new address; to keep the existing object, add $from: \"pet\" to owl"]
{"add":2,"change":0,"destroy":2,"read":1,"blast_radius":3}
```

Name the old address in `$from`, and the plan renames the record. The entry has the action `move`, followed by the ordinary entry for the new address:

```sh run
sed -i.bak 's/random_pet & {/random_pet \& {$from: "pet",/' main.cue
pravo plan --json --include-skipped 2>/dev/null | jq -c '.changes[] | select(.addr == "owl") | {addr, action}'
```

```text out
{"addr":"owl","action":"move"}
{"addr":"owl","action":"no-op"}
```

## Labels and the revision

`--actor` and `--reason` are recorded on the document, on every transaction, lock and reservation, and on saved plans. pravo does not verify them.

```sh run
pravo plan --json --actor ci --reason nightly 2>/dev/null | jq -c '{actor, reason, git_revision}'
```

```text out
{"actor":"ci","reason":"nightly","git_revision":null}
```

`git_revision` is `null` here because the directory is not in a git repository.

## Not masked

The text of a plan hides sensitive attributes. The document does not. Sensitive attributes and the outputs marked sensitive appear in it as they are. Do not write rules over secrets, and do not commit a plan document as a fixture without scrubbing it.

## pravo show --json

`pravo show <planfile> --json` wraps the document of a saved plan in an object.

| Field | Meaning |
|---|---|
| `planfile` | The path as given. |
| `stale` | `null` when the plan can still be applied. Otherwise `{"reasons": [...]}`. |
| `plan` | The document. `created`, `actor`, `reason` and `git_revision` are those of the plan file. It carries `reservation` when the plan reserved its nodes. |

```sh run
sed -i.bak 's/\$from: "pet",//' main.cue
sed -i.bak 's/length: 2/length: 3/' main.cue
pravo plan --reserve 15m --actor ci --out change.plan > /dev/null 2>&1
pravo show change.plan --json | jq -c '{planfile, stale, reservation: .plan.reservation}'
```

```text out
{"planfile":"change.plan","stale":null,"reservation":{"until":"2026-01-01T00:00:00Z","addrs":["owl","ticket"]}}
```

After the specification changes, `stale` says why, and `pravo apply change.plan` is refused with exit code 3:

```sh run exit=3
echo 'extra: 1' >> main.cue
pravo show change.plan --json 2>/dev/null | jq -c .stale
pravo apply change.plan
```

```text out
{"reasons":["the configuration changed since the plan was made (main.cue changed)"]}

Error: saved plan is stale; run "pravo plan" again:
  - the configuration changed since the plan was made (main.cue changed)
```

## The saved plan file

`pravo plan --out FILE` writes the plan as JSON, with mode `0600`. `pravo plan --store` writes the same object into the state, where [State format](state-format.md#plans) describes it, and `pravo plan pull` writes it back to a file. The values of the objects are msgpack inside the JSON, so that unknown values survive. A `--policy` does not enter the file. The file is not signed.

| Key | Contents |
|---|---|
| `format` | The layout version of the file. It is `1`. A file with a higher number is refused: `was written by a newer pravo`. |
| `created`, `command` | When it was made and the command line that made it. |
| `lineage` | The lineage of the state the plan was made against. |
| `config_fingerprint` | A digest of every CUE file of the root. |
| `config_files` | The files that the digest covers, each by its name and the first twelve hexadecimal digits of its own digest. The module file is `cue.mod/module.cue`. A file made before the list was recorded has none. |
| `tags`, `sys_tags` | The `-t` and `-T` values. `pravo apply <planfile>` uses them. |
| `providers` | The provider versions the plan used. |
| `destroy`, `stats` | As in the document. |
| `changes` | The changes of the nodes the plan ran, with the configuration, prior and planned values, private provider data and the revision (`prior_revision`) and the generation (`prior_generation`) of each node when the plan was made. A node that the plan left alone has no entry. A data source whose read returned its record has an entry with `kept` set: the apply does not write it. |
| `inputs`, `input_generations` | The records that the changes read, by address, with the revision and the generation each had when the plan was made. A record that has no generation is not in `input_generations`. |
| `eval_outputs`, `drop_outputs` | The outputs that the apply evaluates, and the recorded outputs that it removes. The others keep the value that is recorded. |
| `outputs`, `unknown_outputs`, `sensitive_outputs` | The outputs known after planning, the ones known only after apply, and the sensitive ones. |
| `actor`, `reason`, `git_revision`, `reservation` | As in the document. |
| `apply_txn` | The transaction of the last apply made from the file. A file whose only changes are records and outputs to write has them left to apply until it has one. |

`pravo apply <planfile>` refuses a plan that is stale: the configuration changed (`config_fingerprint`; the reason names the files from `config_files` that differ, as in `(main.cue changed)`, `(extra.cue added)` or `(old.cue removed)`, and lists at most six), the state has another lineage, a provider has another version, or a node that the plan writes, or a record listed in `inputs`, was written since the plan was made (its revision differs), or was removed and created again (its generation differs, at whatever revision). A node that the plan neither writes nor reads does not count. The exit code is 3. The apply compares the revisions and generations again, with the state itself, once it holds the locks of the nodes it writes, so a write that lands between loading the file and taking the locks is refused the same way.

## Apply events

`pravo apply --json` and `pravo destroy --json` stream one JSON object per line instead of a document. Each event has `time`, `kind` and `phase`, and `addr`, `message`, `id`, `elapsed_ms`, `txn` and `trace_id` when they apply.

| Field | Values |
|---|---|
| `kind` | `refresh`, `read`, `open`, `call`, `create`, `update`, `replace`, `destroy`, `lock`, `note`, `plan`, `apply`. |
| `phase` | `start`, `done`, `skip`, `error`. |

The last line is a summary with the counts, the outputs and the transaction. Remove the extra line, and apply the saved plan as the actor that reserved its nodes:

```sh run
sed -i.bak '$d' main.cue
pravo apply change.plan --json --actor ci 2>/dev/null | jq -c '{kind, addr, phase}'
```

```text out
{"kind":"replace","addr":"owl","phase":"done"}
{"kind":"replace","addr":"ticket","phase":"done"}
{"kind":"apply","addr":null,"phase":"done"}
```

A plain `apply --json` that has changes to make and no `--auto-approve` is an error with exit code 1: there is no prompt to answer.

## See also

- [Plans](../concepts/plans.md) explains what a plan contains.
- [`pravo plan`](plan.md) prints and saves a plan.
- [Writing policies](policies-writing.md) judges a plan document.
