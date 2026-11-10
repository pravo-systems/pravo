<!-- Generated from docs/concepts/how-pravo-works.md by tools/skillgen. Do not edit. -->

# How pravo works

One run of pravo in seven stages, from the CUE files of a directory to the providers, the state and the output.

A run is one pravo command. `pravo plan` takes a directory of CUE files, a state and a set of providers, and prints the changes. `pravo apply` makes them. Both pass through the same stages, in the same order.

This page names the stages, says what each one reads and writes, and shows where each command stops.

*Figure: The stages of one run. In the lanes, r marks a read and w marks a write. A bar shows the stages that a command passes through.*

## The seven stages

### 1. Evaluate

pravo loads the CUE package in the directory. Every file of the package joins it, in any order. CUE unifies the files and their imports into one value. The imports include the definitions that `pravo init` generated from the provider schemas.

A conflict, a misspelt attribute or a value outside its constraint ends the run here. The error names the file and the line. No provider has been asked for anything. [Evaluation](evaluation.md) has the details.

### 2. Discover

pravo walks the value and collects every struct that carries a `$kind` field. Such a struct is a resource, a data source, an ephemeral resource, a function call, a provider, an output, an environment variable or the outputs of another root. The address of a struct is its path in the value.

Definitions, hidden fields and the top-level `$settings` field are not searched. [Resources and addresses](resources-and-addresses.md) explains the identity.

### 3. Build the graph

pravo reads every field of every resource and records each reference to another resource as a dependency. A reference in provider configuration is a dependency of every resource that uses the provider. The result is a graph in which a resource runs after everything it refers to. Resources that refer to each other in a circle stop the run with `dependency cycle between` and the names of the resources on the cycle.

#### Loops over a collection

A comprehension can make one resource for each resource of a collection: one route table association for each subnet in `subnets`. In `for name, subnet in subnets`, the resource that an iteration makes reads the subnet of that iteration, through `subnet.id` or through `subnets[name].id`, and waits for that one subnet. pravo takes the element from CUE's evaluator, which knows what the loop variable was bound to.

Where the evaluator does not name the element, the resource waits for every resource of the collection, which is always safe. That is the case for a collection whose elements are only references to a definition or to another field, for a collection that holds plain values beside its resources, and for a loop whose source is an expression and not the name of the collection. A reference to the collection as a whole, such as `json.Marshal(subnets)` or `$depends_on: [subnets]`, is a dependency on every resource in it.

`PRAVO_DEPENDENCIES=syntax` reads the dependencies from the syntax of the specification alone. Every loop over a collection then waits for the whole collection. The result of an apply is the same; the order in which independent resources are applied differs, and a collection of thousands of resources takes longer to load, since the number of dependencies grows with the square of its size.

### 4. Plan

pravo visits the graph in dependency order, at most ten resources at a time (`PRAVO_PARALLELISM` changes the number). For each resource it reads the recorded node from the state and converts the body to a value that the provider understands. A resource that is unchanged since the last apply is skipped. The plan reads the record of a resource from its provider only for the resources it reaches and for the resources they read, and only when the record is older than its refresh age. The other records are not read.

Every other resource goes to its provider, which returns the planned state. From the answer pravo derives one action: create, update, replace, delete or no-op. A record in the state that no struct declares becomes a delete.

This stage writes only records that it refreshed, moved or imported, and the marks that a read leaves on a record whose object differs or is gone. [Plans](plans.md) covers it.

The plan can leave the terminal. `plan --out` saves it to a file, and `plan --store` writes it into the state, where a colleague or a job finds it by its id and applies exactly those actions. [Stored plans and approvals](stored-plans-and-approvals.md) follows it from there.

#### What a plan remembers

Converting a body means evaluating it, and with thousands of resources that evaluation is most of the time of a plan in which little changed. A plan remembers the bodies it converted. For each resource it keeps one digest in a file on the machine that planned: of the body as it is written, of the values that the body reads from other resources, of the provider and the version of its schema, and of what the conversion came to. The next plan works the digest out again, which does not take evaluating the body. When the digest is the same and the record in the state holds what the conversion came to, the resource is unchanged, and the plan skips it as it skips every unchanged resource. In any other case the plan converts the body.

The file says "unchanged since the plan that wrote this" and nothing else. It holds no value and it decides nothing about reading: a record is read when its refresh age has run out or when it carries a mark, and `--replace` and `--full` plan a resource whatever the file says. A plan on another machine, or after the file is deleted, converts every body once and is the same plan.

The file answers for a body that reads other resources through plain references and in no other way: `subnet_id: subnet.id`, or a field of the variable of a loop that reads its own element. A body that builds a value from another resource, with an interpolation, a call, a condition or a comprehension, is converted by every plan. So is a resource that reads a secret or an ephemeral resource. The value of an environment variable, the outputs of another root and the result of a function call are read by every plan and are part of the digest of what reads them, and so is what a data source returned.

The file is kept in the cache directory of the user, under `pravo/plan-memo`, one for each root and environment, and a file that no command has used for 30 days is removed. Its digests are made with a key that is kept apart from it, `plan-memo.key` in the [configuration directory](../reference/global-flags.md#environment-variables), so the file alone does not let anyone test a guess of a value against it.

`PRAVO_PLAN=full` plans without the file. `PRAVO_PLAN=check` plans with it, converts every body it answered for as well, and fails when the two differ.

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

*Figure: Three kinds of neighbour. Providers are separate processes. The state is a directory or a bucket. The specification and the policies are read only.*

A provider process starts the first time a resource needs it and stops when the command ends. [Providers](providers.md) describes how pravo finds, starts and configures them.

## One run, as pravo reports it

`pravo -v` logs what pravo decides, on standard error. The filter below removes the timings and the paths and keeps the lines that mark a stage. Work in an empty directory.

File `main.cue`:

```cue
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

```sh
pravo init
```

Output:

```text
Configuration does not load yet; generating schemas for the declared providers first.
Created cue.mod/module.cue
Providers:
...
pravo has been initialised. Run "pravo plan" to see what it would do.
```

The first run is an apply into an empty state.

```sh
pravo -v apply -auto-approve 2>&1 \
  | sed -E 's/^\+[0-9.]+s debug //; s/ (dir|path|location|took)=[^ ]+//g; s/ why=.*//' \
  | grep -E '^(loaded|state opened|plan |graph built|provider started|task |apply |Plan:|Graph:|Policies|Apply complete)'
```

Output:

```text
loaded managed=2 data=0 ephemeral=0 calls=0 outputs=0 providers=2 moves=0 imports=0
state opened backend=local nodes=0
plan refresh=age full=false destroy=false tasks=2
graph built tasks=2 evaluator=true loops by element=0 loops unconfirmed=0
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

```sh
pravo -v plan 2>&1 \
  | sed -E 's/^\+[0-9.]+s debug //; s/ (dir|path|location|took)=[^ ]+//g; s/ why=.*//' \
  | grep -E '^(loaded|state opened|plan |graph built|provider started|task |apply |Plan:|Graph:|No changes|Policies|Apply complete)'
```

Output:

```text
loaded managed=2 data=0 ephemeral=0 calls=0 outputs=0 providers=2 moves=0 imports=0
state opened backend=local nodes=2
plan refresh=age full=false destroy=false tasks=2
graph built tasks=2 evaluator=true loops by element=0 loops unconfirmed=0
task addr=port phase=plan outcome=ok action=no-op skipped=true unread=true
task addr=web phase=plan outcome=ok action=no-op skipped=true unread=true
No changes. The specification matches the state. 2 records were not read, so a change made outside pravo would not show here; run pravo drift to compare them with the infrastructure.
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
| `pravo promote ENV` | 1 to 4, then 6 and 7 | Reads the notes of the environments that ENV follows, signs a promotion into its state, then applies as `pravo apply --env ENV`. |
| `pravo environments` | 1 and 2 | Reads the record and the newest note of the state of each environment, without a key, and starts no provider. |
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
