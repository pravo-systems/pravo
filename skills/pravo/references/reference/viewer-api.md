<!-- Generated from docs/reference/viewer-api.md by tools/skillgen. Do not edit. -->

# The API of the viewer

The read-only JSON endpoints behind the viewer, for a script: the graph, one node, the stored plans, a plan over the graph, the discoveries, the policies, the studio, the recorded runs and the event stream.

The page of [`pravo serve`](serve.md) reads a set of endpoints, and a script can read the same ones. Every answer is JSON, every `/api/` request needs the token that the command prints, and nothing but the plan file that the studio takes changes anything on the server. The endpoints follow the version of pravo: a response carries `version`, and a layout change raises it. Every response has `Cache-Control: no-store`.

## Endpoints

| Path | Response |
|---|---|
| `GET /` and `/assets/...` | The page, with no data. |
| `GET /api/ping` | `{"ok":true,"version":1}`. |
| `GET /api/graph` | The nodes, edges, groups, outputs, the project, the plan and the warnings. No attribute values. |
| `GET /api/detail?addr=ADDR` | One node with its attributes, dependencies, the plan's change, history, lock and times. With `&plan=ID` the plan is that stored plan, and with `&plan=none` there is none. For an output the one attribute is `value`, which is withheld when the output is sensitive or its value contains a sensitive string, and `description` when the output has one. For a node with no record, the field `schema` lists the attributes of its type, with their types, kinds and descriptions and no values. An ephemeral resource has the `attrs` that the specification sets, withheld by the same rules as a record, and an attribute that is not known before a command runs has `unknown`. An environment variable has the `attrs` `name`, `default` and `value`: the value is that of the variable in the environment of the `pravo serve` process, or the default, and it is withheld when the name matches the rule of the name and always for `pravo.#SecretEnv`. |
| `GET /api/source?addr=ADDR` | The lines that write the node, and everything that shapes it. `&plan=ID` marks the lines that stored plan changes. |
| `GET /api/plans` | The plans stored in the state, newest first, as a list of stored plans. The free text of a plan is not in it. |
| `GET /api/plan?id=ID` | A stored plan laid over the graph: the plan, the action of each node that it ran, and the `reason`, `command` and `git_revision` that the author wrote with it. In a sealed state `plan.signatures` has `items` (`status` of `verified`, `key_not_in_file` or `invalid`, with `signer` or `claimed`, and `at`) and a `note` when they could not be checked; no item and no note is a plan with none. |
| `GET /api/search?q=TEXT` | The nodes and groups that match. |
| `GET /api/fields?q=PATH=VALUE` | The addresses of the nodes whose recorded values match, as `{"version":1,"addrs":[...],"indexed":N,"withheld":M}`. `PATH` is dotted (`settings.region`, `rule.0.cidr`), a name alone is a field of that name at any depth, and `*` stands for any run in the path or the value; with no `=VALUE` it matches the nodes that hold the field. Only what `/api/detail` sends is searched, so a withheld value never matches. `indexed` is the number of nodes searched, `withheld` the number whose values are withheld whole. The index is built on the first search and kept for the revision. |
| `GET /api/access` | How the state is protected, the readers of the store and of the readers file, the verdict between them, who the viewer is, and the suggested commands. See [The State section](viewer-sections.md#the-state-section). |
| `GET /api/discovery` | The discoveries of the specification and what their last runs found, set against the state: for each its scope, its freshness, its last run, the counts by status, the objects that are not managed (at most 2000), its `$ack` entries and the commands. See [The Discovery section](viewer-sections.md#the-discovery-section). |
| `GET /api/adopt?discovery=NAME&key=KEY` | The resource that adopts one object of the last run of a discovery, as CUE, and the commands to copy. `KEY` is the `key` of the object in `/api/discovery`. 404 for an object that is not in the run or is managed, 400 for a name or key that is too long. The server asks no provider. |
| `GET /api/policies` | The policies of the state and of the specification: for each its status, the file and line that write it, and its versions newest first with the text, the digest, the reference, the time, the signature and signer (when a readers file is there), and the transactions that the version judged (at most 8 each). `in_force` and `pending` count the policies. |
| `GET /api/studio` | What the studio starts from: the plans it can try (stored plans and plan files, each with its `ref`), the state as something a rule can be tried on (`state`, with the count of its resources and the `ref` `state`), the policies of the specification and of the state, the schema of the plan document as CUE and the examples, each as a policy to start from. `spec_ok` is false when the specification does not load, and then no rule can be tried. |
| `GET /api/studio/try?rule=TEXT&ref=PLAN...` | The verdict of every policy of a text, in the form of a specification, on each plan named by `ref` (a stored plan id, `file:PATH` of a file that `/api/studio` listed, or `state` for the state taken as one plan that updates every resource), at most 100: `holds`, `refused` with its issues (each with the policy and the line of the text), `not-judged`, `sealed`, `unreadable` or `invalid`. `policies` lists the policies that the text declares, and `check` says why the text cannot be stored as it is: its `stage` is `parse`, `imports`, `policies`, `compile`, `empty` or `shape`. 400 for a text over 32 KiB. Writes nothing. |
| `POST /api/studio/plan?name=NAME` | The body is a plan file, sent as `application/octet-stream`, up to 64 MiB. The answer is its entry for the list of plans, with a `ref` that starts with `upload:` and the kind `upload`. The file is held in memory with the last eight, is listed by `GET /api/studio` and is judged by `GET /api/studio/try` like any plan file. A body that is not a plan file gets 400. |
| `GET /api/studio/doc?ref=PLAN&i=N` | The document of a plan as a rule sees it, or the change `N` of it, masked. |
| `GET /api/traces` | The runs that pravo recorded in `.pravo/traces` or `$PRAVO_TRACE_DIR`, newest first, at most 200: for each its `id` (the trace id, a hyphen and the id of its first root span), the command, when it started, how long it took, the exit code, `status` (`ok`, or `error` when a span failed), the counts of spans, failed spans and resources, the transaction it wrote and the actor and reason. `unreadable` counts the files that are not a run of this version. |
| `GET /api/trace?id=ID` | The spans of one run: for each its id and parent, its name, its `kind` (`command`, `engine`, `resource`, `provider`, `mcp` or `other`), its status and message, when it started and how long it took in microseconds from the start of the run, its attributes and its events. At most 30,000 spans, and `omitted` counts the rest. 400 for an id that is not of that form, 404 for a run that is not there, 422 for a file that is not a run. The id names a file and is never a path. |
| `GET /api/events` | A stream of server-sent events. Each event `change` carries the revision and what changed: `state`, `spec`, `plan`, `locks`, `plans`, `access`, `discovery` or `traces`. `plan` is the plan file of `--plan`, `plans` are the plans stored in the state, `access` is the readers file, `discovery` is the runs of the discoveries, and `traces` is the directory of the recorded runs: a run that lands changes no node, so its event carries the revision that the page holds. |

`ADDR` is URL-encoded. An address that is not known gets 404 and `{"error":"..."}`. An `id` or `plan` that is not `plan-` and six or more hexadecimal digits gets 400, and an id that no stored plan has gets 404. The server looks an id up among the plans that it lists. It is never a path, and the beginning of an id is not taken for it. The JSON of the graph names a group `namespace` in its fields, so a reader written for an earlier layout reads it as before.

## Examples

Work in an empty directory named `demo`.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 2}

config: $local.#Resource.local_file & {
	filename: "server.env"
	content:  "NAME=\(pet.id)\n"
}
```

```sh
pravo init > /dev/null
pravo apply --auto-approve | grep '^Apply complete'
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

### Read the interface from a script

A script starts the viewer with `--json`, reads the address from the first line and stops the viewer with a signal. Every request to `/api/` needs the token. The first request here has none, the second has the token as a bearer token, and the third names a host that the server does not own.

```sh
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -o /dev/null -w "no token: %{http_code}\n" "$base/api/ping"
curl -s -H "Authorization: Bearer $token" "$base/api/ping"; echo
curl -s -o /dev/null -w "other host: %{http_code}\n" -H "Host: example.com" -H "Authorization: Bearer $token" "$base/api/ping"
kill $pid
```

Output:

```text
no token: 401
{"ok":true,"version":1}
other host: 421
```

### Read the stored plans

The viewer lists the plans that `pravo plan --store` wrote into the state. Change the specification, so that the plan replaces `pet` and `config`, and store it as `alice`.

File `main.cue`:

```cue
package demo

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

pet: $random.#Resource.random_pet & {length: 3}

config: $local.#Resource.local_file & {
	filename: "server.env"
	content:  "NAME=\(pet.id)\n"
}
```

```sh
pravo plan --store --actor alice --reason "longer pet name" > /dev/null
```

`GET /api/plans` lists the plan. The page reads the same answer.

```sh
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -H "Authorization: Bearer $token" "$base/api/plans" | jq -c '.plans[0] | {id, status, open, author, summary, planned, touches, apply}'
kill $pid
```

Output:

```text
{"id":"plan-<id>","status":"pending","open":true,"author":"alice","summary":{"add":2,"change":0,"destroy":2,"read":0},"planned":2,"touches":[{"addr":"config","action":"replace"},{"addr":"pet","action":"replace"}],"apply":"pravo apply plan-<id>"}
```

| Field of a stored plan | Meaning |
|---|---|
| `id`, `status`, `open` | The id, `pending`, `partial`, `partly-stale`, `stale`, `applied` or `expired`, and whether the plan can be applied, all of it or a part: `pending`, `partial` and `partly-stale`. |
| `author`, `created`, `expires` | Who made the plan, cut at 4 KiB, when, and when an unapplied plan is collected. The list holds no reason, command or revision. |
| `destroy` | `true` for a destroy plan. |
| `summary` | The counts `add`, `change`, `destroy` and `read`. A replacement counts as an add and a destroy. A move, and an import that has nothing else to change, are in `touches` and in no count: the page counts them from `touches`. |
| `planned`, `applied`, `applying` | The number of changes, how many were applied already, and whether an apply from the plan runs now. |
| `reasons`, `reasons_omitted` | Why a stale plan cannot be applied: twenty reasons at most, each cut at 300 characters. `reasons_omitted` counts the others. A partly stale plan has none. |
| `conflicts`, `conflicts_omitted`, `unapplied` | For a partly stale plan: the changes that cannot be applied now, each with its `addr` and its `reasons`, twenty at most, sorted by address. `conflicts_omitted` counts the others, and `unapplied` is the number of changes the plan has left, the conflicts included. |
| `apply_only`, `apply_only_omitted`, `apply_live` | For a partly stale plan: the addresses of the part that can be applied, sorted, up to 2000, with `apply_only_omitted` for the rest. The part is a closed selection for `pravo apply --only`. `apply_live` is the command that applies it, `apply` followed by `--only a,b,c`. It is absent when the part has no change, and when the command would be longer than 16 KiB. |
| `touches`, `truncated` | The resources the plan changes, each with its `addr`, its `action` (`no-op` is a change that only imports an object), `applied` when the plan has applied that change, and `conflict` when a partly stale plan cannot apply it, up to 2000. `truncated` counts the rest. |
| `overlaps` | The other plans that can be applied and have a change left on a resource that this plan has a change left on, in both directions: `id`, `count`, the first twenty addresses in `addrs`, and `truncated` for the rest. A change that a plan has applied does not count, and neither does a change that a partly stale plan cannot apply. |
| `apply` | The command that applies what is left of the plan, `pravo apply ID`, with `-t key=<value>` for each tag the plan was made with and `-T` when it was made with it. It carries the keys of the tags and never the values, so that a line pasted as it is fails in the shell until the values are put in place of `<value>`. It names no state. A partly stale plan is refused by it: `apply_live` is the command for that plan. |

`GET /api/plan?id=ID` lays a stored plan over the graph, as `--plan FILE` does for a plan file: the plan, the action of each node that it ran, and the `reason`, `command` and `git_revision` that the author wrote with it, which the list does not carry. `/api/detail` and `/api/source` take `plan=ID` for the same plan, and `plan=none` for no plan at all. An id that does not have the form of a plan id is 400. An id that no stored plan has is 404, and so is the beginning of an id: the server wants the whole id.

```sh
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
id=$(curl -s -H "Authorization: Bearer $token" "$base/api/plans" | jq -r '.plans[0].id')
curl -s -H "Authorization: Bearer $token" "$base/api/plan?id=$id" | jq -c '{plan: (.plan | {id, status, planned, total}), reason, command, nodes}'
curl -s -H "Authorization: Bearer $token" "$base/api/detail?addr=pet&plan=$id" | jq -c '.change | {action}'
curl -s -H "Authorization: Bearer $token" "$base/api/detail?addr=pet&plan=none" | jq -c '.change'
curl -s -o /dev/null -w "malformed: %{http_code}\n" -H "Authorization: Bearer $token" "$base/api/plan?id=x"
curl -s -o /dev/null -w "absent: %{http_code}\n" -H "Authorization: Bearer $token" "$base/api/plan?id=plan-000000"
curl -s -o /dev/null -w "beginning of an id: %{http_code}\n" -H "Authorization: Bearer $token" "$base/api/plan?id=${id%??}"
kill $pid
```

Output:

```text
{"plan":{"id":"plan-<id>","status":"pending","planned":2,"total":2},"reason":"longer pet name","command":"pravo plan --store --actor alice --reason longer pet name","nodes":[{"addr":"config","action":"replace"},{"addr":"pet","action":"replace"}]}
{"action":"replace"}
null
malformed: 400
absent: 404
beginning of an id: 404
```

The graph carries the plans as well. `Node.plans` lists the ids of the plans that can be applied and still have a change to apply on the node, newest first, and `Node.overlap` is set when two or more can apply that change. A plan that is stale, applied or expired is not on a node, and neither is a plan whose change on that node was applied already. A partly stale plan is on the node of a change that it cannot apply, and it does not count for `overlap`. The lists of `overlaps` and the nodes agree. In `/api/plan`, `plan.stale` is not set for a partly stale plan.

### Read a partly stale plan

A stored plan is partly stale when another command moved the record of some of its changes and the others can still be applied. The root `moved` has three files that do not depend on each other. Alice stores a plan that replaces them, and a plain apply replaces `a`.

File `moved/main.cue`:

```cue
package moved

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

a: $local.#Resource.local_file & {filename: "a.txt", content: "a"}
b: $local.#Resource.local_file & {filename: "b.txt", content: "b"}
c: $local.#Resource.local_file & {filename: "c.txt", content: "c"}
```

```sh
pravo -C moved init > /dev/null
pravo -C moved apply --auto-approve > /dev/null
pravo -C moved plan --store --replace a,b,c --actor alice > /dev/null
pravo -C moved apply --auto-approve --replace a > /dev/null
```

`GET /api/plans` gives the conflicts, the part that can be applied and the command for it. `GET /api/plan?id=ID` does not call the plan stale, and the node `a` still lists the plan.

```sh
rm -f serve.json
pravo -C moved serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -H "Authorization: Bearer $token" "$base/api/plans" | jq -c '.plans[0] | {status, open, reasons, unapplied, conflicts, apply_only, apply_live, touches}'
id=$(curl -s -H "Authorization: Bearer $token" "$base/api/plans" | jq -r '.plans[0].id')
curl -s -H "Authorization: Bearer $token" "$base/api/plan?id=$id" | jq -c '.plan | {status, stale}'
curl -s -H "Authorization: Bearer $token" "$base/api/graph" | jq -c '.nodes[] | {addr, plans, overlap}'
kill $pid
```

Output:

```text
{"status":"partly-stale","open":true,"reasons":null,"unapplied":3,"conflicts":[{"addr":"a","reasons":["a changed in state (revision 2, plan expected 1)"]}],"apply_only":["b","c"],"apply_live":"pravo apply plan-<id> --only b,c","touches":[{"addr":"a","action":"replace","conflict":true},{"addr":"b","action":"replace"},{"addr":"c","action":"replace"}]}
{"status":"partly-stale","stale":null}
{"addr":"a","plans":["plan-<id>"],"overlap":null}
{"addr":"b","plans":["plan-<id>"],"overlap":null}
{"addr":"c","plans":["plan-<id>"],"overlap":null}
```

### Start the viewer with a stored plan

`--plan` takes the id of a stored plan in place of a file. The viewer then lays that plan over the graph when it opens, and `/api/graph` carries it.

```sh
rm -f serve.json
id=$(pravo plan list --json | jq -r '.plans[0].id')
pravo serve --no-open --json --plan "$id" > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -H "Authorization: Bearer $token" "$base/api/graph" | jq -c '.plan | {id, status, planned, total}'
curl -s -H "Authorization: Bearer $token" "$base/api/graph" | jq -c '.nodes[] | {addr, action, plans}'
kill $pid
```

Output:

```text
{"id":"plan-<id>","status":"pending","planned":2,"total":2}
{"addr":"config","action":"replace","plans":["plan-<id>"]}
{"addr":"pet","action":"replace","plans":["plan-<id>"]}
```

### Read the recorded runs

With `PRAVO_TRACES=on`, a command that does some work records its spans in `.pravo/traces`, and `/api/traces` lists the runs. The example applies the change that the stored plan holds, recorded.

```sh
PRAVO_TRACES=on pravo apply --auto-approve --reason "longer pet name" | grep '^Apply complete'
rm -f serve.json
pravo serve --no-open --json > serve.json &
pid=$!
until [ -s serve.json ]; do sleep 0.1; done
url=$(sed 's/.*"url":"\([^"]*\)".*/\1/' serve.json)
base=${url%%/\?*}
token=${url##*token=}
curl -s -H "Authorization: Bearer $token" "$base/api/traces" | jq -c '.traces[0] | {command, line, status, exit, resources, reason}'
id=$(curl -s -H "Authorization: Bearer $token" "$base/api/traces" | jq -r '.traces[0].id')
curl -s -H "Authorization: Bearer $token" "$base/api/trace?id=$id" | jq -c '[.spans[] | select(.kind == "resource") | .name] | unique'
kill $pid
```

Output:

```text
Apply complete! Resources: 2 added, 0 changed, 2 destroyed. [txn <id>]
{"command":"pravo apply","line":"pravo apply --auto-approve --reason \"longer pet name\"","status":"ok","exit":0,"resources":2,"reason":"longer pet name"}
["resource config","resource pet"]
```
