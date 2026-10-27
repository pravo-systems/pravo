package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func protectInitCmd() *cobra.Command {
	var encrypt, yes, allowLocal bool
	var recovery string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Seal the state to the readers in the readers file",
		Long: `Seals the state. It stores every record once as an immutable object,
rec/<sha256>, and encrypts the attributes and the private data of each record,
the outputs, the stored plans and the values in command lines to the readers in
the readers file. Addresses, types, names, dependencies and revisions stay
readable, so the graph and the plans keep working. The state is set to format 3,
which earlier versions of pravo refuse to open.

It shows what it will do and asks for a yes. --yes answers it. Without a
terminal and without --yes it refuses to ask. It does not run while a command is
running or a lock is held. It needs an identity that is on the readers file,
because sealing to a list that you are not on would lock you out.

--recovery K/N also sets a recovery recipient: a key that is split into N shares,
any K of which rebuild it, so that the state can be opened when every reader has
lost their key. The shares are written to recovery-shares/ and the public key to
the recovery file next to the readers file. Give each share to a different person,
offline, and commit the recovery file; never commit the shares.

A run that is interrupted leaves the state sealing; run it again to finish.

Exit codes: 4 when a command is running or a lock is held, 5 when your identity
is not on the readers file, 6 when the readers file is missing or invalid.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !encrypt {
				return errors.New("protect init needs --encrypt: it is the only protection there is")
			}
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.init(yes, allowLocal, recovery)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&encrypt, "encrypt", false, "seal the attributes, outputs and stored plans to the readers")
	f.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	f.BoolVar(&allowLocal, "allow-local", false, "accept a state in a local directory that only you use")
	f.StringVar(&recovery, "recovery", "", "also set a recovery recipient, as K/N: N shares, any K rebuild its key")
	return cmd
}

// atRest refuses, with the exit code of a locked state, while a command is
// running or a lock is held. The commands that rewrite every record of the state
// run only then: a writer that is half way through a write would lose it.
func atRest(st *state.Store) error {
	txns, err := st.Transactions()
	if err != nil {
		return err
	}
	running, locks, err := activity(st, txns)
	if err != nil {
		return err
	}
	if running > 0 || locks > 0 {
		var busy []string
		if running > 0 {
			busy = append(busy, plural(running, "transaction is", "transactions are")+" running")
		}
		if locks > 0 {
			busy = append(busy, plural(locks, "lock is", "locks are")+" held")
		}
		return engine.WithCode(engine.ExitLocked, fmt.Errorf("not ready: %s. Wait for them, or release stale locks with pravo state unlock --stale.", strings.Join(busy, " and ")))
	}
	return nil
}

func (p *protectRun) init(yes, allowLocal bool, recoverySpec string) error {
	if err := p.refuseLocal(allowLocal); err != nil {
		return err
	}
	var rec *protect.Recovery
	var shares []protect.Share
	if recoverySpec != "" {
		var err error
		if rec, shares, err = newRecovery(recoverySpec); err != nil {
			return err
		}
	}
	if err := p.rf.require(); err != nil {
		return err
	}
	// Several identity files and none named like the user: the one on the list.
	var listed []protect.ReaderPublic
	for _, r := range p.rf.Readers {
		listed = append(listed, r.Key)
	}
	id, err := resolveIdentity(identityOptions{Name: flags.Identity, Prompt: identityPrompt, Readers: listed})
	if err != nil {
		if errors.Is(err, errNoIdentity) {
			return engine.WithCode(engine.ExitRefused, fmt.Errorf("%w; sealing to a list you are not on would lock you out", err))
		}
		return err
	}
	if !readersInclude(p.rf.Readers, id) {
		return engine.WithCode(engine.ExitRefused, fmt.Errorf("your identity %s is not in %s. Sealing to a list you are not on would lock you out.", id.Name, p.rf.shown()))
	}
	// Everything before the yes only reads.
	b, err := p.backend(false)
	if err != nil {
		return err
	}
	st, _, _, err := p.open(b, false, false)
	if err != nil {
		return err
	}
	prot := st.Protection()
	if prot.State == "sealed" {
		out.Printf("Already sealed (format %d). Use pravo protect rewrap to change the readers.\n", prot.Format)
		if rec != nil {
			out.Println(out.C(ui.Yellow, "--recovery was not applied. To set a recovery recipient on a sealed state: pravo protect rewrap --recovery "+fmt.Sprintf("%d/%d", rec.K, rec.N)))
		}
		return nil
	}
	if prot.State == "unsealing" {
		return errors.New("an unseal was started and did not finish; finish it with pravo protect unseal before the state is sealed again")
	}
	if rec != nil && prot.State == "migrating" {
		return errors.New("a seal was started and did not finish, and a run that finishes it does not set a recovery. Finish it with pravo protect init --encrypt, then run pravo protect rewrap --recovery " + fmt.Sprintf("%d/%d", rec.K, rec.N))
	}
	if err := atRest(st); err != nil {
		return err
	}
	names := nameList(readerNames(p.rf.Readers))
	out.Printf("%-8s %s  (%s)\n", "Readers", p.rf.shown(), names)
	if m := prot.Migration; prot.State == "migrating" && m != nil {
		out.Printf("A seal was started by %s and did not finish: %s of %s records are done.\n", safeName(m.By), commas(m.Done), commas(m.Total))
	}
	out.Println("This will:")
	out.Printf("  1  write %s as immutable objects  rec/<sha256>\n", counted(prot.Records, "record", "records"))
	out.Printf("  2  encrypt their attributes and private data to %s (%s)\n", plural(len(p.rf.Readers), "reader", "readers"), names)
	out.Println("  3  seal outputs.json, stored-plan values and the values in transaction command lines the same way")
	step := 4
	if rec != nil {
		out.Printf("  %d  add a recovery recipient: %d shares, any %d rebuild the key\n", step, rec.N, rec.K)
		step++
	}
	out.Printf("  %d  set the state to format %d. Earlier versions of pravo will refuse it\n", step, state.FormatSealed)
	if ok, err := confirm("Proceed?", yes); err != nil {
		return err
	} else if !ok {
		return errors.New("protect init cancelled; nothing was written")
	}
	if sb, ok := b.(state.Prober); ok {
		if err := sb.ProbeSteps(p.ctx, nil); err != nil {
			return err
		}
		if err := sb.ProbeStrict(p.ctx, nil); err != nil {
			return err
		}
	}
	out.Println(out.C(ui.Green, "store check passed"))
	if b, err = p.backend(true); err != nil {
		return err
	}
	if st, _, _, err = p.open(b, true, true); err != nil {
		return err
	}
	// The shares are written before the state is touched: a seal that lost them would
	// leave a recovery lock that nobody can open.
	var removeShares func()
	if rec != nil {
		if removeShares, err = writeShares(shares); err != nil {
			return err
		}
	}
	began := time.Now()
	res, err := st.Upgrade(p.ctx, state.UpgradeOptions{
		To: state.FormatSealed, Readers: p.rf.Readers, Identity: id, By: id.Name, Recovery: rec,
		Plans: engine.NewPlanStoreFor(st).SealAll,
	})
	if err != nil {
		if rec != nil {
			p.afterFailure(rec, removeShares, loadRecoveryFile(p.rf))
		}
		return err
	}
	out.Println(out.C(ui.Green, fmt.Sprintf("%s sealed in %s", counted(res.Records, "record", "records"), elapsed(time.Since(began)))))
	out.Println(out.C(ui.Green, "outputs sealed · recipients written to meta.json"))
	if res.PlansSkipped > 0 {
		out.Println(plansInUseLine(res.PlansSkipped, false))
	}
	out.Println()
	out.Printf("The state is sealed to %s.\n", names)
	out.Println()
	out.Println("Commit the readers file. Writers read the list from git:")
	out.Printf("  %s\n", p.rf.shown())
	rcf := loadRecoveryFile(p.rf)
	if rec == nil {
		// A run that was cut short after it set a recovery leaves it in the state.
		if stored, ok := p.storedRecovery(); ok && stored != nil {
			if !rcf.Found {
				if err := writeRecoveryFile(rcf, stored); err != nil {
					return fmt.Errorf("the state has a recovery recipient, but %s could not be written: %w. Write this line to it:\n%s", rcf.shown(), err, stored.Line())
				}
			}
			out.Printf("The state has a recovery recipient from an earlier run: any %d of %d shares rebuild its key. Commit %s.\n", stored.K, stored.N, rcf.shown())
			return nil
		}
		out.Println(out.C(ui.Yellow, "No recovery recipient. If every reader loses their key, the state cannot be read."))
		out.Println("Add one with: pravo protect rewrap --recovery 3/5")
		return nil
	}
	if err := writeRecoveryFile(rcf, rec); err != nil {
		return fmt.Errorf("the recovery is in the state, but %s could not be written: %w. Write this line to it:\n%s", rcf.shown(), err, rec.Line())
	}
	printShares(rec, shares, rcf)
	return nil
}

// elapsed writes a duration as 41 s, or 0.4 s when it is short.
func elapsed(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
	return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
}

// confirm asks a question that is answered with y or yes. With assume set it
// asks nothing. It refuses to ask when stdin is not a terminal, or when the
// output is JSON, because then nobody sees the question.
func confirm(prompt string, assume bool) (bool, error) {
	if assume {
		return true, nil
	}
	if flags.JSON {
		return false, errors.New("refusing to ask with --json; pass --yes")
	}
	if !stdinIsTerminal() {
		return false, errors.New("refusing to ask, stdin is not a terminal; pass --yes")
	}
	out.Printf("%s [y/N] ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
