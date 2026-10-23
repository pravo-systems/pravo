<!-- Generated from docs/reference/global-flags.md by tools/skillgen. Do not edit. -->

# Global flags, environment and exit codes

The flags every pravo command accepts, the flags several commands share, the debug log, JSON output, the environment variables and where the exit codes are listed.

## Quick reference

### Global flags

These flags belong to the command `pravo` and every subcommand accepts them.

| Flag | Default | Meaning |
|---|---|---|
| `-C`, `--chdir DIR` | none | Run as if started in `DIR`. Relative paths in other flags, such as `--policy`, are relative to `DIR`. |
| `--no-color` | off | Do not colour the output. Colour is used only on a terminal. |
| `-v` | off | Log what pravo decides and does to stderr. See [The debug log](#the-debug-log). |
| `-vv` | off | Finer detail, and the log of the providers. |
| `--json` | off | Machine-readable output on stdout, progress on stderr. See [JSON](#json). |
| `--actor LABEL` | `$PRAVO_ACTOR` | A label for who is acting. Recorded on transactions, locks, reservations and plans, and it is the author that `pravo plan list` shows for a stored plan. It is not verified. |
| `--reason TEXT` | `$PRAVO_REASON` | Why. Recorded with the actor. On a plan, it is the note that the viewer and `pravo plan show` give. An apply of a saved or stored plan records the reason of the plan, unless it is given its own. |
| `--identity NAME` | `$PRAVO_IDENTITY` | The identity file to read a sealed state with, from the identity directory. It wins over `$PRAVO_IDENTITY`, and it matters only for a state that is sealed. See [`pravo id and pravo protect`](protect.md#the-identity). |
| `-h`, `--help` | | Print the help of the command. |

`pravo help COMMAND` prints the same help. `pravo completion bash`, `zsh`, `fish` or `powershell` prints a completion script for that shell.

### Flag syntax

A long flag takes one dash or two: `-auto-approve` and `--auto-approve` are the same flag. `-v` repeated, `-vv`, is the verbose flag twice. A value follows `=` or a space, except for `--refresh` of `plan`, `apply` and `destroy`, where the value follows `=` only. `--refresh` alone means `all`, so in `-refresh false` the word `false` is an argument.

A command that takes no argument refuses a word, so a misplaced value is an error with exit code 1. Write the value of `--refresh` with `=`. See [A word after a flag](#a-word-after-a-flag).

### Flags that several commands share

| Flag | Commands | Meaning |
|---|---|---|
| `-t`, `--tag key=value` | `init`, `plan`, `apply`, `destroy`, `output`, `import`, `providers`, `gen`, `validate`, `eval`, `schema`, `why`, `drift`, `serve`, `state` and each of its subcommands | Inject a value into the field tagged `@tag(key)`. Repeatable. |
| `-T`, `--inject-vars` | the same | Inject the system variables of CUE into tags written `@tag(name,var=os)`. Examples are `os`, `arch` and `username`. |
| `--refresh=age\|expired\|false\|all` | `plan`, `apply`, `destroy`, `why` | When to read real state from the providers. See [`pravo plan`](plan.md#refresh). |
| `--full` | `plan`, `apply` | Plan every resource with its provider. |
| `--policy FILE` | `plan`, `apply`, `destroy`, `mcp` | A CUE file the plan must satisfy. Repeatable. |
| `--access-dir DIR` | `protect`, `serve` | The directory of the readers file of a sealed state, in place of `.pravo/access`. `PRAVO_ACCESS_DIR` sets the same, and the flag wins. A relative directory is relative to the root. See [`pravo id and pravo protect`](protect.md#the-readers-file). |

`pravo show`, `pravo fmt`, `pravo version` and `pravo mod` take no tags. Use the same tags for `plan` and `apply`. Otherwise they evaluate different specifications. A saved plan carries its own.

`pravo output` has its own `--json` flag that replaces the global one.

## Examples

Work in an empty directory named `demo`.

File `app/main.cue`:

```cue
package app

import rnd "pravo.dev/providers/hashicorp/random"

$random: rnd.#Provider

base: $random.#Resource.random_integer & {min: 1, max: 9, seed: "base"}
```

### Another directory

```sh
pravo -C app init > /dev/null
pravo -C app validate
```

Output:

```text
Success! 1 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 1 providers.
```

### A word after a flag

`--refresh` takes its value only after `=`. Without it the flag means `all`, and the next word is an argument. `plan` takes none, so it refuses the word:

```sh
pravo -C app plan -refresh false
```

Output:

```text
Error: pravo plan takes no arguments, but got "false"; write the value of -refresh with =, as in -refresh=false
```

### The debug log

`-v` writes pravo's own account of a run to stderr. It says what was loaded, where each provider version came from, which state was opened and how the graph was built. For each task it gives the action and the reason. Each line starts with the time since the command began. Nothing is printed without `-v`.

```sh
pravo -C app plan -v 2>&1 | grep -E ' (loaded|state opened|plan|graph built|task) '
```

Output:

```text
+0.000s debug loaded dir=./app managed=1 data=0 ephemeral=0 calls=0 outputs=0 providers=1 moves=0 imports=0 took=<time>
+0.000s debug state opened backend=local location=./app/pravo.state nodes=0 took=<time>
+0.000s debug plan refresh=age full=false destroy=false tasks=1
+0.000s debug graph built tasks=1 took=<time>
+0.000s debug task addr=base phase=plan outcome=ok took=<time> action=create why="not in the state: a new resource"
```

The `task` line carries the reasons that `pravo why` prints. Values of attributes are never logged. The name of an environment variable is logged and its value is not.

`-vv` adds the finest detail: the dependencies of each task and when it starts. It also turns on the log of the providers, which is off otherwise.

```sh
pravo -C app plan -vv 2>&1 | grep -E ' trace (graph node|task start) '
```

Output:

```text
+0.000s trace graph node addr=base deps=
+0.000s trace task start addr=base phase=plan deps=
```

`PRAVO_LOG=debug` or `trace` does the same as `-v` or `-vv` for pravo, and sets the level of the log of the providers. `info`, `warn` and `error` affect the log of the providers only.

### JSON

With `--json`, stdout holds JSON and nothing else. Human progress lines go to stderr.

| Command | stdout |
|---|---|
| `plan`, `show`, `why`, `drift`, `state show`, `state log`, `state locks`, `providers`, `validate`, `schema`, `mod list`, `mod verify` | One JSON document. |
| `state query` | The value as JSON. |
| `apply`, `destroy` | Newline-delimited events, and a summary line last. |
| `mcp` | Not affected. The server speaks JSON-RPC. |
| `serve` | One line: the address of the viewer, its token and its port. See [`pravo serve`](serve.md). |
| `skill install`, `skill list` | One JSON document. See [`pravo skill`](skill.md). |

An error prints `{"code":N,"kind":"error","message":"..."}` on stdout, where `N` is the exit code, and the text of the error on stderr.

```sh
pravo show missing.plan --json
```

Output:

```text
{"code":1,"kind":"error","message":"open missing.plan: no such file or directory"}

Error: open missing.plan: no such file or directory
```

`apply` and `destroy` emit one event per step. A plain `apply` with changes to make needs `--auto-approve` with `--json`, since nobody can answer the prompt. An event has these fields; those marked "if set" are left out when empty.

| Field | Content |
|---|---|
| `time` | When the event happened, in UTC. |
| `kind` | `refresh`, `read`, `open`, `call`, `create`, `update`, `replace`, `destroy`, `lock`, `note`, `plan` or `apply`. |
| `phase` | `start`, `done`, `skip` or `error`. |
| `addr`, `message`, `id`, `elapsed_ms`, `trace_id` | The address of the resource, the line a person reads, the provider id, the duration in milliseconds, and the trace id (each if set). |
| `txn` | The transaction. |

The event of kind `plan` carries the plan document. The last event, of kind `apply`, has `added`, `changed`, `destroyed`, `outputs`, `planfile` and `remaining`. An apply of a plan stored in the state has `plan`, the id, in place of `planfile`. [`pravo apply`](apply.md#json) shows events.

A string is written as it is: `<`, `>` and `&` are not escaped as `\u003c`, `\u003e` and `\u0026`. That holds for the commands above, for `pravo output --json`, for the events of `apply` and `destroy`, and for the messages of [`pravo mcp`](mcp.md). A list with no entries is `[]` and never `null`. The root `text` declares one output and no resource:

File `text/main.cue`:

```cue
package text

import "pravo.dev/pravo"

link: pravo.#Output & {value: "https://example.com/?a=1&b=<2>"}
```

```sh
pravo -C text init > /dev/null
pravo -C text apply -auto-approve > /dev/null
pravo -C text output --json
pravo -C text state locks --json
pravo -C text mod verify --json
```

Output:

```text
{
  "link": "https://example.com/?a=1&b=<2>"
}
[]
{
  "ok": true,
  "problems": []
}
```

### Actor and reason

`--actor` and `--reason` are labels. pravo records them and does not check them. They appear in `pravo state log`, in `pravo state locks`, in `pravo show` and in the plan document. An actor is also the author of a plan that `pravo plan --store` writes into the state, and `pravo plan list` shows it; without a label, the author is the operating system user. An actor also owns the reservations it takes; without a label, the operating system user owns them.

```sh
pravo -C app apply -auto-approve --actor ci --reason "first apply" > /dev/null
PRAVO_ACTOR=agent PRAVO_REASON="check" pravo -C app plan > /dev/null
pravo -C app state log
```

Output:

```text
<txn>  apply    applied   <time>
    pravo -C app apply --auto-approve --actor ci --reason first apply
    actor ci  first apply
    1 added, 0 changed, 0 destroyed
    create   base  r0 -> r1
```

### Record and replay

`PRAVO_RECORD=DIR` records every response of the providers during a run. `PRAVO_REPLAY=DIR` answers the requests of a run from such a recording, without starting a provider. A recorded plan or apply can be replayed in a pipeline without credentials. A request that was not recorded fails with a message that says so.

```sh
PRAVO_RECORD="$PWD/rec" pravo -C app plan -refresh=all > /dev/null
ls rec
PRAVO_REPLAY="$PWD/rec" pravo -C app plan -refresh=all | tail -n 2
```

Output:

```text
random
No changes. Your infrastructure matches the configuration.
Graph: planned 0 of 1, 1 unchanged and skipped, 1 refreshed.
```

A recording holds the values the provider returned: the attributes of your objects, the sensitive ones included, and the results of ephemeral resources. Handle it like state. pravo creates the directory of each provider with mode 0700 and each file with mode 0600, and it does not write the values of a request. The provider configuration, which carries the credentials, and the write-only attributes are therefore not in the recording. A request is recorded as its method and its type, name or version.

```sh
test -z "$(find rec -type d ! -perm 700)" && echo "every directory has mode 0700"
test -z "$(find rec -type f ! -perm 600)" && echo "every file has mode 0600"
jq -c '{method, request}' rec/random/Configure-*.json
```

Output:

```text
every directory has mode 0700
every file has mode 0600
{"method":"Configure","request":{}}
```

Copying a recording, or keeping it in version control, does not keep the modes. Store it where you store state. [CI](../how-to/ci.md#6-test-without-providers-or-credentials) uses a recording in a pipeline.

## Environment variables

| Variable | Meaning |
|---|---|
| `PRAVO_HOME` | Where providers and cached schemas are kept. Default `~/.pravo`. |
| `PRAVO_LOG` | `trace`, `debug`, `info`, `warn` or `error`. See [The debug log](#the-debug-log). |
| `PRAVO_S3_PROBE` | `off` skips the check that an S3 store honours conditional requests. See [S3-compatible stores](s3-stores.md). |
| `PRAVO_ACTOR`, `PRAVO_REASON` | The values of `--actor` and `--reason` when the flags are absent. |
| `PRAVO_ACCESS_DIR` | The directory of the readers file of a sealed state, in place of `.pravo/access`. The flag `--access-dir` of [`pravo protect`](protect.md) wins. |
| `PRAVO_IDENTITY` | The identity that opens a sealed state: the contents of an identity file, when the value starts with `{`, or the path of one. It is read once and removed from the environment, and providers never receive it. For a CI job, see [`pravo id new --ci`](protect.md#id-new). |
| `PRAVO_IDENTITY_DIR` | The directory that holds identity files, in place of `id` in the configuration directory. Commands that read identities look there, and `pravo id new` writes there, making the directory with mode 0700 when it is missing. |
| `PRAVO_PASSPHRASE`, `PRAVO_IDENTITY_PASSPHRASE` | The passphrase of a sealed identity, for a command that has no terminal to ask on. It is read once and removed from the environment, and providers never receive it. |
| `PRAVO_CONFIG_DIR` | The configuration directory, in place of `$XDG_CONFIG_HOME/pravo` or `~/.config/pravo`. Identities are kept in its `id` directory. |
| `PRAVO_RECORD`, `PRAVO_REPLAY` | A directory to record provider responses to, or to replay them from. |
| `NO_COLOR` | When set, the output is not coloured. |
| `CUE_REGISTRY` | The registry for CUE modules. When it is set, pravo uses it as it is. See [`pravo mod`](mod.md). |
| `TF_PLUGIN_CACHE_DIR` | An OpenTofu plugin cache from which `pravo init` takes a provider before it downloads one. |
| `TF_REATTACH_PROVIDERS` | The reattach information of a provider that already runs. pravo connects to it and does not start or stop it. |
| `OTEL_*`, `TRACEPARENT`, `TRACESTATE` | OpenTelemetry settings and the trace to continue. See [Telemetry](telemetry.md). |

## Exit codes

Every command exits with 0, 1, 2, 3, 4, 5, 6 or 7. [Exit codes](exit-codes.md) gives the meaning of each code and runs a command that ends with it. Under `--json` an error also prints `{"code":N,"kind":"error","message":"..."}` on standard output. The tools of [`pravo mcp`](mcp.md) return the same codes in `structuredContent.exit_code`.

## See also

- [Commands](https://pravo.systems/reference/index.html) lists every command.
- [CI](../how-to/ci.md) uses the exit codes in a pipeline.
- [Telemetry](telemetry.md) describes the `OTEL_*` settings.
