---
title: Let an agent plan and apply
description: Give a program or an AI agent the schema, a plan file, a reservation and a tool server that the policies of the state bind, and see what that does and does not protect.
order: 9
minutes: 15
needs: "[Run pravo in a pipeline](ci.md) for `--json` and exit codes. [Write and roll out policies](policies.md) for the policy. `jq`."
done_when: "An agent plan is refused by the policy with exit code 5 through `pravo mcp`, and a second actor is told to wait with exit code 4."
level: advanced
env:
  PRAVO_PASSPHRASE: an example passphrase
  PRAVO_CONFIG_DIR: people/ada
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
  - pattern: '\[id=[^\]]+\]'
    replace: '[id=<id>]'
  - pattern: '<sealed:[0-9a-f]+>'
    replace: '<sealed>'
  - pattern: 'sha256:[0-9a-f]{12}'
    replace: 'sha256:<digest>'
  - pattern: '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}(Z|[+-][0-9:]{5})'
    replace: '<time>'
group: Operate
keywords: "mcp model context protocol agent tools"
---

pravo gives a program or an agent data, exit codes and plan files. The commands that report a result print it with `--json` ([the list is in global flags](../reference/global-flags.md#json)), the stops that a pipeline must tell apart have their own exit codes, and a plan is a file that someone can review before it runs. This page uses those parts, runs the agent's tool server under a policy and states what none of it prevents.

> [!WARNING]
> pravo does not know who is calling it. `--actor agent` is a label that the caller types, and another caller can type another one. Everything below is a set of checks for a cooperating tool and a record of what happened. It is not a boundary against a caller that holds the specification, the job definition, the state or the cloud credentials. The last section lists what to use for a boundary.

Work in an empty directory. The root is `root/`. It declares a policy, `no_destroy`, and its state is sealed to one reader so that the policy can be signed.

```cue file=root/main.cue title="root/main.cue"
package agents

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 2}
b: $random.#Resource.random_pet & {length: 2}
```

```cue file=root/policy.cue title="root/policy.cue"
package agents

import "pravo.dev/pravo"

no_destroy: pravo.#Policy & {
	rule: #"""
		// A managed resource is neither deleted nor replaced.
		changes: [...{
			addr:   string
			mode:   string
			action: string
			if mode == "managed" && (action == "delete" || action == "replace") {
				error("\(addr): \(action) is not allowed")
			}
		}]
		"""#
}
```

```sh run hidden
mkdir -p root/.pravo/access
pravo -C root id new --name ada | grep '^  ada ssh' | sed 's/^  //' > root/.pravo/access/readers.txt
pravo -C root init > /dev/null
pravo -C root protect init --encrypt --yes --allow-local > /dev/null
```

```sh run
pravo -C root apply -auto-approve --actor alice --reason "first apply" | grep '^Apply complete'
```

```text out
Apply complete! Resources: 2 added, 0 changed, 0 destroyed. [txn <id>]
```

## 1. Let the agent read before it writes

An agent should not guess the shape of a resource. `pravo schema` prints the CUE definition of a type with the provider's documentation, and `--required` keeps what must be set. The comments are left out here.

```sh run
pravo -C root schema random_integer --required | grep -v '^[[:space:]]*//'
```

```text out
random_integer: {
	$kind:   "resource"
	$type:   "random_integer"
	$source: "registry.opentofu.org/hashicorp/random"
	$from?: __string
	$import?: __string | {...}
	$depends_on?: [..._]
	$lifecycle?: #Lifecycle
	max: number
	min: number
}
```

The agent writes a resource by unifying a struct with `$random.#Resource.random_integer`, where `$random` is a provider struct in the root. `pravo validate` refuses a misspelt attribute before any provider starts.

## 2. Plan to a file and reserve

The agent adds a resource `c` and saves a plan. `--actor` and `--reason` label it. `-reserve=15m` claims the nodes that have pending changes for fifteen minutes.

```cue file=root/c.cue title="root/c.cue"
package agents

c: $random.#Resource.random_pet & {length: 2}
```

```sh run
pravo -C root plan -out change.plan --actor agent --reason "a third name" -reserve=15m | grep -E '^(Plan|Reserved|Saved)'
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Reserved 1 node until <time>.
Saved the plan to change.plan. To apply exactly these actions, run:
```

The plan file is a complete record of what runs. `pravo show change.plan` prints it with the label, and `--json` gives the same as data.

```sh run
pravo -C root show change.plan --json | jq -c '.plan.actor, .plan.reason, (.plan.changes[] | select(.action != "no-op") | {addr, action})'
```

```text out
"agent"
"a third name"
{"addr":"c","action":"create"}
```

## 3. Tell another actor to wait

Another tool plans the same change under its own label.

```sh run exit=4
pravo -C root plan -refresh=false --actor bob
```

```text out
Error: plan rejected: c is reserved by agent (pravo -C root plan --out change.plan --actor <sealed> --reason <sealed> --reserve=15m until <time>, since <time>)
```

Exit code 4 means that a node is locked or reserved. Two cooperating agents that work on separate parts of the graph do not collide, and an overlap is reported at plan time instead of halfway through an apply. `pravo state locks` lists the reservations, and `pravo state unlock <address>` releases one.

```sh run
pravo -C root state locks
pravo -C root state log | grep -E '^ +actor'
```

```text out
c  reserved by agent until <time>: pravo -C root plan --out change.plan --actor <sealed> --reason <sealed> --reserve=15m
    actor bob
    actor alice  first apply
```

The refused attempt is recorded too. A reservation is coordination, not protection: the owner is a name, anyone can release it, and the owner's own commands are not refused by it. The owner is the `--actor` label, or the name of the operating system user when there is none.

## 4. Give the agent the tools

`pravo mcp` serves pravo's commands to an agent over the Model Context Protocol on standard input and output. The server needs no policy argument: it runs the same commands, and they judge with the policies of the state.

A client configuration has this shape. The key names differ between clients.

```json
{"mcpServers": {"pravo": {"command": "pravo", "args": ["mcp", "--actor", "agent"], "cwd": "/path/to/root"}}}
```

To see what the agent sees, run the server by hand. First the agent edits `a` in a way that forces a replacement.

```cue file=root/main.cue title="root/main.cue" hl="7"
package agents

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

a: $random.#Resource.random_pet & {length: 3}
b: $random.#Resource.random_pet & {length: 2}
```

The file holds the requests that a client sends: the handshake, a list of the tools, a plan that replaces `a`, and a call whose `planfile` is `-auto-approve`. A command line reads that value as a flag.

```json file=calls.jsonl title="calls.jsonl"
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pravo_plan","arguments":{"out":"agent.plan"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"pravo_apply_plan","arguments":{"planfile":"-auto-approve"}}}
```

```sh run
pravo -C root mcp --actor agent < calls.jsonl > replies.jsonl
jq -c 'select(.id == 2) | [.result.tools[].name]' replies.jsonl
jq -c 'select(.id == 3 or .id == 4) | {id, isError: .result.isError, exit_code: .result.structuredContent.exit_code}' replies.jsonl
```

```text out
["pravo_validate","pravo_plan","pravo_plan_list","pravo_show_plan","pravo_apply_plan","pravo_schema","pravo_why","pravo_state_query","pravo_state_show","pravo_dependents","pravo_state_log","pravo_locks","pravo_drift","pravo_providers"]
{"id":3,"isError":true,"exit_code":5}
{"id":4,"isError":true,"exit_code":null}
```

Both calls came back as tool errors. The plan was refused by the policy, with the exit code of the pravo process. The second call has no exit code, because the server refused it itself, before any pravo process ran. The reply to the first one carries the plan document and then the refusal, which is what the agent reads:

```sh run
jq -r 'select(.id == 3) | .result.content[0].text' replies.jsonl | grep -A2 '^Error'
jq -r 'select(.id == 4) | .result.content[0].text' replies.jsonl
```

```text out
Error: the plan violates the policy no_destroy@1 (sha256:<digest>):
changes.0: a: replace is not allowed:
    no_destroy:7:3
planfile "-auto-approve" is not a usable value: it is empty or begins with "-"
```

No tool argument turns the judging off. The server applies only from saved plan files, and refuses the id of a plan stored in the state. It refuses an `addr`, `addrs`, `types`, `expression` or `planfile` that is empty or begins with `-`. Its instructions to the agent give the order of work and say to treat a refusal as a decision by the operators.

The tools take the options of the commands that a scheduled job uses. `pravo_plan` takes `refresh` (`age`, `expired`, `false` or `all`) and `include_skipped`, which adds the nodes the plan left alone to the document. `pravo_drift` takes `due` and `limit`, the sweep of [CI](ci.md) that reads the oldest records past their refresh age.

## 5. Ask why

`pravo why` lists the reasons behind a change, so that a person or an agent can check a plan against what was intended. It plans while it explains, so it runs as the owner of the reservation.

```sh run
pravo -C root why a --actor agent
```

```text out
a: replace
  - the specification changed since the last apply (hash f2caf74c767a7efa -> df0a9c7e3f0c3d57)
  - refreshed because the record is older than its refresh age; no drift
  - the provider cannot change length in place; replacement required
```

## Instructions for the agent

Paste this into the instructions of an agent that works in a pravo root. Every command in it is covered above. The label after `--actor` is recorded with each transaction. pravo does not verify it.

```text title="prompt"
You operate infrastructure with pravo in this directory.

1. Read before you write: pravo schema <type> --required
2. Validate until it exits 0: pravo validate
3. Plan to a file and read every change: pravo plan --out change.plan --json
4. Apply only that file: pravo apply change.plan
5. Label every command: --actor agent --reason "<why>"

Exit code 3 means the plan is stale: plan again.
Exit code 4 means another actor holds a node: wait.
Exit code 5 means a policy refused the plan. Exit code 6 means the specification is invalid.
Do not try to bypass a refusal: a policy or a reservation is a decision by the operators. Report it and stop.
```

## Give the agent the skill

The instructions above are what an agent needs to run pravo safely. They do not teach it to write a specification. An agent that has not seen pravo writes HCL habits into CUE: `count`, variables files, module blocks. The skill is the guide and the documentation that prevent that.

```sh
pravo skill install
```

This writes `~/.claude/skills/pravo`, or `$CLAUDE_CONFIG_DIR/skills/pravo`, and Claude loads the skill when a task is about pravo. The skill is part of the binary, so it always matches the version that is installed. For an agent without a skills directory, install it into the project and name the guide in `AGENTS.md`: `pravo skill install .agents`, then "Read .agents/pravo/SKILL.md before you write or run pravo." Each release also carries `pravo-skill.zip` for tools that add a skill from an archive. [pravo skill](../reference/skill.md) describes the files and how they are kept current.

## What this does not prevent

A policy binds every pravo that opens the state, because the versions are in the state and the apply judges them. It does not bind a caller that does not run pravo. An agent with the cloud credentials needs no pravo at all. An agent that can edit the specification and apply can change a policy, and the new version is signed with the name of whoever applied it. A pravo that is old enough not to know format 4 refuses the state.

For a boundary, use controls outside pravo:

- **Credentials.** Give the agent credentials that can only read and plan. Keep the ones that change infrastructure in a separate job that applies reviewed plan files.
- **The repository.** Require review for the policy nodes of the specification and for every job or tool configuration. Apply only from the main branch.
- **The state store.** Limit who can write to the bucket or directory that holds the state. To keep the values in it from an agent that has no need of them, [seal the state](seal-the-state.md) and give the agent no identity: it can list addresses and cannot read an attribute.
- **Exceptions.** No flag turns a policy off. A change to a policy is a change to the specification, and review sees it.

[Policies](../concepts/policies.md) explains the model, [pravo mcp](../reference/mcp.md) lists every tool and [pravo skill](../reference/skill.md) describes the skill.

## What next

- [Write and roll out policies](policies.md) writes and tests a policy.
- [Find out why a run fails](debugging.md) reads the log of a run, and telemetry follows an agent's work from the state into a trace.
