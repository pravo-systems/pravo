<!-- Generated from docs/concepts/traces.md by tools/skillgen. Do not edit. -->

# Traces

What a run of pravo records about itself: one trace of spans per command, the calls to the providers and the network calls under them, where the time went and where a run stopped, and what a span never contains.

A run of pravo is a tree of work. The command loads the specification, opens the state, starts the providers, plans, and applies; a plan visits resources; a resource calls its provider; a provider calls an API over the network; the state is read and written in requests. pravo records that tree as a trace: one span for each piece of work, with its start, its end, its outcome and a few facts about it, nested as the work was nested. A trace answers three questions that a log answers badly: where did the time go, where did the run stop, and what did it call.

This page explains what a trace holds and what it never holds. [Telemetry](../reference/telemetry.md) lists every span and attribute, [The Traces section](../reference/viewer-sections.md#the-traces-section) is where a recorded run is read, and [Record what a run does](../how-to/record-what-a-run-does.md) turns recording on and reads a run.

## The shape of a run

The command is the root span, and everything else nests under it. The setup spans ready the command: loading and evaluating the CUE, opening the state, resolving and starting the providers, reading their schemas. The engine span is the run itself, with one span for each of its stages, and under the stages that walk the resources, one span per resource and phase. Under a resource sit the calls to its provider, and under a call the requests the provider made over the network. A request to the state is a span of its own, wherever it happens.

```text
pravo apply                         the command: actor, reason, exit code, version
├── setup load specification        loading and evaluating the CUE
├── setup open state                the state is opened
│   └── state get meta.json         and each request to it is a span
├── setup resolve providers         choosing, installing and starting the providers
└── engine apply                    the run: transaction id, added, changed, destroyed
    ├── phase lock                  a stage of the run
    │   └── state create locks/pet.json
    ├── phase apply changes
    │   ├── resource pet            one per resource and phase
    │   │   ├── provider PlanResourceChange
    │   │   ├── provider ApplyResourceChange
    │   │   │   └── network POST api.example.com
    │   │   └── state replace-if resources/pet.json
    │   └── resource hello
    ├── phase record outputs
    └── phase finish transaction
```

*Figure: The same run as a timeline, which is how the viewer draws it. Time runs across, nesting runs down. The width of a bar is how long that work took, and a bar that sits under another is part of it.*

Each kind of span carries the facts that are useful at that level and nothing else.

| Kind | One span for | Carries |
|---|---|---|
| command | The command. | The command line, the actor and the reason, the version, the exit code. |
| setup | A step that readies the command: loading the specification, opening the state, resolving, installing and starting a provider, reading its schema, building the graph of tasks. A task that stood still for more than 20 ms, for a provider to start or to be configured or for the CUE evaluation, has a `wait for ...` span of that time, so that a pause in a trace is never unexplained. | The provider, its version and source, the backend of the state. |
| engine | The plan, the apply, a discovery, a policy check. | The transaction, the counts of a plan or an apply, how many resources were planned, skipped, refreshed, not read. |
| phase | A stage of the engine: locking, evaluating and refreshing, applying changes, recording outputs, finishing the transaction. | How many tasks the stage ran. |
| resource | One resource in one phase. | Its address, type, mode, provider and instance, the action, whether it was skipped, refreshed, drifted or deferred. |
| provider | One call to a provider: plan a change, apply it, read a resource, validate, configure. | The provider and instance, the method, the resource type, the warnings. |
| state | One request to the state: get, put, create, replace-if, delete-if, delete, list. | The operation, the key, the backend, the bytes, whether a conditional write was made. |
| seal | A value sealed or opened in a sealed state, and the data keys opened with the reader's key. | The kind and name of the object, the group, the bytes, the identity that opened the state. |
| network | One request over the network, under the provider call or the state request that made it. | The method, the host, the path, the status, the sizes of the request and the answer. |
| mcp | One tool call of `pravo mcp`. | The tool, the actor, the exit code. |

A failure is recorded on the span that failed, with its error text, so the first red span in a trace is where the run stopped. The events of the debug log are attached to the span that was running when they were logged, so a `task` event with the action and its reasons sits on its resource span, whether or not `-v` was set.

## Network calls

A provider is a process that pravo starts, and its calls to an API are the part of a run that pravo sees least of: a slow `ApplyResourceChange` is slow because of something the provider waited for. With recording on, pravo gives each provider a proxy of its own and a certificate authority made for the run, through the environment variables that HTTP clients honour, `HTTPS_PROXY` and the bundle of roots in `SSL_CERT_FILE` and the like. The proxy opens each HTTPS request to note it, sends it on to its host and passes the answer back. Nothing in the provider changes, and each request becomes a `network` span under the provider call that was running.

What is noted is the method, the host and the path, the status, the sizes of the request and the answer, and the time. Never a header, a body or a query string, so no credential and no payload is in a span. A path can still name an object, such as the key of an object in a bucket, so a recorded run is as private as the project that holds it.

The requests of a remote state need no proxy: pravo makes them itself, so each one is a `network` span under its `state` span, with a retry as a span of its own. A run against a bucket or an HTTP state server therefore shows where its time went in the state as well: a long `state list` is the size of the state, and a long `phase lock` is a store that is slow to take a lock.

Some traffic is not seen. A Go program never sends a request to `localhost` or a loopback address through a proxy, so a provider that talks to an emulator at `http://localhost:4566` shows no network spans, while a name that resolves to the loopback address is proxied. A provider that ignores the proxy variables, traffic that is not HTTP, and a client that does not trust the bundle are not seen either. The authority is made in memory when the provider starts, valid for a day; its public part is a file that only your user can read, removed when the provider is closed, and its key is never written. `PRAVO_TRACES_NET=off` records the runs and leaves the providers as they are. [Network calls of a provider](../reference/telemetry.md#network-calls-of-a-provider) lists each case.

## What a span never contains

Addresses, types, actions, counts, outcomes and timings are recorded. Attribute values never are: not configuration, not state, not provider ids, not outputs, not the content of a file that a plan shows. A resource's values can be secrets, and a trace backend is rarely where they belong, so the rule is absolute rather than a mask. Error messages are recorded as they are printed, so a provider that puts a value in its own error text puts it in the span too; the viewer masks a text that quotes a secret of the state, as it masks every text it shows.

The same rule gives the trace its place among the records of a run. The transaction in the state says what changed and holds the values that changed, for the readers. The plan document holds the values before and after. The trace holds neither: it says what was called, how long it took and whether it worked.

## Recorded runs

Exporting a trace to a backend is one way to keep it. The other needs no backend. With `PRAVO_TRACES=on`, a command that did some work writes its trace to `.pravo/traces/<trace id>-<span id>.json` in the directory it ran in, when it ends, and the Traces section of [the viewer](the-viewer.md) reads that directory. Recording is off by default, because it writes a file for every command, and the section says so when it finds nothing.

- The file holds the spans that an exporter would get: the same names, attributes and debug events, with times counted in microseconds from the start of the run.
- The directory keeps the newest 100 runs and ignores itself in git. A run of more than 20,000 spans keeps every command, engine, stage and resource span and leaves out the provider calls, their network calls and the requests to the state that end after that, and says how many.
- A command that ran no engine, such as `pravo version` or `pravo state log`, writes nothing, and so does `pravo serve`, which runs until it is stopped.
- A failure to write the file never fails the command and prints nothing.
- A command that is started inside a trace of its caller, with `TRACEPARENT` set as CI systems set it, continues that trace and still writes a file of its own, so a job that runs several commands has one file for each.

A run is joined to the state by the transaction. The `pravo.txn` attribute of the engine span names the transaction that an apply wrote, which is how the viewer opens a run from the History tab of a resource, and a transaction from a run. The trace id itself is written to the transaction, the plan document and the events of an apply only when an exporter is on.

## A collector

Nothing leaves the machine until the standard `OTEL_*` variables name where it goes. `OTEL_EXPORTER_OTLP_ENDPOINT` turns export on, for any collector or backend that speaks OTLP, and `OTEL_TRACES_EXPORTER=console` prints each span as JSON on standard error instead. Export and recording are separate: a run can be exported, recorded in the project, both or neither. A collector that is down costs at most the flush timeout, prints one warning and does not change the exit code; with `--json`, telemetry never writes to standard output.

Under `pravo mcp`, each tool call is a span, and the pravo process it runs continues the same trace, so an agent's call, the plan or apply it caused and the provider calls under it are one trace. Provider processes receive no trace context; their own telemetry, if any, is separate.

## Recap

- A run is one trace: the command at the root, the setup and the engine under it, the stages, then each resource with its provider calls, and the requests to the state wherever they happen.
- A network call that a provider made, or that pravo made to a state on the network, is a span under the call that made it, with the method, host, path, status and sizes, and never a header, a body or a query.
- A span never holds an attribute value. It says what was called, how long it took and whether it worked.
- `PRAVO_TRACES=on` keeps the newest 100 runs in `.pravo/traces` for the viewer, with no collector. The `OTEL_*` variables export the same spans to one.

Next: [Discovery](discovery.md) explains the runs that list what exists and nothing manages.
