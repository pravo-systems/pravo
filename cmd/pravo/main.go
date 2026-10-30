// Command pravo is an infrastructure-as-code tool that evaluates CUE
// configurations and drives OpenTofu providers.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/progress"
	"github.com/pravo-systems/pravo/internal/telemetry"
	"github.com/pravo-systems/pravo/internal/ui"
)

// Version is the pravo version string.
var Version = "0.1.0-dev"

// out prints the output of the command that runs.
var out *ui.Printer

// cmdSpan is the span of the running command; ended in main.
var (
	cmdSpan  trace.Span
	cmdName  string
	cmdStart time.Time
)

func main() {
	os.Args = normaliseArgs(os.Args)
	interrupt := &engine.Interrupt{}
	ctx, cancel := context.WithCancel(engine.WithInterrupt(context.Background(), interrupt))
	defer cancel()
	sigs := make(chan os.Signal, 3)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	finished := make(chan struct{})
	go watchSignals(sigs, finished, cancel, interrupt, os.Stderr, func(code int) {
		stopProgress()
		plugin.CleanupClients()
		os.Exit(code)
	}, interruptGrace)
	if progress.Wanted(env.OS().Get) {
		spinner = progress.New(os.Stderr)
		telemetry.Watch(spinner)
	}
	shutdownTelemetry := telemetry.Setup(ctx, Version, env.OS())
	// A trace started by the caller (a CI job, the MCP server) continues here.
	ctx = telemetry.ParentFromEnv(ctx, environment())
	root := newRootCmd()
	err := root.ExecuteContext(ctx)
	stopProgress()
	plugin.CleanupClients()
	code := 0
	if err != nil {
		if out == nil {
			out = ui.NewPrinter(os.Stderr, false)
		}
		code = exitCodeOf(err)
		if !errors.Is(err, errSilent) {
			if flags.JSON {
				emitLine(map[string]any{"kind": "error", "code": code, "message": err.Error()})
			}
			out.Error(err)
		}
	}
	finishCommand(ctx, err, code)
	_ = shutdownTelemetry(context.Background())
	close(finished)
	if code != 0 {
		cancel()
		os.Exit(code)
	}
}

// interruptGrace is how long a command waits, once the providers were asked to
// stop, for them to return before it gives up and exits.
const interruptGrace = 30 * time.Second

// abandonWait is how long a command that gives its apply up waits for the state
// to take what it writes: the marks on the changes in flight, the transaction and
// the release of the locks.
var abandonWait = 20 * time.Second

// interrupter is what the handling of signals needs of the apply that a command
// runs.
type interrupter interface {
	// InFlight is the number of changes that providers are making.
	InFlight() int
	// Stop asks the providers to stop.
	Stop()
	// Abandon gives the apply up and returns what it says of the changes whose
	// result is not known.
	Abandon() []string
}

// watchSignals handles the signals that a command receives, in stages. The first
// cancels the command, so that no task is started, and says how many changes
// the providers are still making: they finish, and the state records them. The
// second asks the providers to stop, and waits for them for as long as grace. The
// third, or the end of grace, gives the apply up and exits; so does the second
// when no change is in flight, since there is nothing to wait for. It returns
// when the command is finished.
func watchSignals(sigs <-chan os.Signal, finished <-chan struct{}, cancel context.CancelFunc, in interrupter, w io.Writer, exit func(int), grace time.Duration) {
	select {
	case <-sigs:
	case <-finished:
		return
	}
	n := in.InFlight()
	cancel()
	if n > 0 {
		fmt.Fprintf(w, "Interrupted. Waiting for %s; interrupt again to ask the providers to stop.\n", operations(n))
	}
	select {
	case <-sigs:
	case <-finished:
		return
	}
	if in.InFlight() > 0 {
		// A provider that does not answer must not keep the third signal waiting.
		go in.Stop()
		fmt.Fprintf(w, "Asking the providers to stop. Waiting up to %s for what they return; interrupt again to exit now.\n", grace.Round(time.Second))
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-sigs:
		case <-timer.C:
		case <-finished:
			return
		}
	}
	// Abandoning writes to the state: the marks, the transaction and the release of
	// the locks. A state that does not answer must not keep the command from
	// exiting.
	abandoned := make(chan []string, 1)
	go func() { abandoned <- in.Abandon() }()
	var notes []string
	select {
	case notes = <-abandoned:
	case <-time.After(abandonWait):
		fmt.Fprintf(w, "The state did not answer within %s. Exiting; changes that were in flight may have no record, and the locks stay until they go stale.\n", abandonWait.Round(time.Second))
		exit(1)
		return
	}
	if len(notes) == 0 {
		fmt.Fprintln(w, "Exiting.")
	} else {
		fmt.Fprintln(w, "Exiting without waiting for the providers. The result of these changes is not known:")
		for _, note := range notes {
			fmt.Fprintf(w, "  %s\n", note)
		}
	}
	exit(1)
}

func operations(n int) string {
	if n == 1 {
		return "1 operation"
	}
	return fmt.Sprintf("%d operations", n)
}

// startCommand opens the command's span; called once the command is known.
func startCommand(cmd *cobra.Command) {
	runningCommand = cmd
	cmdName = cmd.CommandPath()
	cmdStart = time.Now()
	ctx, span := telemetry.Tracer().Start(cmd.Context(), cmdName, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(
		attribute.String("pravo.command", cmdName),
		attribute.String("pravo.version", Version),
		attribute.String("pravo.actor", flags.Actor),
		attribute.String("pravo.reason", flags.Reason),
		attribute.Bool("pravo.json", flags.JSON),
		attribute.String("pravo.command_line", commandLine(os.Args)),
	))
	cmdSpan = span
	cmd.SetContext(ctx)
}

// commandLine is the command as it was typed, for the record of the run: the name of the program as "pravo", and the
// arguments quoted where a shell would need it. The viewer masks the values of tags and of secrets before it shows it.
func commandLine(argv []string) string {
	out := []string{"pravo"}
	for _, a := range argv[1:] {
		if a == "" || strings.ContainsAny(a, " \t\"'$`\\") {
			a = strconv.Quote(a)
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

// finishCommand ends the command's span and records its duration and, for a
// refusal, why.
func finishCommand(ctx context.Context, err error, code int) {
	if cmdSpan == nil {
		return
	}
	plog.Debug(ctx, "finished", "command", cmdName, "exit", code, "took", took(cmdStart))
	cmdSpan.SetAttributes(attribute.Int("pravo.exit_code", code))
	if code == int(engine.ExitChanges) {
		err = nil // exit 2 reports changes or drift; it is not a failure
	}
	switch engine.ExitCode(code) {
	case engine.ExitStale:
		telemetry.Rejected(ctx, "stale")
	case engine.ExitLocked:
		telemetry.Rejected(ctx, "locked")
	case engine.ExitRefused:
		telemetry.Rejected(ctx, "policy")
	}
	telemetry.RunDone(ctx, cmdName, time.Since(cmdStart), code)
	if err != nil && errors.Is(err, errSilent) {
		err = nil
	}
	telemetry.End(cmdSpan, err)
}

// normaliseArgs lets users write OpenTofu-style single-dash long flags
// (-auto-approve, -refresh=false) by rewriting them to --long form. The arguments after a
// bare -- are left as they are.
func normaliseArgs(args []string) []string {
	out := make([]string, 0, len(args))
	ended := false
	for i, a := range args {
		if a == "--" {
			ended = true // what follows belongs to a command that pravo runs, as in pravo secret exec
		}
		if i > 0 && !ended && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			name := a[1:]
			if j := strings.IndexByte(name, '='); j >= 0 {
				name = name[:j]
			}
			if len(name) > 1 && strings.Trim(name, "v") != "" {
				a = "-" + a // -vv is the verbose flag twice, not a long flag
			}
		}
		out = append(out, a)
	}
	return out
}

// spinner draws what a command is doing on a terminal; nil when stderr is not
// one, in CI and when asked not to.
var spinner *progress.Tracker

func stopProgress() {
	if spinner != nil {
		spinner.Stop()
	}
}

// showsProgress lists the commands that work for long enough, and print their
// result only at the end, to show what they are doing meanwhile. serve shows it
// until the server is up.
func showsProgress(cmd *cobra.Command) bool {
	switch strings.Fields(cmd.CommandPath())[1:][0] {
	case "plan", "apply", "destroy", "drift", "discover", "init", "import", "validate", "why", "serve":
		return true
	}
	return false
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "pravo",
		Short:         "Infrastructure as code with CUE and the OpenTofu provider ecosystem",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			defer startCommand(cmd) // after the flags that the span records are resolved
			e := env.OS()
			plog.Configure(flags.Verbose, e.Get("PRAVO_LOG"))
			if flags.Verbose >= 2 && e.Get("PRAVO_LOG") == "" {
				e = e.With("PRAVO_LOG", "debug") // the providers' own log, too
			}
			setEnvironment(e)
			applyProcessSettings(e)
			if err := applyParallelism(e); err != nil {
				return err
			}
			if flags.JSON || flags.Markdown {
				// Human output moves to stderr; stdout carries JSON or Markdown only.
				out = ui.NewPrinter(os.Stderr, true)
			} else {
				out = ui.NewPrinter(os.Stdout, colourOff(flags.NoColor))
			}
			if spinner != nil && flags.Verbose == 0 && showsProgress(cmd) {
				// The status line gives way to whatever the command prints.
				out.Out = spinner.Guard(out.Out)
				spinner.Start(!colourOff(flags.NoColor))
			}
			if flags.Actor == "" {
				flags.Actor = e.Get("PRAVO_ACTOR")
			}
			if flags.Reason == "" {
				flags.Reason = e.Get("PRAVO_REASON")
			}
			if flags.Dir != "" {
				if err := os.Chdir(flags.Dir); err != nil {
					return err
				}
			}
			return nil
		},
	}
	pf := cmd.PersistentFlags()
	pf.StringVarP(&flags.Dir, "chdir", "C", "", "run as if started in this directory")
	pf.BoolVar(&flags.NoColor, "no-color", false, "disable coloured output")
	pf.CountVarP(&flags.Verbose, "verbose", "v", "log what pravo decides and does to stderr; -vv adds finer detail and the providers' own log ($PRAVO_LOG=debug|trace)")
	pf.BoolVar(&flags.JSON, "json", false, "machine-readable output on stdout (progress goes to stderr)")
	pf.StringVar(&flags.Actor, "actor", "", "a label for who is acting, recorded on transactions, locks and plans; not verified ($PRAVO_ACTOR)")
	pf.StringVar(&flags.Reason, "reason", "", "why; recorded on transactions and plans ($PRAVO_REASON)")
	pf.StringVar(&flags.Identity, "identity", "", identityUsage)
	cmd.AddCommand(
		initCmd(), planCmd(), applyCmd(), destroyCmd(), showCmd(), outputCmd(), stateCmd(), importCmd(), discoverCmd(),
		providersCmd(), genCmd(), validateCmd(), evalCmd(), fmtCmd(), versionCmd(),
		schemaCmd(), whyCmd(), driftCmd(), mcpCmd(), modCmd(), serveCmd(), skillCmd(), idCmd(), protectCmd(), policyCmd(), secretCmd(),
	)
	return cmd
}

const (
	tagUsage     = "inject a value for an @tag() field: -t key=value"
	sysTagsUsage = "inject system tag variables (os, arch, cwd, ...)"
)

func addTagFlags(cmd *cobra.Command) {
	cmd.Flags().StringArrayVarP(&flags.Tags, "tag", "t", nil, tagUsage)
	cmd.Flags().BoolVarP(&flags.SysTags, "inject-vars", "T", false, sysTagsUsage)
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the pravo version and the version of CUE it embeds",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("pravo %s\n", Version)
			// Policies are evaluated by the CUE inside this binary, so the version
			// of a cue command used to test them has to match.
			if v := cueVersion(); v != "" {
				fmt.Printf("cue %s\n", v)
			}
			return nil
		},
	}
}

// cueVersion is the version of the CUE module this binary was built with.
func cueVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, d := range bi.Deps {
		if d.Path == "cuelang.org/go" {
			return d.Version
		}
	}
	return ""
}

// stderr is where a command writes a warning while it works: the status line is
// taken off the screen first.
func stderr() io.Writer {
	if spinner != nil {
		return spinner.Guard(os.Stderr)
	}
	return os.Stderr
}
