<!-- Generated from docs/how-to/embed-pravo-in-your-program.md by tools/skillgen. Do not edit. -->

# Embed pravo in your program

Build a small runner on the Go library. Open a workspace, plan, judge the plan with the policies of the specification, apply it or store it for later, read the result as data, map an error to an exit code, and open a sealed state with a key the program holds.

The command line is a thin layer over a Go package, `github.com/pravo-systems/pravo/pravo`, and a program can use the package directly: a runner that plans every root of a repository, a step of a pipeline, a service that plans and applies on behalf of its users. It gets what the command line gets, as values rather than text, and it decides for itself what the run sees of the outside world. This page builds `runner`, a program of some eighty lines that does what `pravo plan` and `pravo apply` do, and then goes where a shell script cannot: a stored plan applied by its id, an error read as a code, and a sealed state opened with a key that the program fetched itself.

> [!NOTE]
> The package is unstable, at v0: a release may change its types until it is declared stable. What is stable is the JSON that the commands print, and the library returns the same documents, so a program that keeps to them has a contract that holds. The page says where the two meet.

## 1. Start a Go module

```sh
mkdir runner && cd runner
go mod init example.com/runner
go get github.com/pravo-systems/pravo/pravo@main
```

`@main` takes the newest commit. Pin a commit or a tag once the program works, and move it when you mean to: the package is in the same Go module as the `pravo` command, so the version you pin is a version of pravo.

## 2. Open a workspace

A workspace is a specification opened with its state and its providers, which is what every command of pravo works on. `pravo.Open` loads the CUE of a directory, reads the lock file, opens the state, resolves and starts the providers, and fetches their schemas. Everything it needs is in `Config`, and it reads nothing else: no flag, no variable of the process, no prompt.

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/pravo-systems/pravo/pravo"
)

func main() {
	dir := flag.String("dir", ".", "the directory of the specification")
	apply := flag.Bool("apply", false, "apply the plan")
	flag.Parse()
	if err := run(context.Background(), *dir, *apply); err != nil {
		fmt.Fprintln(os.Stderr, "runner:", err)
		os.Exit(int(pravo.CodeOf(err)))
	}
}

func run(ctx context.Context, dir string, apply bool) error {
	ws, err := pravo.Open(ctx, pravo.Config{
		Dir:     dir,
		Env:     pravo.OSEnv(),
		Install: true,
		Schemas: true,
		State:   pravo.StateWrite,
		Actor:   "runner",
		Reason:  "run by the runner",
		Args:    []string{"plan"},
		Progress: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	})
	if err != nil {
		return err
	}
	defer ws.Close()

	// The rest of the page goes here.
	return nil
}
```

Each field stands for something the command line takes from a flag or from the environment:

| Field | Stands for |
|---|---|
| `Dir`, `Tags`, `SysTags` | The directory, and `-t` and `-T`. |
| `Env` | Everything the run sees of the environment: what the specification reads through `pravo.#Env`, the credentials of a remote state, what the providers are started with. `pravo.OSEnv()` passes the environment of the process on. `pravo.NewEnv(environ)` and `pravo.EnvMap(vars)` build one from a list or a map, which is how a service gives each job the variables of that job and nothing else. The process is never consulted on its own. |
| `Install`, `Schemas` | `pravo init`: download providers that are missing, and fetch their schemas. A plan needs both. |
| `State` | `StateNone` leaves the state closed, `StateRead` opens it to read, `StateWrite` opens it for a run that changes it. |
| `Actor`, `Reason`, `Args` | `--actor`, `--reason`, and the command that the transaction log shows. Labels, not verified. |
| `Progress`, `Log`, `Warn` | Where the lines go that the command line prints as it works. `nil` drops them. |

Nothing is global. Two workspaces on two directories run in one process at the same time, each with its own environment and its own state. `ws.Close()` stops the providers and releases the state.

## 3. Plan, and read the plan as data

`ws.Plan` computes the plan. It reads the state and, by the refresh mode, the resources, and changes neither. `View()` is the document that `pravo plan --json` prints, [the plan document](../reference/plan-document.md), as a value to walk or to encode.

```go
	planned, err := ws.Plan(ctx, pravo.PlanOptions{})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(planned.View()); err != nil {
		return err
	}
```

```sh
go run . -dir ../pravo/examples/getting-started
```

```json
{
  "format": 1,
  "created": "2026-10-28T15:12:31Z",
  "actor": "runner",
  "reason": "run by the runner",
  "summary": {"add": 3, "change": 0, "destroy": 0, "read": 0, "blast_radius": 3},
  "stats": {"total": 3, "reached": 3, "skipped": 0, "refreshed": 0, "fresh": 0, "not_read": 0, "drifted": 0, "opened": 0, "called": 0, "imported": 0, "moved": 0},
  "changes": [
    {
      "addr": "server",
      "mode": "managed",
      "type": "random_pet",
      "name": "server",
      "action": "create",
      "provider": "random",
      "reasons": ["not in the state: a new resource"],
      "after": {"id": {"$unknown": true}, "keepers": {"environment": "dev"}, "length": 3, "prefix": null, "separator": "-"}
    },
    ...
  ],
  "outputs": {"file": "hello-dev.txt", "pet": {"$unknown": true}, "port": {"$unknown": true}},
  "providers": {"local": "2.9.1", "random": "3.9.1"}
}
```

The document is the same one a policy judges and a pipeline reads, so whatever your program decides on it, a policy could decide too. `PlanOptions` has what the flags of `pravo plan` have: `Refresh` (`RefreshAge`, the default, `RefreshExpired`, `RefreshNone` or `RefreshAll`), `Destroy`, `Full`, `Replace`, `Reserve` and `Skipped`. `planned.HasChanges()` says whether applying would change anything.

## 4. Judge it, then apply

The command line keeps an order: print, judge, save or apply. `CheckPolicy` judges the plan with the policies of the state, and its error says which rule refused it. `Apply` makes the changes and returns what it did. It judges the plan again under the locks, as `pravo apply` does, so a program that skips `CheckPolicy` is told at the apply.

```go
	if err := planned.CheckPolicy(ctx); err != nil {
		return err // CodeOf(err) is pravo.ExitRefused
	}
	if !apply || !planned.HasChanges() {
		return nil
	}
	res, err := planned.Apply(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "applied: %d added, %d changed, %d destroyed (txn %s)\n",
		res.Added, res.Changed, res.Destroyed, res.Txn)
```

```sh
go run . -dir ../pravo/examples/getting-started -apply
```

```text
server: Creating...
server: Creation complete after 0s [id=sure-fond-mustang]
listen: Creating...
listen: Creation complete after 0s [id=29474]
hello: Creating...
hello: Creation complete after 0s [id=8ca855702470433d627b71700a2d36cbb13ce045]
applied: 3 added, 0 changed, 0 destroyed (txn 20261028T151231Z-0b3865)
```

The lines of progress are the ones `Progress` received. The plan document went to standard output before them, as in the step before. `res.Outputs` holds the outputs after the apply, and `res.Applied` the addresses the apply finished, which is what tells a run that failed half-way from one that did nothing.

## 5. Store the plan, and apply it later

A service rarely applies in the same breath as it plans. It shows the plan to someone, waits, and applies what they saw. `Store` writes the plan into the state, where [a stored plan](store-a-plan-and-apply-it-later.md) lives: anyone who can read the state lists it, `pravo plan show` prints it, the viewer lays it over the graph, and a policy can ask that other members approve it. `ApplyStored` applies it by its id, with the checks of `pravo apply ID`.

```go
	stored, err := planned.Store(ctx, pravo.StorePlanOptions{TTL: 36 * time.Hour})
	if err != nil {
		return err
	}
	id := stored.Meta.ID
	for _, o := range stored.Overlaps {
		fmt.Fprintf(os.Stderr, "%s by %s also changes %s\n", o.Plan.ID, o.Plan.Author, strings.Join(o.Addrs, ", "))
	}

	// Later, in another process, as the identity that applies:
	done, err := ws.ApplyStored(ctx, id, pravo.ApplyStoredOptions{
		Event: func(e pravo.Event) {
			if e.Addr != "" {
				fmt.Fprintf(os.Stderr, "%s %s %s\n", e.Addr, e.Kind, e.Phase)
			}
		},
	})
	if err != nil {
		return err
	}
	if done.Left > 0 {
		fmt.Fprintln(os.Stderr, "apply the rest with:", done.Again)
	}
```

`Event` receives each step of the apply, which is what `pravo apply --json` prints line by line: the kind (`create`, `update`, `destroy`, `lock`, ...), the address, the phase (`start`, `done`, `error`) and the message. `done.Result` is what the apply did, `done.Left` how many changes of the plan remain, `done.Again` the command that applies them, and `done.Partial` is set when an apply failed after it finished some of the changes. `Only` limits an apply to some addresses and what they need, as `--only` does.

## 6. Read an error as a code

An error of the library carries the exit code that the command line would end with, and `pravo.CodeOf` reads it. The `main` above ends the process with that code, so the runner is a drop-in for the binary in a pipeline that reads [exit codes](../reference/exit-codes.md). A program that acts on a failure switches on the code and not on the text:

```go
	switch pravo.CodeOf(err) {
	case pravo.ExitStale: // the state moved on under the plan: plan again
	case pravo.ExitLocked: // another actor holds a resource: wait, then retry
	case pravo.ExitRefused: // a policy, a missing approval, a role or a readers file: report, do not retry
	case pravo.ExitInvalid: // the specification does not evaluate: fix it
	case pravo.ExitUntrusted: // the state failed verification: stop and tell someone
	}
	var stale *pravo.ErrStalePlan
	if errors.As(err, &stale) {
		fmt.Fprintln(os.Stderr, "stale because:", strings.Join(stale.Reasons, "; "))
	}
```

## 7. Open a sealed state

A [sealed state](seal-the-state.md) opens with the key of a reader. The command line finds the identity in `PRAVO_IDENTITY` or in the identity directory, and asks for its passphrase. The library does neither: `Config.Identity` is a function that the workspace calls when it finds the state sealed, with the reader keys that have a lock in the state, and the program returns the key from wherever it keeps it. It never has to prompt.

```go
	Identity: func(readers []pravo.ReaderPublic) (*pravo.Identity, error) {
		data, err := vault.Read(ctx, "pravo/deploy-bot") // the file that pravo id new --ci wrote
		if err != nil {
			return nil, err
		}
		return pravo.ParseIdentity(data, nil)
	},
```

`pravo.ParseIdentity` reads an identity file as `pravo id new` writes it: the form that `--ci` writes needs no passphrase, and for a form sealed with one the second argument supplies it. Without an identity a sealed state opens locked: addresses and revisions are readable, values are not, and a plan cannot be computed. The identity's role, from the readers file of the checkout and the roles of the state, bounds what the workspace may do, as it bounds the command line: a reader plans and stores, a writer applies. Keep the key where the job's secrets live, and give the program the smallest role its work needs, as [Give people roles](give-people-roles.md) says.

## 8. Logs and traces

The debug log of every workspace in the process goes to one handler, the one that `-v` prints on the command line:

```go
	pravo.SetLogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
```

Spans go to the global OpenTelemetry tracer provider of the process. A program that runs under its own tracer, with `otel.SetTracerProvider`, sees [the spans that the command line records](../reference/telemetry.md#traces) inside its own trace: the plan, each resource, each call to a provider, each request to the state. The debug events are attached to them.

## 9. Ask without planning

Not every program plans. The other methods of a workspace answer the questions the other commands answer, and the ones that only read the state need neither the schemas nor a writable state: open with `Schemas: false` and `State: pravo.StateRead`.

| Method | Command | Returns |
|---|---|---|
| `ws.Drift(ctx)` | `pravo drift` | What changed outside pravo, the report of `--json`. `DriftDue(ctx, limit)` reads the records past their refresh age, as `--due --limit`. |
| `ws.Why(ctx, refresh, addrs...)` | `pravo why` | The action, the reasons and the dependents of each address. |
| `ws.Discover(ctx, opts)`, `ws.DiscoveryReport()` | `pravo discover`, `discover show` | The objects a discovery found, and what each is to the state. |
| `ws.QueryState(expr)` | `pravo state query` | A CUE expression over the state, as JSON or CUE. |
| `ws.Dependents(addr)` | `pravo state dependents` | Everything a change to the resource can reach. |

## What to depend on

Types that the package shares with pravo's internal packages, `Root`, `Store`, `Engine`, `Plan` and their kin, are the way to the engine while the interface settles, and they will change. A program that wants a contract reads the documents: the plan document, the drift report, the discovery report and the events of an apply are the same on the command line and in the library, and they are versioned. `examples/embed` in pravo's source is this runner in its shortest form, and it is built with every change to the package, so a change that breaks it is found before it ships.

## What next

- [The Go library](../reference/library.md) lists every field of `Config`, every method and what each returns.
- [The library and the machine interface](../concepts/the-library-and-the-machine-interface.md) says what a program gets from the library, from `--json` and from the MCP server, and which is the stable surface.
- [Let an agent plan and apply](agents-and-mcp.md) serves the same operations to an agent.
- [Run pravo in a pipeline](ci.md) is the shell version of the runner, with the exit codes.
