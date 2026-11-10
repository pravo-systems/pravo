package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/tfstate"
	"github.com/pravo-systems/pravo/internal/ui"
	"github.com/pravo-systems/pravo/pravo"
)

func stateCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Inspect and modify the state graph",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	// The tags select the state that $settings.state.path names, as they do
	// for the commands that load the specification.
	pf := cmd.PersistentFlags()
	pf.StringArrayVarP(&rt.flags.Tags, "tag", "t", nil, tagUsage)
	addEnvFlag(rt, pf)
	pf.BoolVarP(&rt.flags.SysTags, "inject-vars", "T", false, sysTagsUsage)
	// Subcommands run with their own context; the parent's is never set.
	load := func() (*state.Store, error) {
		root, err := loadRoot(rt)
		if err != nil {
			return nil, err
		}
		return openState(commandContext(), rt, root)
	}
	// loadEdit is load for a subcommand that changes the records, as an apply does: a state
	// whose record serves another environment is refused, as an apply is.
	loadEdit := func() (*state.Store, error) {
		root, err := loadRoot(rt)
		if err != nil {
			return nil, err
		}
		st, err := openState(commandContext(), rt, root)
		if err != nil {
			return nil, err
		}
		if err := refuseOtherEnvironment(commandContext(), rt, root, st); err != nil {
			st.Close()
			return nil, err
		}
		return st, nil
	}
	// loadRead is for the subcommands that only look: a state that does not
	// exist is empty, and none is created.
	loadRead := func() (*state.Store, error) {
		root, err := loadRoot(rt)
		if err != nil {
			return nil, err
		}
		return openStateReadOnly(commandContext(), rt, root)
	}
	// loadValues is loadRead for a subcommand that prints values: a sealed state
	// is refused to a user who is not one of its readers.
	loadValues := func() (*state.Store, error) {
		root, err := loadRoot(rt)
		if err != nil {
			return nil, err
		}
		return openStateValues(commandContext(), rt, root)
	}
	// whole is a loader that also refuses a state with a pointer gone, as plan and drift do:
	// a listing that lacks a resource would read as the truth.
	whole := func(l func() (*state.Store, error)) func() (*state.Store, error) {
		return func() (*state.Store, error) {
			st, err := l()
			if err != nil {
				return nil, err
			}
			if prot := st.Protection(); prot.Sealed != nil && !st.Locked() {
				var readers []pravo.Reader
				if root, err := loadRoot(rt); err == nil {
					readers, _ = attributionReaders(readersOf(rt, root))
				}
				if err := st.CheckLoadedNodes(commandContext(), readers); err != nil {
					st.Close()
					return nil, err
				}
			}
			return st, nil
		}
	}
	cmd.AddCommand(
		verifyCmd(rt),
		stateListCmd(whole(loadRead)),
		stateShowCmd(rt, whole(loadValues)),
		stateRmCmd(rt, loadEdit),
		stateMvCmd(rt, loadEdit),
		stateLogCmd(rt, loadRead),
		stateProbeCmd(rt),
		stateLocksCmd(rt, loadRead),
		upgradeCmd(rt, load),
		unlockCmd(rt, load),
		stateCopyCmd(rt, load, "pull <dir>", "Copy the configured state graph into a local directory", false),
		stateCopyCmd(rt, load, "push <dir>", "Copy a local state directory into the configured backend", true),
		stateEdgesCmd(rt, whole(loadRead)),
		stateDependentsCmd(whole(loadRead)),
		stateQueryCmd(rt, whole(loadValues)),
		stateImportTfstateCmd(rt, loadEdit),
	)
	return cmd
}

// stateCopyCmd is "state pull" and "state push": it copies the objects of the state between a
// local directory and the configured backend.
func stateCopyCmd(rt *runContext, load func() (*state.Store, error), use, short string, toRemote bool) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			local, err := state.NewLocalBackend(args[0])
			if err != nil {
				return err
			}
			var src, dst state.Backend
			if toRemote {
				// The objects are copied as they are, sealed or not. The configured state
				// is not opened: a repository that lists readers refuses to open a state
				// that does not exist yet, and a push is what makes it.
				root, err := loadRoot(rt)
				if err != nil {
					return err
				}
				p := &protectRun{ctx: cmd.Context(), root: root, rf: readersOf(rt, root)}
				if dst, err = p.backend(true); err != nil {
					return err
				}
				src = local
			} else {
				st, err := load()
				if err != nil {
					return err
				}
				src, dst = st.Backend(), local
			}
			n, err := state.Copy(cmd.Context(), src, dst)
			if err != nil {
				return err
			}
			rt.out.Printf("Copied %d objects from %s to %s\n", n, src.Name(), dst.Name())
			if toRemote {
				rt.out.Println("Check it with: pravo protect status (a sealed state) or pravo plan.")
			}
			return nil
		},
	}
}

// stateListCmd is "state list".
func stateListCmd(loadRead func() (*state.Store, error)) *cobra.Command {
	return &cobra.Command{
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
	}
}

// stateShowCmd is "state show".
func stateShowCmd(rt *runContext, loadValues func() (*state.Store, error)) *cobra.Command {
	var redact bool
	cmd := &cobra.Command{
		Use: "show <addr>", Short: "Show a node: attributes as CUE plus its bookkeeping", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadValues()
			if err != nil {
				return err
			}
			r := st.Get(args[0])
			if r == nil {
				// "state list" prints an address that is not a plain identifier in quotes.
				r = st.Get(strconv.Quote(args[0]))
			}
			if r == nil {
				return notInState(args[0])
			}
			if redact {
				snap, n, err := redactedSnapshot(cmd.Context(), rt, st)
				if err != nil {
					return err
				}
				r = snap.Resources[r.Address()]
				if n > 0 {
					fmt.Fprintf(os.Stderr, "%d values withheld (--redact)\n", n)
				}
			}
			if rt.flags.JSON {
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
				fmt.Printf("// gone: the provider no longer finds the object as recorded (deleted, or changed into another object)\n")
			}
			if r.Tainted {
				fmt.Printf("// created halfway; the next plan replaces it\n")
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
	}
	cmd.Flags().BoolVar(&redact, "redact", false, "withhold the values the viewer withholds: sensitive and write-only attributes, names of secrets, anything built from them, and the private blob")
	return cmd
}

// stateRmCmd is "state rm".
func stateRmCmd(rt *runContext, load func() (*state.Store, error)) *cobra.Command {
	return &cobra.Command{
		Use: "rm <addr>", Short: "Forget a resource without destroying it", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := load()
			if err != nil {
				return err
			}
			if err := st.Lock(args[0], "pravo state rm", ""); err != nil {
				return err
			}
			defer func() { _ = st.Unlock(args[0]) }()
			r := st.Get(args[0])
			if r == nil {
				return notInState(args[0])
			}
			txn := st.Begin("edit", "pravo state rm "+args[0])
			if err := txn.Start(); err != nil {
				return err
			}
			if _, err := st.Remove(args[0]); err != nil {
				return err
			}
			txn.Record(args[0], "forget", r.Revision, 0)
			rt.out.Printf("Removed %s\n", args[0])
			return txn.Finish("applied", "forgot "+args[0], true)
		},
	}
}

// stateMvCmd is "state mv".
func stateMvCmd(rt *runContext, load func() (*state.Store, error)) *cobra.Command {
	return &cobra.Command{
		Use: "mv <from> <to>", Short: "Rename a resource in state", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := load()
			if err != nil {
				return err
			}
			r := st.Get(args[0])
			if r == nil {
				return notInState(args[0])
			}
			if p := cue.ParsePath(args[1]); p.Err() != nil {
				return fmt.Errorf("%q is not a resource address: %w", args[1], p.Err())
			}
			if st.Get(args[1]) != nil {
				return fmt.Errorf("%s already exists in state", args[1])
			}
			var locked []string
			defer func() {
				for _, a := range locked {
					_ = st.Unlock(a)
				}
			}()
			for _, a := range args {
				if err := st.Lock(a, "pravo state mv", ""); err != nil {
					return err
				}
				locked = append(locked, a)
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
			if err := st.MovePendingCreate(args[0], args[1]); err != nil {
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
					was := other.Revision
					if err := st.Set(other, txn.ID()); err != nil {
						return err
					}
					txn.Record(d, "relink", was, other.Revision)
				}
			}
			rt.out.Printf("Moved %s to %s\n", args[0], args[1])
			return txn.Finish("applied", "moved "+args[0]+" to "+args[1], true)
		},
	}
}

// stateLogCmd is "state log".
func stateLogCmd(rt *runContext, loadRead func() (*state.Store, error)) *cobra.Command {
	return &cobra.Command{
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
			readers, checked := []pravo.Reader(nil), false
			if prot := st.Protection(); prot.Sealed != nil {
				if root, err := loadRoot(rt); err == nil {
					readers, checked = attributionReaders(readersOf(rt, root))
				}
			}
			verdicts := make([]state.TxnVerdict, len(txns))
			invalid := 0
			approvers := txnApprovers(cmd.Context(), st, txns, readers, checked)
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
			if !checked && !rt.flags.JSON && st.Protection().Sealed != nil {
				rt.out.Println(rt.out.C(ui.Dim, "Signatures are not checked: the readers file is missing or invalid."))
			}
			if rt.flags.JSON {
				views := txnViews(txns, verdicts, approvers, checked)
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
				rt.out.Printf("%s  %-8s %-9s %s%s\n", t.ID, t.Kind, t.Status, t.Started.Local().Format("2006-01-02 15:04:05"), dur)
				rt.out.Printf("    %s\n", rt.out.C(ui.Dim, ui.Safe(t.Command)))
				// The signature line names a signer that the readers file vouches for, so an actor
				// that is only that name is not said twice.
				actor := t.Actor
				if checked && actor != "" && verdicts[i].Signer == actor {
					actor = ""
				}
				if actor != "" || t.Reason != "" || t.GitRevision != "" {
					label := ""
					if actor != "" || t.Reason == "" && t.GitRevision == "" {
						label = "actor " + orNone(ui.Safe(actor)) + "  "
					}
					rt.out.Printf("    %s\n", rt.out.C(ui.Dim, strings.TrimSpace(fmt.Sprintf("%s%s  %s", label, ui.Safe(t.Reason), ui.Safe(t.GitRevision)))))
				}
				if checked {
					line := txnSigText(verdicts[i])
					if verdicts[i].Overall() == protect.SigInvalid {
						line = rt.out.C(ui.Red, line)
					} else {
						line = rt.out.C(ui.Dim, line)
					}
					rt.out.Printf("    %s\n", line)
					if a := approvers[t.ID]; len(a) > 0 {
						rt.out.Printf("    %s\n", rt.out.C(ui.Dim, "approved by "+strings.Join(a, ", ")))
					}
				}
				if t.Summary != "" {
					rt.out.Printf("    %s\n", t.Summary)
				}
				for _, c := range t.Changes {
					if t.Kind == "secret" {
						rt.out.Printf("    %-8s %s%s\n", c.Action, printable(c.Addr), rt.out.C(ui.Dim, secretVersions(c)))
						continue
					}
					rt.out.Printf("    %-8s %s", c.Action, c.Addr)
					if c.After > 0 {
						rt.out.Printf("%s", rt.out.C(ui.Dim, fmt.Sprintf("  r%d -> r%d", c.Before, c.After)))
					}
					rt.out.Println()
				}
			}
			noteSealedCommands(rt, txns)
			printRolesLog(cmd.Context(), rt, st)
			return invalidTxns(invalid)
		},
	}
}

// txnViews is the JSON form of state log: the transactions with the check of their signatures
// and the approvers of the plan they ran from.
func txnViews(txns []state.Transaction, verdicts []state.TxnVerdict, approvers map[string][]string, checked bool) any {
	type view struct {
		state.Transaction
		Signature *sigStatusJSON `json:"signature,omitempty"`
		Approvals []string       `json:"approved_by,omitempty"`
	}
	views := make([]view, len(txns))
	for i, t := range txns {
		views[i] = view{Transaction: t}
		if checked {
			views[i].Signature = sigJSON(verdicts[i])
			views[i].Approvals = approvers[t.ID]
		}
	}
	return views
}

// noteSealedCommands explains <sealed:...> once, when a command line in the log has one.
func noteSealedCommands(rt *runContext, txns []state.Transaction) {
	for _, t := range txns {
		if strings.Contains(t.Command, "<sealed") {
			rt.out.Println()
			rt.out.Println(rt.out.C(ui.Dim, "<sealed:...> stands for a flag value that a sealed state keeps out of its log, because it can hold a secret. Who ran a command is on its signed-by line, and the reason is on the line under the command."))
			return
		}
	}
}

// txnApprovers says, for the transactions that ran from a stored plan, who approved the plan,
// as "name (role)", by transaction id. A plan that was removed after the apply has no
// approvals to read, so a transaction of it is missing from the result.
func txnApprovers(ctx context.Context, st *state.Store, txns []state.Transaction, readers []protect.Reader, checked bool) map[string][]string {
	res := map[string][]string{}
	if !checked || st.Protection().Sealed == nil {
		return res
	}
	ps := engine.NewPlanStoreFor(st)
	for _, t := range txns {
		if t.PlanID == "" || t.PlanDigest == "" {
			continue
		}
		as, err := ps.Approvals(ctx, t.PlanID, t.PlanDigest, readers)
		if err != nil {
			continue
		}
		for _, a := range as {
			if a.Status == protect.SigVerified {
				res[t.ID] = append(res[t.ID], fmt.Sprintf("%s (%s)", safeName(a.Signer), a.Role))
			}
		}
	}
	return res
}

// printRolesLog lists the versions of the roles of a sealed state after the transactions: who
// stored each and what it changed.
func printRolesLog(ctx context.Context, rt *runContext, st *state.Store) {
	if rt.flags.JSON || st.Protection().Sealed == nil {
		return
	}
	chain, err := st.RolesChain(ctx)
	if err != nil || len(chain.Versions) == 0 {
		return
	}
	rt.out.Println()
	rt.out.Println(rt.out.C(ui.Bold, "Roles, newest first (each version is signed by an admin of the one before)"))
	for i := len(chain.Versions) - 1; i >= 0; i-- {
		v := chain.Versions[i]
		var before []protect.Reader
		if i > 0 {
			before = chain.Versions[i-1].Set
		}
		who := "unsigned"
		if v.Signature != nil {
			who = safeName(v.Signature.Signer)
		}
		rt.out.Printf("version %d  %s  stored by %s\n", max(v.Seq, i+1), v.At.Local().Format("2006-01-02 15:04:05"), who)
		for _, l := range roleChanges(before, v.Set) {
			rt.out.Printf("    %s\n", rt.out.C(ui.Dim, l))
		}
	}
}

// secretVersions is the versions a change of a secret went between: the first version of a
// secret has no earlier one, and a purge has no later one.
func secretVersions(c state.Change) string {
	switch {
	case c.Before == 0 && c.After == 0:
		return ""
	case c.After == 0:
		return fmt.Sprintf("  v%d", c.Before)
	case c.Before == 0:
		return fmt.Sprintf("  v%d", c.After)
	}
	return fmt.Sprintf("  v%d -> v%d", c.Before, c.After)
}

// stateProbeCmd is "state probe".
func stateProbeCmd(rt *runContext) *cobra.Command {
	return &cobra.Command{
		Use: "probe", Short: "Check that the S3 store honours the conditional requests that locks need", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loadRoot(rt)
			if err != nil {
				return err
			}
			return probeS3(commandContext(), rt, root)
		},
	}
}

// stateLocksCmd is "state locks".
func stateLocksCmd(rt *runContext, loadRead func() (*state.Store, error)) *cobra.Command {
	return &cobra.Command{
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
			// The note of a create that was interrupted while its node had a record is shown
			// with the locks: it refuses the next create of the node as a lock does.
			notes, _ := st.PendingCreates()
			held := locks
			for _, p := range notes {
				orphan := p.OrphanedAmong(st.Get(p.Addr), held)
				locks = append(locks, state.Lock{Addr: p.Addr, PID: p.PID, Host: p.Host, Command: p.Command, Txn: p.Txn, Since: p.Since, RenewedAt: p.Since, Kind: "note", Stale: orphan})
			}
			if rt.flags.JSON {
				if locks == nil {
					locks = []state.Lock{}
				}
				return emitJSON(locks)
			}
			if len(locks) == 0 {
				rt.out.Println("No locks or reservations held.")
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
				if l.Kind == "note" {
					rt.out.Printf("%s  note of a create that stopped (txn %s, on %s since %s, %s)%s: the object it created may exist untracked; find it with pravo discover (with a #Discovery rule), adopt it (pravo state rm %s, then pravo import %s ID) or clear the note with pravo state unlock --allow-create %s\n", l.Addr, l.Txn, l.Host, l.Since.Local().Format(time.RFC3339), l.Command, stale, l.Addr, l.Addr, l.Addr)
					continue
				}
				if l.Kind == "reservation" {
					until := "no expiry"
					if l.Expires != nil {
						until = "until " + l.Expires.Local().Format(time.RFC3339)
					}
					rt.out.Printf("%s  reserved by %s %s: %s%s\n", l.Addr, who, until, l.Command, stale)
					continue
				}
				rt.out.Printf("%s  locked by %s, pid %d on %s since %s: %s%s\n", l.Addr, who, l.PID, l.Host, l.Since.Local().Format(time.RFC3339), l.Command, stale)
			}
			return nil
		},
	}
}

// stateDependentsCmd is "state dependents".
func stateDependentsCmd(loadRead func() (*state.Store, error)) *cobra.Command {
	return &cobra.Command{
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
	}
}

// stateQueryCmd is "state query".
func stateQueryCmd(rt *runContext, loadValues func() (*state.Store, error)) *cobra.Command {
	var redact bool
	cmd := &cobra.Command{
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
			var res *pravo.QueryResult
			if redact {
				snap, _, rerr := redactedSnapshot(cmd.Context(), rt, st)
				if rerr != nil {
					return rerr
				}
				res, err = pravo.QuerySnapshot(snap, args[0])
			} else {
				res, err = pravo.QueryState(st, args[0])
			}
			if err != nil {
				return err
			}
			if rt.flags.JSON {
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
	}
	cmd.Flags().BoolVar(&redact, "redact", false, "withhold the values the viewer withholds (see state show --redact)")
	return cmd
}

// upgradeCmd is "state upgrade": the first step of "protect init" alone. It is
// hidden, because a state in format 2 is not encrypted and nothing but a test or
// a careful operator has a use for it.
func upgradeCmd(rt *runContext, load func() (*state.Store, error)) *cobra.Command {
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
			if rt.flags.JSON {
				return emitJSON(map[string]any{"from": res.From, "to": res.To, "records": res.Records})
			}
			rt.out.Printf("The state is in format %d: %s as immutable objects\n", res.To, plural(res.Records, "record", "records"))
			return nil
		},
	}
}

// unlockCmd is "state unlock": it releases one lock, or with --stale every lock
// that may be reclaimed.
func unlockCmd(rt *runContext, load func() (*state.Store, error)) *cobra.Command {
	var stale, allowCreate, force bool
	cmd := &cobra.Command{
		Use: "unlock [addr]", Short: "Release a resource lock left behind by a crashed command", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case stale && len(args) > 0:
				return errors.New("--stale releases every stale lock; it takes no address")
			case !stale && len(args) == 0:
				return errors.New("give the address of the lock to release, or --stale for every lock that is stale")
			}
			st, err := load()
			if err != nil {
				return err
			}
			// The lock of an apply that stopped while it created a node is the only trace of
			// the create. Releasing it would let the next apply create the node again, so it
			// stays unless --allow-create says that the create never reached the cloud (or the
			// object was adopted, which gives the node a record).
			creating := map[string]bool{}
			if suspects, err := st.Suspects(); err == nil {
				for a, l := range suspects {
					creating[a] = l.Creating && st.Get(a) == nil
				}
			}
			kept := func(a string) {
				rt.out.Printf("%s has no record, and an apply stopped while it was creating it: the lock stays, because the object may exist in the cloud and the next apply would create it again. Adopt the object (pravo discover, or pravo import %s ID), or run pravo state unlock --allow-create %s if the create never reached the cloud.\n", a, a, a)
			}
			if stale {
				released, err := st.ReleaseStaleExcept(func(l state.Lock) bool { return !allowCreate && creating[l.Addr] })
				for _, a := range released {
					rt.out.Printf("Unlocked %s\n", a)
				}
				var held []string
				for a, c := range creating {
					if c && !allowCreate {
						held = append(held, a)
					}
				}
				slices.Sort(held)
				for _, a := range held {
					kept(a)
				}
				if err != nil {
					return err
				}
				if len(released) == 0 && len(held) == 0 {
					rt.out.Println("No stale locks.")
				}
				if len(held) > 0 {
					return engine.WithCode(engine.ExitRefused, fmt.Errorf("%s kept", plural(len(held), "lock", "locks")))
				}
				return nil
			}
			if note, _ := st.PendingCreateOf(args[0]); note != nil {
				if !allowCreate {
					rt.out.Printf("%s has the note of a create that stopped (txn %s): the object it created may exist in the cloud untracked, and the next create of the node would make a second one. Adopt the object (pravo state rm %s, then pravo import %s ID), or run pravo state unlock --allow-create %s if the create never reached the cloud.\n", args[0], note.Txn, args[0], args[0], args[0])
					return engine.WithCode(engine.ExitRefused, errors.New("note kept"))
				}
				if err := st.ClearPendingCreate(args[0]); err != nil {
					return err
				}
				rt.out.Printf("Cleared the note of the stopped create of %s\n", args[0])
			}
			if !force {
				if l, err := st.LockOf(args[0]); err == nil && l.Live() {
					rt.out.Printf("%s is held by a running command: %s (pid %d on %s, %s, last renewed %s). Releasing it lets a second command change the node at the same time, and the first one stops with a lost lock. Wait for it to end, or run pravo state unlock --force %s to release the lock anyway.\n", args[0], l.Command, l.PID, l.Host, l.Holder(), l.RenewedAt.Local().Format(time.RFC3339), args[0])
					return engine.WithCode(engine.ExitRefused, errors.New("lock kept: its holder is running"))
				}
			}
			// The mark is read from the lock itself, whatever its age: a lock that is still
			// live (a bucket keeps one for ten minutes) of a killed apply carries it too.
			if l, err := st.LockOf(args[0]); err == nil && l.Kind != "reservation" && l.Creating && st.Get(args[0]) == nil {
				creating[args[0]] = true
			}
			if creating[args[0]] && !allowCreate {
				kept(args[0])
				return engine.WithCode(engine.ExitRefused, errors.New("lock kept"))
			}
			ok, err := st.ForceUnlock(args[0])
			if err != nil {
				return err
			}
			if !ok {
				rt.out.Printf("%s was not locked\n", args[0])
			} else {
				rt.out.Printf("Unlocked %s\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&stale, "stale", false, "release every lock and reservation that may be reclaimed: its process is gone, it was not renewed for 10 minutes, or it expired")
	cmd.Flags().BoolVar(&allowCreate, "allow-create", false, "also release the lock of a node with no record that an apply stopped while it created: you know the create never reached the cloud, so the next apply may create the node; also clears the note of a stopped create over a record")
	cmd.Flags().BoolVar(&force, "force", false, "release the lock of a command that may still be running: its holder is alive on this host, or renewed the lock in the last three minutes")
	return cmd
}

func stateEdgesCmd(rt *runContext, load func() (*state.Store, error)) *cobra.Command {
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
				rt.out.Println("No dependency edges recorded.")
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

func stateImportTfstateCmd(rt *runContext, load func() (*state.Store, error)) *cobra.Command {
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
			rt.out.Printf("Imported %d resources and %d outputs from %s into %s\n", len(res.Resources), len(res.Outputs), args[0], st.Dir())
			for _, n := range res.Notes {
				rt.out.Warning(n)
			}
			if len(res.Renamed) > 0 {
				rt.out.Println()
				rt.out.Println(rt.out.C(ui.Bold, "Renamed instances") + " (these are the addresses now in the state; point each at its place in the specification with $from):")
				for _, r := range res.Renamed {
					rt.out.Printf("  %s -> %s\n", r.From, r.To)
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

// notInState is the error of a command that names an address the state does not hold.
func notInState(addr string) error {
	return fmt.Errorf("%s is not in state", addr)
}
