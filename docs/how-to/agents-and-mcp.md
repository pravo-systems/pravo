---
title: Let an agent plan and apply
description: Give a program or an AI agent the schema, a plan file, a reservation, a tool server that the policies of the state bind, and an identity with the role of a reader, so that it plans and stores and a person applies, and see what that does and does not protect.
order: 3
minutes: 20
needs: "[Run pravo in a pipeline](ci.md) for `--json` and exit codes. [Write and roll out policies](policies.md) for the policy, and [Give people roles](give-people-roles.md) for the roles. `jq`."
done_when: "An agent plan is refused by the policy with exit code 5 through `pravo mcp`, a second actor is told to wait with exit code 4, and an apply by the agent's own identity is refused with exit code 5 while its stored plan waits for a person."
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
  - pattern: 'roles-genesis [0-9a-f]{64}'
    replace: 'roles-genesis <digest>'
  - pattern: 'plan-[0-9a-f]{8}'
    replace: 'plan-<id>'
group: Operate
keywords: "mcp model context protocol agent tools"
---

pravo gives a program or an agent data, exit codes and plan files. The commands that report a result print it with `--json` ([the list is in global flags](../reference/global-flags.md#json)), the stops that a pipeline must tell apart have their own exit codes, and a plan is a file that someone can review before it runs. This page uses those parts, runs the agent's tool server under a policy and states what none of it prevents.

> [!WARNING]
> In a state that is not sealed, `--actor` is a label that the caller types, and another caller can type another one. In a sealed state pravo ignores the label: the actor is the name of the identity that opens the state, which signs what it does, and its role bounds what it may do. This page seals its state, so the agent has an identity from the start. Sections 1 to 5 are checks for a cooperating tool and a record of what happened. Section 6 gives that identity the role of a reader, which is the one boundary that pravo itself holds. None of it is a boundary against a caller that holds the cloud credentials, and the last section lists what to use for one.

Work in an empty directory. The root is `root/`. It declares a policy, `no_destroy`, and its state is sealed to two readers, Ada and the agent, so that the policy can be signed and each of them has a name.

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
	rule: {
		// A managed resource is neither deleted nor replaced.
		changes: [...{
			addr:   string
			mode:   string
			action: string
			if mode == "managed" && (action == "delete" || action == "replace") {
				error("\(addr): \(action) is not allowed")
			}
		}]
	}
}
```

```sh run hidden
mkdir -p root/.pravo/access
pravo -C root id new --name ada | grep '^  ada ssh' | sed 's/^  //' > root/.pravo/access/readers.txt
pravo id new --ci --name agent --out agent.identity > /dev/null
echo "$(PRAVO_IDENTITY="$(cat agent.identity)" pravo -C root id show | tail -n 1 | sed 's/^ *//')" >> root/.pravo/access/readers.txt
pravo -C root init > /dev/null
pravo -C root protect init --encrypt --yes --allow-local > /dev/null
```

```sh run
pravo -C root apply -auto-approve --reason "first apply" | grep '^Apply complete'
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

The agent adds a resource `c` and saves a plan. It runs as its own identity: `PRAVO_IDENTITY` holds the identity, and `PRAVO_CONFIG_DIR=nobody` keeps pravo from finding Ada's key as well. `--reason` says why. `-reserve=15m` claims the nodes that have pending changes for fifteen minutes.

```cue file=root/c.cue title="root/c.cue"
package agents

c: $random.#Resource.random_pet & {length: 2}
```

```sh run
PRAVO_CONFIG_DIR=nobody PRAVO_IDENTITY="$(cat agent.identity)" pravo -C root plan -out change.plan --reason "a third name" -reserve=15m | grep -E '^(Plan|Reserved|Saved)'
```

```text out
Plan: 1 to add, 0 to change, 0 to destroy.
Reserved 1 node until <time>.
Saved the plan to change.plan. To apply exactly these actions, run:
```

The plan file is a complete record of what runs. `pravo show change.plan` prints it with the name of the actor, and `--json` gives the same as data.

```sh run
pravo -C root show change.plan --json | jq -c '.plan.actor, .plan.reason, (.plan.changes[] | select(.action != "no-op") | {addr, action})'
```

```text out
"agent"
"a third name"
{"addr":"c","action":"create"}
```

## 3. Tell another actor to wait

Ada plans the same change.

```sh run exit=4
pravo -C root plan -refresh=false
```

```text out
Error: plan rejected: c is reserved by agent (pravo -C root plan --out change.plan --reason <sealed> --reserve=15m until <time>, since <time>)
```

Exit code 4 means that a node is locked or reserved. Two cooperating agents that work on separate parts of the graph do not collide, and an overlap is reported at plan time instead of halfway through an apply. `pravo state locks` lists the reservations, and `pravo state unlock <address>` releases one.

```sh run
pravo -C root state locks
pravo -C root state log | grep -c ' rejected '
```

```text out
c  reserved by agent until <time>: pravo -C root plan --out change.plan --reason <sealed> --reserve=15m
1
```

The refused attempt is recorded too. A reservation is coordination, not protection: the owner is a name, anyone can release it, and the owner's own commands are not refused by it. In a sealed state the owner is the name of the identity that signs. Without a seal it is the `--actor` label, or the name of the operating system user when there is none.

## 4. Give the agent the tools

`pravo mcp` serves pravo's commands to an agent over the Model Context Protocol on standard input and output. The server needs no policy argument: it runs the same commands, and they judge with the policies of the state.

A client configuration has this shape. The key names differ between clients.

```json
{"mcpServers": {"pravo": {"command": "pravo", "args": ["mcp"], "cwd": "/path/to/root"}}}
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
PRAVO_CONFIG_DIR=nobody PRAVO_IDENTITY="$(cat agent.identity)" pravo -C root mcp < calls.jsonl > replies.jsonl
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

`pravo why` lists the reasons behind a change, so that a person or an agent can check a plan against what was intended. It plans while it explains, so it runs as the owner of the reservation, the agent.

```sh run
PRAVO_CONFIG_DIR=nobody PRAVO_IDENTITY="$(cat agent.identity)" pravo -C root why a
```

```text out
a: replace
  - the specification changed since the last apply (hash f2caf74c767a7efa -> df0a9c7e3f0c3d57)
  - refreshed because the record is older than its refresh age; no drift
  - the provider cannot change length in place; replacement required
```

## 6. Give the agent an identity of its own

Everything so far takes the agent at its word. In a sealed state with [roles](give-people-roles.md), the identity that opens the state is not a word: it signs what it does, and its role says what it may do. Give the agent an identity with the role of a reader. It can then read, plan, ask why and store a plan, and it cannot apply, move or remove anything, whatever label it types. A person, or the apply job, applies what it stored.

First put `a` back as it was, so that the policy has nothing to refuse, and release the reservation of step 2. Then give ada the role of an admin and the agent, whose identity was made [as a CI job gets one](seal-the-state.md#9-let-a-ci-job-read), the role of a reader:

```sh run
sed -i.bak 's/length: 3/length: 2/' root/main.cue && rm root/main.cue.bak
pravo -C root state unlock c
sed -i.bak -e 's/^ada \(.*\)$/ada \1 role:admin/' -e 's/^agent \(.*\)$/agent \1 role:reader/' root/.pravo/access/readers.txt && rm root/.pravo/access/readers.txt.bak
pravo -C root protect roles --yes | tail -n 1
```

```text out
Unlocked c
Added roles-genesis <digest> to .pravo/access/readers.txt. Commit it: until it is in the repository the roles of the file decide.
```

The agent's identity is the secret of the agent's environment, `PRAVO_IDENTITY`, and the server passes its environment to the commands it runs. The calls below plan, save the plan to a file, store it in the state, and try to apply the file:

```json file=calls-reader.jsonl title="calls-reader.jsonl"
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pravo_plan","arguments":{"out":"agent.plan","store":true}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pravo_apply_plan","arguments":{"planfile":"agent.plan"}}}
```

```sh run
PRAVO_CONFIG_DIR=nobody PRAVO_IDENTITY="$(cat agent.identity)" pravo -C root mcp < calls-reader.jsonl > replies-reader.jsonl
jq -c 'select(.id == 2 or .id == 3) | {id, isError: .result.isError, exit_code: .result.structuredContent.exit_code}' replies-reader.jsonl
jq -r 'select(.id == 3) | .result.content[0].text' replies-reader.jsonl | grep '^Error'
pravo -C root plan list
```

```text out
{"id":2,"isError":false,"exit_code":0}
{"id":3,"isError":true,"exit_code":5}
Error: your identity is a reader in .pravo/access/readers.txt, and a writer is needed to change the state.
plan-<id>  pending  agent  just now  +1 ~0 -0  expires in 7 days
```

The plan is stored, signed by the agent's key, and the apply is refused before it changes anything. Ada reads the plan, with `pravo plan show` or in the Plans section of the viewer, and applies it by its id. The transaction is signed by the key that applied, and the plan keeps the signature of the key that stored it:

```sh run
ID=$(pravo -C root plan list | awk 'NR==1 {print $1}')
pravo -C root apply "$ID" --auto-approve | grep '^Apply complete'
pravo -C root state log | grep -m1 'signed by'
```

```text out
Apply complete! Resources: 1 added, 0 changed, 0 destroyed. [txn <id>]
    signed by ada (verified)
```

Where a policy asks for approvals, the stored plan waits for them as well, and the agent's own key never counts: [Approve a plan](approve-a-plan.md). A server that runs with no identity at all can list addresses and gets exit code 5 from every tool that needs a value.

## Instructions for the agent

Paste this into the instructions of an agent that works in a pravo root. Every command in it is covered above. In a state that is not sealed, the label after `--actor` is recorded with each transaction, and pravo does not verify it. In a sealed state the label is ignored: the identity names the actor.

```text title="prompt"
You operate infrastructure with pravo in this directory.

1. Read before you write: pravo schema <type> --required
2. Validate until it exits 0: pravo validate
3. Plan to a file and read every change: pravo plan --out change.plan --json
4. Apply only that file: pravo apply change.plan
5. Give every command a reason: --reason "<why>"

Exit code 3 means the plan is stale: plan again.
Exit code 4 means another actor holds a node: wait.
Exit code 5 means a policy refused the plan. Exit code 6 means the specification is invalid.
If pravo says that your identity is a reader, store the plan instead (pravo plan --store) and report its id: a person applies it.
Do not try to bypass a refusal: a policy, a reservation or a role is a decision by the operators. Report it and stop.
```

## Give the agent the skill

The instructions above are what an agent needs to run pravo safely. They do not teach it to write a specification. An agent that has not seen pravo writes HCL habits into CUE: `count`, variables files, module blocks. The skill is the guide and the documentation that prevent that.

```sh
pravo skill install
```

This writes `~/.claude/skills/pravo`, or `$CLAUDE_CONFIG_DIR/skills/pravo`, and Claude loads the skill when a task is about pravo. The skill is part of the binary, so it always matches the version that is installed. For an agent without a skills directory, install it into the project and name the guide in `AGENTS.md`: `pravo skill install .agents`, then "Read .agents/pravo/SKILL.md before you write or run pravo." Each release also carries `pravo-skill.zip` for tools that add a skill from an archive. [pravo skill](../reference/skill.md) describes the files and how they are kept current.

## What this does not prevent

A policy binds every pravo that opens the state, because the versions are in the state and the apply judges them, and a role binds every pravo that opens a sealed state with an identity. Neither binds a caller that does not run pravo. An agent with the cloud credentials needs no pravo at all. An agent that can edit the specification can change a policy, and the plan that carries the change needs a writer to apply it and, where the policy asks for approvals, an admin to approve it. A pravo that is old enough not to know format 4 refuses the state.

For a boundary, use controls outside pravo as well:

- **Credentials.** Give the agent credentials that can only read and plan. Keep the ones that change infrastructure in a separate job that applies reviewed plans, a file or a stored plan by its id.
- **The repository.** Require review for the policy nodes of the specification and for every job or tool configuration. Apply only from the main branch.
- **The state store.** Limit who can write to the bucket or directory that holds the state, and give its credentials by role. To keep the values in it from an agent that has no need of them, [seal the state](seal-the-state.md) and give the agent no identity: it can list addresses and cannot read an attribute. An agent that needs to plan gets a reader's identity, as in step 6.
- **Secrets of the state.** A run that reads a secret from the sealed state needs the identity of a reader, and a reader can print every secret with `pravo secret get`. The MCP server has no tool that returns one, but an agent with a shell and that identity is not limited to the tools. Give an agent that must plan a specification which reads secrets an identity of its own, so that its key can be dropped from the readers file ([Store secrets in the sealed state](store-secrets-in-the-state.md#10-when-a-reader-leaves) shows the steps), and keep the secrets that it must not read in another state.
- **Exceptions.** No flag turns a policy off. A change to a policy is a change to the specification, and review sees it.

[Constraints and policies](../concepts/policies.md) explains the model, [Signatures and roles](../concepts/signatures-and-roles.md) what an identity proves, [pravo mcp](../reference/mcp.md) lists every tool and [pravo skill](../reference/skill.md) describes the skill.

## What next

- [Write and roll out policies](policies.md) writes and tests a policy, and [Approve a plan](approve-a-plan.md) makes the agent's stored plan wait for a person.
- [Give people roles](give-people-roles.md) sets up the roles that step 6 uses.
- [Embed pravo in your program](embed-pravo-in-your-program.md) is for a program that calls pravo in its own process rather than through a tool server.
- [Find out why a run fails](debugging.md) reads the log of a run, and [Record what a run does](record-what-a-run-does.md) follows an agent's work, tool call by tool call, in a trace.
