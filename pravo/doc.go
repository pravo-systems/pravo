// Package pravo is the programmatic interface to pravo: what the command line
// does, as a library.
//
// A program that builds on pravo (a runner, a pipeline, a service that plans and
// applies on behalf of its users) opens a Workspace with Open and calls its
// methods, instead of running the binary and parsing what it prints.
//
//	ws, err := pravo.Open(ctx, pravo.Config{
//		Dir: "/work/estate", Env: pravo.NewEnv(environ),
//		Install: true, Schemas: true, State: pravo.StateWrite,
//	})
//	if err != nil { ... }
//	defer ws.Close()
//	plan, err := ws.Plan(ctx, pravo.PlanOptions{})
//
// A plan can be stored in the state (Planned.Store) for someone else to review
// and apply (Workspace.ApplyStored), which claims the plan while it applies and
// records what was applied from it.
//
// The package takes everything it needs as arguments. It reads no flag and no
// variable of the environment of the process, it prints nothing, and it asks
// nobody for a passphrase: what a program shows or asks is a function in Config.
// The environment is a value (Env) that the caller builds.
//
// # Embedding
//
// Workspaces on different directories can run in one process at the same time:
// nothing changes the working directory of the process, and each workspace has
// its own environment and state. A relative Config.Dir is made absolute when the
// workspace is opened. The remote state takes its credentials from Config.Env
// (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, AWS_REGION,
// AWS_PROFILE, PRAVO_STATE_TOKEN) and leaves the rest of the AWS SDK's default
// chain (shared files, instance role) to the SDK, which reads the process. The
// debug log goes where SetLogHandler says; spans go to the global OpenTelemetry
// tracer provider of the host.
//
// This package is unstable (v0). Types that are aliases of types in pravo's
// internal packages (Root, Store, Engine, Plan and the like) are the way to the
// engine while the interface is settled; they will be replaced by types of this
// package, and the methods that return them will change.
package pravo
