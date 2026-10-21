---
title: Telemetry
description: OpenTelemetry traces and metrics for every command, resource and provider call, configured with the standard OTEL variables.
order: 53
group: Data and formats
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ''
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]{6}\]'
    replace: '[txn <id>]'
---

pravo emits OpenTelemetry traces and metrics. Both are off by default and cost almost nothing when off. The standard `OTEL_*` environment variables configure them, so any collector, backend or CI system that speaks OTLP works without pravo-specific settings.

| Variable | Effect |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | Setting any of them turns the matching signals on. |
| `OTEL_EXPORTER_OTLP_PROTOCOL`, `..._TRACES_PROTOCOL`, `..._METRICS_PROTOCOL` | `http/protobuf` (default) or `grpc`. |
| `OTEL_EXPORTER_OTLP_HEADERS`, `..._TIMEOUT`, `..._CERTIFICATE` | Passed to the exporters as the OpenTelemetry specification defines. |
| `OTEL_TRACES_EXPORTER`, `OTEL_METRICS_EXPORTER` | `otlp`, `console` (JSON on standard error) or `none`. Any other value turns the signal off. |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | The identity of the resource. The service is `pravo` unless you say otherwise. |
| `OTEL_SDK_DISABLED=true` | Turns everything off. |
| `TRACEPARENT`, `TRACESTATE` | A trace started by the caller continues in pravo. See [Propagation](#propagation). |

Export never affects a command. A collector that is down costs at most the flush timeout, five seconds, produces one warning on standard error and does not change the exit code. With `--json`, telemetry never writes to standard output.

Work in an empty directory.

```cue file=main.cue
package demo

import loc "pravo.dev/providers/hashicorp/local"

$local: loc.#Provider

hello: $local.#Resource.local_file & {
	filename: "hello.txt"
	content:  "s3cr3t-value"
}
```

```sh run
pravo init > /dev/null
```

## Traces

One trace per command. The command span is the root, and everything else nests under it. With the console exporter, each span is one JSON object on standard error:

```sh run
OTEL_TRACES_EXPORTER=console pravo plan 2> trace.jsonl > /dev/null
jq -r .Name trace.jsonl
```

```text out
provider ValidateProviderConfig
provider ConfigureProvider
provider ValidateResourceConfig
provider PlanResourceChange
resource hello
engine plan
pravo plan
```

The spans end in this order: the provider calls, the resource, the engine, the command. Their nesting:

```text
pravo apply                         command: actor, reason, exit code, version
└── engine apply                    the run: transaction id, added, changed, destroyed
    ├── resource pet                one per resource and phase (plan, apply)
    │   ├── provider ValidateResourceConfig
    │   ├── provider PlanResourceChange
    │   └── provider ApplyResourceChange
    └── resource hello
```

The address in `resource <address>` is the CUE path of the resource, such as `pet`, `api.config` or `servers[0]`. `pravo plan` and `pravo drift` have an `engine plan` span in the same position. Commands that start a plan inside them (`apply` without a file, `why`) show both. Under `pravo mcp`, each tool call is a span and the pravo process it runs continues the same trace.

| Span | Attributes |
|---|---|
| command (`pravo plan`, ...) | `pravo.command`, `pravo.version`, `pravo.actor`, `pravo.reason`, `pravo.json`, `pravo.exit_code` |
| `engine plan`, `engine apply` | `pravo.actor`, `pravo.reason`, `vcs.revision`, `pravo.destroy`, `pravo.txn`. For plan: `pravo.plan.add`, `.change`, `.destroy` and `pravo.nodes.total`, `.planned`, `.skipped`, `.refreshed`, `.fresh`, `.not_read`, `.drifted`, `pravo.ephemeral.opened`, `pravo.function.calls`. For apply: what was added, changed and destroyed. |
| `resource <address>` | `pravo.resource.address`, `pravo.resource.type`, `pravo.mode`, `pravo.provider`, `pravo.phase`, `pravo.action`, `pravo.skipped`, `pravo.refreshed`, `pravo.drifted`, `pravo.deferred` |
| `mcp tools/call <tool>` | `mcp.tool`, `pravo.actor`, `pravo.exit_code` |
| `provider <RPC>` | `rpc.system`, `rpc.service`, `rpc.method`, `pravo.provider`, `pravo.protocol`, `pravo.resource.type`, `pravo.warnings` |

```sh run
jq -c 'select(.Name == "pravo plan") | [.Attributes[] | {(.Key): .Value.Value}] | add' trace.jsonl
jq -c 'select(.Name == "resource hello") | [.Attributes[] | {(.Key): .Value.Value}] | add' trace.jsonl
```

```text out
{"pravo.command":"pravo plan","pravo.version":"0.1.0-dev","pravo.actor":"","pravo.reason":"","pravo.json":false,"pravo.exit_code":0}
{"pravo.resource.address":"hello","pravo.resource.type":"local_file","pravo.mode":"managed","pravo.provider":"local","pravo.phase":"plan","pravo.action":"create","pravo.skipped":false,"pravo.refreshed":false,"pravo.drifted":false,"pravo.deferred":false}
```

Failures are recorded on the span that failed, with its error, so the first red span in a trace is where the run stopped.

### What spans never contain

Addresses, types, actions, counts and outcomes are recorded. Attribute values never are: not configuration, not state, not provider ids, not outputs. A resource's values can be secrets, and a trace backend is rarely where they belong. Error messages are recorded as they are printed, so a provider that puts a value in its own error text puts it in the span too.

The plan shows the content of the file. The trace does not hold it:

```sh run
pravo plan 2> /dev/null | grep -c 's3cr3t-value'
grep -c 's3cr3t-value' trace.jsonl || true
```

```text out
1
0
```

### Debug events

The events of the debug log (`-v`) are attached to the spans they belong to. A `task` event sits on its `resource` span, with the action and the reasons. The events `plan`, `tasks run` and `settled` sit on the engine span. The events of loading the specification, opening the state, reading the schemas and choosing the providers sit on the command span. They are recorded whenever traces are on, whether or not `-v` is set.

```sh run
jq -c '{Name, events: [.Events[]? | .Name]}' trace.jsonl | grep -E 'resource hello|engine plan|pravo plan'
```

```text out
{"Name":"resource hello","events":["configuring provider","task"]}
{"Name":"engine plan","events":["plan","tasks run","settled"]}
{"Name":"pravo plan","events":["loaded","state opened","provider version from the lock file","provider found installed","schema from the cache","schemas ready"]}
```

## Metrics

| Instrument | Type | Attributes |
|---|---|---|
| `pravo.run.duration` | histogram, seconds | `pravo.command`, `pravo.exit_code` |
| `pravo.provider.rpc.duration` | histogram, seconds | `pravo.provider`, `rpc.method`, `error` |
| `pravo.resource.operations` | counter | `pravo.mode`, `pravo.resource.type`, `pravo.action`, `pravo.outcome` |
| `pravo.plan.nodes` | counter | `pravo.disposition`: `planned`, `skipped`, `refreshed`, `fresh`, `not_read`, `drifted`. `fresh` counts the records that were not read and are within their refresh age, `not_read` the records that were not read and are older, and neither counts any under `-refresh=false`. |
| `pravo.lock.acquire.duration` | histogram, seconds | `pravo.locks` |
| `pravo.rejections` | counter | `pravo.reason`: `stale`, `locked`, `policy` |
| `pravo.ephemeral.opened` | counter | `pravo.resource.type` |
| `pravo.function.calls` | counter | `pravo.function` |

A command lives for seconds, so metrics are exported once, when it ends. An instrument appears only after something was recorded on it.

```sh run
OTEL_METRICS_EXPORTER=console pravo plan 2> metrics.jsonl > /dev/null
jq -r '.ScopeMetrics[].Metrics[].Name' metrics.jsonl | sort
```

```text out
pravo.plan.nodes
pravo.provider.rpc.duration
pravo.resource.operations
pravo.run.duration
```

A `policy` rejection is a run that exited with code 5: a plan that violated a policy named with `--policy`, a plain `apply` under a policy, or a policy file that could not be used. A module that does not match its lock also exits with code 5 and is counted under the same reason. A `locked` rejection is exit code 4, a lock or a reservation held by another actor. A `stale` rejection is exit code 3.

The counter measures refusals. It cannot measure omission: a run that was not given `--policy` is not checked, and nothing is counted for it. Whether a run was given `--policy` can be read afterwards from the command line recorded on its transaction (`pravo state log`). That record is evidence and not a control.

## Finding a run again

When tracing is on, the trace id is stored where you already look:

- every transaction in the state has a `trace_id` (`pravo state log --json`);
- `pravo plan --json` documents carry `trace_id`;
- apply and destroy events (`--json`) carry `trace_id` on every line.

When tracing is off, the field is omitted. Given a transaction in the log, open its trace. Given a trace, the `pravo.txn` attribute of the `engine` span names the transaction.

```sh run
OTEL_TRACES_EXPORTER=console pravo plan --json 2> trace.jsonl | jq -r .trace_id > doc-trace-id.txt
jq -r 'select(.Name == "pravo plan") | .SpanContext.TraceID' trace.jsonl | diff - doc-trace-id.txt && echo "the document carries the trace id of the command"
```

```text out
the document carries the trace id of the command
```

## Propagation

- **In.** `TRACEPARENT` and `TRACESTATE` in the environment, the convention that CI systems and orchestrators use for command-line tools, make the command span a child of the caller's span. A pipeline's trace then shows pravo's work inside it.
- **Through MCP.** `pravo mcp` creates a span per tool call and passes its context to the pravo process it runs. An agent's tool call, the plan or apply it caused and the provider calls under it are one trace. A call that the server refuses before it runs anything, because an argument is empty or begins with `-`, starts no process and has no span.
- **Out.** Provider processes do not receive trace context. Their own telemetry, if any, is separate.

```sh run
TRACEPARENT=00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01 OTEL_TRACES_EXPORTER=console pravo plan 2> trace.jsonl > /dev/null
jq -c 'select(.Name == "pravo plan") | {trace: .SpanContext.TraceID, parent: .Parent.SpanID}' trace.jsonl
```

```text out
{"trace":"4bf92f3577b34da6a3ce929d0e0e4736","parent":"00f067aa0ba902b7"}
```

## A collector that is down

```sh run
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:1 pravo plan > /dev/null
echo "exit=$?"
```

```text out
pravo: telemetry export problem (further ones are not shown): traces export: Post "http://127.0.0.1:1/v1/traces": dial tcp 127.0.0.1:1: connect: connection refused
exit=0
```

## Not included

Logs are not exported as an OpenTelemetry signal. The transaction log and the `--json` event stream cover the same ground and carry the trace id. There are no metrics for the contents of resources, by the same rule as spans.

## See also

- [Find out why a run fails](../how-to/debugging.md) follows a command in a trace.
- [Global flags, environment and exit codes](global-flags.md) lists the other variables.
