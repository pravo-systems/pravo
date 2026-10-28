---
title: How pravo works
description: One run of pravo in seven stages, from the CUE files of a directory to the providers, the state and the output.
order: 1
minutes: 9
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9]{8}T[0-9]{6}Z-[0-9a-f]+\]'
    replace: '[txn <id>]'
  - pattern: 'version=[0-9]+\.[0-9]+\.[0-9]+'
    replace: 'version=<v>'
---

A run is one pravo command. `pravo plan` takes a directory of CUE files, a state and a set of providers, and prints the changes. `pravo apply` makes them. Both pass through the same stages, in the same order.

This page names the stages, says what each one reads and writes, and shows where each command stops.

:::figure The stages of one run. In the lanes, r marks a read and w marks a write. A bar shows the stages that a command passes through.
<svg viewBox="0 0 780 378" role="img" aria-label="Seven stages in a row: evaluate, discover, graph, plan, judge, apply, record. Below them three rows, files, providers and state, mark what each stage reads and writes. Four bars show where validate and eval, drift and why, plan, and apply and destroy stop." style="min-width:680px">
<line x1="136" y1="100" x2="136" y2="238" style="stroke:var(--line);stroke-width:1" stroke-dasharray="1 4"/>
<line x1="234" y1="100" x2="234" y2="238" style="stroke:var(--line);stroke-width:1" stroke-dasharray="1 4"/>
<line x1="332" y1="100" x2="332" y2="238" style="stroke:var(--line);stroke-width:1" stroke-dasharray="1 4"/>
<line x1="430" y1="100" x2="430" y2="238" style="stroke:var(--line);stroke-width:1" stroke-dasharray="1 4"/>
<line x1="528" y1="100" x2="528" y2="238" style="stroke:var(--line);stroke-width:1" stroke-dasharray="1 4"/>
<line x1="626" y1="100" x2="626" y2="238" style="stroke:var(--line);stroke-width:1" stroke-dasharray="1 4"/>
<line x1="724" y1="100" x2="724" y2="238" style="stroke:var(--line);stroke-width:1" stroke-dasharray="1 4"/>
<rect x="0" y="104" width="780" height="44" rx="0" style="fill:var(--surface-2);stroke:none;stroke-width:1"/>
<rect x="0" y="148" width="780" height="44" rx="0" style="fill:none;stroke:none;stroke-width:1"/>
<rect x="0" y="192" width="780" height="44" rx="0" style="fill:var(--surface-2);stroke:none;stroke-width:1"/>
<text x="0" y="130" style="fill:var(--text-3);font-size:11px">files</text>
<text x="0" y="174" style="fill:var(--text-3);font-size:11px">providers</text>
<text x="0" y="218" style="fill:var(--text-3);font-size:11px">state</text>
<line x1="0" y1="104" x2="780" y2="104" style="stroke:var(--line-strong);stroke-width:1"/>
<line x1="0" y1="236" x2="780" y2="236" style="stroke:var(--line-strong);stroke-width:1"/>
<rect x="92" y="14" width="88" height="46" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="100" y="29" style="fill:var(--text-3);font-size:10px">1</text>
<text x="136" y="50" text-anchor="middle" style="fill:var(--text);font-size:13px;font-weight:600">evaluate</text>
<text x="136" y="78" text-anchor="middle" style="fill:var(--text-3);font-size:11px">one value</text>
<polygon points="181,33 189,37 181,41" style="fill:var(--text-3)"/>
<rect x="190" y="14" width="88" height="46" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="198" y="29" style="fill:var(--text-3);font-size:10px">2</text>
<text x="234" y="50" text-anchor="middle" style="fill:var(--text);font-size:13px;font-weight:600">discover</text>
<text x="234" y="78" text-anchor="middle" style="fill:var(--text-3);font-size:11px">addresses</text>
<polygon points="279,33 287,37 279,41" style="fill:var(--text-3)"/>
<rect x="288" y="14" width="88" height="46" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="296" y="29" style="fill:var(--text-3);font-size:10px">3</text>
<text x="332" y="50" text-anchor="middle" style="fill:var(--text);font-size:13px;font-weight:600">graph</text>
<text x="332" y="78" text-anchor="middle" style="fill:var(--text-3);font-size:11px">dependencies</text>
<polygon points="377,33 385,37 377,41" style="fill:var(--text-3)"/>
<rect x="386" y="14" width="88" height="46" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="394" y="29" style="fill:var(--text-3);font-size:10px">4</text>
<text x="430" y="50" text-anchor="middle" style="fill:var(--text);font-size:13px;font-weight:600">plan</text>
<text x="430" y="78" text-anchor="middle" style="fill:var(--text-3);font-size:11px">actions</text>
<polygon points="475,33 483,37 475,41" style="fill:var(--text-3)"/>
<rect x="484" y="14" width="88" height="46" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2" stroke-dasharray="4 3"/>
<text x="492" y="29" style="fill:var(--text-3);font-size:10px">5</text>
<text x="528" y="50" text-anchor="middle" style="fill:var(--text);font-size:13px;font-weight:600">judge</text>
<text x="528" y="78" text-anchor="middle" style="fill:var(--text-3);font-size:11px">verdict</text>
<polygon points="573,33 581,37 573,41" style="fill:var(--text-3)"/>
<rect x="582" y="14" width="88" height="46" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="590" y="29" style="fill:var(--text-3);font-size:10px">6</text>
<text x="626" y="50" text-anchor="middle" style="fill:var(--text);font-size:13px;font-weight:600">apply</text>
<text x="626" y="78" text-anchor="middle" style="fill:var(--text-3);font-size:11px">changes</text>
<polygon points="671,33 679,37 671,41" style="fill:var(--text-3)"/>
<rect x="680" y="14" width="88" height="46" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1.2"/>
<text x="688" y="29" style="fill:var(--text-3);font-size:10px">7</text>
<text x="724" y="50" text-anchor="middle" style="fill:var(--text);font-size:13px;font-weight:600">record</text>
<text x="724" y="78" text-anchor="middle" style="fill:var(--text-3);font-size:11px">records</text>
<text x="528" y="92" text-anchor="middle" style="fill:var(--text-3);font-size:10px">if a policy exists</text>
<circle cx="136" cy="120" r="8.5" style="fill:var(--key-wash);stroke:var(--key);stroke-width:1.5"/>
<text x="136" y="123.6" text-anchor="middle" style="fill:var(--key);font-size:11px;font-weight:600">r</text>
<text x="136" y="140" text-anchor="middle" style="fill:var(--text-3);font-size:10px">*.cue, cue.mod</text>
<circle cx="528" cy="120" r="8.5" style="fill:var(--key-wash);stroke:var(--key);stroke-width:1.5"/>
<text x="528" y="123.6" text-anchor="middle" style="fill:var(--key);font-size:11px;font-weight:600">r</text>
<text x="528" y="140" text-anchor="middle" style="fill:var(--text-3);font-size:10px">policies</text>
<circle cx="430" cy="164" r="8.5" style="fill:var(--key-wash);stroke:var(--key);stroke-width:1.5"/>
<text x="430" y="167.6" text-anchor="middle" style="fill:var(--key);font-size:11px;font-weight:600">r</text>
<text x="430" y="184" text-anchor="middle" style="fill:var(--text-3);font-size:10px">refresh, plan</text>
<circle cx="626" cy="164" r="8.5" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.5"/>
<text x="626" y="167.6" text-anchor="middle" style="fill:var(--change);font-size:11px;font-weight:600">w</text>
<text x="626" y="184" text-anchor="middle" style="fill:var(--text-3);font-size:10px">create, change</text>
<circle cx="419" cy="208" r="8.5" style="fill:var(--key-wash);stroke:var(--key);stroke-width:1.5"/>
<text x="419" y="211.6" text-anchor="middle" style="fill:var(--key);font-size:11px;font-weight:600">r</text>
<circle cx="441" cy="208" r="8.5" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.5"/>
<text x="441" y="211.6" text-anchor="middle" style="fill:var(--change);font-size:11px;font-weight:600">w</text>
<text x="430" y="228" text-anchor="middle" style="fill:var(--text-3);font-size:10px">refresh, moves</text>
<circle cx="626" cy="208" r="8.5" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.5"/>
<text x="626" y="211.6" text-anchor="middle" style="fill:var(--change);font-size:11px;font-weight:600">w</text>
<text x="626" y="228" text-anchor="middle" style="fill:var(--text-3);font-size:10px">locks, nodes</text>
<circle cx="724" cy="208" r="8.5" style="fill:var(--change-wash);stroke:var(--change);stroke-width:1.5"/>
<text x="724" y="211.6" text-anchor="middle" style="fill:var(--change);font-size:11px;font-weight:600">w</text>
<text x="724" y="228" text-anchor="middle" style="fill:var(--text-3);font-size:10px">outputs, txn</text>
<text x="0" y="270" style="fill:var(--text-3);font-size:11px">commands</text>
<rect x="92" y="252" width="186" height="24" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1"/>
<text x="102" y="268" style="fill:var(--text);font-size:12px">validate, eval</text>
<circle cx="278" cy="264" r="3.5" style="fill:var(--text-3);stroke:var(--text-3);stroke-width:1.5"/>
<rect x="92" y="283" width="382" height="24" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1"/>
<text x="102" y="299" style="fill:var(--text);font-size:12px">drift, why</text>
<circle cx="474" cy="295" r="3.5" style="fill:var(--text-3);stroke:var(--text-3);stroke-width:1.5"/>
<rect x="92" y="314" width="480" height="24" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1"/>
<text x="102" y="330" style="fill:var(--text);font-size:12px">plan</text>
<circle cx="572" cy="326" r="3.5" style="fill:var(--text-3);stroke:var(--text-3);stroke-width:1.5"/>
<rect x="92" y="345" width="676" height="24" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1"/>
<text x="102" y="361" style="fill:var(--text);font-size:12px">apply, destroy</text>
<circle cx="768" cy="357" r="3.5" style="fill:var(--text-3);stroke:var(--text-3);stroke-width:1.5"/>
</svg>
:::

## The seven stages

### 1. Evaluate

pravo loads the CUE package in the directory. Every file of the package joins it, in any order. CUE unifies the files and their imports into one value. The imports include the definitions that `pravo init` generated from the provider schemas.

A conflict, a misspelt attribute or a value outside its constraint ends the run here. The error names the file and the line. No provider has been asked for anything. [Evaluation](evaluation.md) has the details.

### 2. Discover

pravo walks the value and collects every struct that carries a `$kind` field. Such a struct is a resource, a data source, an ephemeral resource, a function call, a provider, an output, an environment variable or the outputs of another root. The address of a struct is its path in the value.

Definitions, hidden fields and the top-level `$settings` field are not searched. [Resources and addresses](resources-and-addresses.md) explains the identity.

### 3. Build the graph

pravo reads every field of every resource and records each reference to another resource as a dependency. A reference in provider configuration is a dependency of every resource that uses the provider. The result is a graph in which a resource runs after everything it refers to. Resources that refer to each other in a circle stop the run with `dependency cycle between` and the names of the resources on the cycle.

### 4. Plan

pravo visits the graph in dependency order, at most ten resources at a time (`PRAVO_PARALLELISM` changes the number). For each resource it reads the recorded node from the state and converts the body to a value that the provider understands. A resource that is unchanged since the last apply is skipped. The plan reads the record of a resource from its provider only for the resources it reaches and for the resources they read, and only when the record is older than its refresh age. The other records are not read.

Every other resource goes to its provider, which returns the planned state. From the answer pravo derives one action: create, update, replace, delete or no-op. A record in the state that no struct declares becomes a delete.

This stage writes only records that it refreshed, moved or imported, and the marks that a read leaves on a record whose object differs or is gone. [Plans](plans.md) covers it.

The plan can leave the terminal. `plan --out` saves it to a file, and `plan --store` writes it into the state, where a colleague or a job finds it by its id and applies exactly those actions. [Stored plans and approvals](stored-plans-and-approvals.md) follows it from there.

### 5. Judge

A policy is a `pravo.#Policy` node of the specification. The first apply that carries it writes it to the state as a signed version. For every plan that changes a resource, pravo unifies the rule of each version in force with the plan document. A plan that violates a policy ends the run with exit code 5. `apply` judges under the locks, before the transaction starts, and the signed start of the transaction lists the versions that judged. `plan` judges the same way, for feedback. A policy can also let a plan through on the condition that other people approve it, which only a stored plan can collect: the apply waits with exit code 5 until enough keys have approved.

When no policy is in force, this stage does nothing. [Constraints and policies](policies.md) covers both.

### 6. Apply

pravo starts a transaction and locks every node that the plan writes. A lock held by another process stops the run before anything changes. pravo then runs the graph again over the nodes the plan changes and the nodes they read.

Each changed resource is planned a second time, now with the values of the resources applied before it, compared with the plan, and then changed through its provider. The apply does not read the providers again: it uses what the plan read. A node that a change reads is taken from its record. Records that no struct declares are destroyed last, in reverse dependency order.

### 7. Record

pravo writes each node when its change completes, with a new revision. When the graph is finished, it writes the outputs and the transaction and releases the locks. An interrupted apply leaves the nodes that were written before it stopped. In a sealed state the values are encrypted to the readers, and the transaction is signed by the identity that ran it, so the log says who changed what and the signature can be checked later.

[State](state.md) describes the records, and [Signatures and roles](signatures-and-roles.md) what sealing and signing protect.

## The parts of a run

pravo is one process. It reads the files of the root and the policies that the state keeps. It starts one child process for each provider instance. It talks to the process over gRPC, in version 5 or 6 of the plugin protocol. It reads and writes the state through a small storage interface that a directory or an S3 bucket implements.

:::figure Three kinds of neighbour. Providers are separate processes. The state is a directory or a bucket. The specification and the policies are read only.
<svg viewBox="0 0 780 262" role="img" aria-label="The pravo process in the middle. On its left, the specification files and the policies, which it reads. On its right, provider processes, reached over gRPC, and the state, a directory or a bucket, which it reads and writes." style="min-width:680px">
<rect x="0" y="14" width="200" height="104" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="14" y="36" style="fill:var(--text);font-size:12px;font-weight:600">specification</text>
<text x="14" y="58" style="fill:var(--text-2);font-size:11px">*.cue in the root</text>
<text x="14" y="76" style="fill:var(--text-2);font-size:11px">cue.mod/gen/</text>
<text x="14" y="94" style="fill:var(--text-3);font-size:10.5px">  pravo.dev/providers/...</text>
<text x="14" y="110" style="fill:var(--text-2);font-size:11px">pravo.lock.cue</text>
<rect x="0" y="144" width="200" height="76" rx="2" style="fill:var(--surface);stroke:var(--text-3);stroke-width:1" stroke-dasharray="4 3"/>
<text x="14" y="166" style="fill:var(--text);font-size:12px;font-weight:600">policies</text>
<text x="14" y="188" style="fill:var(--text-2);font-size:11px">#Policy nodes of the root</text>
<text x="14" y="206" style="fill:var(--text-3);font-size:11px">kept in the state</text>
<rect x="290" y="14" width="200" height="206" rx="2" style="fill:var(--surface-2);stroke:var(--text-3);stroke-width:1.4"/>
<text x="390" y="40" text-anchor="middle" style="fill:var(--text);font-size:14px;font-weight:600">pravo</text>
<line x1="306" y1="52" x2="474" y2="52" style="stroke:var(--line-strong);stroke-width:1"/>
<text x="310" y="72" style="fill:var(--text-3);font-size:10px">1</text>
<text x="326" y="72" style="fill:var(--text-2);font-size:12px">evaluate</text>
<text x="310" y="93" style="fill:var(--text-3);font-size:10px">2</text>
<text x="326" y="93" style="fill:var(--text-2);font-size:12px">discover</text>
<text x="310" y="114" style="fill:var(--text-3);font-size:10px">3</text>
<text x="326" y="114" style="fill:var(--text-2);font-size:12px">graph</text>
<text x="310" y="135" style="fill:var(--text-3);font-size:10px">4</text>
<text x="326" y="135" style="fill:var(--text-2);font-size:12px">plan</text>
<text x="310" y="156" style="fill:var(--text-3);font-size:10px">5</text>
<text x="326" y="156" style="fill:var(--text-2);font-size:12px">judge</text>
<text x="310" y="177" style="fill:var(--text-3);font-size:10px">6</text>
<text x="326" y="177" style="fill:var(--text-2);font-size:12px">apply</text>
<text x="310" y="198" style="fill:var(--text-3);font-size:10px">7</text>
<text x="326" y="198" style="fill:var(--text-2);font-size:12px">record</text>
<rect x="580" y="14" width="200" height="104" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="594" y="36" style="fill:var(--text);font-size:12px;font-weight:600">provider processes</text>
<rect x="594" y="48" width="76" height="24" rx="2" style="fill:var(--surface-2);stroke:var(--text-3);stroke-width:1"/>
<text x="632" y="64" text-anchor="middle" style="fill:var(--text);font-size:11px">random</text>
<rect x="682" y="48" width="84" height="24" rx="2" style="fill:var(--surface-2);stroke:var(--text-3);stroke-width:1"/>
<text x="724" y="64" text-anchor="middle" style="fill:var(--text);font-size:11px">local</text>
<text x="594" y="92" style="fill:var(--text-3);font-size:10.5px">one per provider instance</text>
<text x="594" y="108" style="fill:var(--text-3);font-size:10.5px">started on first use</text>
<rect x="580" y="144" width="200" height="76" rx="2" style="fill:var(--surface);stroke:var(--line-strong);stroke-width:1"/>
<text x="594" y="166" style="fill:var(--text);font-size:12px;font-weight:600">state</text>
<text x="594" y="188" style="fill:var(--text-2);font-size:11px">pravo.state/ or S3 bucket</text>
<text x="594" y="206" style="fill:var(--text-3);font-size:10.5px">nodes, txn, locks, outputs</text>
<line x1="200" y1="66" x2="288" y2="66" style="stroke:var(--key);stroke-width:1.5"/>
<polygon points="288,66 282,62.4 282,69.6" style="fill:var(--key)"/>
<text x="244" y="58" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">read</text>
<line x1="200" y1="182" x2="288" y2="182" style="stroke:var(--key);stroke-width:1.5"/>
<polygon points="288,182 282,178.4 282,185.6" style="fill:var(--key)"/>
<text x="244" y="174" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">read</text>
<line x1="492" y1="66" x2="578" y2="66" style="stroke:var(--text-3);stroke-width:1.5"/>
<polygon points="578,66 572,62.4 572,69.6" style="fill:var(--text-3)"/>
<polygon points="492,66 498,62.4 498,69.6" style="fill:var(--text-3)"/>
<text x="535" y="58" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">gRPC</text>
<text x="535" y="82" text-anchor="middle" style="fill:var(--text-3);font-size:10px">tfplugin 5, 6</text>
<line x1="492" y1="182" x2="578" y2="182" style="stroke:var(--change);stroke-width:1.5"/>
<polygon points="578,182 572,178.4 572,185.6" style="fill:var(--change)"/>
<polygon points="492,182 498,178.4 498,185.6" style="fill:var(--change)"/>
<text x="535" y="174" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">read, write</text>
<text x="390" y="250" text-anchor="middle" style="fill:var(--text-3);font-size:10.5px">processes: one pravo, one per provider instance</text>
</svg>
:::

A provider process starts the first time a resource needs it and stops when the command ends. [Providers](providers.md) describes how pravo finds, starts and configures them.

## One run, as pravo reports it

`pravo -v` logs what pravo decides, on standard error. The filter below removes the timings and the paths and keeps the lines that mark a stage. Work in an empty directory.

```cue file=main.cue
package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	loc "pravo.dev/providers/hashicorp/local"
)

$random: rnd.#Provider
$local:  loc.#Provider

port: $random.#Resource.random_integer & {
	min:  20000
	max:  29999
	seed: "web"
}

web: $local.#Resource.local_file & {
	filename: "web.conf"
	content:  "port = \(port.result)\n"
}
```

```sh run
pravo init
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

The first run is an apply into an empty state.

```sh run
pravo -v apply -auto-approve 2>&1 \
  | sed -E 's/^\+[0-9.]+s debug //; s/ (dir|path|location|took)=[^ ]+//g; s/ why=.*//' \
  | grep -E '^(loaded|state opened|plan |graph built|provider started|task |apply |Plan:|Graph:|Policies|Apply complete)'
```

```text out
loaded managed=2 data=0 ephemeral=0 calls=0 outputs=0 providers=2 moves=0 imports=0
state opened backend=local nodes=0
plan refresh=age full=false destroy=false tasks=2
graph built tasks=2
provider started provider=random version=<v>
task addr=port phase=plan outcome=ok action=create
provider started provider=local version=<v>
task addr=web phase=plan outcome=ok action=create
Plan: 2 to add, 0 to change, 0 to destroy.
Graph: planned 2
apply changes=2 destroy=false
task addr=port phase=apply outcome=ok action=create
task addr=web phase=apply outcome=ok action=create
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

Read the lines in order.

| Line | Stage |
|---|---|
| `loaded` | 1 and 2. The directory evaluated, and discovery found two resources and no data sources. |
| `state opened` | The state was read. It holds no nodes. |
| `plan`, `graph built` | 3, and the start of 4. Two resources are in the graph. |
| `provider started` | A provider process starts when the first resource that needs it is reached. |
| `task ... phase=plan` | 4. One line per resource, with the action. |
| `apply` | 6. The graph runs again over the nodes the plan changes and the nodes they read, now with the values of the resources applied before. |
| `Apply complete!` | 7. The transaction id is the record of the run. |

The second run is a plan. Nothing changed, so every resource is skipped.

```sh run
pravo -v plan 2>&1 \
  | sed -E 's/^\+[0-9.]+s debug //; s/ (dir|path|location|took)=[^ ]+//g; s/ why=.*//' \
  | grep -E '^(loaded|state opened|plan |graph built|provider started|task |apply |Plan:|Graph:|No changes|Policies|Apply complete)'
```

```text out
loaded managed=2 data=0 ephemeral=0 calls=0 outputs=0 providers=2 moves=0 imports=0
state opened backend=local nodes=2
plan refresh=age full=false destroy=false tasks=2
graph built tasks=2
task addr=port phase=plan outcome=ok action=no-op skipped=true unread=true
task addr=web phase=plan outcome=ok action=no-op skipped=true unread=true
No changes. The specification matches the state. 2 records were not read; run pravo drift to compare them with the infrastructure.
Graph: planned 0 · skipped 2 · not read 2
```

No policy is in force, so stage 5 prints nothing. With one, `plan` prints one `Policies` line after the plan and before any file is saved.

## What a run leaves behind

A run writes to the state and prints to the terminal, and it leaves two more things that other commands read. With `PRAVO_TRACES=on`, every command that did some work records its trace in `.pravo/traces`: one span for each stage, resource, provider call, request to the state and network call, with its timing and its outcome, and never an attribute value. [Traces](traces.md) explains what a span is, and the Traces section of the viewer draws a run as a timeline. The viewer itself, `pravo serve`, reads the specification, the state, the stored plans, the policies, the discoveries and the recorded runs and shows them in a browser, and writes nothing: [The viewer](the-viewer.md) explains what it makes of them. A program that drives pravo reads the same documents as JSON, through `--json`, the MCP server or the Go library: [The library and the machine interface](the-library-and-the-machine-interface.md).

## Where each command stops

| Command | Stages | What else it does |
|---|---|---|
| `pravo validate` | 1 and 2 | Resolves the providers and reads their schemas. It starts no provider and opens no state. |
| `pravo eval` | 1 and 2 | Opens the state and fills the recorded attributes of each resource into the value. |
| `pravo plan` | 1 to 5 | `-out` saves the plan after the policies are satisfied, and `--store` writes it into the state. |
| `pravo drift`, `pravo why` | 1 to 4 | `drift` reads every record, or with `--due` the records past their refresh age. `why` prints the reasons of each change. |
| `pravo apply` | 1 to 4, then 6 and 7 | Prints the plan and asks for `yes`. Stage 5 judges the plan it built. |
| `pravo apply FILE`, `pravo apply ID` | 1 and 2, then 5 to 7 | Reads the saved plan, or the plan stored in the state, in place of stages 3 and 4 and checks that it is current. Stage 5 also checks the approvals a policy asked for. Stage 6 builds the graph and plans each resource of the plan again. |
| `pravo destroy` | 1 and 2, then 4 to 7 | The plan is the list of records in the state, with no graph built from the specification. It is judged before the prompt. |
| `pravo discover` | 1 and 2 | Runs the discoveries of the specification through their providers and writes what each found to the state. No plan is made. |
| `pravo serve` | 1 and 2 | Reads the state, the stored plans, the policies, the discoveries and the recorded runs, and serves them. It starts no provider and writes nothing. |

## What each stage touches

| Stage | Reads | Writes |
|---|---|---|
| 1 Evaluate | The `.cue` files, `cue.mod`, tags | Nothing |
| 2 Discover | The value | Nothing |
| 3 Graph | The value | Nothing |
| 4 Plan | Records, providers for the records it looks at, ephemeral resources, data sources | Refreshed, moved and imported records, marks, a plan transaction when it wrote anything, reservations |
| 5 Judge | The policy versions in the state | Nothing |
| 6 Apply | Records, providers | Locks, the objects that providers manage |
| 7 Record | Nothing | Nodes, outputs, the transaction |

## Recap

- A run evaluates, discovers, builds the graph and plans. Apply adds locks, changes and records.
- Evaluation and discovery read files only. No provider is asked for anything before the plan stage.
- Each command stops at a fixed stage. `validate` and `eval` stop after discovery.

Next: [Evaluation](evaluation.md) follows the first three stages in detail.
