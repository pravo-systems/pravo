package main

import (
	"context"
	"fmt"
	"github.com/pravo-systems/pravo/pravo"
	"os"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/tfstate"
	"github.com/pravo-systems/pravo/internal/ui"
)

func stateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Inspect and modify the state graph",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	// The tags select the state that $settings.state.path names, as they do
	// for the commands that load the specification.
	pf := cmd.PersistentFlags()
	pf.StringArrayVarP(&flagTags, "tag", "t", nil, tagUsage)
	pf.BoolVarP(&flagSysTags, "inject-vars", "T", false, sysTagsUsage)
	// Subcommands run with their own context; the parent's is never set.
	load := func() (*state.Store, error) {
		root, err := loadRoot()
		if err != nil {
			return nil, err
		}
		return openState(context.Background(), root)
	}
	// loadRead is for the subcommands that only look: a state that does not
	// exist is empty, and none is created.
	loadRead := func() (*state.Store, error) {
		root, err := loadRoot()
		if err != nil {
			return nil, err
		}
		return openStateReadOnly(context.Background(), root)
	}
	// loadValues is loadRead for a subcommand that prints values: a sealed state
	// is refused to a user who is not one of its readers.
	loadValues := func() (*state.Store, error) {
		root, err := loadRoot()
		if err != nil {
			return nil, err
		}
		return openStateValues(context.Background(), root)
	}
	copyCmd := func(use, short string, toRemote bool) *cobra.Command {
		return &cobra.Command{
			Use: use, Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				st, err := load()
				if err != nil {
					return err
				}
				local, err := state.NewLocalBackend(args[0])
				if err != nil {
					return err
				}
				src, dst := state.Backend(local), st.Backend()
				if !toRemote {
					src, dst = st.Backend(), state.Backend(local)
				}
				n, err := state.Copy(cmd.Context(), src, dst)
				if err != nil {
					return err
				}
				out.Printf("Copied %d objects from %s to %s\n", n, src.Name(), dst.Name())
				return nil
			},
		}
	}
	cmd.AddCommand(verifyCmd(), &cobra.Command{
		Use: "list", Short: "List resource addresses in state", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadRead()
			if err != nil {
				return err
			}
			for _, a := range st.Addresses() {
				fmt.Println(a)
			}
			return nil
		},
	}, &cobra.Command{
		Use: "show <addr>", Short: "Show a node: attributes as CUE plus its bookkeeping", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadValues()
			if err != nil {
				return err
			}
			r := st.Get(args[0])
			if r == nil {
				return fmt.Errorf("%s is not in state", args[0])
			}
			if flagJSON {
				return emitJSON(map[string]any{"node": r, "dependents": st.Dependents(r.Address())})
			}
			provider := r.Provider
			if r.ProviderVersion != "" {
				provider += " v" + r.ProviderVersion
			}
			fmt.Printf("// %s (provider %s as %s, schema version %d, revision %d)\n", r.Address(), provider, r.ProviderKey, r.SchemaVersion, r.Revision)
			fmt.Printf("// refreshed %s, applied %s, config hash %s\n", ago(r.RefreshedAt), ago(r.AppliedAt), orNone(r.ConfigHash))
			if r.DriftedAt != nil {
				fmt.Printf("// drifted %s\n", ago(r.DriftedAt))
			}
			if r.Gone {
				fmt.Printf("// gone\n")
			}
			if len(r.Dependencies) > 0 {
				fmt.Printf("// depends on %s\n", strings.Join(r.Dependencies, ", "))
			}
			if deps := st.Dependents(r.Address()); len(deps) > 0 {
				fmt.Printf("// needed by %s\n", strings.Join(deps, ", "))
			}
			fmt.Println(jsonToCUE(r.Attributes, ""))
			return nil
		},
	}, &cobra.Command{
		Use: "rm <addr>", Short: "Forget a resource without destroying it", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := load()
			if err != nil {
				return err
			}
			if err := st.Lock(args[0], "pravo state rm", ""); err != nil {
				return err
			}
			defer st.Unlock(args[0])
			r := st.Get(args[0])
			if r == nil {
				return fmt.Errorf("%s is not in state", args[0])
			}
			txn := st.Begin("edit", "pravo state rm "+args[0])
			if err := txn.Start(); err != nil {
				return err
			}
			if _, err := st.Remove(args[0]); err != nil {
				return err
			}
			txn.Record(args[0], "forget", r.Revision, 0)
			out.Printf("Removed %s\n", args[0])
			return txn.Finish("applied", "forgot "+args[0], true)
		},
	}, &cobra.Command{
		Use: "mv <from> <to>", Short: "Rename a resource in state", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := load()
			if err != nil {
				return err
			}
			r := st.Get(args[0])
			if r == nil {
				return fmt.Errorf("%s is not in state", args[0])
			}
			if p := cue.ParsePath(args[1]); p.Err() != nil {
				return fmt.Errorf("%q is not a resource address: %w", args[1], p.Err())
			}
			if st.Get(args[1]) != nil {
				return fmt.Errorf("%s already exists in state", args[1])
			}
			for _, a := range args {
				if err := st.Lock(a, "pravo state mv", ""); err != nil {
					return err
				}
				defer st.Unlock(a)
			}
			// The records that depend on the one that moves are written too, and
			// every record is read again from the state with its lock held.
			deps, release, err := st.LockDependents(args[0], "pravo state mv", "")
			if err != nil {
				return err
			}
			defer release()
			for _, a := range args {
				if err := st.Verify(a); err != nil {
					return err
				}
			}
			txn := st.Begin("edit", "pravo state mv "+args[0]+" "+args[1])
			if err := txn.Start(); err != nil {
				return err
			}
			before := r.Revision
			if _, err := st.Remove(args[0]); err != nil {
				return err
			}
			moved := *r
			moved.Addr = args[1]
			if sels := cue.ParsePath(args[1]).Selectors(); len(sels) > 0 {
				moved.Name = sels[len(sels)-1].String()
			}
			moved.Revision = 0
			if err := st.Set(&moved, txn.ID()); err != nil {
				return err
			}
			txn.Record(args[0], "move-from", before, 0)
			txn.Record(args[1], "move-to", 0, moved.Revision)
			for _, d := range deps {
				other := st.Get(d)
				changed := false
				for i, dep := range other.Dependencies {
					if dep == args[0] {
						other.Dependencies[i] = args[1]
						changed = true
					}
				}
				if changed {
					if err := st.Set(other, txn.ID()); err != nil {
						return err
					}
				}
			}
			out.Printf("Moved %s to %s\n", args[0], args[1])
			return txn.Finish("applied", "moved "+args[0]+" to "+args[1], true)
		},
	}, &cobra.Command{
		Use: "log", Short: "List recorded transactions, newest first", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadRead()
			if err != nil {
				return err
			}
			txns, err := st.Transactions()
			if err != nil {
				return err
			}
			// Under a sealed state the signatures are checked against the readers file, on the
			// transactions as they are stored: the status below is a reading of them.
			readers, checked := []protect.Reader(nil), false
			if prot := st.Protection(); prot.Sealed != nil {
				if root, err := loadRoot(); err == nil {
					readers, checked = attributionReaders(loadReadersFile(root.Dir, flagAccessDir))
				}
			}
			verdicts := make([]state.TxnVerdict, len(txns))
			invalid := 0
			if checked {
				for i := range txns {
					if verdicts[i] = state.VerifyTransaction(st.Lineage(), &txns[i], readers); verdicts[i].Overall() == protect.SigInvalid {
						invalid++
					}
				}
			}
			// A transaction that is recorded as running and whose command is gone was
			// left by a command that was killed.
			interrupted, err := st.Interrupted(txns)
			if err != nil {
				return err
			}
			for i := range txns {
				if interrupted[txns[i].ID] {
					txns[i].Status = "interrupted"
				}
				// A reader of a sealed state gets the text of an error that the
				// transaction keeps sealed; anyone else sees the summary as it is.
				if txns[i].SummarySealed != nil {
					txns[i].Summary = st.SummaryText(txns[i])
					txns[i].SummarySealed = nil
				}
			}
			if !checked && !flagJSON && st.Protection().Sealed != nil {
				out.Println(out.C(ui.Dim, "Signatures are not checked: the readers file is missing or invalid."))
			}
			if flagJSON {
				type view struct {
					state.Transaction
					Signature *sigStatusJSON `json:"signature,omitempty"`
				}
				views := make([]view, len(txns))
				for i, t := range txns {
					views[i] = view{Transaction: t}
					if checked {
						views[i].Signature = sigJSON(verdicts[i])
					}
				}
				if err := emitJSON(views); err != nil {
					return err
				}
				return invalidTxns(invalid)
			}
			for i, t := range txns {
				dur := ""
				if t.Finished != nil {
					dur = " " + t.Finished.Sub(t.Started).Round(time.Millisecond).String()
				}
				out.Printf("%s  %-8s %-9s %s%s\n", t.ID, t.Kind, t.Status, t.Started.Local().Format("2006-01-02 15:04:05"), dur)
				out.Printf("    %s\n", out.C(ui.Dim, t.Command))
				if t.Actor != "" || t.Reason != "" || t.GitRevision != "" {
					out.Printf("    %s\n", out.C(ui.Dim, strings.TrimSpace(fmt.Sprintf("actor %s  %s  %s", orNone(t.Actor), t.Reason, t.GitRevision))))
				}
				if checked {
					line := txnSigText(verdicts[i])
					if verdicts[i].Overall() == protect.SigInvalid {
						line = out.C(ui.Red, line)
					} else {
						line = out.C(ui.Dim, line)
					}
					out.Printf("    %s\n", line)
				}
				if t.Summary != "" {
					out.Printf("    %s\n", t.Summary)
				}
				for _, c := range t.Changes {
					out.Printf("    %-8s %s", c.Action, c.Addr)
					if c.After > 0 {
						out.Printf("%s", out.C(ui.Dim, fmt.Sprintf("  r%d -> r%d", c.Before, c.After)))
					}
					out.Println()
				}
			}
			return invalidTxns(invalid)
		},
	}, &cobra.Command{
		Use: "probe", Short: "Check that the S3 store honours the conditional requests that locks need", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loadRoot()
			if err != nil {
				return err
			}
			return probeS3(context.Background(), root)
		},
	}, &cobra.Command{
		Use: "locks", Short: "List resource locks held by running commands", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadRead()
			if err != nil {
				return err
			}
			locks, err := st.Locks()
			if err != nil {
				return err
			}
			if flagJSON {
				if locks == nil {
					locks = []state.Lock{}
				}
				return emitJSON(locks)
			}
			if len(locks) == 0 {
				out.Println("No locks or reservations held.")
			}
			for _, l := range locks {
				who := l.Actor
				if who == "" {
					who = l.User
				}
				stale := ""
				if l.Stale {
					stale = " [stale]"
				}
				if l.Kind == "reservation" {
					until := "no expiry"
					if l.Expires != nil {
						until = "until " + l.Expires.Local().Format(time.RFC3339)
					}
					out.Printf("%s  reserved by %s %s: %s%s\n", l.Addr, who, until, l.Command, stale)
					continue
				}
				out.Printf("%s  locked by %s, pid %d on %s since %s: %s%s\n", l.Addr, who, l.PID, l.Host, l.Since.Local().Format(time.RFC3339), l.Command, stale)
			}
			return nil
		},
	}, upgradeCmd(load), unlockCmd(load), copyCmd("pull <dir>", "Copy the configured state graph into a local directory", false),
		copyCmd("push <dir>", "Copy a local state directory into the configured backend", true),
		stateEdgesCmd(loadRead), &cobra.Command{
			Use: "dependents <addr>", Short: "List everything a change to the resource can reach", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				st, err := loadRead()
				if err != nil {
					return err
				}
				reach, err := pravo.Dependents(st, args[0])
				if err != nil {
					return err
				}
				for _, a := range reach {
					fmt.Println(a)
				}
				return nil
			},
		}, &cobra.Command{
			Use:   "query <cue-expression>",
			Short: "Evaluate a CUE expression over the state graph",
			Long: `Evaluates a CUE expression with the state graph in scope and prints the
result. The scope has these fields:

  resources: {<addr>: {mode, type, name, provider, attributes, dependencies, revision, refreshed_at, ...}}
  edges:     [{from: <addr>, to: <addr>}]
  outputs:   {<name>: value}

Examples:

  pravo state query '[for a, r in resources if r.type == "aws_instance" {a}]'
  pravo state query '{for a, r in resources {"\(a)": r.attributes.id}}'
  pravo state query 'len(resources)'`,
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				st, err := loadValues()
				if err != nil {
					return err
				}
				res, err := pravo.QueryState(st, args[0])
				if err != nil {
					return err
				}
				if flagJSON {
					b, err := res.JSON()
					if err != nil {
						return err
					}
					os.Stdout.Write(append(b, '\n'))
					return nil
				}
				b, err := res.CUE()
				if err != nil {
					return err
				}
				os.Stdout.Write(b)
				fmt.Println()
				return nil
			},
		}, stateImportTfstateCmd(load))
	return cmd
}

// unlockCmd is "state unlock": it releases one lock, or with --stale every lock
// that may be reclaimed.
// upgradeCmd is "state upgrade": the first step of "protect init" alone. It is
// hidden, because a state in format 2 is not encrypted and nothing but a test or
// a careful operator has a use for it.
func upgradeCmd(load func() (*state.Store, error)) *cobra.Command {
	return &cobra.Command{
		Use:    "upgrade",
		Short:  "Store each record as an immutable object (format 2), without encrypting it",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := load()
			if err != nil {
				return err
			}
			res, err := st.Upgrade(cmd.Context(), state.UpgradeOptions{To: state.FormatImmutable})
			if err != nil {
				return err
			}
			if flagJSON {
				return emitJSON(map[string]any{"from": res.From, "to": res.To, "records": res.Records})
			}
			out.Printf("The state is in format %d: %s as immutable objects\n", res.To, plural(res.Records, "record", "records"))
			return nil
		},
	}
}

func unlockCmd(load func() (*state.Store, error)) *cobra.Command {
	var stale bool
	cmd := &cobra.Command{
		Use: "unlock [addr]", Short: "Release a resource lock left behind by a crashed command", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case stale && len(args) > 0:
				return fmt.Errorf("--stale releases every stale lock; it takes no address")
			case !stale && len(args) == 0:
				return fmt.Errorf("give the address of the lock to release, or --stale for every lock that is stale")
			}
			st, err := load()
			if err != nil {
				return err
			}
			if stale {
				released, err := st.ReleaseStale()
				for _, a := range released {
					out.Printf("Unlocked %s\n", a)
				}
				if err != nil {
					return err
				}
				if len(released) == 0 {
					out.Println("No stale locks.")
				}
				return nil
			}
			ok, err := st.ForceUnlock(args[0])
			if err != nil {
				return err
			}
			if !ok {
				out.Printf("%s was not locked\n", args[0])
			} else {
				out.Printf("Unlocked %s\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&stale, "stale", false, "release every lock and reservation that may be reclaimed: its process is gone, it was not renewed for 10 minutes, or it expired")
	return cmd
}

func stateEdgesCmd(load func() (*state.Store, error)) *cobra.Command {
	var dot bool
	cmd := &cobra.Command{
		Use: "graph", Short: "Print the dependency edges, optionally as DOT", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := load()
			if err != nil {
				return err
			}
			edges := st.Edges()
			if dot {
				fmt.Println("digraph pravo {")
				fmt.Println("  rankdir = LR;")
				for _, a := range st.Addresses() {
					fmt.Printf("  %q;\n", a)
				}
				for _, e := range edges {
					fmt.Printf("  %q -> %q;\n", e.From, e.To)
				}
				fmt.Println("}")
				return nil
			}
			if len(edges) == 0 {
				out.Println("No dependency edges recorded.")
			}
			for _, e := range edges {
				fmt.Printf("%s -> %s\n", e.From, e.To)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dot, "dot", false, "print Graphviz DOT")
	return cmd
}

func stateImportTfstateCmd(load func() (*state.Store, error)) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "import-tfstate <terraform.tfstate>",
		Short: "Convert an OpenTofu or legacy Terraform state file (format 4) into the state graph",
		Long: `Converts an OpenTofu or legacy Terraform state file (format version 4) into
pravo's state.

Every managed resource and data source instance becomes one node, keeping its
OpenTofu address (aws_s3_bucket.logs, data.aws_region.current). Instances
created by count or for_each get the name <name>_<key>; resources inside
modules get the module path as a prefix. In pravo a resource's address is
where it sits in the specification, so tell pravo where each one went with $from
on the resource:

    logs: $aws.#Resource.aws_s3_bucket & {$from: "aws_s3_bucket.logs", ...}

or rename a record directly with "pravo state mv". Outputs are copied. The
.tfstate file itself is not modified. Imported nodes have no freshness or
configuration hash yet, so the next plan refreshes and plans each of them
once.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			res, err := tfstate.Convert(data)
			if err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}
			st, err := load()
			if err != nil {
				return err
			}
			if !force && (st.Len() > 0 || len(st.Outputs()) > 0) {
				return fmt.Errorf("%s already holds %d resources and %d outputs; use -force to replace them", st.Dir(), st.Len(), len(st.Outputs()))
			}
			txn := st.Begin("import", "pravo state import-tfstate "+args[0])
			if err := txn.Start(); err != nil {
				return err
			}
			if err := st.Clear(); err != nil {
				return err
			}
			for _, r := range res.Resources {
				if err := st.Set(r, txn.ID()); err != nil {
					return err
				}
				txn.Record(r.Address(), "import", 0, r.Revision)
			}
			if err := st.SetOutputs(res.Outputs); err != nil {
				return err
			}
			if err := txn.Finish("applied", fmt.Sprintf("imported %d resources from %s", len(res.Resources), args[0]), true); err != nil {
				return err
			}
			out.Printf("Imported %d resources and %d outputs from %s into %s\n", len(res.Resources), len(res.Outputs), args[0], st.Dir())
			for _, n := range res.Notes {
				out.Warning(n)
			}
			if len(res.Renamed) > 0 {
				out.Println()
				out.Println(out.C(ui.Bold, "Renamed instances") + " (these are the addresses now in the state; point each at its place in the specification with $from):")
				for _, r := range res.Renamed {
					out.Printf("  %s -> %s\n", r.From, r.To)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing non-empty state")
	return cmd
}

func ago(t *time.Time) string {
	if t == nil {
		return "never"
	}
	d := time.Since(*t).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return t.Local().Format("2006-01-02")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
