---
title: The library and the machine interface
description: "The three ways a program uses pravo: the commands with --json and exit codes, the MCP server for an agent, and the Go library for a program that plans and applies on behalf of its users. What each gives, what each never does, and which is the stable surface."
order: 16
minutes: 8
keywords: "json exit codes mcp agent go library embed sdk runner platform automation stable interface"
---

People use pravo through the commands. Programs use it three ways, and all three drive one engine, so a policy, a role, a lock and a trace mean the same thing whichever way a change arrives.

| Surface | For | Gives |
|---|---|---|
| The commands with `--json` | A script, a pipeline, a job. | One JSON document per command on standard output, events as lines where a command runs long, and an exit code that classifies the outcome. |
| `pravo mcp` | An agent that works in a checkout. | Fourteen tools over the Model Context Protocol, each of which runs a command, with the plan document and the exit code in the result. |
| The Go library, package `pravo` | A program that plans and applies on behalf of its users: a runner, a service, a platform. | A workspace in the process, with the plan, the apply, drift, discovery and the queries as methods, and nothing read from flags, the environment of the process or a prompt. |

## The commands with --json

Every command that reports something prints a document with `--json`: the plan, the drift report, the discovery report, the list of stored plans, the log of the state, the answer of a query. A command that runs long, `apply` and `destroy`, prints one event per line as it goes, so a pipeline can follow it. The exit code says what happened without parsing anything: 0 for success, 2 for a plan with changes or a drift found when the command is asked to say so, 3 for a plan that is stale, 4 for a lock or a reservation held by someone else, 5 for a refusal by a policy, a missing approval, a role or the readers file, 6 for a specification that does not evaluate, 7 for a state that failed verification, and 1 for anything else.

The plan document is the contract. It is what `pravo plan --json` prints, what a saved plan file holds, what a stored plan is in the state, what a policy is judged against and what the library returns from a plan, so a program that reads it reads the same thing everywhere. [The plan document](../reference/plan-document.md) lists its fields, [Global flags, environment and exit codes](../reference/global-flags.md#json) lists the document of each command, and [Exit codes](../reference/exit-codes.md) says what each code means for a pipeline.

The commands are the stable surface. The documents and the codes change only in ways that an existing reader can ignore, and a change that a reader cannot ignore is a new version of the document.

## The server for agents

`pravo mcp` serves pravo to an agent over the Model Context Protocol, on standard input and output. Each tool runs one command in the root the server was started in, with `--json`, and returns what the command printed with its exit code. The tools are the commands an agent needs in the order it works: read the schema of a type, validate, plan to a file or store the plan, show it, apply it, and read the state, the log, the locks and the drift. [`pravo mcp`](../reference/mcp.md) lists the tools and their arguments.

The server is built so that the agent cannot do more through it than a person can with the same identity, and some things less.

- There is no tool that applies without a plan. `pravo_apply_plan` takes a plan file or the id of a stored plan, and the plan is judged whole before anything is applied, as on the command line.
- There is no tool that edits the state. Moves, removals and imports are a person's commands.
- An argument that reaches the command line as a word of its own is refused when it is empty or begins with `-`, before any process runs, so no argument can be read as a flag.
- The policies in force judge every plan and apply, because the tool runs the command, and a tool argument cannot remove a policy. A refusal reaches the agent as an error with exit code 5 and the document that was judged.
- The server never asks for a passphrase. It opens a sealed state with the identity in its environment, and the roles of that identity apply: an agent with a reader's identity plans and stores plans, and a writer applies them.
- `--actor` labels every transaction, lock and plan the agent causes, and `--reason` says why. Neither is verified; the signature of a sealed state is.

A policy binds the tool and not the process around it. An agent with a shell next to the server, or with cloud credentials of its own, does not need the tool. [Agents and MCP](../how-to/agents-and-mcp.md) sets a server up, and [`pravo skill`](../reference/skill.md) installs the skill that teaches an agent to use it.

## The Go library

The package `github.com/pravo-systems/pravo/pravo` is what the command line is built on, offered to a program. `pravo.Open` loads a specification, reads the lock file, opens the state and resolves the providers, and returns a workspace whose methods are the commands: `Plan`, then `CheckPolicy`, `Store` or `Apply`; `ApplyStored` for a plan that waits in the state; `Drift`, `DriftDue`, `Why`, `Discover`, `DiscoveryReport`, `QueryState` and `Dependents`. [The Go library](../reference/library.md) lists them, and [Embed pravo in your program](../how-to/embed-pravo-in-your-program.md) builds a small runner.

The library differs from the commands in what it does not do. It reads no flag and no variable of the process: the directory, the tags, the environment the specification sees, the credentials of the state and the identity for a sealed state are all given in `Config`, so a service runs workspaces on different directories in one process, each with an environment of its own. It prints nothing: progress, warnings and the log go to functions the program supplies. It never prompts: a sealed state is opened with the identity that a function of the program returns, from a vault or from the identity of a job, and a program that cannot answer returns an error. An error carries the exit code the command would give, so a program classifies a failure with `pravo.CodeOf` and not by reading its text.

The library is unstable, at v0, and a release may break its callers until it is declared stable. The documents it returns are the same JSON that the commands print, so a program that wants a contract that holds across releases keeps to them: the plan document, the drift report, the discovery report and the events of an apply.

## Which one

| You are writing | Use |
|---|---|
| A CI job that plans on a pull request and applies on merge. | The commands. `pravo plan --store`, `pravo apply ID` and the exit codes do the work, and the job needs no code. |
| A script that asks a question of the estate: what depends on this, which records are older than a day, what changed outside pravo. | The commands with `--json`, and `jq`. |
| An agent that edits a specification and proposes a change. | `pravo mcp`, with the identity of a reader, and a person or a job with a writer's identity to apply what it stores. |
| A service that plans and applies for many teams and many estates, with its own rules about who may do what. | The library. It opens a workspace per request with the identity of that request, judges with `CheckPolicy`, and applies under the locks. |
| A tool that reads what pravo produced: a plan file, a stored plan, the state. | The documents. A plan file and a stored plan are the plan document, and `pravo show --json` and `pravo state show --json` print them without a program of your own. |

## One engine

Whatever the surface, the same things hold. A plan is judged by the policies in force in the state, under the locks, at the apply. A sealed state opens for a reader, is changed by a writer and has its readers and roles changed by an admin, whether the identity came from the environment of a command, of the server, or from a function of a program. A transaction is signed by that identity. A run is recorded, with `PRAVO_TRACES=on` or an exporter, with the same spans, and the actor and the reason are on it. The viewer shows what any of them did.

## Recap

- Three surfaces, one engine: the commands with `--json` and exit codes, the MCP server for an agent, and the Go library for a program.
- The JSON documents and the exit codes are the stable surface. The library is v0.
- The server applies only from a plan, edits no state, refuses an argument that could be a flag, and is judged by the policies like any apply.
- The library reads nothing from the process, prints nothing and prompts never. What it is given in `Config` is all it knows.

[Embed pravo in your program](../how-to/embed-pravo-in-your-program.md) builds a runner on the library.
