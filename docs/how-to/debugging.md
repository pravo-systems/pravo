---
title: Find out why a run fails
description: Read an evaluation error, ask pravo what it decided with -v, ask why a resource changes, check for drift and follow a run in a trace.
order: 4
minutes: 15
needs: "[Reading a CUE error](../learn/reading-a-cue-error.md) for the shape of a CUE error. `jq`, and an empty directory."
done_when: "You can name the file and line of an evaluation error, print the reason for a task from the debug log and follow a command in a trace."
level: intermediate
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: 'took=[0-9.]+(µs|ms|s)'
    replace: 'took=0ms'
  - pattern: 'the record is [0-9]+[smhd] old'
    replace: 'the record is <age> old'
group: Observe
keywords: "field not allowed conflicting values error debug why"
---

A run fails in one of four places. The specification does not evaluate, a task does something you did not expect, the real world differs from the state, or a provider reports an error. Each has a command that shows it.

Work in an empty directory.

```cue file=main.cue title="main.cue"
package debugging

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

$settings: refresh: "1h"

server: $random.#Resource.random_pet & {length: 2}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(server.id)\n"
}
```

```sh run
pravo init > /dev/null
pravo apply -auto-approve | grep '^Apply complete'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

## 1. Read an evaluation error

An evaluation error has a path, the values that disagree, and a list of places. Add a second value for a field that already has one.

```cue file=bad.cue title="bad.cue" status=rejected
package debugging

server: length: 3
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
server.length: 2 errors in empty disjunction:
server.length: conflicting values 2 and 3:
    ./bad.cue:3:17
    ./main.cue:13:49
server.length: conflicting values 3 and null (mismatched types int and null):
    ./bad.cue:3:17
    ./cue.mod/gen/pravo.dev/providers/hashicorp/random/random_gen.cue:236:22
    ./main.cue:13:9
```

- **Path.** `server.length` is the field that disagrees.
- **Values.** `2` and `3` cannot both hold. `2` is in `main.cue`, `3` is in `bad.cue`.
- **Places.** Each indented line is `file:line:column`. The first is the value you wrote, and the others are the values it met.
- **Alternatives.** `2 errors in empty disjunction` means that the schema offers two alternatives for the field, a number or `null`, and the value fits neither. Read the first error. The second compares `3` with `null`.

`pravo validate`, `plan` and `apply` print the same message and exit with 6 for an evaluation error. A misspelt field and an unknown name have shorter messages.

```cue file=bad.cue title="bad.cue" status=rejected
package debugging

note: filenam: "x"
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
note.filenam: field not allowed:
    ./bad.cue:3:7
```

`field not allowed` means that a definition is closed and the field is not in it. A resource is built from a closed definition, so a typo is an error before any provider starts. [Closed or open?](../learn/closed-or-open.md) explains it.

```cue file=bad.cue title="bad.cue" status=rejected
package debugging

note: content: "\(servr.id)"
```

```sh run exit=6
pravo validate
```

```text out
Error: evaluating .:
note.content: reference "servr" not found:
    ./bad.cue:3:19
```

`reference "servr" not found` names the name that does not exist. Remove the file when you have read it.

```sh run
rm bad.cue
```

These messages come up most often.

| Message | Meaning and fix |
|---|---|
| `conflicting values A and B` | Two files give a field two values. Read both places. [Reading a CUE error](../learn/reading-a-cue-error.md) |
| `N errors in empty disjunction` | A field has alternatives and the value fits none. Read the first error. |
| `field not allowed` | A closed definition does not have the field. Check the spelling. |
| `reference "x" not found` | A name does not exist in the scope. |
| `value depends on cfg, which is not concrete (is a -t tag missing?)` | A value still depends on a tag. Pass `-t`. [Why is my value incomplete?](../learn/why-is-my-value-incomplete.md) |
| `$settings.state.path is not concrete` | The state path depends on a tag. Pass `-t env=...`. |
| `a provider schema package is missing` | Run `pravo init`. |
| `saved plan is stale` | The specification or the state changed after the plan. Plan again. Exit code 3. |
| `plan rejected: ... is reserved by` | Another actor holds the node. Exit code 4. |

The [error catalogue](../reference/errors.md) lists the rest, with causes and fixes.

## 2. Ask what pravo decided

`-v` writes pravo's own account of a run to standard error. It shows what was loaded, which state was opened, where each provider version came from, the graph, and for every task the action and the reason. Nothing is printed without it.

```sh run
pravo plan -v 2>&1 | grep -E 'debug (state opened|provider version from the lock file|plan |graph built)' | cut -d' ' -f2- | sort
```

```text out
debug graph built tasks=2 took=0ms
debug plan refresh=age full=false destroy=false tasks=2
debug provider version from the lock file provider=local version=2.9.1 constraint=
debug provider version from the lock file provider=random version=3.9.1 constraint=
debug state opened backend=local location=./pravo.state nodes=2 took=0ms
```

Every line carries the time since the command began, which `cut` drops here. The `task` line carries the reason that `pravo why` prints.

```sh run
pravo plan -v 2>&1 | grep 'task addr=note' | cut -d' ' -f2-
```

```text out
debug task addr=note phase=plan outcome=ok took=0ms action=no-op skipped=true fresh=true unread=true why="skipped: nothing the provider saw has changed; not read: the plan did not reach it; the record is <age> old, its refresh age is 1h"
```

Seven lines carry `took=`: the time one phase of the command took and, on the last line, the time of the whole command. The phases follow one another.

| Line | Phase | Its time follows |
|---|---|---|
| `loaded` | Loading and evaluating the specification, with the provider packages it imports. | The size of the specification and of the packages it imports. |
| `state opened` | Reading the records of the state. | The number of records. |
| `schemas ready` | Reading the schema of each provider from the cache. | The providers the specification uses. |
| `graph built` | Working out which task reads which, from the references in the body of each. | The number of tasks. |
| `tasks run` | Walking the graph: planning each task, or applying it. `phase` says which. | The tasks the run reaches and the calls it makes to providers. A task it does not reach costs a comparison of its configuration with its record. |
| `settled` | Unifying the values that the tasks computed into the configuration. `fills` counts the tasks that contributed. | The tasks whose values something reads. |
| `finished` | The whole command. | |

```sh run
pravo plan -v 2>&1 | grep -E 'debug (loaded|state opened|schemas ready|graph built|tasks run|settled|finished) ' | cut -d' ' -f2-
```

```text out
debug loaded dir=. managed=2 data=0 ephemeral=0 calls=0 outputs=0 providers=2 moves=0 imports=0 took=0ms
debug state opened backend=local location=./pravo.state nodes=2 took=0ms
debug schemas ready providers=2 took=0ms
debug graph built tasks=2 took=0ms
debug tasks run phase=plan tasks=2 took=0ms
debug settled phase=plan fills=0 took=0ms
debug finished command="pravo plan" exit=0 took=0ms
```

The first four phases do not depend on what changed. They are the floor of every command: a specification that imports a large provider, such as `hashicorp/aws`, spends most of its floor in `loaded` and `schemas ready`, and an estate of thousands of resources spends it in `loaded`, `state opened` and `graph built`. What a command does for the change is in `tasks run`.

`-vv` adds the finest detail, such as each task's dependencies, and turns on the providers' own log.

```sh run
pravo plan -vv 2>&1 | grep 'trace graph node' | cut -d' ' -f2- | sort
pravo plan -vv -refresh=all 2>&1 | grep -c '\[DEBUG\] plugin: starting plugin'
```

```text out
trace graph node addr=note deps=server
trace graph node addr=server deps=
2
```

`PRAVO_LOG=debug` or `trace` does the same as `-v` or `-vv` for pravo, and shows the providers' log at that level. `info`, `warn` and `error` affect the providers only.

```sh run exit=1
PRAVO_LOG=warn pravo plan 2>&1 | grep -c debug
```

```text out
0
```

Values of attributes are never logged. The name of an environment variable is, and its value is not.

## 3. Ask why a task changes or is skipped

`pravo why` lists the reasons behind the action for each address. The plan does not reach `note`, and its record is within the refresh age of one hour, so pravo did not read it and trusts it. A node that the plan did not look at says so in a `not read:` reason, with the age of the record and its refresh age.

```sh run
pravo why note
```

```text out
note: no-op
  - skipped: nothing the provider saw has changed
  - not read: the plan did not reach it; the record is <age> old, its refresh age is 1h
```

Change the specification and ask about `server`.

```cue file=main.cue title="main.cue" hl="13"
package debugging

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

$settings: refresh: "1h"

server: $random.#Resource.random_pet & {length: 3}

note: $local.#Resource.local_file & {
	filename: "note.txt"
	content:  "server \(server.id)\n"
}
```

```sh run
pravo why server note
```

```text out
server: replace
  - the specification changed since the last apply (hash f2caf74c767a7efa -> df0a9c7e3f0c3d57)
  - refresh skipped: the record is within its refresh age
  - the provider cannot change length in place; replacement required
  reaches: note
note: replace
  - inputs depend on values known only after apply: server
  - refresh skipped: the record is within its refresh age
  - the provider cannot change content in place; replacement required
```

The reasons name what changed in the specification, what the provider cannot change in place, and what the change reaches. Put `length: 2` back before the next step.

```sh run
sed 's/length: 3/length: 2/' main.cue > main.tmp
mv main.tmp main.cue
pravo plan | grep -E '^(No changes|Graph)'
```

```text out
No changes. Your infrastructure matches the configuration.
Graph: planned 0 · skipped 2 · within refresh age 2
```

## 4. Check whether the real world agrees

Change the file behind pravo's back.

```sh run
echo changed > note.txt
pravo plan | grep -E '^(No changes|Graph)'
```

```text out
No changes. Your infrastructure matches the configuration.
Graph: planned 0 · skipped 2 · within refresh age 2
```

The plan found nothing, because the record is within its refresh age and pravo did not read the file. `pravo drift` ignores refresh ages, reads every resource and exits 2 when something differs. `-refresh=all` does the same for a plan. A plan that finds nothing among a large estate may also not have looked: the `Graph:` line says how many records it did not read, and `-refresh=expired` reads every record that is past its refresh age.

```sh run exit=2
pravo drift
```

```text out
Drift. 1 of 2 resources changed outside pravo:
  note: create
    - refresh found the real state differs from the state (drift)
```

```sh run
pravo plan -refresh=all | grep -E '^(note|Plan|Graph)'
```

```text out
note: Refreshing state... [id=<id>]
note: no longer exists; it will be created again
Plan: 1 to add, 0 to change, 0 to destroy.
Graph: planned 1 · skipped 1 · refreshed 2 · drifted 1
```

`-refresh` takes `age`, the default, `expired`, `false` and `all`. The `Graph:` line counts what a plan read: `refreshed` and `drifted`, and what it did not: `within refresh age` and `not read`.

## 5. Follow a run in a trace

Every command can emit OpenTelemetry traces. Exporting them is off by default. `OTEL_TRACES_EXPORTER=console` writes one JSON object per span to standard error, which needs no collector.

```sh run
OTEL_TRACES_EXPORTER=console pravo plan 2> trace.jsonl > /dev/null
jq -r '.Name' trace.jsonl | grep -E '^(pravo|engine|resource|provider)' | sort -u
```

```text out
engine plan
pravo plan
provider ConfigureProvider
provider PlanResourceChange
provider UpgradeResourceState
provider ValidateProviderConfig
provider ValidateResourceConfig
resource note
resource server
```

One trace per command. The command span is the root, and the run, the resources and the provider calls nest under it, with the setup of the command, the stages of the run and each request to the state (`setup open state`, `phase lock`, `state get resources/server.json`) beside them, which [Telemetry](../reference/telemetry.md#traces) lists. A span records addresses, types, actions, counts and outcomes, and never attribute values.

```sh run
jq -c 'select(.Name == "resource server") | [.Attributes[] | {(.Key): .Value.Value}] | add' trace.jsonl
```

```text out
{"pravo.resource.address":"server","pravo.resource.type":"random_pet","pravo.mode":"managed","pravo.provider":"random","pravo.provider.instance":"$random","pravo.phase":"plan","pravo.action":"no-op","pravo.skipped":true,"pravo.refreshed":false,"pravo.drifted":false,"pravo.deferred":false}
```

When tracing is on, the trace id is stored where you already look. It is on every transaction in the state and on every plan document.

```sh run
OTEL_TRACES_EXPORTER=console pravo apply -auto-approve 2> /dev/null | grep -E '^(No changes|Apply complete)'
pravo state log --json | jq -r '.[0].trace_id | length'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
32
```

Without any of this, `PRAVO_TRACES=on pravo plan` (the recording is off until you ask for it) keeps the trace of a command that did some work in `.pravo/traces`, and `pravo serve` shows it as a timeline: the Traces section lists the runs, and a click on a bar shows the span, its error and its parts. That is the quickest way to find the slow resource of an apply or the call that failed. [Record what a run does](record-what-a-run-does.md) turns it on and reads a run that failed, and [The Traces section](../reference/viewer-sections.md#the-traces-section) lists every part of the view.

A real collector takes the standard `OTEL_EXPORTER_OTLP_ENDPOINT` variable. `TRACEPARENT` in the environment makes the command a child of the caller's span, so a pipeline's trace shows pravo's work inside it.

## What next

- [Run pravo in a pipeline](ci.md) lists the exit codes.
- [Global flags](../reference/global-flags.md) and [why and drift](../reference/why-and-drift.md) are the reference for `-v`, `pravo why` and `pravo drift`.
- [Telemetry](../reference/telemetry.md) lists every span, attribute and metric.
- [Why is my value incomplete?](../learn/why-is-my-value-incomplete.md) covers the errors about values that are not yet concrete.
