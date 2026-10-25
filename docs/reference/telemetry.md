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

pravo emits OpenTelemetry traces and metrics. Exporting is off by default: nothing leaves the machine until the standard `OTEL_*` environment variables name where it goes, so any collector, backend or CI system that speaks OTLP works without pravo-specific settings. Apart from that, `PRAVO_TRACES=on` makes a command that did some work keep its own trace in the project, for [the viewer](#the-recorded-runs) to show without a collector. That is off until you ask for it.

| Variable | Effect |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | Setting any of them turns the matching signals on. |
| `OTEL_EXPORTER_OTLP_PROTOCOL`, `..._TRACES_PROTOCOL`, `..._METRICS_PROTOCOL` | `http/protobuf` (default) or `grpc`. |
| `OTEL_EXPORTER_OTLP_HEADERS`, `..._TIMEOUT`, `..._CERTIFICATE` | Passed to the exporters as the OpenTelemetry specification defines. |
| `OTEL_TRACES_EXPORTER`, `OTEL_METRICS_EXPORTER` | `otlp`, `console` (JSON on standard error) or `none`. Any other value turns the signal off. |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | The identity of the resource. The service is `pravo` unless you say otherwise. |
| `OTEL_SDK_DISABLED=true` | Turns everything off, the recorded runs included. |
| `PRAVO_TRACES=on` | Turns the recorded runs on. Without it nothing is recorded in the project. It does not change the exporters. |
| `PRAVO_TRACES_NET=off` | Keeps the network calls of the providers out of the recorded runs (see [Network calls of a provider](#network-calls-of-a-provider)). It matters only with `PRAVO_TRACES=on`. |
| `PRAVO_TRACE_DIR` | Where the recorded runs are kept, in place of `.pravo/traces`. |
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
jq -r .Name trace.jsonl | grep -E '^(pravo|engine|resource|provider)'
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

Those are the spans of the command, the engine, the resource and the provider calls. The run has more: the stages of loading and of the engine, and each request to the state. A count of the spans by the first word of their names:

```sh run
jq -r .Name trace.jsonl | cut -d' ' -f1 | sort | uniq -c
```

```text out
      4 config
      1 engine
      9 phase
      1 pravo
      4 provider
      1 resource
      8 setup
      7 state
```

The spans end in this order: what is under a span ends before it, and the command ends last. Their nesting:

```text
pravo apply                         command: actor, reason, exit code, version
├── setup load specification        loading and evaluating the CUE
│   └── config evaluate             and its stages: load package, evaluate, validate, extract resources
├── setup open state                the state is opened
│   └── state get meta.json         and each request to it is a span
├── setup resolve providers         choosing, installing and starting the providers
├── setup load schemas
└── engine apply                    the run: transaction id, added, changed, destroyed
    ├── phase lock                  a stage of the run
    │   └── state create locks/pet.json
    ├── phase apply changes
    │   ├── resource pet            one per resource and phase (plan, apply)
    │   │   ├── provider ValidateResourceConfig
    │   │   ├── provider PlanResourceChange
    │   │   ├── provider ApplyResourceChange
    │   │   └── state replace-if resources/pet.json
    │   └── resource hello
    ├── phase record outputs
    └── phase finish transaction
```

The address in `resource <address>` is the CUE path of the resource, such as `pet`, `api.config` or `servers[0]`. `pravo plan` and `pravo drift` have an `engine plan` span in the same position, and `pravo discover` an `engine discover <name>` span. Commands that start a plan inside them (`apply` without a file, `why`) show both. A provider that is started, configured or asked for its schema by a resource sits under that resource. Under `pravo mcp`, each tool call is a span and the pravo process it runs continues the same trace.

| Span | Attributes |
|---|---|
| command (`pravo plan`, ...) | `pravo.command`, `pravo.version`, `pravo.actor`, `pravo.reason`, `pravo.json`, `pravo.command_line` (the command as it was typed, with the values of tags and secrets masked when the viewer shows it), `pravo.exit_code` |
| `engine plan`, `engine apply` | `pravo.actor`, `pravo.reason`, `vcs.revision`, `pravo.destroy`, `pravo.txn`. For plan: `pravo.plan.add`, `.change`, `.destroy` and `pravo.nodes.total`, `.planned`, `.skipped`, `.refreshed`, `.fresh`, `.not_read`, `.drifted`, `pravo.ephemeral.opened`, `pravo.function.calls`. For apply: what was added, changed and destroyed. |
| `phase <stage>` | The stage of a plan or an apply: `check specification`, `begin transaction`, `moves`, `evaluate and refresh`, `read inputs`, `plan orphans`, `frontier`, `reservations`, `commit moves`, `lock`, `check revisions`, `apply changes`, `destroy orphans`, `read back`, `record outputs`, `finish transaction`. `pravo.tasks` on the stages that walk the resources. |
| `setup <stage>`, `config <stage>` | What readies a command: loading the specification and its stages, reading the lock file, opening the state, resolving, installing, starting and configuring a provider and reading its schema. `pravo.provider`, `pravo.provider.instance` (when a provider is configured), `pravo.provider.version`, `pravo.provider.source`, `pravo.state.backend`. |
| `seal <kind> <name>`, `unseal <kind> <name>`, `unseal state keys` | Work of the seal of a [sealed state](../concepts/sealed-state.md): a value sealed or opened, and the data keys of the state opened with the reader's key. `pravo.seal.kind` (`resource`, `outputs`, a plan or a summary), `pravo.seal.name` (an address or an id), `pravo.seal.group`, `pravo.seal.bytes` and, for the keys, `pravo.seal.groups`, `pravo.identity` (the name of the identity that opened the state) and `pravo.identity.reader` (its public reader key); the same two are on the span that was running when the state was opened, `setup open state`. Never the value. |
| `network <METHOD> <host>`, `network tunnel <host>` | A request that went over the network: one that a provider sent, under the provider call that was running, or one that pravo sent to a remote state (the `s3` and `http` backends), under the `state` span of the request. `http.request.method`, `server.address`, `url.scheme`, `url.path` (without the query, 300 characters at most), `http.response.status_code`, `pravo.net.request_bytes`, `pravo.net.response_bytes` and `pravo.net.decrypted`. A status of 500 or more is an error. A `tunnel` span is one connection that could not be opened: it has the host and the bytes, and no method, path or status. Only with `PRAVO_TRACES=on`; never a header, a body or a query. |
| `state <operation> <key>` | A request to the state: `get`, `put`, `create` (only if absent), `replace-if`, `delete-if`, `delete`, `list`. `pravo.state.op`, `pravo.state.key`, `pravo.state.backend` (`local`, `s3`, `http`), and where they apply `pravo.state.bytes`, `pravo.state.keys` and `pravo.state.applied` (a conditional write that was made). The key is the name of an object, such as `resources/api.db.json`; a missing object and a refused conditional write are answers, and the span does not fail. |
| `engine discover <name>` | `pravo.discovery.name`, `pravo.discovery.type`, `pravo.provider`, `pravo.discovery.items`, `pravo.discovery.status`, `pravo.warnings` |
| `engine check policy` | `pravo.policies` |
| `resource <address>` | `pravo.resource.address`, `pravo.resource.type`, `pravo.mode`, `pravo.provider`, `pravo.provider.instance`, `pravo.phase`, `pravo.action`, `pravo.skipped`, `pravo.refreshed`, `pravo.drifted`, `pravo.deferred` |
| `mcp tools/call <tool>` | `mcp.tool`, `pravo.actor`, `pravo.exit_code` |
| `provider <RPC>` | `rpc.system`, `rpc.service`, `rpc.method`, `pravo.provider`, `pravo.provider.instance` (the provider block it ran against, such as `aws.west`), `pravo.protocol`, `pravo.resource.type`, `pravo.warnings` |

A request to the state is a span of its own, so a run against a store on the network shows where its time went: a long `state list` is the size of the state, and a long `phase lock` is a store that is slow to take a lock. With an exporter on, a large state sends one span for each record that is read. They are in the recorded runs when `PRAVO_TRACES=on`.

```sh run
jq -c 'select(.Name == "pravo plan") | [.Attributes[] | {(.Key): .Value.Value}] | add' trace.jsonl
jq -c 'select(.Name == "resource hello") | [.Attributes[] | {(.Key): .Value.Value}] | add' trace.jsonl
```

```text out
{"pravo.command":"pravo plan","pravo.version":"0.1.0-dev","pravo.actor":"","pravo.reason":"","pravo.json":false,"pravo.command_line":"pravo plan","pravo.exit_code":0}
{"pravo.resource.address":"hello","pravo.resource.type":"local_file","pravo.mode":"managed","pravo.provider":"local","pravo.provider.instance":"local","pravo.phase":"plan","pravo.action":"create","pravo.skipped":false,"pravo.refreshed":false,"pravo.drifted":false,"pravo.deferred":false}
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

The events of the debug log (`-v`) are attached to the span that was running when they were logged. A `task` event sits on its `resource` span, with the action and the reasons. The event `plan` sits on the engine span, and `tasks run` and `settled` on the stage that walked the resources. The events of opening the state, choosing the providers and reading the schemas sit on the `setup` spans for them, and `loaded` and `schemas ready` on the command span. They are recorded whenever traces are on, whether or not `-v` is set.

```sh run
jq -c 'select((.Events | length) > 0) | {Name, events: [.Events[] | .Name]}' trace.jsonl
```

```text out
{"Name":"setup open state","events":["state opened"]}
{"Name":"setup resolve providers","events":["provider version from the lock file","provider found installed"]}
{"Name":"setup load schema local","events":["schema from the cache"]}
{"Name":"resource hello","events":["provider started","configuring provider","task"]}
{"Name":"phase evaluate and refresh","events":["tasks run","settled"]}
{"Name":"engine plan","events":["plan"]}
{"Name":"pravo plan","events":["loaded","schemas ready"]}
```

## The recorded runs

With `PRAVO_TRACES=on`, a command that did some work (`plan`, `apply`, `destroy`, `drift`, `discover` and the commands that plan inside them, and those that load the specification or read the state) writes its trace to `.pravo/traces/<trace id>-<span id>.json` in the directory it ran in, when it ends. This needs no `OTEL_*` variable and no collector. [`pravo serve`](serve.md#the-traces-section) reads the directory and shows each run as a timeline, which is the quickest way to see where a run spent its time or where it stopped.

- The file holds the spans that the exporters get: the same names, the same attributes and the same debug events, with times counted in microseconds from the start of the run. It holds no attribute value, by the rule [above](#what-spans-never-contain).
- A command that ran no engine (`pravo version`, `pravo serve`, `pravo state log`) writes nothing. A command that continues the trace of its caller (`TRACEPARENT`) writes its own file, so a CI job that runs several commands in one trace has one file for each.
- The directory keeps the newest 100 runs and has a `.gitignore` that ignores the whole of it. A run of more than 20,000 spans keeps every command, engine, stage and resource span and leaves out the provider calls, their network calls and the requests to the state that end after that, and says how many. A command that has no span of work, such as `pravo version`, and `pravo serve`, which runs until it is stopped, write nothing.
- A failure to write the file never fails the command and prints nothing.
- The trace id of a run that only the recorded file holds is not written to the state or to the documents of a command: `trace_id` appears there only when an exporter is on. The `pravo.txn` attribute of the `engine` span names the transaction that an apply wrote, which is how the viewer joins the two.

### Network calls of a provider

With `PRAVO_TRACES=on`, the requests that a provider makes are spans too, so a slow `ApplyResourceChange` shows which API call it waited for. The provider is a process that pravo starts, and pravo gives each one a proxy of its own and a certificate authority made for the run: `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` (the loopback addresses are left out), and a bundle of the system roots with the authority added in `SSL_CERT_FILE`, `AWS_CA_BUNDLE`, `CURL_CA_BUNDLE` and `REQUESTS_CA_BUNDLE`. The proxy opens each HTTPS request to note it, sends it on to its host and passes the answer back. Nothing in the provider changes.

- **The state.** The requests of a remote state need no proxy: pravo makes them, so each one is a `network` span under its `state` span, with a retry as a span of its own. This is so for a store at `localhost` too. `PRAVO_TRACES_NET=off` leaves them out as well.
- **What is recorded.** The method, host and path, the status, the sizes of the request and the answer, and the time. Never a header, a body or a query string, so no credential is in a span. A path can still name an object, such as the key of an object in a bucket, so a recorded run is as private as the project that holds it.
- **The authority.** It is made in memory when the provider starts, valid for a day, and signs a certificate for each host the provider calls. The bundle that holds its public part is a file that only your user can read, in the temporary directory, and is removed when the provider is closed. The key of the authority is never written. Only a process that pravo started with this environment trusts it.
- **What is not seen.** Requests to the machine itself: Go programs, which most providers are, never send a request to `localhost` or to a loopback address through a proxy, so a provider that talks to an emulator such as LocalStack at `http://localhost:4566` shows no `network` spans. A name that resolves to the loopback address, such as `localhost.localstack.cloud`, is proxied. Also a provider that ignores the proxy variables, traffic that is not HTTP, and a client that does not trust the bundle. A Go program on macOS reads the roots of the system and not `SSL_CERT_FILE`: the first connection to each host fails the handshake and is retried by the provider, the next ones to the host are passed through unopened and show as a `network tunnel` span.
- **Which call it belongs to.** A request is placed under the provider call that was running when it was made. When a provider runs several calls at once, it goes under the newest one that is still running.
- **Turning it off.** `PRAVO_TRACES_NET=off`, with `PRAVO_TRACES=on`, records the runs and leaves the providers as they are.

Recording is off by default, because it writes a file for every command. Set `PRAVO_TRACES=on` in the environment of the command (`PRAVO_TRACES=on pravo plan`, or `export PRAVO_TRACES=on` for a session), and `PRAVO_TRACE_DIR` to keep the runs elsewhere. The Traces section of the viewer says so when it finds nothing. `pravo serve` reads the directory that `PRAVO_TRACE_DIR` names, when it is set in its own environment.

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

When an exporter is on, the trace id is stored where you already look:

- every transaction in the state has a `trace_id` (`pravo state log --json`);
- `pravo plan --json` documents carry `trace_id`;
- apply and destroy events (`--json`) carry `trace_id` on every line.

When no exporter is on, the field is omitted. Given a transaction in the log, open its trace. Given a trace, the `pravo.txn` attribute of the `engine` span names the transaction.

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
- [`pravo serve`](serve.md#the-traces-section) shows the recorded runs.
- [Global flags, environment and exit codes](global-flags.md) lists the other variables.
