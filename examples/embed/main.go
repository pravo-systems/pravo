// Command embed is a small program built on the pravo library instead of the
// pravo binary: it opens a specification, plans it, prints the plan as JSON,
// and with -apply applies it.
//
//	go run ./examples/embed -dir examples/getting-started
//	go run ./examples/embed -dir examples/getting-started -apply
//
// It is what a runner, a pipeline step or a service would do around pravo:
// the environment is a value the program builds, the state is opened by the
// library, and what comes back is data, not text to parse.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/pravo-systems/pravo/pravo"
)

func main() {
	dir := flag.String("dir", ".", "directory of the specification")
	apply := flag.Bool("apply", false, "apply the plan")
	verbose := flag.Bool("v", false, "print pravo's debug log")
	flag.Parse()

	if *verbose {
		pravo.SetLogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	if err := run(context.Background(), os.Stdout, *dir, *apply); err != nil {
		fmt.Fprintln(os.Stderr, "embed:", err)
		os.Exit(1)
	}
}

// run plans the specification in dir, writes the plan as JSON to w, and applies it
// when asked and when the policies of the specification allow it.
func run(ctx context.Context, w io.Writer, dir string, apply bool) error {
	ws, err := pravo.Open(ctx, pravo.Config{
		Dir: dir,
		// The program decides what the run sees of the environment. This one
		// passes its own on; a service would pass the variables of one job.
		Env:     pravo.OSEnv(),
		Install: true, Schemas: true, State: pravo.StateWrite,
		Actor: "example/embed", Reason: "run by examples/embed",
		Args: []string{"plan"},
		// What a person would see as progress; nil drops it.
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
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(planned.View()); err != nil {
		return err
	}
	// The policies of the specification judge the plan before it is applied.
	if err := planned.CheckPolicy(ctx); err != nil {
		return err
	}
	if !apply || !planned.HasChanges() {
		return nil
	}
	res, err := planned.Apply(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "applied: %d added, %d changed, %d destroyed (txn %s)\n", res.Added, res.Changed, res.Destroyed, res.Txn)
	return nil
}
