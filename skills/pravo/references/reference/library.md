<!-- Generated from docs/reference/library.md by tools/skillgen. Do not edit. -->

# The Go library

Package pravo: what the command line does, as a Go library. Open a workspace, plan, judge, store, apply, drift, discover and query, with nothing read from flags or the environment of the process.

The package `github.com/pravo-systems/pravo/pravo` is the programmatic interface to pravo. A program that builds on pravo, a runner, a pipeline step or a service that plans and applies on behalf of its users, opens a workspace and calls its methods, instead of running the binary and parsing what it prints. The command line is built on the same package, and so is [`pravo mcp`](mcp.md) for the tools that run in its process. [The library and the machine interface](../concepts/the-library-and-the-machine-interface.md) says when to use which.

| | |
|---|---|
| Import | `github.com/pravo-systems/pravo/pravo` |
| Stability | Unstable, v0. A release may break its callers until the package is declared stable. The JSON that the commands print is the stable surface. |
| Reads | What it is given: a directory, an environment value, options. No flag, no variable of the process, no prompt. |
| Prints | Nothing. Progress, warnings and the log go to functions and handlers that the program supplies. |
| Example | `examples/embed` in the repository, a program that plans a root and applies it. |

## Open a workspace

`pravo.Open` loads the specification of a directory, reads the lock file, opens the state, resolves the providers and, when asked, fetches their schemas. Everything it needs is in `Config`.

```go
ws, err := pravo.Open(ctx, pravo.Config{
	Dir:     "/work/estate",
	Tags:    []string{"env=prod"},
	Env:     pravo.NewEnv(environ),
	Install: true,
	Schemas: true,
	State:   pravo.StateWrite,
	Actor:   "runner/job-123",
	Reason:  "nightly apply",
})
if err != nil {
	return err
}
defer ws.Close()
```

| Field | Meaning |
|---|---|
| `Dir` | The directory of the specification. A relative path is made absolute when the workspace is opened; the working directory of the process never changes. |
| `Tags`, `SysTags` | The values the specification is loaded with, as `-t` and `-T` give them. |
| `Env` | The environment of the run: what the specification reads through `pravo.#Env`, the credentials of a remote state, what the providers and the module loader are given. `pravo.OSEnv()` passes the environment of the process; `pravo.NewEnv` and `pravo.EnvMap` build one from a slice or a map. The process is never consulted on its own. |
| `Root` | A specification that was loaded already, instead of loading `Dir`. |
| `Install`, `Upgrade` | Whether missing providers may be downloaded, and whether the lock file is ignored when versions are chosen. |
| `Schemas` | Fetch the schemas of the providers and unify them into the specification. Planning and applying need them; a query of the state does not. |
| `State` | `StateNone` leaves the state closed, `StateRead` opens it for reading (a location that holds none reads as empty, and nothing is created), `StateWrite` opens it for a run that changes it. |
| `Actor`, `Reason`, `Args` | Recorded on transactions, locks and plans, as the flags of the same names are. They are not verified. |
| `OpenState` | A function that opens the state in place of the library's own, for a program with rules of its own about who may open which state. |
| `Identity` | A function that returns the key of this process for a sealed state, given the reader keys that have a lock in it. It never has to prompt; a program that cannot answer returns an error. |
| `Log`, `Progress`, `Warn` | Where the lines about installing providers, the progress of a plan or an apply, and the warnings of the state go. Nil drops them. |

`pravo.SetLogHandler` sends the debug log of every workspace in the process to an `slog.Handler`. Spans go to the global OpenTelemetry tracer provider of the host, and the same events are attached to them, so a program that runs pravo under its own tracer sees [the spans that the command line records](telemetry.md#traces).

Workspaces on different directories run in one process at the same time: each has its own environment and its own state, and nothing is global but the log handler and the tracer.

## Plan, judge, store, apply

| Method | Does |
|---|---|
| `ws.Plan(ctx, PlanOptions)` | Computes the plan. `PlanOptions` has `Refresh` (`RefreshAge`, the default, `RefreshExpired`, `RefreshNone` or `RefreshAll`, as `-refresh` names them), `Destroy`, `Full`, `Replace`, `Reserve` and `Skipped`. It returns a `*Planned`. |
| `p.View()` | The document of the plan, the one `pravo plan --json` prints. [The plan document](plan-document.md) lists its fields. |
| `p.HasChanges()` | Whether applying the plan would change anything. |
| `p.CheckPolicy(ctx)` | Judges the plan with the policies the specification declares. The error says which rule refused it and carries the exit code the command would give. |
| `p.Overlaps(ctx)` | The stored plans that change the same resources. |
| `p.Store(ctx, StorePlanOptions)` | Stores the plan in the state, with a `TTL`, and returns the plan's `Meta`, its `Overlaps` and the plans that the small collection `Removed`. |
| `p.Apply(ctx)` | Makes the changes of the plan and returns what it did: how many were added, changed and destroyed, and the transaction. |
| `ws.ApplyStored(ctx, ref, ApplyStoredOptions)` | Applies a plan that is stored in the state, by its id or a unique prefix of it, with the checks of `pravo apply ID`: the plan is claimed while it applies, what was applied is recorded, and `Only` limits the apply to some addresses and what they need. `Event` receives the steps. The result says what is `Left` and the command that applies it `Again`, and `Partial` describes the changes an apply finished before it failed. |
| `ws.ReadStoredPlan(ctx, ref, verb)` | Resolves a reference to a stored plan and reads it, with its status. |
| `ws.PlanStore()` | The stored plans of the state, to list and collect them. |

The order of a plan on the command line, print, judge, save, store, is the program's to keep: `Plan`, then `CheckPolicy`, then `Store` or `Apply`. `Apply` judges the plan again under the locks, as `pravo apply` does, so a program that skips `CheckPolicy` is told at the apply.

## Drift, discovery, explanation and queries

| Method | Does |
|---|---|
| `ws.Drift(ctx)` | Reads the real state of every resource, ignoring refresh ages, and reports what changed outside pravo: the JSON of `pravo drift --json`. |
| `ws.DriftDue(ctx, limit)` | Reads only the records that are older than their refresh age, the oldest first, at most `limit` of them: `pravo drift --due`. |
| `ws.Why(ctx, refresh, addrs...)` | Plans, and explains what the plan does to each address and why: the action, the reasons and the dependents. `Found` is false for an address that is neither declared nor in the state. |
| `ws.Discover(ctx, DiscoverOptions)` | Runs the discoveries of the specification: the ones named, the ones that have not run yet and whose scope changed, or `All`. `Error` is called as a discovery fails, and the others still run. |
| `ws.DiscoveryReport()` | Reads the runs and the acknowledgements of the state and sets them against the records: what is managed, unmanaged, acknowledged and gone. |
| `ws.QueryState(expr)` | Evaluates a CUE expression with the state graph in scope, as `pravo state query` does, and returns the value as JSON or CUE. |
| `ws.Dependents(addr)` | Everything a change to the resource can reach. |
| `ws.Root()`, `ws.State()`, `ws.Lock()`, `ws.Schemas()`, `ws.Providers()`, `ws.Registry()` | The specification, the state, the lock file, the schemas by provider, the provider processes and the registry client, for a program that needs the parts. |

## Errors

An error carries the exit code that the command would give, and `pravo.CodeOf(err)` reads it: the codes of [Exit codes](exit-codes.md), with the constants `ExitStale` (3, a stored or saved plan that does not apply any more), `ExitLocked` (4, a lock or a reservation held by someone else), `ExitRefused` (5, a policy, a missing approval, a role or a readers file), `ExitInvalid` (6, a specification that does not evaluate) and `ExitUntrusted` (7, a state that failed verification), and `ExitError` for anything else. The command line ends with the same code for the same error. A stale plan is an `*pravo.ErrStalePlan`, reachable with `errors.As`, which says which changes conflict.

## A sealed state

A workspace opens a sealed state with the identity that `Config.Identity` returns. The library never looks for an identity file and never asks for a passphrase: a program that keeps keys in a vault fetches the file from there, and a program that runs as a job holds the file of the job, and either hands it to `pravo.ParseIdentity(data, passphrase)`, which reads it as `pravo id new` wrote it, the form of `--ci` that needs no passphrase or the sealed form, for which `passphrase` is called once. Without an identity a sealed state opens locked: addresses and revisions are readable, values are not, and a plan cannot be computed. The readers file and the roles are the program's to give to an apply through `ApplyStoredOptions.Tune`, which may change the options of the engine before it is made.

## A program that plans and applies

The program in `examples/embed` opens a root, plans it, prints the document and applies the plan when asked:

```go
ws, err := pravo.Open(ctx, pravo.Config{
	Dir:     dir,
	Env:     pravo.OSEnv(),
	Install: true, Schemas: true, State: pravo.StateWrite,
	Actor: "example/embed", Reason: "run by examples/embed",
	Args: []string{"plan"},
	Progress: func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
})
if err != nil {
	return err
}
defer ws.Close()

planned, err := ws.Plan(ctx, pravo.PlanOptions{})
if err != nil {
	return err
}
json.NewEncoder(w).Encode(planned.View())
if err := planned.CheckPolicy(ctx); err != nil {
	return err
}
if apply && planned.HasChanges() {
	res, err := planned.Apply(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "applied: %d added, %d changed, %d destroyed (txn %s)\n", res.Added, res.Changed, res.Destroyed, res.Txn)
}
```

```text
go run ./examples/embed -dir examples/getting-started
go run ./examples/embed -dir examples/getting-started -apply
```

## Compatibility

Types that are aliases of types in pravo's internal packages (`Root`, `Store`, `Engine`, `Plan` and the like) are the way to the engine while the interface settles; they will be replaced by types of this package, and the methods that return them will change. A program that wants a contract that holds across releases reads the JSON documents, which are the same on the command line and in the library: [the plan document](plan-document.md), the drift report, the discovery report and the events of an apply. The callers in the repository, `cmd/pravo` and `examples/embed`, always build against the package, so a change that breaks them is found before it ships.

## See also

- [The library and the machine interface](../concepts/the-library-and-the-machine-interface.md) says what a program gets from the library, from `--json` and from the MCP server.
- [Embed pravo in your program](../how-to/embed-pravo-in-your-program.md) builds a small runner.
- [`pravo mcp`](mcp.md) serves the same operations to an agent.
- [Global flags, environment and exit codes](global-flags.md) lists the `--json` documents of the commands.
