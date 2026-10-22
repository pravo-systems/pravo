<!-- Generated from docs/reference/mcp.md by tools/skillgen. Do not edit. -->

# pravo mcp

Serve pravo to an agent over the Model Context Protocol on stdio. Thirteen tools, applies only from saved plans, and plan policies the agent cannot remove.

## Quick reference

| | |
|---|---|
| Synopsis | `pravo mcp [--policy FILE]... [--actor LABEL] [--reason TEXT] [-C DIR]` |
| Transport | JSON-RPC 2.0 on standard input and output, one message per line. Protocol version `2024-11-05`. |
| Root | The directory the server was started in, or `-C DIR`. |
| How a tool runs | The server runs the `pravo` binary itself with `--json --no-color`, in the root, and returns what it printed. |
| Taught in | [Agents and MCP](../how-to/agents-and-mcp.md). |

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--policy FILE` | none | A CUE file that every plan and apply the server runs must satisfy. Repeatable. The path must be absolute. |
| `--actor LABEL` | `$PRAVO_ACTOR` | A label for who is acting. It is recorded on every transaction, lock and plan the agent causes. It is not verified. |
| `--reason TEXT` | `$PRAVO_REASON` | Why. Recorded with the actor. |

The [global flags](global-flags.md) apply.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The client closed standard input. |
| 5 | A `--policy` path is relative, missing or unusable. The server does not start. |

A tool call that fails does not end the server. Its exit code is returned in the result.

## Tools

| Tool | Arguments | Runs | `--policy` added |
|---|---|---|---|
| `pravo_validate` | `tags` | `validate` | no |
| `pravo_plan` | `tags`, `out`, `refresh` (`age`, `expired`, `false`, `all`), `full`, `include_skipped`, `reserve`, `destroy`, `replace` | `plan` | yes |
| `pravo_show_plan` | `planfile` (required) | `show PLANFILE` | no |
| `pravo_apply_plan` | `planfile` (required), `only` | `apply PLANFILE --only A,B` | yes |
| `pravo_schema` | `types` (required), `required` | `schema TYPE... --required` | no |
| `pravo_why` | `addrs` (required), `tags` | `why ADDR...` | no |
| `pravo_state_query` | `expression` (required), `tags` | `state query EXPRESSION` | no |
| `pravo_state_show` | `addr` (required), `tags` | `state show ADDR` | no |
| `pravo_dependents` | `addr` (required), `tags` | `state dependents ADDR` | no |
| `pravo_state_log` | `tags` | `state log` | no |
| `pravo_locks` | `tags` | `state locks` | no |
| `pravo_drift` | `tags`, `due`, `limit` | `drift --due --limit N` | no |
| `pravo_providers` | none | `providers` | no |

`tags` is a list of `key=value` strings. `only`, `replace`, `addrs` and `types` are lists of strings. `due` and `include_skipped` are booleans, and `limit` is a whole number above zero. There is no tool that applies without a saved plan, and none that edits the state.

The plan document that `pravo_plan` returns lists the nodes the plan ran. With `include_skipped` it lists the nodes the plan left alone too, each with its address, mode, type and name. `pravo_drift` with `due` reads the records that are past their refresh age, the oldest first and at most `limit` of them, and plans nothing; `limit` without `due` is refused by the command.

### The positional-argument rule

Five arguments reach the command line as a word of their own: `planfile`, `addr`, `addrs`, `types` and `expression`. The server refuses a call in which one of them is empty or begins with `-`, so that the command cannot read it as a flag. The refusal is the same with and without `--policy`.

It is a tool result with `isError` true. It is made before any `pravo` process runs, so it carries no `structuredContent`. `planfile` set to `-auto-approve` therefore does not make a plain apply, and `addr` set to `-C=/other` does not change the root. A value that passes the check is given to the command as it is. The server does not check that a plan file was reviewed.

`pravo_show_plan` and `pravo_apply_plan` take plan files only. A `planfile` that has the form of the id of a plan stored in the state, such as `plan-2f9c1a4e`, and that names no file, is refused in the same way, before any process runs. The message says to write the plan to a file with `pravo plan pull ID FILE`.

### Policies on the server

Each `--policy` is read and compiled when the server starts, so a bad path fails the start and not every later call. The server adds `--policy` to its `pravo_plan` and `pravo_apply_plan` runs, and to no other tool, since no other command takes the flag. A tool argument cannot remove a policy that the server names.

`pravo_apply_plan` always has a plan file, so the server never runs a plain apply. The saved plan is judged whole before anything is applied, as on the command line. A refusal reaches the agent as a result with `isError` true and `structuredContent.exit_code` 5. A plan that a policy refuses returns the plan document and then the error object, so the agent sees what was judged.

Without `--policy`, nothing judges the plan: `pravo_apply_plan` applies the file it is given when the file is not stale. A policy binds the tool and not the process around it. An agent with a shell or with credentials of its own does not need the tool. See [Policies](policies-overview.md) and [Agents and MCP](../how-to/agents-and-mcp.md).

### Results

| Field | Content |
|---|---|
| `content[0].text` | What the command printed on stdout. On a failure, what it printed on stderr follows. |
| `isError` | `true` when the exit code is not 0. |
| `structuredContent.exit_code` | The exit code of the command. Exit code 2, which means changes or drift, is a result and not a failure of the server. |

The `initialize` result carries an `instructions` string that tells the agent the order of work: `pravo_schema`, edit the files, `pravo_validate`, `pravo_plan` with `out`, then `pravo_apply_plan` on that file.

## Examples

Work in an empty directory named `demo`. The root has one resource.

File `main.cue`:

```cue
package demo

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}
```

```sh
pravo init > /dev/null
```

A client sends one JSON object per line. The server answers on standard output.

### Start a session

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"demo","version":"1"}}}' | pravo mcp
```

Output:

```text
{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"instructions":"pravo plans and applies infrastructure specified in CUE. Work in this order: pravo_schema to learn a type, edit the .cue files, pravo_validate until it passes, pravo_plan with out=<file> to produce a reviewable plan, then pravo_apply_plan on that file. Never try to bypass a refusal: a policy or a reservation is a decision by the operators.","protocolVersion":"2024-11-05","serverInfo":{"name":"pravo","version":"0.1.0-dev"}}}
```

The instructions contain `out=<file>`. The server writes `<`, `>` and `&` as they are and does not escape them.

### List the tools

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | pravo mcp | grep -o '"name":"pravo_[a-z_]*"'
```

Output:

```text
"name":"pravo_validate"
"name":"pravo_plan"
"name":"pravo_show_plan"
"name":"pravo_apply_plan"
"name":"pravo_schema"
"name":"pravo_why"
"name":"pravo_state_query"
"name":"pravo_state_show"
"name":"pravo_dependents"
"name":"pravo_state_log"
"name":"pravo_locks"
"name":"pravo_drift"
"name":"pravo_providers"
```

### Call a tool

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"pravo_validate","arguments":{}}}' | pravo mcp
```

Output:

```text
{"jsonrpc":"2.0","id":3,"result":{"content":[{"text":"{\n  \"calls\": 0,\n  \"data_sources\": 0,\n  \"ephemeral\": 0,\n  \"outputs\": 0,\n  \"providers\": 1,\n  \"resources\": 1,\n  \"valid\": true\n}","type":"text"}],"isError":false,"structuredContent":{"exit_code":0}}}
```

### A refused argument

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"pravo_apply_plan","arguments":{"planfile":"-auto-approve"}}}' | pravo mcp
```

Output:

```text
{"jsonrpc":"2.0","id":4,"result":{"content":[{"text":"planfile \"-auto-approve\" is not a usable value: it is empty or begins with \"-\"","type":"text"}],"isError":true}}
```

A tool that does not exist is a JSON-RPC error:

```sh
printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"pravo_destroy","arguments":{}}}' | pravo mcp
```

Output:

```text
{"jsonrpc":"2.0","id":5,"error":{"code":-32602,"message":"unknown tool pravo_destroy"}}
```

### A policy

A policy is a CUE file outside the directory of the specification. Start the server with its absolute path. A plan that the policy refuses comes back as an error result with exit code 5, and no plan file is written.

File `policy/keep.cue`:

```cue
summary: destroy: 0
```

```sh
pravo apply -auto-approve > /dev/null
printf '%s\n' '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"pravo_plan","arguments":{"destroy":true,"out":"x.plan"}}}' | pravo mcp --policy "$PWD/policy/keep.cue" | grep -o '"isError":[a-z]*\|"exit_code":[0-9]*'
```

Output:

```text
"isError":true
"exit_code":5
```

A relative path does not start the server:

```sh
pravo mcp --policy policy/keep.cue < /dev/null
```

Output:

```text
Error: --policy "policy/keep.cue": the server needs an absolute path
```

A client configuration passes the arguments and the working directory:

```json
{"mcpServers": {"pravo": {"command": "pravo", "args": ["mcp", "--actor", "agent", "--policy", "/srv/policies/keep.cue"], "cwd": "/path/to/root"}}}
```

## See also

- [Agents and MCP](../how-to/agents-and-mcp.md) shows the whole workflow.
- [Exit codes](exit-codes.md) lists the codes the tools return.
- [The plan document](plan-document.md) describes the JSON that `pravo_plan` returns.
